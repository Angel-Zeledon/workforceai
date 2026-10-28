package rituals

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/quality"
)

// Everything in this file is a pure function of its inputs: the same tasks and approvals always give the same
// stand-up and the same proposals (tested), with no model involved.

const (
	maxItems       = 3  // items per list kept in an entry
	maxTitleRunes  = 80 // titles are clipped
	maxEvidence    = 3
	maxProposals   = 2 // per agent and retro
	minSignalCount = 2 // occurrences in the week before a signal becomes a proposal
)

// QualitySnapshot is what the retro reads of the advisory quality score (quality.Service.Overview).
type QualitySnapshot struct {
	Score *int
	Level string
	Trend string
}

// agentData groups the tasks and approvals of one agent.
type agentData struct {
	tasks     []domain.Task
	approvals []domain.Approval
}

func groupByAgent(tasks []domain.Task, approvals []domain.Approval) map[string]*agentData {
	m := map[string]*agentData{}
	get := func(id string) *agentData {
		d := m[id]
		if d == nil {
			d = &agentData{}
			m[id] = d
		}
		return d
	}
	for _, t := range tasks {
		if t.AgentID != "" {
			get(t.AgentID).tasks = append(get(t.AgentID).tasks, t)
		}
	}
	for _, a := range approvals {
		if a.AgentID != "" {
			get(a.AgentID).approvals = append(get(a.AgentID).approvals, a)
		}
	}
	return m
}

func clipTitle(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= maxTitleRunes {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:maxTitleRunes-1])) + "…"
}

func inWindow(t *time.Time, from, to time.Time) bool {
	return t != nil && !t.Before(from) && t.Before(to)
}

// standupEntry is what one agent says in the stand-up for the window [from, to).
func standupEntry(a domain.Agent, d *agentData, from, to time.Time, loc string) Entry {
	e := Entry{AgentID: a.ID, Name: a.Name, Role: a.Role, Done: []Item{}, Next: []Item{}, Blockers: []Item{}}
	if d != nil {
		tasks := append([]domain.Task{}, d.tasks...)
		// finished: newest first; open: running first, then the oldest pending
		sort.SliceStable(tasks, func(i, j int) bool { return taskOrder(tasks[i]).Before(taskOrder(tasks[j])) })
		var done, next, blockers []Item
		for i := len(tasks) - 1; i >= 0; i-- {
			t := tasks[i]
			if (t.Status == domain.TaskDone || t.Status == domain.TaskFailed) && inWindow(t.FinishedAt, from, to) {
				done = append(done, Item{TaskID: t.ID, Title: clipTitle(t.Title), Status: string(t.Status)})
			}
		}
		for _, t := range tasks {
			if t.Status == domain.TaskRunning {
				next = append(next, Item{TaskID: t.ID, Title: clipTitle(t.Title), Status: ItemRunning})
			}
		}
		for _, t := range tasks {
			switch t.Status {
			case domain.TaskPending:
				next = append(next, Item{TaskID: t.ID, Title: clipTitle(t.Title), Status: ItemPending})
			case domain.TaskBlocked:
				blockers = append(blockers, Item{TaskID: t.ID, Title: clipTitle(t.Title), Status: ItemBlocked})
			case domain.TaskAwaitingApproval:
				blockers = append(blockers, Item{TaskID: t.ID, Title: clipTitle(t.Title), Status: ItemAwaitingApprove})
			}
		}
		awaiting := map[string]bool{}
		for _, b := range blockers {
			awaiting[b.TaskID] = true
		}
		apps := append([]domain.Approval{}, d.approvals...)
		sort.SliceStable(apps, func(i, j int) bool { return apps[i].CreatedAt.Before(apps[j].CreatedAt) })
		for _, ap := range apps {
			// an approval already shown through its waiting task is not repeated
			if ap.Status == domain.ApprovalPending && !awaiting[ap.TaskID] {
				blockers = append(blockers, Item{TaskID: ap.TaskID, Title: clipTitle(ap.Title), Status: ItemApproval})
			}
		}
		e.Done, e.Next, e.Blockers = capItems(done), capItems(next), capItems(blockers)
	}
	e.Quiet = len(e.Done) == 0 && len(e.Next) == 0 && len(e.Blockers) == 0
	e.Lines = standupLines(e, loc)
	return e
}

func taskOrder(t domain.Task) time.Time {
	if t.FinishedAt != nil {
		return *t.FinishedAt
	}
	return t.CreatedAt
}

// capItems keeps the first maxItems and records how many were left out as a last "+N" item.
func capItems(in []Item) []Item {
	if len(in) <= maxItems {
		if in == nil {
			return []Item{}
		}
		return in
	}
	out := append([]Item{}, in[:maxItems]...)
	return append(out, Item{Title: fmt.Sprintf("+%d", len(in)-maxItems), Status: "more"})
}

