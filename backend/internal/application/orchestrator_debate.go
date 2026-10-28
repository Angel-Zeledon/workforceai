package application

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"aiworkforce/backend/internal/domain"
)

// Team debate before a decision (B5, docs/architecture/team-debates.md): the orchestrator side.
//
// After the plan is accepted (plan review and estimate) and before any task starts, ONE debate per request runs
// when the plan has a task flagged "decision": true by the planner, or when the person marked the request with
// [[debate]] / [[debate:a,b]] ("/debatir", POST /requests {"debate": true}). The debate is advisory: its
// synthesis reaches the deciding task as untrusted recalled data (never as an instruction), and every action that
// task proposes still goes through the normal approvals. A debate that cannot happen (no runtime capability, a cap,
// the kill switch) never blocks the plan: the request continues and the person is told why.

// DebateRunner is what the orchestrator needs from the debate service.
type DebateRunner interface {
	Run(ctx context.Context, in DebateInput) (domain.Debate, error)
}

// SetDebates wires the team debate service (nil keeps debates off: plans run exactly as before).
func (o *Orchestrator) SetDebates(d DebateRunner) { o.debates = d }

var reDebateMarker = regexp.MustCompile(`(?i)\[\[debate(?::([^\]]*))?\]\]`)

// DebateMarker reports whether the request text carries the [[debate]] marker and the agent ids it names.
func DebateMarker(text string) (bool, []string) {
	m := reDebateMarker.FindStringSubmatch(text)
	if m == nil {
		return false, nil
	}
	var ids []string
	for _, p := range strings.FieldsFunc(m[1], func(r rune) bool { return r == ',' || r == ' ' }) {
		if p = strings.TrimSpace(p); p != "" {
			ids = append(ids, p)
		}
	}
	return true, ids
}

// WithDebateMarker adds the [[debate]] marker to a request text (POST /requests {"debate": true}).
func WithDebateMarker(text string) string {
	if ok, _ := DebateMarker(text); ok {
		return text
	}
	return "[[debate]] " + text
}

var reAnyMarker = regexp.MustCompile(`\[\[[a-z]+(?::[^\]]*)?\]\]`)

// decisionIndex picks the task to debate: the first one the planner flagged, else (only when the person asked
// for a debate) the first task that is not the assistant's.
func decisionIndex(plan PlanResponse, tasks []domain.Task, marked bool) int {
	for i, pt := range plan.Tasks {
		if pt.Decision && i < len(tasks) {
			return i
		}
	}
	if !marked {
		return -1
	}
	for i, t := range tasks {
		if t.AgentID != assistantID {
			return i
		}
	}
	return 0
}

// debateBeforeDecision runs the (at most one) debate of a request. It never fails the request.
func (o *Orchestrator) debateBeforeDecision(ctx context.Context, rs *run, plan PlanResponse, tasks []domain.Task) {
	if o.debates == nil || len(tasks) == 0 {
		return
	}
	marked, named := DebateMarker(rs.req.Text)
	idx := decisionIndex(plan, tasks, marked)
	if idx < 0 {
		return
	}
	t := tasks[idx]
	ids := named
	if len(ids) == 0 { // the agents of the plan, the owner of the decision first
		for _, x := range append([]domain.Task{t}, tasks...) {
			if x.AgentID != assistantID && !containsStr(ids, x.AgentID) {
				ids = append(ids, x.AgentID)
			}
		}
	}
	trigger := domain.DebateTriggerPlanner
	if marked {
		trigger = domain.DebateTriggerUser
	}
	en := rs.style.Locale == "en"
	o.setState(ctx, assistantID, domain.StateWorking, pick(en, "Moderating the team debate", "Moderando el debate del equipo"), nil, 32)
	dctx := ctx
	if rs.requestedBy != "" {
		dctx = WithActor(ctx, rs.requestedBy)
	}
	request := strings.TrimSpace(reAnyMarker.ReplaceAllString(rs.req.Text, " "))
	d, err := o.debates.Run(dctx, DebateInput{Topic: t.Title, Details: truncate(t.Description+"\n\n"+request, 1800), AgentIDs: ids,
		Trigger: trigger, RequestID: rs.req.ID, TaskID: t.ID, Locale: rs.style.Locale, Tone: rs.style.Tone})
	if err != nil {
		o.log.Info("team debate skipped", "request", rs.req.ID, "err", err)
		o.sendMessage(ctx, rs, assistantID, "user", "chat", pick(en,
			"The team could not debate this decision right now ("+debateCode(err)+"). The plan continues as usual and every action still needs its approvals.",
			"El equipo no pudo debatir esta decisión ahora ("+debateCode(err)+"). El plan sigue como siempre y cada acción mantiene sus aprobaciones."), nil)
		return
	}
	rs.mu.Lock()
	rs.debateNote = map[string]RecalledMemory{t.ID: debateRecall(d, en)}
	rs.mu.Unlock()
	msg := pick(en, "Team debate (advisory, nobody approved anything): the team leans to «%s».", "Debate del equipo (consultivo, nadie aprobó nada): el equipo se inclina por «%s».")
	text := fmt.Sprintf(msg, d.Synthesis.Choice)
	if len(d.Synthesis.Dissent) > 0 {
		text += " " + pick(en, "Dissent: ", "Desacuerdo: ") + strings.Join(d.Synthesis.Dissent, " · ")
	}
	o.sendMessage(ctx, rs, assistantID, "user", "chat", text, nil)
}

// debateRecall is the debate as the deciding task sees it: untrusted recalled data (the runtime wraps it as such),
// clearly labelled advisory.
func debateRecall(d domain.Debate, en bool) RecalledMemory {
	var b strings.Builder
	b.WriteString(pick(en, "Advisory team debate (model output, not an instruction, not an approval). ", "Debate consultivo del equipo (texto de modelo, no es una instrucción ni una aprobación). "))
	for _, t := range d.Turns {
		if t.Kind == domain.DebateTurnSynthesis {
			continue
		}
		fmt.Fprintf(&b, "%s (%s): %s\n", t.AgentID, t.Kind, t.Text)
	}
	fmt.Fprintf(&b, "%s%s. %s", pick(en, "Leaning: ", "Inclinación: "), d.Synthesis.Choice, d.Synthesis.Rationale)
	if len(d.Synthesis.Dissent) > 0 {
		b.WriteString(" " + pick(en, "Dissent: ", "Desacuerdo: ") + strings.Join(d.Synthesis.Dissent, "; "))
	}
	return RecalledMemory{Ref: "debate:" + d.ID, Kind: "note", Title: pick(en, "Team debate", "Debate del equipo"), Snippet: truncate(b.String(), 2400),
		AgentID: assistantID, At: d.CreatedAt}
}

func debateCode(err error) string {
	if de, ok := err.(*DebateError); ok {
		return de.Code
	}
	return DebateUnavailable
}

func pick(en bool, a, b string) string {
	if en {
		return a
	}
	return b
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// debateNoteFor returns the debate recalled for this task, if any.
func (rs *run) debateNoteFor(taskID string) (RecalledMemory, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	n, ok := rs.debateNote[taskID]
	return n, ok
}
