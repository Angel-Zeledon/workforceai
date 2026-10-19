package meetings

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// MinutesInput is the text the minutes are written from.
type MinutesInput struct {
	// Notes are pasted notes, a transcript or the text of an uploaded file.
	Notes string
	// Source is notes | transcript | upload; empty: detected.
	Source string
	Lang   string
}

// Minutes returns the minutes of a meeting.
func (s *Service) Minutes(ctx context.Context, org, id string) (Minutes, error) {
	if _, err := s.Store.GetMeeting(ctx, org, id); err != nil {
		return Minutes{}, err
	}
	m, err := s.Store.GetMinutes(ctx, org, id)
	if err != nil {
		return Minutes{}, err
	}
	return normMinutes(m), nil
}

func normMinutes(m Minutes) Minutes {
	if m.Decisions == nil {
		m.Decisions = []string{}
	}
	if m.ActionItems == nil {
		m.ActionItems = []ActionItem{}
	}
	return m
}

// summaryText keeps the narrative of a model answer: what comes before the first
// decisions / action items section (the sections themselves are extracted separately).
func summaryText(answer string) string {
	var keep []string
	for _, line := range strings.Split(strings.ReplaceAll(answer, "\r\n", "\n"), "\n") {
		if k := headingKind(line); k == "decision" || k == "action" {
			break
		}
		keep = append(keep, line)
	}
	out := strings.TrimSpace(strings.Join(keep, "\n"))
	if out == "" {
		out = strings.TrimSpace(answer)
	}
	if r := []rune(out); len(r) > 4000 {
		out = string(r[:4000])
	}
	return out
}

// GenerateMinutes writes the minutes of a live or ended meeting from pasted notes, a
// transcript or an uploaded text: the deterministic pass finds decisions and action items,
// the model (through the existing summarize call, in simulation like everything else) writes
// the summary and may find more items, the result is saved to the drive as a markdown file.
// Nothing here creates tasks: that is a separate, explicit step (CreateTasks).
func (s *Service) GenerateMinutes(ctx context.Context, org, actor, id string, in MinutesInput) (Minutes, error) {
	m, err := s.Store.GetMeeting(ctx, org, id)
	if err != nil {
		return Minutes{}, err
	}
	if !m.Status.CanWriteMinutes() {
		return Minutes{}, fmt.Errorf("%w: minutes are written while the meeting is live or after it ended", ErrState)
	}
	prev, perr := s.Store.GetMinutes(ctx, org, id)
	if perr == nil && prev.RequestID != "" {
		return Minutes{}, fmt.Errorf("%w: tasks were already created from these minutes", ErrState)
	}
	notes := strings.TrimSpace(CleanCaptions(strings.ReplaceAll(in.Notes, "\x00", "")))
	if notes == "" {
		return Minutes{}, invalid("notes are required")
	}
	truncated := false
	if r := []rune(notes); len(r) > MaxNotesChars {
		notes, truncated = string(r[:MaxNotesChars]), true
	}
	lang := NormLang(in.Lang)

	ex := ExtractMinutes(notes)
	msgs, transcript := SplitNotes(notes)
	source := in.Source
	switch source {
	case "notes", "transcript", "upload":
	default:
		source = "notes"
		if transcript {
			source = "transcript"
		}
	}
	summary, summarySource := "", "local"
	if s.Summarizer != nil {
		var names []string
		for _, a := range m.Attendees {
			names = append(names, a.Name)
		}
		out, serr := s.Summarizer.Summarize(ctx, org, SummaryIn{Title: m.Title, Participants: names, Messages: msgs, Locale: RuntimeLocale(lang)})
		switch {
		case serr != nil:
			// A budget refusal must surface; any other failure falls back to the local summary.
			if isBudget(serr) {
				return Minutes{}, serr
			}
		case out.Source == "runtime" && strings.TrimSpace(out.Text) != "":
			summary, summarySource = summaryText(out.Text), "runtime"
			ex = MergeExtracted(ex, ExtractMinutes(out.Text))
		}
	}
	if summary == "" {
		if summary = LocalSummary(notes); summary == "" {
			summary = oneLine(notes, 400)
		}
	}
	now := s.Now().UTC()
	min := Minutes{MeetingID: id, OrgID: org, Summary: summary, Decisions: ex.Decisions, ActionItems: ResolveOwners(ex.Actions, m.Attendees),
		Lang: lang, Source: source, SummarySource: summarySource, Truncated: truncated, CreatedBy: actor, CreatedAt: now, UpdatedAt: now}
	if perr == nil {
		min.CreatedAt, min.FileID, min.FilePath = prev.CreatedAt, prev.FileID, prev.FilePath
	}
	min = s.saveMinutesFile(ctx, org, actor, m, min)
	min = normMinutes(min)
	if err := s.Store.PutMinutes(ctx, min); err != nil {
		return Minutes{}, err
	}
	s.Audit(ctx, org, actor, "meeting.minutes", "meeting", id, map[string]any{"source": source, "summary_source": summarySource,
		"decisions": len(min.Decisions), "action_items": len(min.ActionItems), "saved": min.FileID != "", "truncated": truncated})
	s.Emit(ctx, org, "meeting.updated", map[string]any{"meeting_id": id, "status": string(m.Status)})
	return min, nil
}