// ---- texts (organization language: es default, en) ----

func tr(loc, es, en string) string {
	if loc == "en" {
		return en
	}
	return es
}

func quoteList(items []Item, loc string, render func(Item) string) string {
	parts := make([]string, 0, len(items))
	for _, it := range items {
		if it.Status == "more" {
			parts = append(parts, tr(loc, it.Title+" más", it.Title+" more"))
			continue
		}
		parts = append(parts, render(it))
	}
	return strings.Join(parts, "; ")
}

func q(loc, s string) string { return tr(loc, "«"+s+"»", "“"+s+"”") }

// standupLines renders the 2 or 3 sentences of a stand-up entry (blockers only when there are some).
func standupLines(e Entry, loc string) []string {
	var did, will string
	if len(e.Done) == 0 {
		did = tr(loc, "Hice: sin tareas terminadas desde el último stand-up.", "Did: no finished tasks since the last stand-up.")
	} else {
		did = tr(loc, "Hice: ", "Did: ") + quoteList(e.Done, loc, func(it Item) string {
			if it.Status == ItemFailed {
				return q(loc, it.Title) + tr(loc, " (falló)", " (failed)")
			}
			return q(loc, it.Title)
		}) + "."
	}
	if len(e.Next) == 0 {
		will = tr(loc, "Haré: nada asignado; disponible para nuevo trabajo.", "Next: nothing assigned; available for new work.")
	} else {
		will = tr(loc, "Haré: ", "Next: ") + quoteList(e.Next, loc, func(it Item) string {
			if it.Status == ItemRunning {
				return q(loc, it.Title) + tr(loc, " (en curso)", " (in progress)")
			}
			return q(loc, it.Title)
		}) + "."
	}
	lines := []string{did, will}
	if len(e.Blockers) > 0 {
		lines = append(lines, tr(loc, "Bloqueos: ", "Blockers: ")+quoteList(e.Blockers, loc, func(it Item) string {
			switch it.Status {
			case ItemBlocked:
				return q(loc, it.Title) + tr(loc, " está bloqueada", " is blocked")
			default:
				return tr(loc, "espero aprobación de ", "waiting for approval of ") + q(loc, it.Title)
			}
		})+".")
	}
	return lines
}

// ---- retrospective ----

