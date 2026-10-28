package application

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/policy"
	"aiworkforce/backend/internal/sanitize"
)

// Rehearsal (B9, "Ensayo general"; docs/architecture/rehearsal.md).
//
// A rehearsal runs the work for real (planning, reads, reasoning, reports) but
// every side-effect tool call is recorded as a "would have done" entry and NOT
// executed: no approval is requested, nothing is sent, paid, scheduled or
// written. A person then marks each entry "would approve" / "would reject",
// which is the evidence for raising (or not) the agent's autonomy.
//
// Two ways in: the per-agent switch (PUT /agents/{id}/rehearsal) and a
// rehearsal request (POST /requests {"rehearsal": true}). The orchestrator
// stops the calls before they reach any executor; the Tool Gateway enforces it
// again on its own (RehearsalPolicy, fail closed) so that an approval, the
// outbox or a held send can never turn a rehearsal into a real effect.

// Events of the feature.
const (
	EvRehearsalRecorded     = "rehearsal.recorded"
	EvRehearsalAgentChanged = "rehearsal.agent_changed"
	EvRehearsalVerdict      = "rehearsal.verdict"
)

// Scopes and sources of a RehearsalAction.
const (
	RehearsalScopeRequest = "request"
	RehearsalScopeAgent   = "agent"

	RehearsalSourceGateway   = "gateway"
	RehearsalSourceSimulated = "simulated"
	RehearsalSourceCode      = "code"
)

// RehearsalStore persists the per-agent switches and the rehearsed actions.
type RehearsalStore interface {
	// GetAgentRehearsal returns domain.ErrNotFound when the agent never had a switch (off).
	GetAgentRehearsal(ctx context.Context, orgID, agentID string) (domain.AgentRehearsal, error)
	ListAgentRehearsals(ctx context.Context, orgID string) ([]domain.AgentRehearsal, error)
	PutAgentRehearsal(ctx context.Context, orgID string, r domain.AgentRehearsal) error
	AddRehearsalAction(ctx context.Context, orgID string, a domain.RehearsalAction) error
	GetRehearsalAction(ctx context.Context, orgID, id string) (domain.RehearsalAction, error)
	ListRehearsalActions(ctx context.Context, orgID string, q domain.RehearsalQuery) ([]domain.RehearsalAction, error)
	SetRehearsalVerdict(ctx context.Context, orgID, id, verdict, by string, at time.Time) (domain.RehearsalAction, error)
}

// RehearsalService owns the switches, the log and the human verdicts. It also
// implements RehearsalPolicy for the Tool Gateway.
type RehearsalService struct {
	Store RehearsalStore
	// Runs (optional) tells whether a request runs as a rehearsal after a
	// restart or on another replica (RunMeta.Rehearsal).
	Runs     RunStore
	Rec      *Recorder
	Cfg      Config
	Suspects *sanitize.Suspects
	Now      func() time.Time

	mu       sync.Mutex
	requests map[string]bool // org/request -> runs as a rehearsal (this process)
}

var _ RehearsalPolicy = (*RehearsalService)(nil)

func (s *RehearsalService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *RehearsalService) org(ctx context.Context) string { return OrgFrom(ctx, s.Cfg.OrgID) }

// MarkRequest remembers that a request runs as a rehearsal (the gateway asks).
func (s *RehearsalService) MarkRequest(org, requestID string) {
	if s == nil || requestID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.requests == nil || len(s.requests) > 5000 {
		s.requests = map[string]bool{}
	}
	s.requests[org+"/"+requestID] = true
}

// AgentEnabled reports the agent's switch (ErrNotFound = off). Errors are returned: callers fail closed.
func (s *RehearsalService) AgentEnabled(ctx context.Context, org, agentID string) (bool, error) {
	if s == nil || s.Store == nil || agentID == "" {
		return false, nil
	}
	r, err := s.Store.GetAgentRehearsal(ctx, org, agentID)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return r.Enabled, nil
}

// Rehearsing implements RehearsalPolicy: the agent's switch or a rehearsal
// request. An error means unknown; the gateway then refuses the write.
func (s *RehearsalService) Rehearsing(ctx context.Context, org, agentID, requestID string) (bool, error) {
	if s == nil {
		return false, nil
	}
	if requestID != "" {
		s.mu.Lock()
		marked := s.requests[org+"/"+requestID]
		s.mu.Unlock()
		if marked {
			return true, nil
		}
	}
	on, err := s.AgentEnabled(ctx, org, agentID)
	if err != nil || on {
		return true, err
	}
	if requestID != "" && s.Runs != nil {
		m, err := s.Runs.GetRunMeta(ctx, org, requestID)
		switch {
		case errors.Is(err, domain.ErrNotFound):
		case err != nil:
			return true, err
		case m.Rehearsal:
			return true, nil
		}
	}
	return false, nil
}

