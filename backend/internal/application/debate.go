package application

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/sanitize"
	"aiworkforce/backend/internal/textfold"
)

// Team debates (B5, docs/architecture/team-debates.md).
//
// Before a high-impact decision 2 to 4 relevant agents exchange a bounded number of short turns (positions and
// objections) and the assistant closes with a synthesis: the option the team leans to, why, and the dissent. The
// debate is ONE runtime call (POST /v1/debate) with a hard cap on turns and a hard cap on cost, inside the usual
// spend caps (agent + organization, via the planner reservation). It is advisory and untrusted: the transcript is
// model output, it is stored and shown as such, and nothing here approves, rejects or executes anything. The
// decision it informs still goes through the normal approvals.
//
// Triggers (testable, nothing heuristic in the backend):
//   - planner: a task of the plan carries "decision": true (the planner flags at most one high-impact decision);
//     the orchestrator runs ONE debate per request, before the tasks start (orchestrator_debate.go);
//   - user: the request text carries the [[debate]] / [[debate:a,b]] marker (sent by "/debatir" or by
//     POST /requests {"debate": true}), or a person calls POST /debates directly.

const (
	DebateMinParticipants = 2
	DebateMaxParticipants = 4
	DebateMaxOptions      = 4
	defaultDebateTurns    = 8
	minDebateTurns        = 3
	maxDebateTurns        = 12
	defaultDebateCostUSD  = 0.10
	maxDebateTopic        = 300
	maxDebateDetails      = 2000
	maxDebateOption       = 160
	maxDebateTurnText     = 400
)

// UsageDebate is one team debate (B5); priced and recorded like a planner call.
const UsageDebate domain.UsageKind = "debate"

// EvDebateStarted / Completed / Failed drive the office (participants walk to the meeting room, bubbles cycle
// through the turns, then they walk back) and the meetings view.
const (
	EvDebateStarted   = "debate.started"
	EvDebateCompleted = "debate.completed"
	EvDebateFailed    = "debate.failed"
)

// Codes of a debate that could not happen (DebateError.Code; also domain.Debate.FailCode).
const (
	DebateUnavailable = "unavailable"     // no runtime capability, runtime error or empty answer
	DebateBlocked     = "blocked"         // kill switch, controls unavailable, or fewer than 2 participants not paused
	DebateOverBudget  = "budget_exceeded" // the per-debate cost cap or a spending cap (agent, organization) does not allow it
)

// DebateError means the debate could not be produced. It never blocks the work it was meant to inform.
type DebateError struct {
	Code   string
	Err    error
	Debate domain.Debate // the stored failed row (ID set) when there was one
}

func (e *DebateError) Error() string {
	if e.Err == nil {
		return "debate " + e.Code
	}
	return "debate " + e.Code + ": " + e.Err.Error()
}
func (e *DebateError) Unwrap() error { return e.Err }

// ---- runtime contract (POST /v1/debate, optional capability) ----

// DebateSpeaker is a participant as the runtime sees it: no secrets, no connection ids, no memory.
type DebateSpeaker struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Role  string `json:"role"`
	Title string `json:"title,omitempty"`
	Area  string `json:"area,omitempty"`
}

type DebateRequest struct {
	Topic        string          `json:"topic"`
	Details      string          `json:"details,omitempty"`
	Options      []string        `json:"options,omitempty"`
	Participants []DebateSpeaker `json:"participants"`
	Moderator    DebateSpeaker   `json:"moderator"`
	MaxTurns     int             `json:"max_turns"`
	Locale       string          `json:"locale,omitempty"`
	Tone         string          `json:"tone,omitempty"`
	// MaxCostUSD is the hard cost cap of this debate: the runtime refuses the call when its estimate is above.
	MaxCostUSD float64 `json:"max_cost_usd,omitempty"`
}

type DebateTurnOut struct {
	AgentID string `json:"agent_id"`
	Kind    string `json:"kind"`
	Text    string `json:"text"`
}

type DebateResponse struct {
	Options   []string               `json:"options"`
	Turns     []DebateTurnOut        `json:"turns"`
	Synthesis domain.DebateSynthesis `json:"synthesis"`
	Usage     *Usage                 `json:"usage,omitempty"`
	Provider  string                 `json:"provider,omitempty"`
	Model     string                 `json:"model,omitempty"`
}

