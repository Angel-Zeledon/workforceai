package application

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/counters"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/policy"
)

// PolicyService connects the policy engine (internal/policy) to the
// orchestrator. It answers one question for every tool request that is not a
// read: allow, require approval (and from whom) or deny.
//
// Two layers decide, and the strictest wins:
//
//  1. The baseline, which is exactly the rule the orchestrator always had: high
//     runtime risk, the APPROVAL_ACTIONS list and the suggest/approve_each
//     autonomy levels. It never goes away, so an organization with no rules
//     behaves as before (the demo and the smoke scenario do not change).
//  2. The engine, fed with the rules of the organization (stored as data in
//     org_settings.rules: the amount threshold and always-approve actions
//     seeded by the onboarding pack plus the governance an owner configured):
//     amount thresholds, forbidden actions, windowed limits, new recipients,
//     sensitive categories, approver role and double approval.
//
// Nothing the runtime sends can lower a decision: risk and args only add
// restrictions, and an unreadable configuration fails towards a human.
type PolicyService struct {
	Store ConfigStore
	Cfg   Config
	// Counters (optional) persists the usage of the windowed limits so a
	// restart does not reset them (internal/counters). Nil keeps them in memory.
	Counters counters.Store
	// Log (optional) reports counter persistence failures.
	Log *slog.Logger

	mu   sync.Mutex
	orgs map[string]*orgPolicy
}

type orgPolicy struct {
	eng      *policy.Engine
	contacts *contactSwitch
}

// contactSwitch lets the engine keep one ContactBook while the data behind it
// is rebuilt from the organization rules on every decision.
type contactSwitch struct {
	mu   sync.RWMutex
	book policy.ContactBook
}

func (c *contactSwitch) set(b policy.ContactBook) { c.mu.Lock(); c.book = b; c.mu.Unlock() }

func (c *contactSwitch) IsKnown(org, addr string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.book != nil && c.book.IsKnown(org, addr)
}

// PolicyInput describes a tool request to decide.
type PolicyInput struct {
	Agent       domain.Agent
	Tool        string
	Action      string
	Args        map[string]any
	Risk        string
	RequestedBy string // the human on whose behalf the agent acts ("" in demo mode)
}

// PolicyVerdict is the decision of the policy layer for one tool request.
type PolicyVerdict struct {
	Effect string // allow | require_approval | deny
	Reason string
	RuleID string
	Trace  []string
	// Source is "baseline" (no organization rules) or "engine".
	Source string

	RequiredRole      string
	RequiredApprovals int // 1, or 2 for double approval
	NoSelfApproval    bool
	Autonomy          string
	ArgsHash          string
	Amount            *float64
}

// Allowed, NeedsApproval and Denied are convenience predicates.
func (v PolicyVerdict) Allowed() bool       { return v.Effect == string(policy.EffectAllow) }
func (v PolicyVerdict) NeedsApproval() bool { return v.Effect == string(policy.EffectRequireApproval) }
func (v PolicyVerdict) Denied() bool        { return v.Effect == string(policy.EffectDeny) }

func rank(e string) int { return policy.Effect(e).Rank() }

// configured reports whether the organization has rules the engine must apply.
func configured(r catalog.PackRules) bool {
	return r.ApprovalAmountUSD > 0 || len(r.AlwaysApprove) > 0 || (r.Governance != nil && !r.Governance.IsZero())
}

// baseline is the invariant floor (the original needsApproval).
func (p *PolicyService) baseline(a domain.Agent, tool, action, risk string) (bool, string, string) {
	switch {
	case strings.EqualFold(risk, "high"):
		return true, "baseline.risk_high", "the runtime flagged this action as high risk"
	case p.Cfg.ApprovalActions[action]:
		return true, "baseline.approval_action:" + action, fmt.Sprintf("action %s is on the always-approve list", action)
	case a.Autonomy == "suggest" || a.Autonomy == "approve_each":
		return true, "baseline.autonomy:" + a.Autonomy, fmt.Sprintf("agent autonomy %s requires approval", a.Autonomy)
	}
	return false, "", ""
}

func mapAutonomy(a string) policy.Autonomy {
	switch a {
	case "suggest", "approve_each":
		// The engine treats suggest as "deny"; here suggest has always meant
		// "a human decides", so both ask for approval.
		return policy.AutonomyApproveEach
	case "autonomous":
		return policy.AutonomyAutonomous
	}
	return policy.AutonomyRules
}

