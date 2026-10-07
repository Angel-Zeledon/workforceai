package application

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/policy"
)

// Organization approval rules as data (docs/architecture/06-permisos-autonomia.md
// section 3). They live in org_settings.rules next to what the onboarding pack
// seeded and are applied by PolicyService on every tool request.

// RulesView is what GET /policy/rules returns.
type RulesView struct {
	ApprovalAmountUSD float64           `json:"approval_amount_usd"`
	AlwaysApprove     []string          `json:"always_approve"`
	Governance        policy.Governance `json:"governance"`
	// Enforced is true when the organization has rules the engine applies. With
	// none, only the baseline applies (high risk, APPROVAL_ACTIONS, suggest and
	// approve_each autonomy).
	Enforced bool `json:"enforced"`
	// Baseline lists the invariants that no rule can weaken.
	Baseline []string `json:"baseline"`
}

// RulesUpdate replaces the parts that are set; nil leaves them unchanged.
type RulesUpdate struct {
	ApprovalAmountUSD *float64           `json:"approval_amount_usd"`
	AlwaysApprove     *[]string          `json:"always_approve"`
	Governance        *policy.Governance `json:"governance"`
}

var actionPatternRe = regexp.MustCompile(`^[a-z][a-z0-9_.*?\[\]-]*$`)

func (s *OrgConfig) rulesView(r catalog.PackRules) RulesView {
	v := RulesView{ApprovalAmountUSD: r.ApprovalAmountUSD, AlwaysApprove: append([]string{}, r.AlwaysApprove...), Enforced: configured(r),
		Baseline: []string{"runtime risk high always needs approval", "actions in APPROVAL_ACTIONS always need approval",
			"agents in suggest or approve_each mode need approval for every action", "agents can never approve"}}
	if r.Governance != nil {
		v.Governance = *r.Governance
	}
	return v
}

// Rules returns the approval rules of the organization.
func (s *OrgConfig) Rules(ctx context.Context) (RulesView, error) {
	st, err := s.Store.GetOrgSettings(ctx, s.org(ctx))
	if err != nil {
		return RulesView{}, err
	}
	return s.rulesView(st.Rules), nil
}

// UpdateRules changes the approval rules and audits the change.
func (s *OrgConfig) UpdateRules(ctx context.Context, in RulesUpdate) (RulesView, error) {
	org := s.org(ctx)
	st, err := s.Store.GetOrgSettings(ctx, org)
	if err != nil {
		return RulesView{}, err
	}
	r := st.Rules
	if in.ApprovalAmountUSD != nil {
		if *in.ApprovalAmountUSD < 0 {
			return RulesView{}, fmt.Errorf("%w: approval_amount_usd must be >= 0", domain.ErrInvalid)
		}
		r.ApprovalAmountUSD = *in.ApprovalAmountUSD
	}
	if in.AlwaysApprove != nil {
		var list []string
		for _, a := range *in.AlwaysApprove {
			n := policy.NormalizeName(a)
			if !actionPatternRe.MatchString(n) {
				return RulesView{}, fmt.Errorf("%w: invalid action %q in always_approve", domain.ErrInvalid, a)
			}
			if !slices.Contains(list, n) {
				list = append(list, n)
			}
		}
		if len(list) > 200 {
			return RulesView{}, fmt.Errorf("%w: too many always_approve entries", domain.ErrInvalid)
		}
		r.AlwaysApprove = list
	}
	if in.Governance != nil {
		g := *in.Governance
		g.KnownDomains, g.KnownContacts = lowerTrim(g.KnownDomains), lowerTrim(g.KnownContacts)
		if err := g.Validate(); err != nil {
			return RulesView{}, fmt.Errorf("%w: %v", domain.ErrInvalid, err)
		}
		if g.IsZero() {
			r.Governance = nil
		} else {
			r.Governance = &g
		}
	}
	if r.AlwaysApprove == nil {
		r.AlwaysApprove = []string{}
	}
	st.Rules = r
	if err := s.Store.PutOrgSettings(ctx, org, st); err != nil {
		return RulesView{}, err
	}
	d := map[string]any{"approval_amount_usd": r.ApprovalAmountUSD, "always_approve": r.AlwaysApprove}
	if g := r.Governance; g != nil {
		d["deny_actions"], d["new_recipient_requires_approval"] = g.DenyActions, g.NewRecipient
		d["amount_tiers"], d["action_roles"], d["limits"] = len(g.AmountTiers), len(g.ActionRoles), len(g.Limits)
		d["dual_approval"], d["forbid_self_approval"] = g.DualApproval != nil, g.ForbidSelfApproval
		d["known_domains"], d["known_contacts"] = len(g.KnownDomains), len(g.KnownContacts)
	}
	s.Rec.Audit(ctx, domain.AuditLog{Actor: ActorFrom(ctx, "system"), Action: "policy.rules_updated", Entity: "org", EntityID: org, Details: d})
	return s.rulesView(r), nil
}

func lowerTrim(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