// DebateRuntime is the optional runtime capability. A runtime without it simply has no debates.
type DebateRuntime interface {
	Debate(ctx context.Context, in DebateRequest) (DebateResponse, error)
}

// DebateStore keeps the debates (memory and Postgres stores implement it; migration 799).
type DebateStore interface {
	CreateDebate(ctx context.Context, orgID string, d domain.Debate) error
	GetDebate(ctx context.Context, orgID, id string) (domain.Debate, error)
	ListDebates(ctx context.Context, orgID, requestID string, limit int) ([]domain.Debate, error)
}

// DebateInput is what a caller asks for.
type DebateInput struct {
	Topic   string
	Details string
	Options []string
	// AgentIDs are the participants. Empty: the most relevant agents for the topic. For the planner trigger they
	// are the agents of the plan (the owner of the decision first).
	AgentIDs  []string
	Trigger   string
	RequestID string
	TaskID    string
	Locale    string
	Tone      string
}

// Debates runs and lists team debates.
type Debates struct {
	Store  DebateStore
	Agents interface {
		ListAgents(ctx context.Context, orgID string) ([]domain.Agent, error)
	}
	// Runtime is type-asserted to DebateRuntime.
	Runtime any
	// Budget is the orchestrator (kill switch / pause guard, planner reservation, ledger). Same contract as B8.
	Budget CounterargumentBudget
	Rec    *Recorder
	Cfg    Config
	// Locale returns the organization language ("es" when nil).
	Locale func(ctx context.Context) string
	Now    func() time.Time
	// MaxTurns caps the turns of one debate, synthesis included (DEBATE_MAX_TURNS; default 8, clamped 3..12).
	MaxTurns int
	// MaxCostUSD caps the cost of one debate (DEBATE_MAX_COST_USD; default 0.10).
	MaxCostUSD float64
}

func (s *Debates) org(ctx context.Context) string { return OrgFrom(ctx, s.Cfg.OrgID) }

func (s *Debates) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// TurnCap is the effective cap on turns (synthesis included).
func (s *Debates) TurnCap() int {
	n := s.MaxTurns
	if n <= 0 {
		n = defaultDebateTurns
	}
	return min(max(n, minDebateTurns), maxDebateTurns)
}

// CostCap is the effective per-debate cost cap in USD.
func (s *Debates) CostCap() float64 {
	if s.MaxCostUSD > 0 {
		return s.MaxCostUSD
	}
	return defaultDebateCostUSD
}

// NormalizeDebate makes the stored shape stable: never-nil lists and the advisory / untrusted flags.
func NormalizeDebate(d *domain.Debate) {
	if d.Options == nil {
		d.Options = []string{}
	}
	if d.Participants == nil {
		d.Participants = []string{}
	}
	if d.Turns == nil {
		d.Turns = []domain.DebateTurn{}
	}
	if d.Synthesis.Dissent == nil {
		d.Synthesis.Dissent = []string{}
	}
	d.Advisory, d.Untrusted = true, true
}

// Get returns one debate of the organization.
func (s *Debates) Get(ctx context.Context, id string) (domain.Debate, error) {
	return s.Store.GetDebate(ctx, s.org(ctx), id)
}

// List returns the newest debates (optionally of one request); limit is clamped to 1..100 (default 30).
func (s *Debates) List(ctx context.Context, requestID string, limit int) ([]domain.Debate, error) {
	if limit <= 0 {
		limit = 30
	}
	return s.Store.ListDebates(ctx, s.org(ctx), strings.TrimSpace(requestID), min(limit, 100))
}