func (p *PolicyService) engineConfig(r catalog.PackRules, agent domain.Agent) policy.Config {
	var g policy.Governance
	if r.Governance != nil {
		g = *r.Governance
	}
	always := make([]string, 0, len(p.Cfg.ApprovalActions)+len(r.AlwaysApprove))
	for a := range p.Cfg.ApprovalActions {
		always = append(always, a)
	}
	slices.Sort(always)
	always = append(always, r.AlwaysApprove...)
	cfg := policy.Config{
		Org:      policy.OrgRules{AlwaysApprove: always, DenyActions: g.DenyActions, ApprovalAmount: r.ApprovalAmountUSD},
		Autonomy: map[string]policy.Autonomy{agent.ID: mapAutonomy(agent.Autonomy)},
		// Without a grant the engine denies. Which agent may use which tool is
		// the job of the tool grants and the gateway; here every agent starts
		// allowed and the rules below only restrict.
		Grants: []policy.Grant{{ID: "org.default", Agent: "*", Tool: "*", Action: "*", Effect: policy.EffectAllow}},
		Limits: g.EngineLimits(),
	}
	if g.NewRecipient {
		cfg.Grants = append(cfg.Grants, policy.Grant{ID: "org.new_recipient", Agent: "*", Tool: "*", Action: "*", Effect: policy.EffectRequireApproval,
			When: policy.Conditions{Recipient: policy.RecipientNew}, Description: "the recipient is not a known contact"})
	}
	if len(g.ApprovalCategories) > 0 {
		cfg.Grants = append(cfg.Grants, policy.Grant{ID: "org.categories", Agent: "*", Tool: "*", Action: "*", Effect: policy.EffectRequireApproval,
			When: policy.Conditions{Categories: g.ApprovalCategories}, Description: "the action touches a category that needs approval"})
	}
	return cfg
}

func (p *PolicyService) forOrg(org string) *orgPolicy {
	if p.orgs == nil {
		p.orgs = map[string]*orgPolicy{}
	}
	op := p.orgs[org]
	if op == nil {
		op = &orgPolicy{contacts: &contactSwitch{}}
		p.orgs[org] = op
	}
	return op
}

// prepare loads the organization rules into the org's engine (counters of the
// windowed limits are kept across reloads). Caller holds p.mu.
func (p *PolicyService) prepare(ctx context.Context, org string, in PolicyInput) (*policy.Engine, catalog.PackRules, error) {
	st, err := p.Store.GetOrgSettings(ctx, org)
	if err != nil {
		return nil, catalog.PackRules{}, err
	}
	rules := st.Rules
	op := p.forOrg(org)
	var g policy.Governance
	if rules.Governance != nil {
		g = *rules.Governance
	}
	op.contacts.set(policy.NewStaticContacts(g))
	cfg := p.engineConfig(rules, in.Agent)
	if op.eng == nil {
		eng, err := policy.NewEngine(cfg, op.contacts)
		if err != nil {
			return nil, rules, err
		}
		// The persisted usage must be loaded before the first decision: an
		// unreadable snapshot fails like unreadable rules (towards a human)
		// instead of silently starting the windows from zero.
		if err := p.loadUsage(ctx, org, eng); err != nil {
			return nil, rules, err
		}
		op.eng = eng
		return eng, rules, nil
	}
	if err := op.eng.SetConfig(cfg); err != nil {
		return nil, rules, err
	}
	return op.eng, rules, nil
}

func identityOf(org string, in PolicyInput) policy.AgentIdentity {
	return policy.AgentIdentity{OrgID: org, AgentID: in.Agent.ID, OnBehalfOf: in.RequestedBy}
}

func callOf(in PolicyInput) policy.ToolCall {
	return policy.ToolCall{Tool: in.Tool, Action: in.Action, Args: in.Args, Risk: in.Risk}
}