// retroEntry computes the week of one agent and its proposals (at most maxProposals, strongest signal first).
func retroEntry(a domain.Agent, d *agentData, from, to time.Time, qs QualitySnapshot, hasQuality bool, loc string) (Entry, []Proposal) {
	e := Entry{AgentID: a.ID, Name: a.Name, Role: a.Role, Done: []Item{}, Next: []Item{}, Blockers: []Item{}}
	st := &RetroStats{}
	var rejected, reworked, failed []string
	var bestDone string
	var bestAt time.Time
	if d != nil {
		for _, t := range d.tasks {
			if !inWindow(t.FinishedAt, from, to) {
				continue
			}
			switch t.Status {
			case domain.TaskDone:
				st.TasksDone++
				if t.FinishedAt.After(bestAt) && (t.Output == nil || t.Output.Review == nil || t.Output.Review.Verdict == "pass") {
					bestDone, bestAt = clipTitle(t.Title), *t.FinishedAt
				}
			case domain.TaskFailed:
				st.TasksFailed++
				failed = append(failed, clipTitle(t.Title))
			}
			if t.Output != nil && t.Output.Review != nil && (t.Output.Review.Reworks > 0 || t.Output.Review.Verdict == "rework" || t.Output.Review.Verdict == "fail") {
				st.Reworks++
				reason := ""
				if len(t.Output.Review.Reasons) > 0 {
					reason = " — " + clipTitle(t.Output.Review.Reasons[0])
				}
				reworked = append(reworked, q(loc, clipTitle(t.Title))+reason)
			}
		}
		for _, ap := range d.approvals {
			if ap.Status != domain.ApprovalRejected || !inWindow(ap.ResolvedAt, from, to) || quality.SystemRejection(ap.Note) {
				continue
			}
			st.Rejected++
			line := q(loc, clipTitle(ap.Title))
			if n := strings.TrimSpace(ap.Note); n != "" {
				line += tr(loc, " — motivo: ", " — reason: ") + clipTitle(n)
			}
			rejected = append(rejected, line)
		}
	}
	if hasQuality {
		st.Score, st.Level, st.Trend = qs.Score, qs.Level, qs.Trend
	}
	e.Retro = st
	e.Quiet = st.TasksDone == 0 && st.TasksFailed == 0 && st.Rejected == 0 && st.Reworks == 0

	type cand struct {
		n int
		p Proposal
	}
	var cands []cand
	if st.Rejected >= minSignalCount {
		cands = append(cands, cand{st.Rejected, Proposal{Kind: ProposalInstructionNote, Signal: SignalRejected,
			Title: tr(loc, "Explicar mejor mis solicitudes de aprobación", "Explain my approval requests better"),
			Text: tr(loc, fmt.Sprintf("Antes de pedir una aprobación, explica en una línea por qué la acción es necesaria, qué datos usaste y qué pasa si se rechaza; confirma destinatarios, importes y fechas. (Retro: %d aprobaciones rechazadas en la semana.)", st.Rejected),
				fmt.Sprintf("Before asking for an approval, explain in one line why the action is needed, which data you used and what happens if it is rejected; double-check recipients, amounts and dates. (Retro: %d approvals rejected this week.)", st.Rejected)),
			Evidence: firstN(rejected, maxEvidence)}})
	}
	if st.Reworks >= minSignalCount {
		cands = append(cands, cand{st.Reworks, Proposal{Kind: ProposalInstructionNote, Signal: SignalReworks,
			Title: tr(loc, "Revisar mi entrega antes de terminar", "Check my work before finishing"),
			Text: tr(loc, fmt.Sprintf("Antes de dar una tarea por terminada, compara tu entrega con lo que se pidió y con los criterios de aceptación, punto por punto. (Retro: la revisión pidió rehacer %d entregas en la semana.)", st.Reworks),
				fmt.Sprintf("Before marking a task as done, compare your work with what was asked and with the acceptance criteria, point by point. (Retro: the review asked to redo %d deliverables this week.)", st.Reworks)),
			Evidence: firstN(reworked, maxEvidence)}})
	}
	if st.TasksFailed >= minSignalCount {
		ev := make([]string, 0, len(failed))
		for _, f := range failed {
			ev = append(ev, q(loc, f))
		}
		cands = append(cands, cand{st.TasksFailed, Proposal{Kind: ProposalInstructionNote, Signal: SignalFailed,
			Title: tr(loc, "Pedir lo que me falta al empezar", "Ask for what I am missing up front"),
			Text: tr(loc, fmt.Sprintf("Si te falta un dato, un archivo o un permiso, dilo al empezar y pide lo que necesitas en lugar de seguir; si no puedes terminar, explica en una línea por qué. (Retro: %d tareas fallidas en la semana.)", st.TasksFailed),
				fmt.Sprintf("If you are missing a piece of data, a file or a permission, say so at the start and ask for it instead of carrying on; if you cannot finish, explain why in one line. (Retro: %d failed tasks this week.)", st.TasksFailed)),
			Evidence: firstN(ev, maxEvidence)}})
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].n > cands[j].n })
	if hasQuality && qs.Score != nil && (qs.Level == quality.LevelFair || qs.Level == quality.LevelNeedsWork || qs.Trend == quality.TrendDown) {
		ev := []string{tr(loc, fmt.Sprintf("Puntaje de calidad %d (%s), tendencia %s", *qs.Score, qs.Level, qs.Trend),
			fmt.Sprintf("Quality score %d (%s), trend %s", *qs.Score, qs.Level, qs.Trend))}
		if bestDone != "" {
			cands = append(cands, cand{0, Proposal{Kind: ProposalSkillSuggestion, Signal: SignalQuality,
				Title: tr(loc, "Guardar como skill mi mejor procedimiento", "Save my best procedure as a skill"),
				Text: tr(loc, "Guarda como skill (borrador que revisa un administrador) cómo se resolvió la tarea que salió bien, para repetir el mismo procedimiento en las próximas.",
					"Save as a skill (a draft an administrator reviews) how the task that went well was solved, to repeat the same procedure next time."),
				Evidence: append(ev, q(loc, bestDone))}})
		} else {
			cands = append(cands, cand{0, Proposal{Kind: ProposalInstructionNote, Signal: SignalQuality,
				Title: tr(loc, "Confirmar el objetivo antes de empezar", "Confirm the goal before starting"),
				Text: tr(loc, "Antes de empezar, resume en una línea qué te pidieron y cómo sabrás que está bien hecho; si algo no está claro, pregúntalo.",
					"Before starting, sum up in one line what you were asked and how you will know it is done well; if something is unclear, ask."),
				Evidence: ev}})
		}
	}
	var props []Proposal
	for _, c := range cands {
		if len(props) == maxProposals {
			break
		}
		p := c.p
		p.AgentID = a.ID
		if p.Evidence == nil {
			p.Evidence = []string{}
		}
		props = append(props, p)
	}
	e.Lines = retroLines(e, len(props), loc)
	return e, props
}

func firstN(in []string, n int) []string {
	if len(in) > n {
		in = in[:n]
	}
	return append([]string{}, in...)
}

func levelText(level, loc string) string {
	switch level {
	case quality.LevelExcellent:
		return tr(loc, "excelente", "excellent")
	case quality.LevelGood:
		return tr(loc, "bueno", "good")
	case quality.LevelFair:
		return tr(loc, "regular", "fair")
	case quality.LevelNeedsWork:
		return tr(loc, "necesita atención", "needs attention")
	}
	return level
}

