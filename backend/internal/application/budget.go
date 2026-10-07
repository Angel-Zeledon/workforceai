package application

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Budget enforces hard spending caps by reserving before each runtime call and
// reconciling with the real usage afterwards (docs/architecture/07-seguridad-costos.md 5.2).
//
// Three scopes are checked, in this order of explainability:
//   - request: budget_cap_usd of the request (store cap "request/<id>")
//   - agent:   monthly cap of the agent (store cap "agent/<id>", else Config.AgentBudgetCapUSD)
//   - org:     Config.BudgetUSD (not resumable from here: it is the account limit)
//
// A call may start only if spent + reserved + estimate <= cap in every scope.
// Reservations live in memory (they only exist while a call is in flight); the
// spent amounts come from the persisted ledger, so they survive restarts.
type Budget struct {
	cfg   Config
	store Store
	rec   *Recorder
	log   *slog.Logger

	mu        sync.Mutex
	reserved  map[string]float64 // org|scope|id -> USD in flight
	pauses    map[string]*PauseInfo
	warned    map[string]bool
	wake      chan struct{} // closed (and replaced) whenever a cap changes
	confirm   map[string]chan Confirmation
	estimates map[string]domain.CostEstimate // org|request id
}

// NewBudget builds the manager. Config zero values disable the optional caps.
func NewBudget(cfg Config, store Store, rec *Recorder, log *slog.Logger) *Budget {
	return &Budget{cfg: cfg, store: store, rec: rec, log: log,
		reserved: map[string]float64{}, pauses: map[string]*PauseInfo{}, warned: map[string]bool{},
		wake: make(chan struct{}), confirm: map[string]chan Confirmation{}, estimates: map[string]domain.CostEstimate{}}
}

func bkey(org string, scope domain.BudgetScope, id string) string {
	return org + "|" + string(scope) + "|" + id
}

func (b *Budget) org(ctx context.Context) string { return OrgFrom(ctx, b.cfg.OrgID) }

// Exceeded describes the limit that blocked a reservation. It is the data
// behind the budget.exceeded event and the user-facing explanation.
type Exceeded struct {
	Scope       domain.BudgetScope `json:"scope"`
	ScopeID     string             `json:"scope_id"`
	RequestID   string             `json:"request_id"`
	AgentID     string             `json:"agent_id"`
	TaskID      string             `json:"task_id,omitempty"`
	CapUSD      float64            `json:"cap_usd"`
	SpentUSD    float64            `json:"spent_usd"`
	ReservedUSD float64            `json:"reserved_usd"`
	NeededUSD   float64            `json:"needed_usd"` // spent + reserved + the estimate of the blocked call
}

// Resumable reports whether raising a cap through the API can unblock it.
func (e *Exceeded) Resumable() bool { return e.Scope != domain.ScopeOrg }

func (e *Exceeded) Error() string {
	return fmt.Sprintf("budget cap reached (%s %s): needs $%.4f, cap $%.4f", e.Scope, e.ScopeID, e.NeededUSD, e.CapUSD)
}

// PauseInfo is one active pause (a request or an agent waiting for a higher cap).
type PauseInfo struct {
	Scope     domain.BudgetScope `json:"scope"`
	ScopeID   string             `json:"scope_id"`
	RequestID string             `json:"request_id"`
	Since     time.Time          `json:"since"`
	CapUSD    float64            `json:"cap_usd"`
	SpentUSD  float64            `json:"spent_usd"`
	NeededUSD float64            `json:"needed_usd"`
	Waiters   int                `json:"waiters"`
}

// Reservation holds budget for one in-flight call until Release.
type Reservation struct {
	b    *Budget
	keys []string
	usd  float64
	once sync.Once
}

// Release returns the held amount. Safe to call more than once and on nil.
func (r *Reservation) Release() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		r.b.mu.Lock()
		defer r.b.mu.Unlock()
		for _, k := range r.keys {
			r.b.reserved[k] -= r.usd
			if r.b.reserved[k] < 1e-12 {
				delete(r.b.reserved, k)
			}
		}
	})
}