func isBudget(err error) bool { return errors.Is(err, ErrBudget) }

// saveMinutesFile writes the markdown to the drive (a new version when the file exists). A drive
// failure is audited and leaves the minutes without a file: the minutes themselves are kept.
func (s *Service) saveMinutesFile(ctx context.Context, org, actor string, m Meeting, min Minutes) Minutes {
	if s.Files == nil {
		return min
	}
	set, err := s.EffectiveSettings(ctx, org)
	if err != nil {
		return min
	}
	path := set.MinutesFolder + "/" + FileName(m)
	id, saved, err := s.Files.Save(ctx, org, actor, path, []byte(RenderMarkdown(m, min)), "meeting minutes")
	if err != nil {
		s.Audit(ctx, org, actor, "meeting.minutes_save_failed", "meeting", m.ID, map[string]any{"error": truncateErr(err)})
		return min
	}
	min.FileID, min.FilePath = id, saved
	return min
}

func truncateErr(err error) string {
	r := []rune(err.Error())
	if len(r) > 160 {
		r = r[:160]
	}
	return string(r)
}

// MinutesEdit is a human correction of the minutes.
type MinutesEdit struct {
	Summary     *string
	Decisions   *[]string
	ActionItems *[]ActionItem
}

// EditMinutes lets a person fix the minutes before tasks are created from them.
func (s *Service) EditMinutes(ctx context.Context, org, actor, id string, e MinutesEdit) (Minutes, error) {
	m, err := s.Store.GetMeeting(ctx, org, id)
	if err != nil {
		return Minutes{}, err
	}
	min, err := s.Store.GetMinutes(ctx, org, id)
	if err != nil {
		return Minutes{}, err
	}
	if min.RequestID != "" {
		return Minutes{}, fmt.Errorf("%w: tasks were already created from these minutes", ErrState)
	}
	if e.Summary != nil {
		min.Summary = cleanText(*e.Summary, 4000)
	}
	if e.Decisions != nil {
		min.Decisions = nil
		for _, d := range *e.Decisions {
			if d = oneLine(d, maxItemChar); d != "" && len(min.Decisions) < maxItems {
				min.Decisions = append(min.Decisions, d)
			}
		}
	}
	if e.ActionItems != nil {
		var items []ActionItem
		for _, it := range *e.ActionItems {
			it.Text = oneLine(it.Text, maxItemChar)
			if it.Text == "" || len(items) >= maxItems {
				continue
			}
			it.Owner, it.TaskKey = oneLine(it.Owner, 80), ""
			if it.Due != "" && !reDueDate.MatchString(it.Due) {
				it.Due = ""
			}
			it.OwnerID, it.OwnerKind = "", ""
			items = append(items, it)
		}
		min.ActionItems = ResolveOwners(items, m.Attendees)
	}
	min.UpdatedAt = s.Now().UTC()
	min = s.saveMinutesFile(ctx, org, actor, m, min)
	min = normMinutes(min)
	if err := s.Store.PutMinutes(ctx, min); err != nil {
		return Minutes{}, err
	}
	s.Audit(ctx, org, actor, "meeting.minutes_edited", "meeting", id, map[string]any{"decisions": len(min.Decisions), "action_items": len(min.ActionItems)})
	return min, nil
}

