package policy

import (
	"fmt"
	"path"
	"strings"
	"time"
)

// Governance is the approval policy of an organization, stored as data (the
// "rules" of org_settings, next to the rules seeded by onboarding packs). It
// is evaluated by the engine plus the helpers below; an organization without
// governance and without seeded rules keeps the legacy behaviour.
//
// Everything in it can only ADD supervision: it can require approval, raise
// the role of the approver, ask for two approvers or deny. Nothing here can
// make an action easier than the invariants of the orchestrator (high risk,
// the APPROVAL_ACTIONS list, suggest/approve_each autonomy).
type Governance struct {
	// DenyActions are never allowed ("name" or "tool.name", globs allowed).
	DenyActions []string `json:"deny_actions,omitempty"`
	// NewRecipient makes an outbound action to a recipient that is not a known
	// contact (see KnownDomains/KnownContacts) require approval. An outbound
	// action with no recognizable recipient counts as new.
	NewRecipient  bool     `json:"new_recipient_requires_approval,omitempty"`
	KnownDomains  []string `json:"known_domains,omitempty"`  // "acme.com": every address of the domain is known
	KnownContacts []string `json:"known_contacts,omitempty"` // exact addresses
	// ApprovalCategories require approval when the call is detected as one of
	// them ("legal", "financial", or a declared category).
	ApprovalCategories []string `json:"approval_categories,omitempty"`
	// AmountTiers set the minimum role of the approver by amount: the highest
	// matching tier wins.
	AmountTiers []AmountTier `json:"amount_tiers,omitempty"`
	// ActionRoles set the minimum role of the approver by action type.
	ActionRoles []ActionRole `json:"action_roles,omitempty"`
	// DualApproval asks for two distinct human approvers on high-risk actions.
	DualApproval *DualApproval `json:"dual_approval,omitempty"`
	// Limits cap calls and/or cumulative amount per sliding window.
	Limits []LimitRule `json:"limits,omitempty"`
	// ForbidSelfApproval: the person on whose behalf the agent acts cannot
	// approve the resulting request (maker-checker). With DualApproval this is
	// always the case.
	ForbidSelfApproval bool `json:"forbid_self_approval,omitempty"`
}

// AmountTier: when the amount of the call is above AmountAbove, the approver
// needs at least Role. Action (optional glob) restricts the tier to some actions.
type AmountTier struct {
	AmountAbove float64 `json:"amount_above"`
	Role        string  `json:"role"`
	Action      string  `json:"action,omitempty"`
}

// ActionRole: approvals for Action (glob over "name" or "tool.name") need Role.
type ActionRole struct {
	Action string `json:"action"`
	Role   string `json:"role"`
}

// DualApproval selects the actions that need two approvers. A call matches
// when its action is in Actions, or its amount is above AmountAbove (0 = off),
// or HighRisk is set and the runtime flagged it as high risk.
type DualApproval struct {
	Actions     []string `json:"actions,omitempty"`
	AmountAbove float64  `json:"amount_above,omitempty"`
	HighRisk    bool     `json:"high_risk,omitempty"`
}

// LimitRule is the stored form of Limit (window in seconds).
type LimitRule struct {
	ID            string  `json:"id"`
	Agent         string  `json:"agent,omitempty"`  // default "*"
	Tool          string  `json:"tool,omitempty"`   // default "*"
	Action        string  `json:"action,omitempty"` // default "*"
	WindowSeconds int     `json:"window_seconds"`
	MaxCalls      int     `json:"max_calls,omitempty"`
	MaxAmount     float64 `json:"max_amount,omitempty"`
	PerOrg        bool    `json:"per_org,omitempty"`
	OnExceed      string  `json:"on_exceed,omitempty"` // deny (default) | require_approval
}

// IsZero reports whether no governance rule is set.
func (g Governance) IsZero() bool {
	return len(g.DenyActions) == 0 && !g.NewRecipient && len(g.KnownDomains) == 0 && len(g.KnownContacts) == 0 &&
		len(g.ApprovalCategories) == 0 && len(g.AmountTiers) == 0 && len(g.ActionRoles) == 0 &&
		g.DualApproval == nil && len(g.Limits) == 0 && !g.ForbidSelfApproval
}

