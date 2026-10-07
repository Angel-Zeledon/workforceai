package policy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"
)

// Evaluator is what callers (tools executor) depend on.
type Evaluator interface {
	// Evaluate is side-effect free (apart from pruning counters).
	Evaluate(ctx context.Context, id AgentIdentity, call ToolCall) Decision
	// EvaluateAndReserve evaluates and, when allowed, atomically records the
	// usage against limits.
	EvaluateAndReserve(ctx context.Context, id AgentIdentity, call ToolCall) Decision
	// EvaluateApproved re-evaluates a call a human approved. Approval satisfies
	// require_approval only; denies still deny. Single use.
	EvaluateApproved(ctx context.Context, id AgentIdentity, call ToolCall, tok ApprovalToken) Decision
}

// ApprovalToken proves a human approved exactly this call.
type ApprovalToken struct {
	ApprovalID  string
	ApprovedBy  string
	Fingerprint string // from Fingerprint(id, call) at request time
}

type usageEvent struct {
	t      time.Time
	amount float64
}

// Engine is the policy engine. Safe for concurrent use.
type Engine struct {
	mu       sync.Mutex
	cfg      Config
	contacts ContactBook
	now      func() time.Time
	usage    map[string][]usageEvent
	spent    float64
	used     map[string]bool // consumed approval ids
	curOrg   string          // org of the call being decided (guarded by mu)
}

var _ Evaluator = (*Engine)(nil)

// Option customizes the engine.
type Option func(*Engine)

// WithClock injects a clock (tests).
func WithClock(now func() time.Time) Option { return func(e *Engine) { e.now = now } }

// NewEngine validates cfg and builds an engine. contacts may be nil (nobody is known).
func NewEngine(cfg Config, contacts ContactBook, opts ...Option) (*Engine, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	e := &Engine{cfg: cfg.normalized(), contacts: contacts, now: time.Now,
		usage: map[string][]usageEvent{}, used: map[string]bool{}}
	for _, o := range opts {
		o(e)
	}
	return e, nil
}

// SetConfig atomically replaces the configuration (usage counters are kept).
func (e *Engine) SetConfig(cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	e.mu.Lock()
	e.cfg = cfg.normalized()
	e.mu.Unlock()
	return nil
}

// Config returns a copy of the current configuration.
func (e *Engine) Config() Config {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg
}

// RecordSpend adds LLM/tool spend (USD) to the org budget.
func (e *Engine) RecordSpend(usd float64) {
	if usd <= 0 {
		return
	}
	e.mu.Lock()
	e.spent += usd
	e.mu.Unlock()
}

// Spent returns the recorded spend.
func (e *Engine) Spent() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.spent
}

// Validate checks a configuration.
func (c Config) Validate() error {
	seen := map[string]bool{}
	for _, g := range c.Grants {
		if g.ID == "" {
			return fmt.Errorf("policy: grant without id (%s.%s)", g.Tool, g.Action)
		}
		if seen[g.ID] {
			return fmt.Errorf("policy: duplicate grant id %q", g.ID)
		}
		seen[g.ID] = true
		if !g.Effect.Valid() {
			return fmt.Errorf("policy: grant %q has invalid effect %q", g.ID, g.Effect)
		}
		if g.Agent == "" || g.Tool == "" || g.Action == "" {
			return fmt.Errorf("policy: grant %q needs agent, tool and action", g.ID)
		}
		switch g.When.Recipient {
		case RecipientAny, RecipientNew, RecipientKnown:
		default:
			return fmt.Errorf("policy: grant %q has invalid recipient scope %q", g.ID, g.When.Recipient)
		}
		for _, p := range []string{g.Agent, g.Tool, g.Action} {
			if _, err := path.Match(p, "x"); err != nil {
				return fmt.Errorf("policy: grant %q bad pattern %q", g.ID, p)
			}
		}
	}
	for a, l := range c.Autonomy {
		if !l.Valid() {
			return fmt.Errorf("policy: agent %q has invalid autonomy %q", a, l)
		}
	}
	lseen := map[string]bool{}
	for _, l := range c.Limits {
		if l.ID == "" || lseen[l.ID] {
			return fmt.Errorf("policy: limit needs unique id (%q)", l.ID)
		}
		lseen[l.ID] = true
		if l.Window <= 0 || (l.MaxCalls <= 0 && l.MaxAmount <= 0) {
			return fmt.Errorf("policy: limit %q needs window and max_calls or max_amount", l.ID)
		}
		if l.OnExceed != "" && l.OnExceed != EffectDeny && l.OnExceed != EffectRequireApproval {
			return fmt.Errorf("policy: limit %q has invalid on_exceed", l.ID)
		}
	}
	if c.Org.MaxDelegationDepth < 0 || c.Org.ApprovalAmount < 0 || c.Org.BudgetUSD < 0 {
		return fmt.Errorf("policy: negative org values")
	}
	return nil
}