// Run holds one debate and stores it. A person asking (trigger "user_request") must not be an agent: agents can
// neither start a debate on their own nor read its synthesis as a decision.
func (s *Debates) Run(ctx context.Context, in DebateInput) (domain.Debate, error) {
	actor := ActorFrom(ctx, "user")
	if in.Trigger == "" {
		in.Trigger = domain.DebateTriggerUser
	}
	if in.Trigger != domain.DebateTriggerUser && in.Trigger != domain.DebateTriggerPlanner {
		return domain.Debate{}, fmt.Errorf("%w: unknown trigger", domain.ErrInvalid)
	}
	if in.Trigger == domain.DebateTriggerUser && isAgentPrincipal(actor, "") {
		return domain.Debate{}, fmt.Errorf("%w: only people can ask for a debate", domain.ErrForbidden)
	}
	if in.Trigger == domain.DebateTriggerPlanner {
		actor = assistantID
	}
	topic := cleanReview(sanitize.RedactSecrets(in.Topic, nil), maxDebateTopic)
	if topic == "" {
		return domain.Debate{}, fmt.Errorf("%w: topic is required", domain.ErrInvalid)
	}
	options, err := cleanOptions(in.Options)
	if err != nil {
		return domain.Debate{}, err
	}
	org := s.org(ctx)
	agents, err := s.Agents.ListAgents(ctx, org)
	if err != nil {
		return domain.Debate{}, err
	}
	picked, err := pickDebaters(agents, in.AgentIDs, topic+" "+in.Details, in.Trigger == domain.DebateTriggerUser && len(in.AgentIDs) > 0)
	if err != nil {
		return domain.Debate{}, err
	}
	locale := in.Locale
	if locale == "" && s.Locale != nil {
		locale = s.Locale(ctx)
	}
	if locale == "" {
		locale = "es"
	}
	d := domain.Debate{ID: newID(), Topic: topic, Trigger: in.Trigger, RequestID: in.RequestID, TaskID: in.TaskID, Options: options,
		MaxTurns: s.TurnCap(), Locale: locale, CreatedBy: actor, CreatedAt: s.now()}
	for _, a := range picked {
		d.Participants = append(d.Participants, a.ID)
	}

	// Guard: kill switch / controls block the whole debate; a paused agent sits it out.
	if s.Budget != nil {
		if v := s.Budget.AdmitReview(ctx, assistantID); !v.Allowed {
			return domain.Debate{}, s.fail(ctx, d, DebateBlocked, errors.New(v.Code))
		}
		kept := picked[:0]
		for _, a := range picked {
			v := s.Budget.AdmitReview(ctx, a.ID)
			if !v.Allowed && v.Code != "agent_paused" {
				return domain.Debate{}, s.fail(ctx, d, DebateBlocked, errors.New(v.Code))
			}
			if v.Allowed {
				kept = append(kept, a)
			}
		}
		picked = kept
		d.Participants = d.Participants[:0]
		for _, a := range picked {
			d.Participants = append(d.Participants, a.ID)
		}
		if len(picked) < DebateMinParticipants {
			return domain.Debate{}, s.fail(ctx, d, DebateBlocked, errors.New("fewer than 2 participants available"))
		}
	}
	rt, ok := s.Runtime.(DebateRuntime)
	if !ok || rt == nil {
		return domain.Debate{}, s.fail(ctx, d, DebateUnavailable, errors.New("runtime without the capability"))
	}
	req := DebateRequest{Topic: topic, Details: cleanReview(sanitize.RedactSecrets(in.Details, nil), maxDebateDetails), Options: options,
		Moderator: DebateSpeaker{ID: assistantID, Name: "", Role: "assistant"}, MaxTurns: d.MaxTurns, Locale: locale, Tone: in.Tone,
		MaxCostUSD: s.CostCap()}
	for _, a := range agents {
		if a.ID == assistantID {
			req.Moderator = DebateSpeaker{ID: a.ID, Name: a.Name, Role: a.Role, Title: a.Title}
		}
	}
	for _, a := range picked {
		req.Participants = append(req.Participants, DebateSpeaker{ID: a.ID, Name: a.Name, Role: a.Role, Title: a.Title, Area: areaOf(a.Role, locale)})
	}
	// Hard cost cap: the worst case of the call must fit in it before anything is reserved.
	if est := callEstimateUSD("", UsageDebate, req); est > req.MaxCostUSD+1e-9 {
		return domain.Debate{}, s.fail(ctx, d, DebateOverBudget, fmt.Errorf("estimate %.4f above the debate cap %.4f", est, req.MaxCostUSD))
	}
	var res *Reservation
	if s.Budget != nil {
		var ex *Exceeded
		if res, ex = s.Budget.ReservePlanner(ctx, UsageDebate, req); ex != nil {
			return domain.Debate{}, s.fail(ctx, d, DebateOverBudget, fmt.Errorf("%s cap", ex.Scope))
		}
	}
	s.Rec.Emit(ctx, Action{Type: EvDebateStarted, AgentID: assistantID, Entity: "debate", EntityID: d.ID, SkipAudit: true,
		Payload: map[string]any{"debate_id": d.ID, "topic": d.Topic, "participants": d.Participants, "trigger": d.Trigger,
			"request_id": d.RequestID, "task_id": d.TaskID, "max_turns": d.MaxTurns}})
	resp, err := rt.Debate(ctx, req)
	usage := Usage{}
	if resp.Usage != nil {
		usage = *resp.Usage
	}
	if s.Budget != nil {
		s.Budget.NoteProviderNoBalance(ctx, assistantID, err)
		s.Budget.RecordPlannerUsage(ctx, usage, res) // also releases the reservation when the call failed
	}
	if err != nil {
		return domain.Debate{}, s.fail(ctx, d, DebateUnavailable, err)
	}
	d.Turns, d.Synthesis = normalizeTurns(resp, d.Participants, d.MaxTurns)
	if len(d.Turns) < 2 { // at least one position and the synthesis
		return domain.Debate{}, s.fail(ctx, d, DebateUnavailable, errors.New("empty debate"))
	}
	if len(d.Options) == 0 {
		d.Options, _ = cleanOptions(resp.Options)
		if len(d.Options) > DebateMaxOptions {
			d.Options = d.Options[:DebateMaxOptions]
		}
	}
	d.Status = domain.DebateDone
	d.Model, d.Provider, d.CostUSD = firstNonEmpty(resp.Model, usage.Model), firstNonEmpty(resp.Provider, usage.Provider), usage.CostUSD
	NormalizeDebate(&d)
	if err := s.Store.CreateDebate(ctx, org, d); err != nil {
		return domain.Debate{}, err
	}
	s.Rec.Audit(ctx, domain.AuditLog{Actor: actor, Action: "debate.completed", Entity: "debate", EntityID: d.ID, RequestID: d.RequestID,
		Details: map[string]any{"trigger": d.Trigger, "task_id": d.TaskID, "participants": d.Participants, "turns": len(d.Turns),
			"max_turns": d.MaxTurns, "model": d.Model, "cost_usd": d.CostUSD, "advisory": true}})
	s.Rec.Emit(ctx, Action{Type: EvDebateCompleted, AgentID: assistantID, Entity: "debate", EntityID: d.ID, SkipAudit: true,
		Payload: map[string]any{"debate": d}})
	return d, nil
}

