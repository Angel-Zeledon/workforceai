package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/sanitize"
)

// Devil's advocate (B8, docs/architecture/devils-advocate.md).
//
// Before a human decides a HIGH-RISK approval, the runtime argues the opposite position and lists what could go
// wrong. The review is advisory: this file never touches the approval (no Decide, no status change), the runtime
// answer has no verdict field and agents cannot call it as a decision. It is generated lazily, on demand, from the
// approval card only (already sanitized; redacted once more here), and cached per approval so it is neither
// regenerated nor charged twice. A failure of any kind degrades to a typed CounterargumentError: the approval
// itself keeps working.

// CounterargumentRequest is POST /v1/counterargument (optional runtime capability).
type CounterargumentRequest struct {
	Approval CounterargumentBrief `json:"approval"`
	Locale   string               `json:"locale,omitempty"`
	Tone     string               `json:"tone,omitempty"`
}

// CounterargumentBrief is the approval card as the human sees it. Never a secret, a connection id or message content
// that is not already on the card.
type CounterargumentBrief struct {
	Action            string `json:"action"`
	Tool              string `json:"tool,omitempty"`
	Title             string `json:"title"`
	Details           string `json:"details"`
	Risk              string `json:"risk"`
	AgentRole         string `json:"agent_role,omitempty"`
	RequiredApprovals int    `json:"required_approvals"`
}

type CounterargumentResponse struct {
	Counterargument string   `json:"counterargument"`
	Risks           []string `json:"risks"`
	Checks          []string `json:"checks"`
	Usage           *Usage   `json:"usage,omitempty"`
	Provider        string   `json:"provider,omitempty"`
	Model           string   `json:"model,omitempty"`
}

// CounterargumentRuntime is the optional runtime capability behind the review. A runtime without it simply has no
// review: the card shows "unavailable" and the approval is unaffected.
type CounterargumentRuntime interface {
	Counterargument(ctx context.Context, in CounterargumentRequest) (CounterargumentResponse, error)
}

// CounterargumentStore caches the review per approval (memory and Postgres stores implement it).
type CounterargumentStore interface {
	GetCounterargument(ctx context.Context, orgID, approvalID string) (domain.Counterargument, error)
	// PutCounterargument stores c unless the approval already has one; it returns the row that is stored (the
	// existing one when another generation won the race) and whether c was the one inserted.
	PutCounterargument(ctx context.Context, orgID string, c domain.Counterargument) (stored domain.Counterargument, inserted bool, err error)
}

// CounterargumentBudget is what the review needs from the orchestrator: the execution guard, the budget
// reservation sized from the real payload, the ledger and the "out of balance" banner (all existing mechanisms).
type CounterargumentBudget interface {
	AdmitReview(ctx context.Context, agentID string) GuardVerdict
	ReservePlanner(ctx context.Context, kind domain.UsageKind, payload any) (*Reservation, *Exceeded)
	RecordPlannerUsage(ctx context.Context, u Usage, res *Reservation)
	NoteProviderNoBalance(ctx context.Context, agentID string, err error) bool
}

// Codes of a degraded review (CounterargumentError.Code).
const (
	CounterargumentUnavailable = "unavailable"     // no runtime capability, runtime error or empty answer
	CounterargumentBlocked     = "blocked"         // kill switch, paused agent or controls unavailable
	CounterargumentOverBudget  = "budget_exceeded" // a spending cap (agent, organization) does not allow the call
)

// CounterargumentError means the review could not be produced now. It never affects the approval.
type CounterargumentError struct {
	Code string
	Err  error
}

func (e *CounterargumentError) Error() string {
	if e.Err == nil {
		return "counterargument " + e.Code
	}
	return "counterargument " + e.Code + ": " + e.Err.Error()
}
func (e *CounterargumentError) Unwrap() error { return e.Err }

// IsHighRiskApproval decides which approvals get a devil's advocate. It reads what the policy engine already
// decided and stored on the approval: the risk the runtime declared ("high") or a double approval (the policy
// engine only asks two people for high-risk actions). No heuristic of its own.
func IsHighRiskApproval(ap domain.Approval) bool {
	return strings.EqualFold(strings.TrimSpace(ap.Risk), "high") || ap.RequiredApprovals >= 2
}