func (c Config) normalized() Config {
	out := c
	if out.Org.MaxDelegationDepth == 0 {
		out.Org.MaxDelegationDepth = MaxDelegationDepthDefault
	}
	out.Org.AlwaysApprove = normList(c.Org.AlwaysApprove)
	out.Org.DenyActions = normList(c.Org.DenyActions)
	out.Autonomy = make(map[string]Autonomy, len(c.Autonomy))
	for k, v := range c.Autonomy {
		out.Autonomy[k] = v
	}
	out.Grants = append([]Grant(nil), c.Grants...)
	for i := range out.Grants {
		out.Grants[i].Tool = NormalizeName(out.Grants[i].Tool)
		out.Grants[i].Action = NormalizeName(out.Grants[i].Action)
		if out.Grants[i].Tool == "" {
			out.Grants[i].Tool = "*"
		}
	}
	out.Limits = append([]Limit(nil), c.Limits...)
	return out
}

func normList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if n := strings.ToLower(strings.TrimSpace(s)); n != "" {
			out = append(out, strings.NewReplacer("-", "_", " ", "_").Replace(n))
		}
	}
	return out
}

func glob(pattern, s string) bool {
	if pattern == "*" {
		return true
	}
	ok, err := path.Match(pattern, s)
	return err == nil && ok
}

