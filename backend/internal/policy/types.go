// Package policy implements the authorization and autonomy engine.
//
// Principles:
//   - Default deny: a call is only allowed when a grant says so.
//   - The strictest verdict always wins (deny > require_approval > allow).
//   - Nothing coming from the runtime (args, risk, external content) can ever
//     raise a permission. Risk can only escalate; args are only used as facts
//     (amount, recipients, categories) that can only add restrictions.
//   - Delegation never elevates: every agent in the chain must be allowed.
package policy

import (
	"regexp"
	"strings"
	"time"
)

// Autonomy is the per-agent autonomy level.
type Autonomy string

const (
	// AutonomySuggest: the agent may only read/draft; effectful actions are
	// denied (recorded as suggestions, a human performs them).
	AutonomySuggest Autonomy = "suggest"
	// AutonomyApproveEach: every effectful action needs approval.
	AutonomyApproveEach Autonomy = "approve_each"
	// AutonomyRules: grants decide; runtime risk "high" forces approval.
	AutonomyRules Autonomy = "rules"
	// AutonomyAutonomous: grants decide; runtime risk is not escalated.
	// Org rules, deny grants, limits and delegation depth still apply.
	AutonomyAutonomous Autonomy = "autonomous"
)

// Valid reports whether a is a known level.
func (a Autonomy) Valid() bool {
	switch a {
	case AutonomySuggest, AutonomyApproveEach, AutonomyRules, AutonomyAutonomous:
		return true
	}
	return false
}

// Effect is the outcome a grant prescribes.
type Effect string

const (
	EffectAllow           Effect = "allow"
	EffectRequireApproval Effect = "require_approval"
	EffectDeny            Effect = "deny"
)

// Valid reports whether e is a known effect.
func (e Effect) Valid() bool {
	return e == EffectAllow || e == EffectRequireApproval || e == EffectDeny
}

// Rank orders effects by strictness: allow < require_approval < deny.
func (e Effect) Rank() int {
	switch e {
	case EffectDeny:
		return 2
	case EffectRequireApproval:
		return 1
	}
	return 0
}

// RecipientScope is a grant condition on recipients.
type RecipientScope string

const (
	RecipientAny   RecipientScope = ""      // condition not used
	RecipientNew   RecipientScope = "new"   // applies when at least one recipient is unknown
	RecipientKnown RecipientScope = "known" // applies when all recipients are known (and there is at least one)
)

// Conditions restrict when a grant applies. All set conditions must hold.
type Conditions struct {
	AmountAbove  *float64       `json:"amount_above,omitempty"`   // applies if amount > X (or amount is unparseable)
	AmountAtMost *float64       `json:"amount_at_most,omitempty"` // applies if a valid amount <= X
	Recipient    RecipientScope `json:"recipient,omitempty"`
	Categories   []string       `json:"categories,omitempty"` // applies if the call has any of these categories
}

// Grant is a permission rule. Agent, Tool and Action accept "*" or path-style
// globs ("send_*").
type Grant struct {
	ID          string     `json:"id"`
	Agent       string     `json:"agent"`
	Tool        string     `json:"tool"`
	Action      string     `json:"action"`
	Effect      Effect     `json:"effect"`
	When        Conditions `json:"when,omitempty"`
	Description string     `json:"description,omitempty"`
}

// AgentIdentity says who is acting and on whose behalf.
type AgentIdentity struct {
	OrgID      string `json:"org_id"`
	AgentID    string `json:"agent_id"`
	OnBehalfOf string `json:"on_behalf_of"`
	// Chain is the delegation chain, first element is the originating
	// principal (human or agent), last element must be AgentID.
	// Empty means [OnBehalfOf, AgentID] (or just [AgentID]).
	Chain []string `json:"chain,omitempty"`
}

// ToolCall is a request to run an action of a tool.
type ToolCall struct {
	Tool   string         `json:"tool"`
	Action string         `json:"action"`
	Args   map[string]any `json:"args,omitempty"`
	// Risk is the risk declared by the runtime ("low","medium","high"). It can
	// only escalate decisions, never lower them.
	Risk string `json:"risk,omitempty"`
	// Source is informational (task id, "runtime", ...); never used to decide.
	Source string `json:"source,omitempty"`
}