// PeriodStart is the beginning of the agent budget period (calendar month, UTC).
func PeriodStart(now time.Time) time.Time {
	n := now.UTC()
	return time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func capsMap(caps []domain.BudgetCap) map[string]float64 {
	m := make(map[string]float64, len(caps))
	for _, c := range caps {
		m[string(c.Scope)+"/"+c.ScopeID] = c.CapUSD
	}
	return m
}

// agentCap resolves the cap of an agent (explicit cap, else the default; 0 = none).
func (b *Budget) agentCap(caps map[string]float64, agentID string) float64 {
	if v, ok := caps["agent/"+agentID]; ok {
		return v
	}
	return b.cfg.AgentBudgetCapUSD
}

func agentSpend(usage []domain.UsageEntry, agentID string, since time.Time) float64 {
	var s float64
	for _, u := range usage {
		if u.AgentID == agentID && !u.TS.Before(since) {
			s += u.CostUSD
		}
	}
	return s
}

// Reserve checks every scope and, when the call fits, holds `estimate` USD for
// it. When a limit blocks the call it returns (nil, *Exceeded, nil): the caller
// decides whether to fail or to pause. Errors are infrastructure errors.
func (b *Budget) Reserve(ctx context.Context, requestID, agentID string, estimate float64) (*Reservation, *Exceeded, error) {
	org := b.org(ctx)
	if estimate < 0 {
		estimate = 0
	}
	// Everything below runs under the lock: the spent amounts and the in-flight
	// reservations must be read atomically, or two parallel calls could both
	// pass against a stale snapshot (record + release happen in that order).
	b.mu.Lock()
	defer b.mu.Unlock()
	capList, err := b.store.ListBudgetCaps(ctx, org)
	if err != nil {
		return nil, nil, err
	}
	caps := capsMap(capList)
	used, err := b.store.OrgCost(ctx, org)
	if err != nil {
		return nil, nil, err
	}
	var reqSpent float64
	if req, err := b.store.GetRequest(ctx, org, requestID); err == nil {
		reqSpent = req.CostUSD
	}
	var agSpent float64
	agCap := b.agentCap(caps, agentID)
	if agCap > 0 {
		usage, err := b.store.ListUsage(ctx, org)
		if err != nil {
			return nil, nil, err
		}
		agSpent = agentSpend(usage, agentID, PeriodStart(time.Now()))
	}

	type check struct {
		scope            domain.BudgetScope
		id               string
		cap, spent       float64
		requestID, agent string
	}
	checks := []check{
		{domain.ScopeRequest, requestID, caps["request/"+requestID], reqSpent, requestID, agentID},
		{domain.ScopeAgent, agentID, agCap, agSpent, requestID, agentID},
		{domain.ScopeOrg, org, b.cfg.BudgetUSD, used, requestID, agentID},
	}
	var keys []string
	for _, c := range checks {
		if c.cap <= 0 {
			continue
		}
		k := bkey(org, c.scope, c.id)
		held := b.reserved[k]
		// Tolerance: float noise must not flip a call that fits exactly.
		if c.spent+held+estimate > c.cap+1e-9 {
			return nil, &Exceeded{Scope: c.scope, ScopeID: c.id, RequestID: requestID, AgentID: agentID,
				CapUSD: c.cap, SpentUSD: c.spent, ReservedUSD: held, NeededUSD: c.spent + held + estimate}, nil
		}
		keys = append(keys, k)
	}
	for _, k := range keys {
		b.reserved[k] += estimate
	}
	return &Reservation{b: b, keys: keys, usd: estimate}, nil, nil
}

// Record writes the reconciled cost of a finished call to the request/task
// totals and to the ledger, then raises 80% warnings. The recorded cost is the
// greater of the runtime's number and the backend's own recalculation.
func (b *Budget) Record(ctx context.Context, requestID string, u domain.UsageEntry, runtimeCost float64) (float64, error) {
	org := b.org(ctx)
	cost := math.Max(runtimeCost, recalcCost(u.Model, u.InputTokens, u.OutputTokens))
	u.ID, u.RequestID, u.CostUSD = newID(), requestID, cost
	if u.TS.IsZero() {
		u.TS = time.Now().UTC()
	}
	if u.Tools == nil {
		u.Tools = []string{}
	}
	ctx = context.WithoutCancel(ctx)
	if err := b.store.AddCost(ctx, org, requestID, u.TaskID, cost); err != nil {
		return cost, err
	}
	if err := b.store.AddUsage(ctx, org, u); err != nil {
		return cost, err
	}
	b.checkWarnings(ctx, org, requestID, u.AgentID)
	return cost, nil
}

// checkWarnings emits budget.warning once per scope and cap when spend crosses 80%.
func (b *Budget) checkWarnings(ctx context.Context, org, requestID, agentID string) {
	capList, err := b.store.ListBudgetCaps(ctx, org)
	if err != nil {
		return
	}
	caps := capsMap(capList)
	type w struct {
		scope      domain.BudgetScope
		id         string
		cap, spent float64
	}
	var ws []w
	if c := caps["request/"+requestID]; c > 0 {
		if r, err := b.store.GetRequest(ctx, org, requestID); err == nil {
			ws = append(ws, w{domain.ScopeRequest, requestID, c, r.CostUSD})
		}
	}
	if c := b.agentCap(caps, agentID); c > 0 {
		if usage, err := b.store.ListUsage(ctx, org); err == nil {
			ws = append(ws, w{domain.ScopeAgent, agentID, c, agentSpend(usage, agentID, PeriodStart(time.Now()))})
		}
	}
	if b.cfg.BudgetUSD > 0 {
		if used, err := b.store.OrgCost(ctx, org); err == nil {
			ws = append(ws, w{domain.ScopeOrg, org, b.cfg.BudgetUSD, used})
		}
	}
	for _, x := range ws {
		pct := x.spent / x.cap * 100
		if pct < 80 {
			continue
		}
		key := fmt.Sprintf("%s#%.6f", bkey(org, x.scope, x.id), x.cap)
		b.mu.Lock()
		seen := b.warned[key]
		b.warned[key] = true
		b.mu.Unlock()
		if seen {
			continue
		}
		b.rec.Emit(ctx, Action{Type: domain.EvBudgetWarning, AgentID: agentID, Entity: "budget", EntityID: x.id,
			Payload: map[string]any{"scope": x.scope, "scope_id": x.id, "request_id": requestID, "agent_id": agentID,
				"spent_usd": x.spent, "cap_usd": x.cap, "pct": math.Round(pct*10) / 10},
			Text: fmt.Sprintf("Aviso de presupuesto: %.0f%% del tope (%s)", pct, x.scope)})
	}
}

// SetCap changes (or, with capUSD <= 0, removes) the cap of an agent or a
// request, audits it, and wakes every call paused on a cap so it can re-check:
// this is the "raise the cap and continue" action.
func (b *Budget) SetCap(ctx context.Context, scope domain.BudgetScope, id string, capUSD float64) error {
	if scope != domain.ScopeAgent && scope != domain.ScopeRequest {
		return fmt.Errorf("%w: only agent and request caps can be changed", domain.ErrInvalid)
	}
	if math.IsNaN(capUSD) || math.IsInf(capUSD, 0) || capUSD < 0 || capUSD > 1e6 {
		return fmt.Errorf("%w: cap must be between 0 and 1000000 USD", domain.ErrInvalid)
	}
	org := b.org(ctx)
	switch scope {
	case domain.ScopeAgent:
		if _, err := b.store.GetAgent(ctx, org, id); err != nil {
			return err
		}
	case domain.ScopeRequest:
		if _, err := b.store.GetRequest(ctx, org, id); err != nil {
			return err
		}
	}
	if err := b.store.SetBudgetCap(ctx, org, scope, id, capUSD); err != nil {
		return err
	}
	b.rec.Audit(ctx, domain.AuditLog{Actor: ActorFrom(ctx, "user"), Action: "budget.cap_changed", Entity: string(scope), EntityID: id,
		Details: map[string]any{"cap_usd": capUSD}})
	b.mu.Lock()
	close(b.wake)
	b.wake = make(chan struct{})
	b.mu.Unlock()
	return nil
}

// WakeChan returns a channel closed on the next cap change. Fetch it BEFORE
// calling Reserve so a change in between is never missed.
func (b *Budget) WakeChan() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.wake
}