const (
	maxBriefDetails = 4000
	maxBriefTitle   = 300
)

// DevilsAdvocate generates and caches the review.
type DevilsAdvocate struct {
	Store CounterargumentStore
	Core  interface {
		GetApproval(ctx context.Context, orgID, id string) (domain.Approval, error)
		GetAgent(ctx context.Context, orgID, id string) (domain.Agent, error)
	}
	// Runtime is type-asserted to CounterargumentRuntime.
	Runtime any
	Budget  CounterargumentBudget
	Rec     *Recorder
	Cfg     Config
	// Locale returns the organization language ("es" when nil).
	Locale func(ctx context.Context) string
	Now    func() time.Time

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (d *DevilsAdvocate) org(ctx context.Context) string { return OrgFrom(ctx, d.Cfg.OrgID) }

func (d *DevilsAdvocate) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}

// lockFor serializes generations of one approval, so two clicks cost one call.
func (d *DevilsAdvocate) lockFor(key string) *sync.Mutex {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.locks == nil {
		d.locks = map[string]*sync.Mutex{}
	}
	l := d.locks[key]
	if l == nil {
		l = &sync.Mutex{}
		d.locks[key] = l
	}
	return l
}

// Get returns the cached review of an approval (ErrNotFound when none was generated yet, or the approval does not
// belong to the organization).
func (d *DevilsAdvocate) Get(ctx context.Context, approvalID string) (domain.Counterargument, error) {
	org := d.org(ctx)
	if _, err := d.Core.GetApproval(ctx, org, approvalID); err != nil {
		return domain.Counterargument{}, err
	}
	c, err := d.Store.GetCounterargument(ctx, org, approvalID)
	if err != nil {
		return domain.Counterargument{}, err
	}
	c.Cached = true
	return c, nil
}

// Generate returns the review of a pending high-risk approval, producing it on the first call and serving the
// cache afterwards. locale "" uses the organization language.
func (d *DevilsAdvocate) Generate(ctx context.Context, approvalID, locale string) (domain.Counterargument, error) {
	actor := ActorFrom(ctx, "user")
	org := d.org(ctx)
	ap, err := d.Core.GetApproval(ctx, org, approvalID)
	if err != nil {
		return domain.Counterargument{}, err
	}
	if isAgentPrincipal(actor, ap.AgentID) {
		return domain.Counterargument{}, fmt.Errorf("%w: only humans can ask for a review", domain.ErrForbidden)
	}
	if c, err := d.Store.GetCounterargument(ctx, org, approvalID); err == nil {
		c.Cached = true
		return c, nil
	}
	if !IsHighRiskApproval(ap) {
		return domain.Counterargument{}, fmt.Errorf("%w: only high-risk approvals have a devil's advocate review", domain.ErrInvalid)
	}
	if ap.Status != domain.ApprovalPending {
		return domain.Counterargument{}, fmt.Errorf("%w: approval already %s", domain.ErrConflict, ap.Status)
	}
	l := d.lockFor(org + "/" + approvalID)
	l.Lock()
	defer l.Unlock()
	if c, err := d.Store.GetCounterargument(ctx, org, approvalID); err == nil { // another click finished first
		c.Cached = true
		return c, nil
	}
	rt, ok := d.Runtime.(CounterargumentRuntime)
	if !ok || rt == nil {
		return domain.Counterargument{}, d.degrade(ctx, ap, actor, CounterargumentUnavailable, errors.New("runtime without the capability"))
	}
	if d.Budget != nil {
		if v := d.Budget.AdmitReview(ctx, ap.AgentID); !v.Allowed {
			return domain.Counterargument{}, d.degrade(ctx, ap, actor, CounterargumentBlocked, errors.New(v.Code))
		}
	}
	req := d.request(ctx, org, ap, locale)
	var res *Reservation
	if d.Budget != nil {
		var ex *Exceeded
		if res, ex = d.Budget.ReservePlanner(ctx, UsageCounterargument, req); ex != nil {
			return domain.Counterargument{}, d.degrade(ctx, ap, actor, CounterargumentOverBudget, fmt.Errorf("%s cap", ex.Scope))
		}
	}
	resp, err := rt.Counterargument(ctx, req)
	usage := Usage{}
	if resp.Usage != nil {
		usage = *resp.Usage
	}
	if d.Budget != nil {
		d.Budget.NoteProviderNoBalance(ctx, assistantID, err)
		d.Budget.RecordPlannerUsage(ctx, usage, res) // also releases the reservation when the call failed
	}
	if err != nil {
		return domain.Counterargument{}, d.degrade(ctx, ap, actor, CounterargumentUnavailable, err)
	}
	text := cleanReview(resp.Counterargument, 1200)
	if text == "" {
		return domain.Counterargument{}, d.degrade(ctx, ap, actor, CounterargumentUnavailable, errors.New("empty answer"))
	}
	c := domain.Counterargument{ApprovalID: approvalID, Counterargument: text,
		Risks: cleanReviewList(resp.Risks), Checks: cleanReviewList(resp.Checks), Locale: req.Locale,
		Model: firstNonEmpty(resp.Model, usage.Model), Provider: firstNonEmpty(resp.Provider, usage.Provider), CostUSD: usage.CostUSD,
		GeneratedBy: actor, CreatedAt: d.now(), Advisory: true}
	stored, inserted, err := d.Store.PutCounterargument(ctx, org, c)
	if err != nil {
		return domain.Counterargument{}, d.degrade(ctx, ap, actor, CounterargumentUnavailable, err)
	}
	if !inserted {
		stored.Cached = true
		return stored, nil
	}
	d.Rec.Audit(ctx, domain.AuditLog{Actor: actor, Action: "approval.counterargument", Entity: "approval", EntityID: approvalID,
		Details: map[string]any{"task_id": ap.TaskID, "action": ap.Action, "risk": ap.Risk, "model": stored.Model, "cost_usd": stored.CostUSD,
			"advisory": true}})
	return stored, nil
}