// Decision is the result of Evaluate. Exactly one of Allow, RequireApproval
// and Deny is true.
type Decision struct {
	Allow           bool     `json:"allow"`
	RequireApproval bool     `json:"require_approval"`
	Deny            bool     `json:"deny"`
	Reason          string   `json:"reason"`
	RuleID          string   `json:"rule_id"`
	Trace           []string `json:"trace,omitempty"` // every non-allow rule that fired
}

// Verdict returns "allow", "require_approval" or "deny".
func (d Decision) Verdict() string {
	switch {
	case d.Deny:
		return string(EffectDeny)
	case d.RequireApproval:
		return string(EffectRequireApproval)
	case d.Allow:
		return string(EffectAllow)
	}
	return string(EffectDeny)
}

func (d Decision) effect() Effect { return Effect(d.Verdict()) }

func mk(e Effect, ruleID, reason string) Decision {
	d := Decision{Reason: reason, RuleID: ruleID}
	switch e {
	case EffectAllow:
		d.Allow = true
	case EffectRequireApproval:
		d.RequireApproval = true
	default:
		d.Deny = true
	}
	return d
}

// ContactBook tells whether an address is a known contact. Contacts must only
// be added through trusted paths (CRM), never because someone wrote to us.
type ContactBook interface {
	IsKnown(orgID, address string) bool
}

// OrgRules are organization-wide rules.
type OrgRules struct {
	// AlwaysApprove actions (name or tool.name, globs allowed) always need approval.
	AlwaysApprove []string `json:"always_approve"`
	// DenyActions are never allowed.
	DenyActions []string `json:"deny_actions,omitempty"`
	// MaxDelegationDepth is the max number of delegation hops (default 5).
	MaxDelegationDepth int `json:"max_delegation_depth"`
	// ApprovalAmount: any call with amount above this needs approval (0 = off).
	ApprovalAmount float64 `json:"approval_amount"`
	// BudgetUSD: once spent reaches it every call is denied (0 = unlimited).
	BudgetUSD float64 `json:"budget_usd"`
}

// DefaultAlwaysApprove is the baseline list of actions that need approval.
var DefaultAlwaysApprove = []string{
	"send_contract", "make_payment", "hire", "fire",
	"send_binding_offer", "modify_financials", "send_proposal",
}

// MaxDelegationDepthDefault is the spec default.
const MaxDelegationDepthDefault = 5

// Limit caps calls and/or cumulative amount per sliding window.
type Limit struct {
	ID        string        `json:"id"`
	Agent     string        `json:"agent"`  // "*" = every agent (counted per agent unless PerOrg)
	Tool      string        `json:"tool"`   // glob
	Action    string        `json:"action"` // glob
	Window    time.Duration `json:"window"`
	MaxCalls  int           `json:"max_calls,omitempty"`
	MaxAmount float64       `json:"max_amount,omitempty"`
	PerOrg    bool          `json:"per_org,omitempty"`
	// OnExceed is deny (default) or require_approval.
	OnExceed Effect `json:"on_exceed,omitempty"`
}

// Config is the full policy configuration of an organization.
type Config struct {
	Org      OrgRules            `json:"org"`
	Autonomy map[string]Autonomy `json:"autonomy"` // agent id -> level; also the set of known agents
	Grants   []Grant             `json:"grants"`
	Limits   []Limit             `json:"limits,omitempty"`
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// NormalizeName lower-cases and canonicalizes a tool/action name.
func NormalizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("-", "_", " ", "_").Replace(s)
	return s
}

// ValidName reports whether a normalized name is acceptable.
func ValidName(s string) bool { return nameRe.MatchString(s) }

var readPrefixes = []string{
	"read", "get", "list", "search", "find", "view", "draft", "calculate", "calc",
	"lookup", "summarize", "query", "analyze", "compute", "evaluate", "check", "preview",
	"margin", "percent",
}

// IsReadOnly reports whether the action has no external side effects. Unknown
// actions are treated as effectful (fail-safe).
func IsReadOnly(action string) bool {
	a := NormalizeName(action)
	for _, p := range readPrefixes {
		if a == p || strings.HasPrefix(a, p+"_") {
			return true
		}
	}
	return false
}