// Fingerprint returns a stable hash of the identity and call (excluding risk
// and source, which are runtime metadata). Used to bind approvals to the exact
// action so args cannot be swapped after approval.
func Fingerprint(id AgentIdentity, call ToolCall) string {
	payload := map[string]any{
		"org": id.OrgID, "agent": id.AgentID, "on_behalf_of": id.OnBehalfOf, "chain": id.Chain,
		"tool": NormalizeName(call.Tool), "action": NormalizeName(call.Action), "args": call.Args,
	}
	b, err := json.Marshal(payload) // map keys are sorted: canonical
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Evaluate implements Evaluator.
func (e *Engine) Evaluate(ctx context.Context, id AgentIdentity, call ToolCall) Decision {
	e.mu.Lock()
	defer e.mu.Unlock()
	d, _ := e.decide(ctx, id, call)
	return d
}

// EvaluateAndReserve implements Evaluator.
func (e *Engine) EvaluateAndReserve(ctx context.Context, id AgentIdentity, call ToolCall) Decision {
	e.mu.Lock()
	defer e.mu.Unlock()
	d, f := e.decide(ctx, id, call)
	if d.Allow {
		e.reserve(id.AgentID, call, f)
	}
	return d
}

// EvaluateApproved implements Evaluator.
func (e *Engine) EvaluateApproved(ctx context.Context, id AgentIdentity, call ToolCall, tok ApprovalToken) Decision {
	e.mu.Lock()
	defer e.mu.Unlock()
	if tok.ApprovalID == "" || strings.TrimSpace(tok.ApprovedBy) == "" {
		return mk(EffectDeny, "approval.invalid", "approval token needs an id and an approver")
	}
	if e.used[tok.ApprovalID] {
		return mk(EffectDeny, "approval.replayed", "approval already consumed")
	}
	fp := Fingerprint(id, call)
	if fp == "" || fp != tok.Fingerprint {
		return mk(EffectDeny, "approval.mismatch", "the call differs from what was approved")
	}
	d, f := e.decide(ctx, id, call)
	if d.RequireApproval {
		d = Decision{Allow: true, RuleID: d.RuleID,
			Reason: fmt.Sprintf("approved by %s (%s)", tok.ApprovedBy, d.Reason), Trace: d.Trace}
	}
	if d.Allow {
		e.used[tok.ApprovalID] = true
		e.reserve(id.AgentID, call, f)
	}
	return d
}

// resolveChain validates the delegation chain.
func (e *Engine) resolveChain(id AgentIdentity) ([]string, *Decision) {
	deny := func(rule, reason string) ([]string, *Decision) { d := mk(EffectDeny, rule, reason); return nil, &d }
	if strings.TrimSpace(id.AgentID) == "" {
		return deny("identity.missing", "agent id is required")
	}
	chain := id.Chain
	if len(chain) == 0 {
		if id.OnBehalfOf != "" && id.OnBehalfOf != id.AgentID {
			chain = []string{id.OnBehalfOf, id.AgentID}
		} else {
			chain = []string{id.AgentID}
		}
	}
	if chain[len(chain)-1] != id.AgentID {
		return deny("delegation.invalid", "delegation chain must end with the acting agent")
	}
	if id.OnBehalfOf != "" && chain[0] != id.OnBehalfOf && chain[0] != id.AgentID {
		return deny("delegation.invalid", "delegation chain does not start at on_behalf_of")
	}
	seen := map[string]bool{}
	for _, c := range chain {
		if strings.TrimSpace(c) == "" || seen[c] {
			return deny("delegation.invalid", "empty or repeated participant in delegation chain (cycle)")
		}
		seen[c] = true
	}
	if hops := len(chain) - 1; hops > e.cfg.Org.MaxDelegationDepth {
		return deny("delegation.depth", fmt.Sprintf("delegation depth %d exceeds maximum %d", hops, e.cfg.Org.MaxDelegationDepth))
	}
	return chain, nil
}

func matchAny(list []string, tool, action string) (string, bool) {
	for _, p := range list {
		if glob(p, action) || glob(p, tool+"."+action) {
			return p, true
		}
	}
	return "", false
}

// decide computes the decision. Must be called with e.mu held.
func (e *Engine) decide(ctx context.Context, id AgentIdentity, call ToolCall) (Decision, facts) {
	var f facts
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return mk(EffectDeny, "ctx.done", "context ended: "+err.Error()), f
		}
	}
	e.curOrg = id.OrgID
	tool, action := NormalizeName(call.Tool), NormalizeName(call.Action)
	if !ValidName(tool) || !ValidName(action) {
		return mk(EffectDeny, "call.invalid", "invalid tool or action name"), f
	}
	chain, bad := e.resolveChain(id)
	if bad != nil {
		return *bad, f
	}
	f = analyze(call.Args)
	effectful := !IsReadOnly(action)

	var ds []Decision
	if p, ok := matchAny(e.cfg.Org.DenyActions, tool, action); ok {
		ds = append(ds, mk(EffectDeny, "org.deny:"+p, fmt.Sprintf("action %s.%s is forbidden by organization rules", tool, action)))
	}
	if b := e.cfg.Org.BudgetUSD; b > 0 && e.spent >= b {
		ds = append(ds, mk(EffectDeny, "org.budget", fmt.Sprintf("organization budget exhausted (%.2f/%.2f USD)", e.spent, b)))
	}
	if p, ok := matchAny(e.cfg.Org.AlwaysApprove, tool, action); ok {
		ds = append(ds, mk(EffectRequireApproval, "org.always_approve:"+p, fmt.Sprintf("action %s always requires approval", action)))
	}
	if t := e.cfg.Org.ApprovalAmount; t > 0 {
		if m, ok := f.maxAmount(); ok && m > t {
			ds = append(ds, mk(EffectRequireApproval, "org.amount", fmt.Sprintf("amount %.2f exceeds approval threshold %.2f", m, t)))
		}
	}
	if f.amountInvalid {
		ds = append(ds, mk(EffectRequireApproval, "amount.unparseable", "amount could not be interpreted; approval required"))
	}

	// Every known agent in the chain must be allowed (delegation never elevates).
	// The acting agent is evaluated first so its reason leads.
	agents := []string{id.AgentID}
	for i := len(chain) - 2; i >= 0; i-- {
		c := chain[i]
		if _, known := e.cfg.Autonomy[c]; known {
			agents = append(agents, c)
		} else if i != 0 {
			ds = append(ds, mk(EffectDeny, "delegation.unknown_agent", fmt.Sprintf("unknown agent %q in delegation chain", c)))
		}
	}
	for _, a := range agents {
		ds = append(ds, e.decideAgent(a, tool, action, call, f, effectful))
	}
	if eff, rid, reason, ok := e.checkLimits(id, tool, action, f); ok {
		ds = append(ds, mk(eff, rid, reason))
	}
	return reduce(ds), f
}