// enterPause registers a waiter on a pause; first is true for a new episode.
func (b *Budget) enterPause(org string, ex *Exceeded) (first bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := bkey(org, ex.Scope, ex.ScopeID)
	p, ok := b.pauses[k]
	if !ok {
		p = &PauseInfo{Scope: ex.Scope, ScopeID: ex.ScopeID, RequestID: ex.RequestID, Since: time.Now().UTC()}
		b.pauses[k] = p
	}
	p.Waiters++
	p.CapUSD, p.SpentUSD, p.NeededUSD = ex.CapUSD, ex.SpentUSD, ex.NeededUSD
	return !ok
}

// leavePause unregisters a waiter; last is true when the episode ended.
func (b *Budget) leavePause(org string, ex *Exceeded) (last bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := bkey(org, ex.Scope, ex.ScopeID)
	p, ok := b.pauses[k]
	if !ok {
		return false
	}
	p.Waiters--
	if p.Waiters <= 0 {
		delete(b.pauses, k)
		return true
	}
	return false
}

// ---- cost estimates and confirmation ----

// Confirmation is the user's answer to a cost estimate that needs approval.
type Confirmation struct {
	Proceed bool
	CapUSD  float64 // optional new cap for the request (0 = unchanged)
}

func (b *Budget) setEstimate(org string, e domain.CostEstimate) {
	b.mu.Lock()
	b.estimates[org+"|"+e.RequestID] = e
	b.mu.Unlock()
}