// Approver roles, lowest to highest. They mirror auth.Role (a test keeps both
// in sync); only admin and owner can decide approvals.
const (
	RoleMember = "member"
	RoleAdmin  = "admin"
	RoleOwner  = "owner"
)

// RoleRank orders roles; unknown or empty roles rank 0.
func RoleRank(role string) int {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "owner":
		return 4
	case "admin":
		return 3
	case "member":
		return 2
	case "viewer":
		return 1
	}
	return 0
}

func validApproverRole(r string) bool {
	r = strings.ToLower(strings.TrimSpace(r))
	return r == RoleAdmin || r == RoleOwner
}

const maxGovernanceItems = 200

// Validate checks the rules and returns an error describing the first problem.
func (g Governance) Validate() error {
	for name, n := range map[string]int{"deny_actions": len(g.DenyActions), "known_domains": len(g.KnownDomains),
		"known_contacts": len(g.KnownContacts), "approval_categories": len(g.ApprovalCategories),
		"amount_tiers": len(g.AmountTiers), "action_roles": len(g.ActionRoles), "limits": len(g.Limits)} {
		if n > maxGovernanceItems {
			return fmt.Errorf("governance: too many %s (max %d)", name, maxGovernanceItems)
		}
	}
	for _, p := range g.DenyActions {
		if err := validPattern(p); err != nil {
			return fmt.Errorf("governance: deny_actions: %w", err)
		}
	}
	for _, t := range g.AmountTiers {
		if t.AmountAbove < 0 {
			return fmt.Errorf("governance: amount tier with negative amount_above")
		}
		if !validApproverRole(t.Role) {
			return fmt.Errorf("governance: amount tier role %q must be admin or owner", t.Role)
		}
		if t.Action != "" {
			if err := validPattern(t.Action); err != nil {
				return fmt.Errorf("governance: amount tier action: %w", err)
			}
		}
	}
	for _, r := range g.ActionRoles {
		if err := validPattern(r.Action); err != nil || strings.TrimSpace(r.Action) == "" {
			return fmt.Errorf("governance: action role needs a valid action pattern")
		}
		if !validApproverRole(r.Role) {
			return fmt.Errorf("governance: action role %q must be admin or owner", r.Role)
		}
	}
	if d := g.DualApproval; d != nil {
		if d.AmountAbove < 0 {
			return fmt.Errorf("governance: dual_approval.amount_above is negative")
		}
		if len(d.Actions) == 0 && d.AmountAbove == 0 && !d.HighRisk {
			return fmt.Errorf("governance: dual_approval needs actions, amount_above or high_risk")
		}
		for _, p := range d.Actions {
			if err := validPattern(p); err != nil {
				return fmt.Errorf("governance: dual_approval.actions: %w", err)
			}
		}
	}
	seen := map[string]bool{}
	for _, l := range g.Limits {
		if strings.TrimSpace(l.ID) == "" || seen[l.ID] {
			return fmt.Errorf("governance: every limit needs a unique id (%q)", l.ID)
		}
		seen[l.ID] = true
		if l.WindowSeconds <= 0 || l.WindowSeconds > 30*24*3600 {
			return fmt.Errorf("governance: limit %q window_seconds must be between 1 and 2592000", l.ID)
		}
		if l.MaxCalls < 0 || l.MaxAmount < 0 || (l.MaxCalls == 0 && l.MaxAmount == 0) {
			return fmt.Errorf("governance: limit %q needs max_calls or max_amount", l.ID)
		}
		if l.OnExceed != "" && l.OnExceed != string(EffectDeny) && l.OnExceed != string(EffectRequireApproval) {
			return fmt.Errorf("governance: limit %q on_exceed must be deny or require_approval", l.ID)
		}
		for _, p := range []string{l.Agent, l.Tool, l.Action} {
			if err := validPattern(p); err != nil {
				return fmt.Errorf("governance: limit %q: %w", l.ID, err)
			}
		}
	}
	for _, d := range g.KnownDomains {
		if strings.ContainsAny(d, "@ ") || strings.TrimSpace(d) == "" {
			return fmt.Errorf("governance: known_domains entries are bare domains such as acme.com (%q)", d)
		}
	}
	return nil
}

func validPattern(p string) error {
	if _, err := path.Match(NormalizeName(p), "x"); err != nil {
		return fmt.Errorf("bad pattern %q", p)
	}
	return nil
}