// reduce picks the strictest decision (first of equal rank) and collects a trace.
func reduce(ds []Decision) Decision {
	best := mk(EffectDeny, "none", "no decision")
	bestRank := -1
	var trace []string
	for _, d := range ds {
		if r := d.effect().Rank(); r > bestRank {
			best, bestRank = d, r
		}
		if !d.Allow {
			trace = append(trace, d.RuleID)
		}
	}
	best.Trace = trace
	if bestRank == 0 && len(ds) > 1 {
		best.Trace = nil
	}
	return best
}

func (e *Engine) decideAgent(agent, tool, action string, call ToolCall, f facts, effectful bool) Decision {
	level, ok := e.cfg.Autonomy[agent]
	if !ok {
		return mk(EffectDeny, "agent.unknown", fmt.Sprintf("agent %q is not configured", agent))
	}
	var allowG, approvalG *Grant
	concreteApproval := false
	for i := range e.cfg.Grants {
		g := &e.cfg.Grants[i]
		if !glob(g.Agent, agent) || !glob(g.Tool, tool) || !glob(g.Action, action) {
			continue
		}
		if !e.conditionsHold(g.When, f, call, action) {
			continue
		}
		switch g.Effect {
		case EffectDeny:
			return mk(EffectDeny, g.ID, fmt.Sprintf("denied for %s on %s.%s: %s", agent, tool, action, descr(g)))
		case EffectRequireApproval:
			if approvalG == nil {
				approvalG = g
			}
			// only a concrete tool+action approval grant opens access by itself;
			// wildcard approval grants are modifiers on top of an existing path
			if !strings.ContainsAny(g.Tool+g.Action, "*?[") {
				concreteApproval = true
			}
		case EffectAllow:
			if allowG == nil {
				allowG = g
			}
		}
	}
	if allowG == nil && !concreteApproval {
		return mk(EffectDeny, "grant.none", fmt.Sprintf("no grant for %s on %s.%s", agent, tool, action))
	}
	var d Decision
	switch {
	case approvalG != nil:
		d = mk(EffectRequireApproval, approvalG.ID, fmt.Sprintf("%s on %s.%s requires approval: %s", agent, tool, action, descr(approvalG)))
	case allowG != nil:
		d = mk(EffectAllow, allowG.ID, fmt.Sprintf("authorized: %s", descr(allowG)))
	default:
		return mk(EffectDeny, "grant.none", fmt.Sprintf("no grant for %s on %s.%s", agent, tool, action))
	}
	if effectful {
		switch level {
		case AutonomySuggest:
			return mk(EffectDeny, "autonomy.suggest", fmt.Sprintf("%s is in suggest mode: %s.%s is only recorded as a suggestion", agent, tool, action))
		case AutonomyApproveEach:
			if d.Allow {
				d = mk(EffectRequireApproval, "autonomy.approve_each", fmt.Sprintf("%s requires approval for every action", agent))
			}
		}
	}
	// runtime risk can only escalate
	if effectful && d.Allow && strings.EqualFold(strings.TrimSpace(call.Risk), "high") && level != AutonomyAutonomous {
		d = mk(EffectRequireApproval, "risk.high", "runtime flagged this action as high risk")
	}
	return d
}