// fail stores the failed attempt (so the meetings view can say why), audits it and tells the office.
func (s *Debates) fail(ctx context.Context, d domain.Debate, code string, cause error) error {
	d.Status, d.FailCode, d.Turns = domain.DebateFailed, code, nil
	NormalizeDebate(&d)
	if err := s.Store.CreateDebate(ctx, s.org(ctx), d); err != nil {
		d.ID = ""
	}
	s.Rec.Audit(ctx, domain.AuditLog{Actor: d.CreatedBy, Action: "debate.failed", Entity: "debate", EntityID: d.ID, RequestID: d.RequestID,
		Details: map[string]any{"trigger": d.Trigger, "task_id": d.TaskID, "reason": code}})
	s.Rec.Emit(ctx, Action{Type: EvDebateFailed, AgentID: assistantID, Entity: "debate", EntityID: d.ID, SkipAudit: true,
		Payload: map[string]any{"debate_id": d.ID, "participants": d.Participants, "code": code, "request_id": d.RequestID}})
	return &DebateError{Code: code, Err: cause, Debate: d}
}

// cleanOptions trims, de-duplicates and bounds the options (more than 4 is an error).
func cleanOptions(in []string) ([]string, error) {
	out := []string{}
	for _, o := range in {
		o = cleanReview(sanitize.RedactSecrets(o, nil), maxDebateOption)
		if o != "" && !slices.Contains(out, o) {
			out = append(out, o)
		}
	}
	if len(out) > DebateMaxOptions {
		return nil, fmt.Errorf("%w: at most %d options", domain.ErrInvalid, DebateMaxOptions)
	}
	return out, nil
}