// Estimate returns the stored estimate of a request.
func (b *Budget) Estimate(ctx context.Context, requestID string) (domain.CostEstimate, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.estimates[b.org(ctx)+"|"+requestID]
	return e, ok
}

func (b *Budget) awaitConfirmation(org, requestID string) chan Confirmation {
	ch := make(chan Confirmation, 1)
	b.mu.Lock()
	b.confirm[org+"|"+requestID] = ch
	b.mu.Unlock()
	return ch
}

func (b *Budget) dropConfirmation(org, requestID string) {
	b.mu.Lock()
	delete(b.confirm, org+"|"+requestID)
	b.mu.Unlock()
}

// Confirm answers a request waiting in awaiting_confirmation. A positive capUSD
// replaces the request cap before the run continues.
func (b *Budget) Confirm(ctx context.Context, requestID string, proceed bool, capUSD float64) error {
	org := b.org(ctx)
	if _, err := b.store.GetRequest(ctx, org, requestID); err != nil {
		return err
	}
	if capUSD < 0 || math.IsNaN(capUSD) || math.IsInf(capUSD, 0) || capUSD > 1e6 {
		return fmt.Errorf("%w: invalid budget_cap_usd", domain.ErrInvalid)
	}
	b.mu.Lock()
	ch, ok := b.confirm[org+"|"+requestID]
	if ok {
		delete(b.confirm, org+"|"+requestID)
	}
	b.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: the request is not waiting for confirmation", domain.ErrConflict)
	}
	if proceed && capUSD > 0 {
		if err := b.SetCap(ctx, domain.ScopeRequest, requestID, capUSD); err != nil {
			b.mu.Lock()
			b.confirm[org+"|"+requestID] = ch // let the user retry
			b.mu.Unlock()
			return err
		}
	}
	b.rec.Audit(ctx, domain.AuditLog{Actor: ActorFrom(ctx, "user"), Action: "cost.estimate_answered", Entity: "request", EntityID: requestID,
		Details: map[string]any{"proceed": proceed, "cap_usd": capUSD}})
	ch <- Confirmation{Proceed: proceed, CapUSD: capUSD}
	return nil
}