func descr(g *Grant) string {
	if g.Description != "" {
		return g.Description
	}
	return "rule " + g.ID
}

func (e *Engine) conditionsHold(w Conditions, f facts, call ToolCall, action string) bool {
	if w.AmountAbove != nil {
		m, ok := f.maxAmount()
		if !f.amountInvalid && !(ok && m > *w.AmountAbove) {
			return false
		}
	}
	if w.AmountAtMost != nil {
		m, ok := f.maxAmount()
		if f.amountInvalid || !ok || m > *w.AmountAtMost {
			return false
		}
	}
	switch w.Recipient {
	case RecipientNew:
		if !e.anyUnknown(f, action) {
			return false
		}
	case RecipientKnown:
		if e.anyUnknown(f, action) || len(f.recipients) == 0 {
			return false
		}
	}
	if len(w.Categories) > 0 && !f.hasCategory(w.Categories) {
		return false
	}
	return true
}

func isOutbound(action string) bool {
	for _, p := range []string{"send", "forward", "share", "invite", "reply", "publish"} {
		if action == p || strings.HasPrefix(action, p+"_") {
			return true
		}
	}
	return false
}

// anyUnknown: true when at least one recipient is not a known contact. An
// outbound action with no recognizable recipient is treated as unknown.
func (e *Engine) anyUnknown(f facts, action string) bool {
	if len(f.recipients) == 0 {
		return isOutbound(action)
	}
	if e.contacts == nil {
		return true
	}
	// OrgID is not carried in facts; contacts are checked by caller org below.
	for _, r := range f.recipients {
		if !e.contacts.IsKnown(e.curOrg, r) {
			return true
		}
	}
	return false
}

func (e *Engine) limitKey(l Limit, agent string) string {
	if l.PerOrg {
		return l.ID
	}
	return l.ID + "|" + agent
}

func (e *Engine) matchingLimits(id AgentIdentity, tool, action string) []Limit {
	var out []Limit
	for _, l := range e.cfg.Limits {
		if glob(l.Agent, id.AgentID) && glob(strings.ToLower(l.Tool), tool) && glob(strings.ToLower(l.Action), action) {
			out = append(out, l)
		}
	}
	return out
}

func (e *Engine) window(key string, w time.Duration) (int, float64) {
	cut := e.now().Add(-w)
	evs := e.usage[key]
	keep := evs[:0]
	var sum float64
	for _, ev := range evs {
		if ev.t.After(cut) {
			keep = append(keep, ev)
			sum += ev.amount
		}
	}
	e.usage[key] = keep
	return len(keep), sum
}

func (e *Engine) checkLimits(id AgentIdentity, tool, action string, f facts) (Effect, string, string, bool) {
	amt, _ := f.maxAmount()
	for _, l := range e.matchingLimits(id, tool, action) {
		n, sum := e.window(e.limitKey(l, id.AgentID), l.Window)
		exceed := Effect(l.OnExceed)
		if exceed == "" {
			exceed = EffectDeny
		}
		if l.MaxCalls > 0 && n+1 > l.MaxCalls {
			return exceed, "limit:" + l.ID, fmt.Sprintf("limit %s reached: %d calls per %s", l.ID, l.MaxCalls, l.Window), true
		}
		if l.MaxAmount > 0 && sum+amt > l.MaxAmount {
			return exceed, "limit:" + l.ID, fmt.Sprintf("limit %s: cumulative amount %.2f would exceed %.2f per %s", l.ID, sum+amt, l.MaxAmount, l.Window), true
		}
	}
	return "", "", "", false
}

func (e *Engine) reserve(agent string, call ToolCall, f facts) {
	amt, _ := f.maxAmount()
	id := AgentIdentity{AgentID: agent}
	for _, l := range e.matchingLimits(id, NormalizeName(call.Tool), NormalizeName(call.Action)) {
		k := e.limitKey(l, agent)
		e.usage[k] = append(e.usage[k], usageEvent{t: e.now(), amount: amt})
	}
}