// pickDebaters resolves the participants: the named agents (strict: unknown or too many is an error when the
// person named them), else the most relevant agents for the text, topped up in the organization's order. The
// assistant moderates and never debates.
func pickDebaters(agents []domain.Agent, ids []string, text string, strict bool) ([]domain.Agent, error) {
	byID := map[string]domain.Agent{}
	for _, a := range agents {
		byID[a.ID] = a
	}
	var out []domain.Agent
	seen := map[string]bool{assistantID: true}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		a, ok := byID[id]
		if !ok || id == assistantID {
			if strict {
				return nil, fmt.Errorf("%w: unknown participant %q", domain.ErrInvalid, id)
			}
			continue
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, a)
		}
	}
	if strict && len(out) > DebateMaxParticipants {
		return nil, fmt.Errorf("%w: at most %d participants", domain.ErrInvalid, DebateMaxParticipants)
	}
	if strict && len(out) < DebateMinParticipants {
		return nil, fmt.Errorf("%w: at least %d participants", domain.ErrInvalid, DebateMinParticipants)
	}
	if len(out) > DebateMaxParticipants {
		out = out[:DebateMaxParticipants]
	}
	if len(out) >= 3 || (strict && len(out) >= DebateMinParticipants) {
		return out, nil
	}
	// Top up with the agents whose profile shares the most words with the topic, then in the organization's order.
	words := topicWords(text)
	type cand struct {
		a     domain.Agent
		score int
		idx   int
	}
	var cands []cand
	for i, a := range agents {
		if seen[a.ID] {
			continue
		}
		profile := topicWords(strings.Join(append([]string{a.Role, a.Title, a.Description}, a.Responsibilities...), " "))
		score := 0
		for w := range words {
			if profile[w] {
				score++
			}
		}
		cands = append(cands, cand{a, score, i})
	}
	slices.SortStableFunc(cands, func(x, y cand) int { return y.score - x.score })
	want := 3
	for _, c := range cands {
		if len(out) >= want || (len(out) >= DebateMinParticipants && c.score == 0) {
			break
		}
		out = append(out, c.a)
	}
	if len(out) < DebateMinParticipants {
		return nil, fmt.Errorf("%w: the organization needs at least %d agents besides the assistant", domain.ErrInvalid, DebateMinParticipants)
	}
	return out, nil
}

func topicWords(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(textfold.Fold(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(w)) >= 4 {
			out[w] = true
		}
	}
	return out
}

// normalizeTurns keeps what the runtime said within the contract: positions and objections only from the
// participants, one synthesis from the moderator at the end, at most maxTurns turns in all, every text collapsed,
// bounded and redacted once more. The synthesis turn is rebuilt from the synthesis object when missing.
func normalizeTurns(resp DebateResponse, participants []string, maxTurns int) ([]domain.DebateTurn, domain.DebateSynthesis) {
	syn := domain.DebateSynthesis{Choice: cleanReview(sanitize.RedactSecrets(resp.Synthesis.Choice, nil), maxDebateOption),
		Rationale: cleanReview(sanitize.RedactSecrets(resp.Synthesis.Rationale, nil), 600), Dissent: []string{}}
	for _, x := range cleanReviewList(resp.Synthesis.Dissent) {
		if len(syn.Dissent) < DebateMaxParticipants {
			syn.Dissent = append(syn.Dissent, sanitize.RedactSecrets(x, nil))
		}
	}
	turns := []domain.DebateTurn{}
	synText := ""
	for _, t := range resp.Turns {
		text := cleanReview(sanitize.RedactSecrets(t.Text, nil), maxDebateTurnText)
		if text == "" {
			continue
		}
		switch t.Kind {
		case domain.DebateTurnSynthesis:
			if t.AgentID == assistantID && synText == "" {
				synText = text
			}
		case domain.DebateTurnPosition, domain.DebateTurnObjection:
			if slices.Contains(participants, t.AgentID) && len(turns) < maxTurns-1 {
				turns = append(turns, domain.DebateTurn{Seq: len(turns) + 1, AgentID: t.AgentID, Kind: t.Kind, Text: text})
			}
		}
	}
	if synText == "" {
		synText = strings.TrimSpace(strings.TrimSpace(syn.Choice + ". " + syn.Rationale))
		synText = strings.Trim(synText, ". ")
	}
	if len(turns) > 0 && synText != "" {
		turns = append(turns, domain.DebateTurn{Seq: len(turns) + 1, AgentID: assistantID, Kind: domain.DebateTurnSynthesis, Text: cleanReview(synText, maxDebateTurnText)})
	}
	return turns, syn
}