func retroLines(e Entry, proposals int, loc string) []string {
	st := e.Retro
	week := tr(loc, fmt.Sprintf("Semana: %d tareas hechas, %d fallidas, %d aprobaciones rechazadas, %d retrabajos.", st.TasksDone, st.TasksFailed, st.Rejected, st.Reworks),
		fmt.Sprintf("Week: %d tasks done, %d failed, %d approvals rejected, %d reworks.", st.TasksDone, st.TasksFailed, st.Rejected, st.Reworks))
	if st.Score != nil {
		week += tr(loc, fmt.Sprintf(" Puntaje de calidad %d (%s).", *st.Score, levelText(st.Level, loc)), fmt.Sprintf(" Quality score %d (%s).", *st.Score, levelText(st.Level, loc)))
	}
	prop := tr(loc, "Sin propuestas: ninguna señal se repitió en la semana.", "No proposals: no signal repeated this week.")
	if proposals == 1 {
		prop = tr(loc, "Propongo 1 mejora a mi trabajo (pendiente de que una persona la acepte).", "I propose 1 improvement to my work (pending a person's decision).")
	} else if proposals > 1 {
		prop = tr(loc, fmt.Sprintf("Propongo %d mejoras a mi trabajo (pendientes de que una persona las acepte).", proposals),
			fmt.Sprintf("I propose %d improvements to my work (pending a person's decision).", proposals))
	}
	return []string{week, prop}
}

// ---- team summary and chat note ----

// deterministicSummary is the team summary written without a model.
func deterministicSummary(r Ritual, proposals int, loc string) string {
	present := len(r.Entries)
	if r.Kind == KindRetro {
		var done, failed, rejected, reworks int
		for _, e := range r.Entries {
			if e.Retro != nil {
				done, failed, rejected, reworks = done+e.Retro.TasksDone, failed+e.Retro.TasksFailed, rejected+e.Retro.Rejected, reworks+e.Retro.Reworks
			}
		}
		return tr(loc, fmt.Sprintf("Retrospectiva semanal con %d agentes: %d tareas hechas, %d fallidas, %d aprobaciones rechazadas, %d retrabajos; %d propuestas de mejora pendientes de decisión.", present, done, failed, rejected, reworks, proposals),
			fmt.Sprintf("Weekly retrospective with %d agents: %d tasks done, %d failed, %d approvals rejected, %d reworks; %d improvement proposals awaiting a decision.", present, done, failed, rejected, reworks, proposals))
	}
	var done, next, blocked, quiet int
	for _, e := range r.Entries {
		done, next, blocked = done+realCount(e.Done), next+realCount(e.Next), blocked+realCount(e.Blockers)
		if e.Quiet {
			quiet++
		}
	}
	return tr(loc, fmt.Sprintf("Stand-up con %d agentes: %d tareas terminadas, %d por hacer, %d bloqueos; %d sin novedades.", present, done, next, blocked, quiet),
		fmt.Sprintf("Stand-up with %d agents: %d tasks finished, %d to do, %d blockers; %d with nothing new.", present, done, next, blocked, quiet))
}

// realCount counts the items of a list, expanding the "+N" marker.
func realCount(items []Item) int {
	n := 0
	for _, it := range items {
		if it.Status == "more" {
			var k int
			_, _ = fmt.Sscanf(it.Title, "+%d", &k)
			n += k
			continue
		}
		n++
	}
	return n
}

// chatNote is the message posted in the office chat.
func chatNote(r Ritual, loc string) string {
	var b strings.Builder
	if r.Kind == KindRetro {
		b.WriteString(tr(loc, "Retrospectiva semanal", "Weekly retrospective"))
	} else {
		b.WriteString(tr(loc, "Stand-up diario", "Daily stand-up"))
	}
	b.WriteString("\n" + r.Summary + "\n")
	var quiet []string
	for _, e := range r.Entries {
		if e.Quiet {
			quiet = append(quiet, e.Name)
			continue
		}
		b.WriteString("\n" + e.Name + ":\n")
		for _, l := range e.Lines {
			b.WriteString("- " + l + "\n")
		}
	}
	if len(quiet) > 0 {
		b.WriteString("\n" + tr(loc, "Sin novedades: ", "Nothing new: ") + strings.Join(quiet, ", ") + "\n")
	}
	if len(r.Absent) > 0 {
		names := make([]string, 0, len(r.Absent))
		for _, a := range r.Absent {
			names = append(names, a.Name)
		}
		b.WriteString(tr(loc, "En pausa (no participan): ", "Paused (not taking part): ") + strings.Join(names, ", ") + "\n")
	}
	return strings.TrimSpace(b.String())
}