// EngineLimits converts the stored limit rules to engine limits.
func (g Governance) EngineLimits() []Limit {
	out := make([]Limit, 0, len(g.Limits))
	star := func(s string) string {
		if s = strings.TrimSpace(s); s == "" {
			return "*"
		}
		return NormalizeName(s)
	}
	for _, l := range g.Limits {
		out = append(out, Limit{ID: l.ID, Agent: star(l.Agent), Tool: star(l.Tool), Action: star(l.Action),
			Window: time.Duration(l.WindowSeconds) * time.Second, MaxCalls: l.MaxCalls, MaxAmount: l.MaxAmount,
			PerOrg: l.PerOrg, OnExceed: Effect(l.OnExceed)})
	}
	return out
}

// StaticContacts is a ContactBook backed by the governance data.
type StaticContacts struct {
	domains map[string]bool
	addrs   map[string]bool
}

// NewStaticContacts builds the book of known recipients.
func NewStaticContacts(g Governance) *StaticContacts {
	c := &StaticContacts{domains: map[string]bool{}, addrs: map[string]bool{}}
	for _, d := range g.KnownDomains {
		c.domains[strings.ToLower(strings.TrimSpace(d))] = true
	}
	for _, a := range g.KnownContacts {
		c.addrs[strings.ToLower(strings.TrimSpace(a))] = true
	}
	return c
}

// IsKnown implements ContactBook. Contacts are data set by an owner, never
// learned from incoming mail.
func (c *StaticContacts) IsKnown(_ string, address string) bool {
	a := strings.ToLower(strings.TrimSpace(address))
	if c.addrs[a] {
		return true
	}
	if i := strings.LastIndexByte(a, '@'); i > 0 {
		return c.domains[a[i+1:]]
	}
	return false
}

func matchesAction(pattern, tool, action string) bool {
	p := NormalizeName(pattern)
	return glob(p, action) || glob(p, tool+"."+action)
}

// Requirements are the approval requirements the governance adds to a call.
type Requirements struct {
	RequiredRole      string
	RequiredApprovals int    // 1 or 2
	DualRule          string // why two approvers ("" when single)
	NoSelfApproval    bool
	// TierRule is set when an amount tier matched: an amount above a tier needs
	// approval by that tier's role, whatever the other thresholds say.
	TierRule string
}

// RequirementsFor computes who must approve a call. It does not decide whether
// approval is needed (the engine does); it only says how strict it is.
func (g Governance) RequirementsFor(tool, action string, args map[string]any, risk string) Requirements {
	tool, action = NormalizeName(tool), NormalizeName(action)
	f := analyze(args)
	amount, hasAmount := f.maxAmount()
	// An amount that cannot be interpreted is treated as above every threshold.
	over := func(limit float64) bool { return f.amountInvalid || (hasAmount && amount > limit) }
	req := Requirements{RequiredApprovals: 1, NoSelfApproval: g.ForbidSelfApproval}
	raise := func(role string) {
		role = strings.ToLower(strings.TrimSpace(role))
		if RoleRank(role) > RoleRank(req.RequiredRole) {
			req.RequiredRole = role
		}
	}
	for _, t := range g.AmountTiers {
		if over(t.AmountAbove) && (t.Action == "" || matchesAction(t.Action, tool, action)) {
			raise(t.Role)
			req.TierRule = fmt.Sprintf("org.amount_tier:%s>%g", strings.ToLower(t.Role), t.AmountAbove)
		}
	}
	for _, r := range g.ActionRoles {
		if matchesAction(r.Action, tool, action) {
			raise(r.Role)
		}
	}
	if d := g.DualApproval; d != nil {
		for _, p := range d.Actions {
			if matchesAction(p, tool, action) {
				req.DualRule = "dual_approval.action:" + NormalizeName(p)
				break
			}
		}
		if req.DualRule == "" && d.AmountAbove > 0 && over(d.AmountAbove) {
			req.DualRule = "dual_approval.amount"
		}
		if req.DualRule == "" && d.HighRisk && strings.EqualFold(strings.TrimSpace(risk), "high") {
			req.DualRule = "dual_approval.high_risk"
		}
		if req.DualRule != "" {
			req.RequiredApprovals = 2
			req.NoSelfApproval = true
		}
	}
	return req
}