// request builds the payload from the approval card only, redacted once more (the card is already sanitized).
func (d *DevilsAdvocate) request(ctx context.Context, org string, ap domain.Approval, locale string) CounterargumentRequest {
	if locale == "" && d.Locale != nil {
		locale = d.Locale(ctx)
	}
	if locale == "" {
		locale = "es"
	}
	role := ""
	if ag, err := d.Core.GetAgent(ctx, org, ap.AgentID); err == nil {
		role = ag.Role
	}
	return CounterargumentRequest{Locale: locale, Approval: CounterargumentBrief{
		Action: ap.Action, Tool: ap.Tool, Title: reviewClip(sanitize.RedactSecrets(ap.Title, nil), maxBriefTitle),
		Details: reviewClip(sanitize.RedactSecrets(ap.Details, nil), maxBriefDetails), Risk: strings.ToLower(ap.Risk),
		AgentRole: role, RequiredApprovals: max(ap.RequiredApprovals, 1)}}
}

// degrade audits why no review was produced and returns the typed error.
func (d *DevilsAdvocate) degrade(ctx context.Context, ap domain.Approval, actor, code string, cause error) error {
	d.Rec.Audit(ctx, domain.AuditLog{Actor: actor, Action: "approval.counterargument_failed", Entity: "approval", EntityID: ap.ID,
		Details: map[string]any{"task_id": ap.TaskID, "action": ap.Action, "reason": code}})
	return &CounterargumentError{Code: code, Err: cause}
}

func reviewClip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func cleanReview(s string, n int) string { return reviewClip(strings.Join(strings.Fields(s), " "), n) }

func cleanReviewList(in []string) []string {
	out := []string{}
	for _, s := range in {
		if s = cleanReview(s, 400); s != "" && len(out) < 6 {
			out = append(out, s)
		}
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// AdmitReview asks the execution guard (kill switch, agent pause) whether the review of an approval of agentID
// may spend model budget. Read-only mode does not apply: the review has no side effects. The working-hours
// window does not either: a person is asking, the agent is not starting work.
func (o *Orchestrator) AdmitReview(ctx context.Context, agentID string) GuardVerdict {
	v := o.chatAdmit(ctx, agentID)
	if !v.Allowed && (v.Code == "read_only_mode" || v.Code == "outside_operating_hours") {
		return GuardVerdict{Allowed: true}
	}
	return v
}