// Decide returns the verdict for one tool request.
func (p *PolicyService) Decide(ctx context.Context, in PolicyInput) PolicyVerdict {
	org := OrgFrom(ctx, p.Cfg.OrgID)
	v := PolicyVerdict{Effect: string(policy.EffectAllow), RequiredApprovals: 1, Source: "baseline", Autonomy: in.Agent.Autonomy}
	if need, rule, why := p.baseline(in.Agent, in.Tool, in.Action, in.Risk); need {
		v.Effect, v.RuleID, v.Reason = string(policy.EffectRequireApproval), rule, why
	}
	id, call := identityOf(org, in), callOf(in)
	v.ArgsHash = policy.Fingerprint(id, call)
	if a, ok := policy.MaxAmount(in.Args); ok {
		v.Amount = &a
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	eng, rules, err := p.prepare(ctx, org, in)
	if err != nil {
		// Fail towards a human: rules that cannot be read must not become "allow".
		v.Source = "engine"
		if v.Effect == string(policy.EffectAllow) {
			v.Effect, v.RuleID, v.Reason = string(policy.EffectRequireApproval), "policy.unavailable", "organization rules could not be loaded; approval required"
		}
		return v
	}
	if !configured(rules) {
		return v
	}
	v.Source = "engine"
	d := eng.Evaluate(ctx, id, call)
	v.Trace = d.Trace
	eff := d.Verdict()
	if d.Deny && d.RuleID == "call.invalid" {
		eff = string(policy.EffectRequireApproval) // odd tool names go to a human, they are not blocked
	}
	if rank(eff) > rank(v.Effect) {
		v.Effect, v.RuleID, v.Reason = eff, d.RuleID, d.Reason
	}
	var req policy.Requirements
	req.RequiredApprovals = 1
	if rules.Governance != nil {
		req = rules.Governance.RequirementsFor(in.Tool, in.Action, in.Args, in.Risk)
	}
	if req.TierRule != "" && v.Allowed() {
		v.Effect, v.RuleID, v.Reason = string(policy.EffectRequireApproval), req.TierRule, "the amount is above an approval tier"
	}
	if req.RequiredApprovals > 1 && !v.Denied() {
		// Double approval is a hard requirement: no autonomy level skips it.
		if v.Allowed() || (req.TierRule != "" && v.RuleID == req.TierRule) || strings.HasPrefix(v.RuleID, "baseline.") {
			v.Effect, v.RuleID, v.Reason = string(policy.EffectRequireApproval), req.DualRule, "double approval is required for this action"
		}
		v.Trace = append(v.Trace, req.DualRule)
	}
	if v.NeedsApproval() {
		v.RequiredRole, v.RequiredApprovals, v.NoSelfApproval = req.RequiredRole, max(req.RequiredApprovals, 1), req.NoSelfApproval
	}
	return v
}

// Reserve records the usage of an action that is about to run without approval
// against the windowed limits. The decision to run was already taken by
// Decide; this only fails when a concurrent call used the last slot meanwhile.
func (p *PolicyService) Reserve(ctx context.Context, in PolicyInput) PolicyVerdict {
	org := OrgFrom(ctx, p.Cfg.OrgID)
	v := PolicyVerdict{Effect: string(policy.EffectAllow), RequiredApprovals: 1, Source: "baseline", Autonomy: in.Agent.Autonomy}
	p.mu.Lock()
	defer p.mu.Unlock()
	eng, rules, err := p.prepare(ctx, org, in)
	if err != nil || !configured(rules) {
		return v
	}
	v.Source = "engine"
	if d := eng.EvaluateAndReserve(ctx, identityOf(org, in), callOf(in)); !d.Allow {
		v.Effect, v.RuleID, v.Reason = d.Verdict(), d.RuleID, d.Reason
		if d.RequireApproval {
			v.Effect = string(policy.EffectDeny) // it was decided as allowed a moment ago: do not run it
		}
	} else {
		p.saveUsage(ctx, org, eng)
	}
	return v
}

// Recheck re-validates an approved request at execution time (rules or limits
// may have changed while it waited). Approval satisfies "require approval"
// only: a deny rule or an exhausted limit still stops it. Each approval can be
// used once.
func (p *PolicyService) Recheck(ctx context.Context, in PolicyInput, approvalID string, approvers []string) PolicyVerdict {
	org := OrgFrom(ctx, p.Cfg.OrgID)
	v := PolicyVerdict{Effect: string(policy.EffectAllow), RequiredApprovals: 1, Source: "baseline", Autonomy: in.Agent.Autonomy}
	p.mu.Lock()
	defer p.mu.Unlock()
	eng, rules, err := p.prepare(ctx, org, in)
	if err != nil {
		v.Effect, v.RuleID, v.Reason, v.Source = string(policy.EffectDeny), "policy.unavailable", "organization rules could not be loaded; the approved action was not run", "engine"
		return v
	}
	if !configured(rules) {
		return v
	}
	v.Source = "engine"
	id, call := identityOf(org, in), callOf(in)
	tok := policy.ApprovalToken{ApprovalID: approvalID, ApprovedBy: strings.Join(approvers, "+"), Fingerprint: policy.Fingerprint(id, call)}
	if d := eng.EvaluateApproved(ctx, id, call, tok); !d.Allow {
		v.Effect, v.RuleID, v.Reason = d.Verdict(), d.RuleID, d.Reason
		if d.RequireApproval {
			v.Effect = string(policy.EffectDeny)
		}
	} else {
		p.saveUsage(ctx, org, eng)
	}
	return v
}

// loadUsage restores the persisted usage of the windowed limits into a new engine.
func (p *PolicyService) loadUsage(ctx context.Context, org string, eng *policy.Engine) error {
	if p.Counters == nil {
		return nil
	}
	b, err := p.Counters.LoadCounters(ctx, org, counters.ScopePolicyLimits)
	if err != nil {
		return fmt.Errorf("load windowed limit counters: %w", err)
	}
	if len(b) == 0 {
		return nil
	}
	var u map[string][]policy.UsageEvent
	if err := json.Unmarshal(b, &u); err != nil {
		return fmt.Errorf("decode windowed limit counters: %w", err)
	}
	eng.RestoreUsage(u)
	return nil
}

// saveUsage persists the usage after it changed. A failure keeps the counters
// correct in this process (only a restart would forget them) and is logged.
// Caller holds p.mu, so snapshots of one process are written in order.
func (p *PolicyService) saveUsage(ctx context.Context, org string, eng *policy.Engine) {
	if p.Counters == nil {
		return
	}
	b, err := json.Marshal(eng.UsageSnapshot())
	if err == nil {
		err = p.Counters.SaveCounters(context.WithoutCancel(ctx), org, counters.ScopePolicyLimits, b)
	}
	if err != nil {
		log := p.Log
		if log == nil {
			log = slog.Default()
		}
		log.Warn("persist windowed limit counters", "org", org, "err", err)
	}
}

// argKeys returns the sorted argument names of a tool call: the audit trail
// records which fields were sent, never their values.
func argKeys(args map[string]any) []string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