// CreateTasks turns action items of the minutes into tasks (one request, one task per item,
// through the orchestrator like /assign and /reunion). indexes selects items; empty selects
// every item that has no task yet. Items owned by an invited agent go to that agent; the rest
// go to the default agent, which tracks them for the person who owns them. An item never
// becomes two tasks.
func (s *Service) CreateTasks(ctx context.Context, org, actor, id string, indexes []int, lang string) (Minutes, string, error) {
	if s.Tasks == nil {
		return Minutes{}, "", ErrUnavailable
	}
	m, err := s.Store.GetMeeting(ctx, org, id)
	if err != nil {
		return Minutes{}, "", err
	}
	min, err := s.Store.GetMinutes(ctx, org, id)
	if err != nil {
		return Minutes{}, "", err
	}
	var pick []int
	if len(indexes) == 0 {
		for i, it := range min.ActionItems {
			if it.TaskKey == "" {
				pick = append(pick, i)
			}
		}
	} else {
		seen := map[int]bool{}
		for _, i := range indexes {
			if i < 0 || i >= len(min.ActionItems) {
				return Minutes{}, "", invalid("no action item %d", i)
			}
			if !seen[i] && min.ActionItems[i].TaskKey == "" {
				seen[i] = true
				pick = append(pick, i)
			}
		}
		sort.Ints(pick)
	}
	if len(pick) == 0 {
		return Minutes{}, "", fmt.Errorf("%w: there are no action items without a task", ErrNothingToDo)
	}
	l := NormLang(lang)
	if lang == "" {
		l = NormLang(min.Lang)
	}
	tr := func(es, en, pt string) string { return map[string]string{"es": es, "en": en, "pt": pt}[l] }
	ctxLine := fmt.Sprintf(tr("Acción de la reunión «%s» del %s", "Action item from the meeting “%s” of %s", "Ação da reunião «%s» de %s"), m.Title, m.Start.UTC().Format("2006-01-02"))
	decisions := ""
	if len(min.Decisions) > 0 {
		top := min.Decisions
		if len(top) > 3 {
			top = top[:3]
		}
		decisions = "\n" + tr("Decisiones de la reunión: ", "Decisions of the meeting: ", "Decisões da reunião: ") + strings.Join(top, "; ")
	}
	agents, _ := s.agentIndex(ctx, org)
	b := TaskBatch{Text: tr("Acciones de la reunión: ", "Meeting action items: ", "Ações da reunião: ") + m.Title,
		Objectives: []string{tr("Dar seguimiento a las acciones de: ", "Follow up the action items of: ", "Acompanhar as ações de: ") + m.Title}}
	keys := map[int]string{}
	for n, i := range pick {
		it := min.ActionItems[i]
		agent, note := s.DefaultAgentID, ""
		if it.OwnerKind == KindAgent && (len(agents) == 0 || agents[it.OwnerID].ID != "") && it.OwnerID != "" {
			agent = it.OwnerID
		} else if it.Owner != "" {
			note = "\n" + fmt.Sprintf(tr("Responsable humano: %s. Prepara lo necesario y haz seguimiento; no actúes fuera de la organización sin aprobación.",
				"Human owner: %s. Prepare what is needed and track it; do not act outside the organization without approval.",
				"Responsável humano: %s. Prepare o necessário e acompanhe; não aja fora da organização sem aprovação."), it.Owner)
		}
		due := ""
		if it.Due != "" {
			due = "\n" + tr("Fecha límite: ", "Due: ", "Prazo: ") + it.Due
		}
		key := fmt.Sprintf("a%d", n+1)
		keys[i] = key
		b.Tasks = append(b.Tasks, TaskSpec{Key: key, Title: oneLine(it.Text, 80), AgentID: agent,
			Description: ctxLine + ":\n" + it.Text + due + note + decisions + "\n\n" + tr("(El texto viene de las notas de la reunión: verifícalo antes de actuar.)", "(The text comes from the meeting notes: verify it before acting.)", "(O texto vem das notas da reunião: verifique antes de agir.)")})
	}
	req, err := s.Tasks.CreateTasks(ctx, org, b)
	if err != nil {
		return Minutes{}, "", err
	}
	for i, k := range keys {
		min.ActionItems[i].TaskKey = k
	}
	min.RequestID, min.UpdatedAt = req, s.Now().UTC()
	if err := s.Store.PutMinutes(ctx, min); err != nil {
		return Minutes{}, req, err
	}
	s.Audit(ctx, org, actor, "meeting.tasks_created", "meeting", id, map[string]any{"tasks": len(pick), "request_id": req})
	s.Emit(ctx, org, "meeting.updated", map[string]any{"meeting_id": id, "status": string(m.Status)})
	return normMinutes(min), req, nil
}