// SetAgent turns an agent's switch on or off (the API checks who may).
func (s *RehearsalService) SetAgent(ctx context.Context, agentID string, enabled bool, actor string) (domain.AgentRehearsal, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return domain.AgentRehearsal{}, fmt.Errorf("%w: agent id is required", domain.ErrInvalid)
	}
	org := s.org(ctx)
	r := domain.AgentRehearsal{AgentID: agentID, Enabled: enabled, SetBy: actor, UpdatedAt: s.now()}
	if err := s.Store.PutAgentRehearsal(ctx, org, r); err != nil {
		return domain.AgentRehearsal{}, err
	}
	if s.Rec != nil {
		s.Rec.Emit(ctx, Action{Type: EvRehearsalAgentChanged, AgentID: agentID, Entity: "agent", EntityID: agentID,
			Payload: map[string]any{"agent_id": agentID, "enabled": enabled, "by": actor}})
	}
	return r, nil
}

// AgentSummary is an agent's switch plus the evidence its rehearsals produced.
type AgentSummary struct {
	AgentID      string     `json:"agent_id"`
	Enabled      bool       `json:"enabled"`
	SetBy        string     `json:"set_by,omitempty"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
	Actions      int        `json:"actions"`
	WouldApprove int        `json:"would_approve"`
	WouldReject  int        `json:"would_reject"`
	Undecided    int        `json:"undecided"`
}

// VerdictCounts is the tally of human verdicts over a list of rehearsed actions.
type VerdictCounts struct {
	Total        int `json:"total"`
	WouldApprove int `json:"would_approve"`
	WouldReject  int `json:"would_reject"`
	Undecided    int `json:"undecided"`
}

// Count tallies the verdicts of as.
func Count(as []domain.RehearsalAction) VerdictCounts {
	c := VerdictCounts{Total: len(as)}
	for _, a := range as {
		switch a.Verdict {
		case domain.VerdictWouldApprove:
			c.WouldApprove++
		case domain.VerdictWouldReject:
			c.WouldReject++
		default:
			c.Undecided++
		}
	}
	return c
}

const maxRehearsalList = 500

// Agents lists every agent that has a switch or rehearsed actions, with its
// evidence. This is what a trust promotion (B1) can read: rehearsed actions a
// human said they would approve vs reject.
func (s *RehearsalService) Agents(ctx context.Context) ([]AgentSummary, error) {
	org := s.org(ctx)
	rs, err := s.Store.ListAgentRehearsals(ctx, org)
	if err != nil {
		return nil, err
	}
	as, err := s.Store.ListRehearsalActions(ctx, org, domain.RehearsalQuery{Limit: maxRehearsalList})
	if err != nil {
		return nil, err
	}
	by := map[string]*AgentSummary{}
	get := func(id string) *AgentSummary {
		if by[id] == nil {
			by[id] = &AgentSummary{AgentID: id}
		}
		return by[id]
	}
	for _, r := range rs {
		x := get(r.AgentID)
		at := r.UpdatedAt
		x.Enabled, x.SetBy, x.UpdatedAt = r.Enabled, r.SetBy, &at
	}
	for _, a := range as {
		x := get(a.AgentID)
		c := Count([]domain.RehearsalAction{a})
		x.Actions++
		x.WouldApprove += c.WouldApprove
		x.WouldReject += c.WouldReject
		x.Undecided += c.Undecided
	}
	out := make([]AgentSummary, 0, len(by))
	for _, x := range by {
		out = append(out, *x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	return out, nil
}

// List returns rehearsed actions, newest first.
func (s *RehearsalService) List(ctx context.Context, q domain.RehearsalQuery) ([]domain.RehearsalAction, error) {
	if q.Limit <= 0 || q.Limit > maxRehearsalList {
		q.Limit = maxRehearsalList
	}
	as, err := s.Store.ListRehearsalActions(ctx, s.org(ctx), q)
	if as == nil {
		as = []domain.RehearsalAction{}
	}
	return as, err
}

// RequestReport is the "Ensayo" report of one request.
type RequestReport struct {
	RequestID string                   `json:"request_id"`
	Rehearsal bool                     `json:"rehearsal"` // the whole request ran as a rehearsal
	Actions   []domain.RehearsalAction `json:"actions"`
	Summary   VerdictCounts            `json:"summary"`
}

// Report builds the rehearsal report of a request.
func (s *RehearsalService) Report(ctx context.Context, requestID string) (RequestReport, error) {
	as, err := s.List(ctx, domain.RehearsalQuery{RequestID: requestID})
	if err != nil {
		return RequestReport{}, err
	}
	rep := RequestReport{RequestID: requestID, Actions: as, Summary: Count(as)}
	s.mu.Lock()
	rep.Rehearsal = s.requests[s.org(ctx)+"/"+requestID]
	s.mu.Unlock()
	if !rep.Rehearsal && s.Runs != nil {
		if m, err := s.Runs.GetRunMeta(ctx, s.org(ctx), requestID); err == nil {
			rep.Rehearsal = m.Rehearsal
		}
	}
	return rep, nil
}

// SetVerdict records how the human would have decided a rehearsed action
// ("" clears it). Agents never get here: the API is for people only.
func (s *RehearsalService) SetVerdict(ctx context.Context, id, verdict, actor string) (domain.RehearsalAction, error) {
	switch verdict {
	case domain.VerdictWouldApprove, domain.VerdictWouldReject, "":
	default:
		return domain.RehearsalAction{}, fmt.Errorf("%w: verdict must be would_approve, would_reject or empty", domain.ErrInvalid)
	}
	a, err := s.Store.SetRehearsalVerdict(ctx, s.org(ctx), id, verdict, actor, s.now())
	if err != nil {
		return domain.RehearsalAction{}, err
	}
	if s.Rec != nil {
		s.Rec.Emit(ctx, Action{Type: EvRehearsalVerdict, Entity: "rehearsal_action", EntityID: id,
			Payload: map[string]any{"id": id, "request_id": a.RequestID, "agent_id": a.AgentID, "tool": a.Tool, "action": a.Action,
				"verdict": verdict, "would_decision": a.WouldDecision, "by": actor}})
	}
	return a, nil
}

// ---- preview of the arguments ----

const (
	previewMaxKeys  = 12
	previewMaxChars = 160
)

var previewAddr = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)

// ArgsPreview is a short, sanitized view of a call's arguments: at most 12 keys,
// strings redacted (secrets) and truncated, nested values summarized. It is
// for the human reviewing the rehearsal; it never goes to an event.
func ArgsPreview(args map[string]any, sus *sanitize.Suspects) map[string]string {
	keys := make([]string, 0, len(args))
	for k := range args {
		if k != "connection_alias" {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	out := map[string]string{}
	for i, k := range keys {
		if i >= previewMaxKeys {
			out["…"] = fmt.Sprintf("+%d", len(keys)-previewMaxKeys)
			break
		}
		out[k] = previewValue(args[k], sus)
	}
	return out
}

func previewValue(v any, sus *sanitize.Suspects) string {
	inline := func(s string) string {
		return sanitize.SanitizeInline(sanitize.RedactSecrets(s, sus), previewMaxChars, sanitize.Options{Suspects: sus})
	}
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return inline(x)
	case bool, float64, float32, int, int64, int32:
		return fmt.Sprint(x)
	case []any:
		parts := make([]string, 0, len(x))
		for _, i := range x {
			switch y := i.(type) {
			case string, bool, float64, int:
				parts = append(parts, fmt.Sprint(y))
			default:
				parts = append(parts, "{…}")
			}
		}
		return inline(strings.Join(parts, ", "))
	case []string:
		return inline(strings.Join(x, ", "))
	case map[string]any:
		return fmt.Sprintf("{%d fields}", len(x))
	}
	return inline(fmt.Sprint(v))
}

func recipientsIn(args map[string]any) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, k := range []string{"to", "cc", "bcc", "attendees", "recipient", "recipients", "email"} {
		for _, a := range previewAddr.FindAllString(fmt.Sprint(args[k]), 20) {
			a = strings.ToLower(a)
			if !seen[a] {
				seen[a] = true
				out = append(out, a)
			}
		}
	}
	return out
}

// ---- orchestrator side ----

// SetRehearsal installs the rehearsal service (nil: off, the requests can still
// run as rehearsals: the orchestrator then only notes what it skipped).
func (o *Orchestrator) SetRehearsal(s *RehearsalService) { o.rehearsal = s }

type rehearsalKey struct{}

// WithRehearsal marks the request submitted under ctx as a rehearsal.
func WithRehearsal(ctx context.Context, on bool) context.Context {
	return context.WithValue(ctx, rehearsalKey{}, on)
}

// RehearsalFrom reports whether ctx asks for a rehearsal request.
func RehearsalFrom(ctx context.Context) bool {
	on, _ := ctx.Value(rehearsalKey{}).(bool)
	return on
}

func (r *run) rehearsalRequest() bool {
	r.ext.mu.Lock()
	defer r.ext.mu.Unlock()
	return r.ext.rehearsal
}

// rehearsalScope says whether a tool request of agent in rs must be rehearsed
// ("" = no). An unreadable switch rehearses (fail closed).
func (o *Orchestrator) rehearsalScope(ctx context.Context, rs *run, agent domain.Agent) string {
	if rs.rehearsalRequest() {
		return RehearsalScopeRequest
	}
	if o.rehearsal == nil {
		return ""
	}
	on, err := o.rehearsal.AgentEnabled(ctx, o.org(ctx), agent.ID)
	if err != nil {
		o.log.Warn("rehearsal switch unreadable: rehearsing", "agent", agent.ID, "err", err)
		return RehearsalScopeAgent
	}
	if on {
		return RehearsalScopeAgent
	}
	return ""
}

// rehearsalSideEffect classifies a simulated (not connection-backed) tool
// request for a rehearsal: well-known reads run as usual, anything else is
// rehearsed (unknown actions count as side effects, fail closed).
func (o *Orchestrator) rehearsalSideEffect(tr ToolRequest) bool {
	if g := o.conn.guard; g != nil {
		return g.IsSideEffect(tr.Tool, tr.Action)
	}
	switch strings.ToLower(strings.TrimSpace(tr.Action)) {
	case "list", "read", "get", "search", "query", "view", "summarize", "analyze", "calculate", "evaluate", "describe", "lookup", "find", "count", "check":
		return false
	}
	return true
}

// rehearseTool records what a side-effect tool request would have done and
// does NOT execute it. route is how it would have been served.
func (o *Orchestrator) rehearseTool(ctx context.Context, rs *run, t *domain.Task, agent domain.Agent, tr ToolRequest, route GatewayRoute, scope string) {
	a := domain.RehearsalAction{RequestID: rs.req.ID, TaskID: t.ID, AgentID: agent.ID, Tool: tr.Tool, Action: tr.Action, Scope: scope,
		Risk: strings.ToLower(tr.Risk), Recipients: recipientsIn(tr.Args), Flags: []string{}, ArgsHash: argsHashOf(tr)}
	if amt, ok := policy.MaxAmount(tr.Args); ok {
		a.Amount = &amt
	}
	pv := o.decideTool(ctx, rs, t, agent, tr) // audited like a real decision; never reserved
	switch {
	case isCodeRun(tr):
		a.Source, a.WouldDecision, a.Risk = RehearsalSourceCode, domain.WouldApprove, "high" // a code run is never auto-approved
		if pv.Denied() {
			a.WouldDecision, a.DenyReason = domain.WouldDeny, "policy:"+pv.RuleID
		}
	case route == RouteNone:
		a.Source = RehearsalSourceSimulated
		g := o.conn.guard
		switch {
		case g != nil && g.ToolBlocked(ctx, o.org(ctx), tr.Tool, tr.Action):
			a.WouldDecision, a.DenyReason = domain.WouldDeny, "tool_disabled"
		case g != nil && g.IsSideEffect(tr.Tool, tr.Action) && (rs.readOnlyRequest() || g.SideEffectsBlocked(ctx, o.org(ctx), agent.ID)):
			a.WouldDecision, a.DenyReason = domain.WouldDeny, "read_only_mode"
		case pv.Denied():
			a.WouldDecision, a.DenyReason = domain.WouldDeny, "policy:"+pv.RuleID
		case pv.NeedsApproval():
			a.WouldDecision = domain.WouldApprove
		default:
			a.WouldDecision = domain.WouldAllow
		}
	default:
		a.Source = RehearsalSourceGateway
		call := o.gatewayCall(ctx, rs, *t, agent, tr)
		call.Rehearsal = true
		out := o.conn.gw.Execute(ctx, call)
		o.applyPreview(&a, out)
		if pv.Denied() {
			a.WouldDecision, a.DenyReason = domain.WouldDeny, "policy:"+pv.RuleID
		} else if pv.NeedsApproval() && a.WouldDecision == domain.WouldAllow {
			a.WouldDecision = domain.WouldApprove
		}
	}
	if pv.RequiredApprovals >= 2 {
		a.Flags = append(a.Flags, "double_approval")
	}
	o.recordRehearsal(ctx, t, agent, a, tr.Args)
}

func (o *Orchestrator) applyPreview(a *domain.RehearsalAction, out GatewayOutcome) {
	p := out.Rehearsal
	if out.Decision != DecisionRehearsed || p == nil {
		// Defensive: the gateway must answer a rehearsal with a preview; anything
		// else is recorded as a denial and still nothing is executed here.
		a.WouldDecision, a.DenyReason = domain.WouldDeny, firstNonEmpty(out.DenyReason, "rehearsal_unavailable")
		return
	}
	a.WouldDecision, a.DenyReason, a.Account, a.Reversibility = p.WouldDecision, p.DenyReason, p.Account, p.Reversibility
	if p.Risk != "" {
		a.Risk = p.Risk
	}
	if len(p.Recipients) > 0 {
		a.Recipients = p.Recipients
	}
	a.Flags = append(a.Flags, p.Flags...)
}

// rehearsedByGateway records a call the gateway itself refused to execute
// because of a rehearsal the orchestrator did not know about (the switch was
// turned on while the task worked or waited for an approval).
func (o *Orchestrator) rehearsedByGateway(ctx context.Context, rs *run, t *domain.Task, agent domain.Agent, tr ToolRequest, out GatewayOutcome) {
	a := domain.RehearsalAction{RequestID: rs.req.ID, TaskID: t.ID, AgentID: agent.ID, Tool: tr.Tool, Action: tr.Action, Scope: RehearsalScopeAgent,
		Source: RehearsalSourceGateway, Risk: strings.ToLower(tr.Risk), Recipients: recipientsIn(tr.Args), Flags: []string{}, ArgsHash: argsHashOf(tr)}
	if amt, ok := policy.MaxAmount(tr.Args); ok {
		a.Amount = &amt
	}
	o.applyPreview(&a, out)
	o.recordRehearsal(ctx, t, agent, a, tr.Args)
}

// recordRehearsal stores the entry (with a sanitized preview of args, never the
// args themselves), audits it, emits rehearsal.recorded and notes the task.
func (o *Orchestrator) recordRehearsal(ctx context.Context, t *domain.Task, agent domain.Agent, a domain.RehearsalAction, args map[string]any) {
	a.ID, a.CreatedAt = newID(), time.Now().UTC()
	if a.Risk == "" {
		a.Risk = "medium"
	}
	var sus *sanitize.Suspects
	if o.rehearsal != nil {
		sus = o.rehearsal.Suspects
	}
	a.Preview = ArgsPreview(args, sus)
	stored := false
	if o.rehearsal != nil && o.rehearsal.Store != nil {
		if err := o.rehearsal.Store.AddRehearsalAction(ctx, o.org(ctx), a); err != nil {
			o.log.Warn("record rehearsed action", "err", err) // still not executed
		} else {
			stored = true
		}
	}
	o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: "tool.rehearsed", Entity: "task", EntityID: t.ID,
		Details: map[string]any{"tool": a.Tool, "action": a.Action, "would_decision": a.WouldDecision, "reason": a.DenyReason,
			"scope": a.Scope, "source": a.Source, "rehearsal_id": a.ID, "stored": stored, "arg_keys": previewKeys(a.Preview)}})
	if stored {
		o.rec.Emit(ctx, Action{Type: EvRehearsalRecorded, AgentID: agent.ID, Entity: "task", EntityID: t.ID, SkipAudit: true,
			Payload: map[string]any{"id": a.ID, "request_id": a.RequestID, "task_id": a.TaskID, "agent_id": a.AgentID, "tool": a.Tool,
				"action": a.Action, "would_decision": a.WouldDecision, "scope": a.Scope}})
	}
	o.traceEvent(ctx, t.ID, "tool_decision", map[string]any{"tool": a.Tool, "action": a.Action, "effect": "rehearsed", "rule_id": a.WouldDecision})
	noteTask(t, "Ensayo: %s.%s no se ejecutó (en real: %s)", a.Tool, a.Action, a.WouldDecision)
}

func previewKeys(p map[string]string) []string {
	ks := make([]string, 0, len(p))
	for k := range p {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}