// reset drops all in-memory state (demo reset).
func (b *Budget) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reserved = map[string]float64{}
	b.pauses = map[string]*PauseInfo{}
	b.warned = map[string]bool{}
	b.confirm = map[string]chan Confirmation{}
	b.estimates = map[string]domain.CostEstimate{}
	close(b.wake)
	b.wake = make(chan struct{})
}

// ---- status ----

// AgentBudget is the budget state of one agent.
type AgentBudget struct {
	AgentID     string  `json:"agent_id"`
	CapUSD      float64 `json:"cap_usd"` // 0 = no cap
	SpentUSD    float64 `json:"spent_usd"`
	ReservedUSD float64 `json:"reserved_usd"`
	Paused      bool    `json:"paused"`
}

// BudgetStatus is GET /budget.
type BudgetStatus struct {
	Org struct {
		BudgetUSD   float64 `json:"budget_usd"`
		UsedUSD     float64 `json:"used_usd"`
		ReservedUSD float64 `json:"reserved_usd"`
	} `json:"org"`
	Defaults struct {
		RequestCapUSD       float64   `json:"request_cap_usd"`
		AgentCapUSD         float64   `json:"agent_cap_usd"`
		ConfirmThresholdUSD float64   `json:"confirm_threshold_usd"`
		PeriodStart         time.Time `json:"period_start"`
	} `json:"defaults"`
	Agents []AgentBudget `json:"agents"`
	Pauses []PauseInfo   `json:"pauses"`
}

// Status reports limits, spend, reservations and active pauses.
func (b *Budget) Status(ctx context.Context) (BudgetStatus, error) {
	org := b.org(ctx)
	var st BudgetStatus
	agents, err := b.store.ListAgents(ctx, org)
	if err != nil {
		return st, err
	}
	capList, err := b.store.ListBudgetCaps(ctx, org)
	if err != nil {
		return st, err
	}
	caps := capsMap(capList)
	usage, err := b.store.ListUsage(ctx, org)
	if err != nil {
		return st, err
	}
	used, err := b.store.OrgCost(ctx, org)
	if err != nil {
		return st, err
	}
	since := PeriodStart(time.Now())
	st.Org.BudgetUSD, st.Org.UsedUSD = b.cfg.BudgetUSD, used
	st.Defaults.RequestCapUSD, st.Defaults.AgentCapUSD = b.cfg.RequestBudgetCapUSD, b.cfg.AgentBudgetCapUSD
	st.Defaults.ConfirmThresholdUSD, st.Defaults.PeriodStart = b.cfg.ConfirmThresholdUSD, since
	st.Agents, st.Pauses = []AgentBudget{}, []PauseInfo{}
	b.mu.Lock()
	defer b.mu.Unlock()
	st.Org.ReservedUSD = b.reserved[bkey(org, domain.ScopeOrg, org)]
	for _, a := range agents {
		_, paused := b.pauses[bkey(org, domain.ScopeAgent, a.ID)]
		st.Agents = append(st.Agents, AgentBudget{AgentID: a.ID, CapUSD: b.agentCap(caps, a.ID),
			SpentUSD: agentSpend(usage, a.ID, since), ReservedUSD: b.reserved[bkey(org, domain.ScopeAgent, a.ID)], Paused: paused})
	}
	for k, p := range b.pauses {
		if len(k) > len(org) && k[:len(org)+1] == org+"|" {
			cur := *p
			// Refresh the spend: other calls may have recorded cost since the pause began.
			var spent float64
			switch p.Scope {
			case domain.ScopeRequest:
				if r, err := b.store.GetRequest(ctx, org, p.ScopeID); err == nil {
					spent = r.CostUSD
				}
			case domain.ScopeAgent:
				spent = agentSpend(usage, p.ScopeID, since)
			}
			if spent > 0 {
				cur.NeededUSD += spent - cur.SpentUSD
				cur.SpentUSD = spent
			}
			st.Pauses = append(st.Pauses, cur)
		}
	}
	slices.SortFunc(st.Pauses, func(a, c PauseInfo) int { return a.Since.Compare(c.Since) })
	return st, nil
}
