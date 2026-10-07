package policy

import (
	"strings"
	"testing"
	"time"
)

func TestGovernanceValidate(t *testing.T) {
	ok := Governance{
		DenyActions:  []string{"make_payment", "crm.delete_*"},
		AmountTiers:  []AmountTier{{AmountAbove: 5000, Role: "admin"}, {AmountAbove: 50000, Role: "owner", Action: "make_payment"}},
		ActionRoles:  []ActionRole{{Action: "send_contract", Role: "owner"}},
		DualApproval: &DualApproval{Actions: []string{"make_payment"}, AmountAbove: 20000, HighRisk: true},
		Limits:       []LimitRule{{ID: "mail", Tool: "email", Action: "send*", WindowSeconds: 3600, MaxCalls: 30}},
		KnownDomains: []string{"acme.com"},
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid governance rejected: %v", err)
	}
	bad := map[string]Governance{
		"tier role member":      {AmountTiers: []AmountTier{{AmountAbove: 1, Role: "member"}}},
		"tier negative amount":  {AmountTiers: []AmountTier{{AmountAbove: -1, Role: "admin"}}},
		"action role unknown":   {ActionRoles: []ActionRole{{Action: "x", Role: "god"}}},
		"action role no action": {ActionRoles: []ActionRole{{Role: "admin"}}},
		"dual without trigger":  {DualApproval: &DualApproval{}},
		"limit without window":  {Limits: []LimitRule{{ID: "a", MaxCalls: 1}}},
		"limit without max":     {Limits: []LimitRule{{ID: "a", WindowSeconds: 60}}},
		"limit duplicate id":    {Limits: []LimitRule{{ID: "a", WindowSeconds: 60, MaxCalls: 1}, {ID: "a", WindowSeconds: 60, MaxCalls: 2}}},
		"limit bad on_exceed":   {Limits: []LimitRule{{ID: "a", WindowSeconds: 60, MaxCalls: 1, OnExceed: "allow"}}},
		"bad glob":              {DenyActions: []string{"[abc"}},
		"domain with at":        {KnownDomains: []string{"@acme.com"}},
	}
	for name, g := range bad {
		if err := g.Validate(); err == nil {
			t.Errorf("%s: must be rejected", name)
		}
	}
}

func TestGovernanceIsZero(t *testing.T) {
	if !(Governance{}).IsZero() {
		t.Fatal("empty governance is zero")
	}
	for _, g := range []Governance{{NewRecipient: true}, {ForbidSelfApproval: true}, {DenyActions: []string{"x"}}, {DualApproval: &DualApproval{HighRisk: true}}} {
		if g.IsZero() {
			t.Fatalf("%+v is not zero", g)
		}
	}
}

func TestRequirementsByAmountAndAction(t *testing.T) {
	g := Governance{
		AmountTiers: []AmountTier{{AmountAbove: 5000, Role: "admin"}, {AmountAbove: 50000, Role: "owner"}},
		ActionRoles: []ActionRole{{Action: "send_contract", Role: "owner"}},
	}
	cases := []struct {
		name   string
		tool   string
		action string
		args   map[string]any
		role   string
	}{
		{"small amount: anyone allowed to decide", "ledger", "make_payment", map[string]any{"amount": 100}, ""},
		{"mid amount needs admin", "ledger", "make_payment", map[string]any{"amount": 7000}, "admin"},
		{"large amount needs owner", "ledger", "make_payment", map[string]any{"amount": "$60,000"}, "owner"},
		{"the highest rule wins: action role beats amount tier", "documents", "send_contract", map[string]any{"amount": 7000}, "owner"},
		{"unparseable amount counts as above every tier", "ledger", "make_payment", map[string]any{"amount": "a lot"}, "owner"},
		{"no amount, no rule", "crm", "update_deal", map[string]any{"stage": "won"}, ""},
	}
	for _, tc := range cases {
		got := g.RequirementsFor(tc.tool, tc.action, tc.args, "low")
		if got.RequiredRole != tc.role {
			t.Errorf("%s: role = %q, want %q", tc.name, got.RequiredRole, tc.role)
		}
		if got.RequiredApprovals != 1 {
			t.Errorf("%s: without dual approval one approver is needed, got %d", tc.name, got.RequiredApprovals)
		}
	}
}

func TestAmountTierCanBeScopedToAnAction(t *testing.T) {
	g := Governance{AmountTiers: []AmountTier{{AmountAbove: 1000, Role: "owner", Action: "make_payment"}}}
	if r := g.RequirementsFor("ledger", "make_payment", map[string]any{"amount": 2000}, ""); r.RequiredRole != "owner" {
		t.Fatalf("scoped tier must apply: %+v", r)
	}
	if r := g.RequirementsFor("crm", "update_deal", map[string]any{"amount": 2000}, ""); r.RequiredRole != "" {
		t.Fatalf("scoped tier must not apply to other actions: %+v", r)
	}
}

func TestDualApprovalTriggers(t *testing.T) {
	g := Governance{DualApproval: &DualApproval{Actions: []string{"make_payment", "ledger.wire_*"}, AmountAbove: 20000, HighRisk: true}}
	cases := []struct {
		name   string
		tool   string
		action string
		args   map[string]any
		risk   string
		rule   string
	}{
		{"by action", "ledger", "make_payment", map[string]any{"amount": 5}, "low", "dual_approval.action:make_payment"},
		{"by tool.action glob", "ledger", "wire_transfer", nil, "low", "dual_approval.action:ledger.wire_*"},
		{"by amount", "crm", "update_deal", map[string]any{"deal_value": 25000}, "low", "dual_approval.amount"},
		{"by unparseable amount", "crm", "update_deal", map[string]any{"deal_value": "huge"}, "low", "dual_approval.amount"},
		{"by high risk", "email", "send", nil, "HIGH", "dual_approval.high_risk"},
		{"not triggered", "email", "send", map[string]any{"amount": 10}, "medium", ""},
	}
	for _, tc := range cases {
		r := tc.rule
		got := g.RequirementsFor(tc.tool, tc.action, tc.args, tc.risk)
		if got.DualRule != r {
			t.Errorf("%s: rule = %q, want %q", tc.name, got.DualRule, r)
		}
		wantN := 1
		if r != "" {
			wantN = 2
		}
		if got.RequiredApprovals != wantN || (r != "" && !got.NoSelfApproval) {
			t.Errorf("%s: %+v (double approval always forbids self approval)", tc.name, got)
		}
	}
}

func TestForbidSelfApprovalWithoutDual(t *testing.T) {
	r := Governance{ForbidSelfApproval: true}.RequirementsFor("email", "send", nil, "")
	if !r.NoSelfApproval || r.RequiredApprovals != 1 {
		t.Fatalf("%+v", r)
	}
}

func TestStaticContactsAreDataSetByTheOwner(t *testing.T) {
	c := NewStaticContacts(Governance{KnownDomains: []string{"Acme.com"}, KnownContacts: []string{"Friend@other.org"}})
	for addr, want := range map[string]bool{
		"ana@acme.com": true, "ANA@ACME.COM": true, "friend@other.org": true,
		"stranger@other.org": false, "ana@sub.acme.com": false, "ana@acme.com.evil.io": false, "acme.com": false, "": false,
	} {
		if c.IsKnown("org", addr) != want {
			t.Errorf("IsKnown(%q) != %v", addr, want)
		}
	}
}

func TestEngineLimitsConversion(t *testing.T) {
	ls := Governance{Limits: []LimitRule{{ID: "a", Tool: "Email", Action: "send*", WindowSeconds: 90, MaxCalls: 3, OnExceed: "require_approval"}}}.EngineLimits()
	if len(ls) != 1 || ls[0].Agent != "*" || ls[0].Tool != "email" || ls[0].Window != 90*time.Second || ls[0].OnExceed != EffectRequireApproval {
		t.Fatalf("%+v", ls)
	}
}

func TestRoleRankOrdersRoles(t *testing.T) {
	if !(RoleRank("owner") > RoleRank("admin") && RoleRank("admin") > RoleRank("member") && RoleRank("member") > RoleRank("viewer") && RoleRank("viewer") > RoleRank("")) {
		t.Fatal("role order")
	}
	if RoleRank(" Admin ") != RoleRank("admin") || RoleRank("nobody") != 0 {
		t.Fatal("role parsing")
	}
}

// The engine, configured the way PolicyService configures it for an
// organization with rules: every agent allowed, the rules only restrict.
func orgEngine(t *testing.T, g Governance, amount float64, always ...string) *Engine {
	t.Helper()
	cfg := Config{
		Org:      OrgRules{AlwaysApprove: always, DenyActions: g.DenyActions, ApprovalAmount: amount},
		Autonomy: map[string]Autonomy{"sales": AutonomyRules},
		Grants:   []Grant{{ID: "org.default", Agent: "*", Tool: "*", Action: "*", Effect: EffectAllow}},
		Limits:   g.EngineLimits(),
	}
	if g.NewRecipient {
		cfg.Grants = append(cfg.Grants, Grant{ID: "org.new_recipient", Agent: "*", Tool: "*", Action: "*", Effect: EffectRequireApproval,
			When: Conditions{Recipient: RecipientNew}})
	}
	e, err := NewEngine(cfg, NewStaticContacts(g))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestOrgRulesThroughTheEngine(t *testing.T) {
	g := Governance{NewRecipient: true, KnownDomains: []string{"acme.com"}, DenyActions: []string{"delete_*"},
		Limits: []LimitRule{{ID: "mail.hourly", Tool: "email", Action: "send*", WindowSeconds: 3600, MaxCalls: 2}}}
	e := orgEngine(t, g, 3000, "send_contract")
	id := AgentIdentity{OrgID: "o", AgentID: "sales", OnBehalfOf: "user-1"}
	eval := func(tool, action string, args map[string]any) Decision {
		return e.Evaluate(t.Context(), id, ToolCall{Tool: tool, Action: action, Args: args})
	}
	if d := eval("crm", "update_deal", map[string]any{"stage": "x"}); !d.Allow {
		t.Fatalf("plain action: %+v", d)
	}
	if d := eval("ledger", "make_payment", map[string]any{"amount": 3500}); !d.RequireApproval || d.RuleID != "org.amount" {
		t.Fatalf("amount above the seeded threshold: %+v", d)
	}
	if d := eval("ledger", "make_payment", map[string]any{"amount": 2999}); !d.Allow {
		t.Fatalf("amount below the threshold: %+v", d)
	}
	if d := eval("documents", "send_contract", nil); !d.RequireApproval || !strings.HasPrefix(d.RuleID, "org.always_approve") {
		t.Fatalf("always approve: %+v", d)
	}
	if d := eval("crm", "delete_contact", nil); !d.Deny {
		t.Fatalf("deny action: %+v", d)
	}
	if d := eval("email", "send", map[string]any{"to": "ana@acme.com"}); !d.Allow {
		t.Fatalf("known recipient: %+v", d)
	}
	if d := eval("email", "send", map[string]any{"to": "ceo@rival.com"}); !d.RequireApproval || d.RuleID != "org.new_recipient" {
		t.Fatalf("new recipient: %+v", d)
	}
	if d := eval("email", "send", map[string]any{"subject": "no recipient field"}); !d.RequireApproval {
		t.Fatalf("an outbound action with no recognizable recipient counts as new: %+v", d)
	}
	if d := eval("email", "send", map[string]any{"to": "x@acme.com", "amount": "abc"}); !d.RequireApproval {
		t.Fatalf("unparseable amount must ask a human: %+v", d)
	}
}

func TestWindowedLimitsPerAgentAndAmount(t *testing.T) {
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	g := Governance{Limits: []LimitRule{
		{ID: "mail", Tool: "email", Action: "send*", WindowSeconds: 3600, MaxCalls: 2},
		{ID: "pay", Tool: "ledger", Action: "make_payment", WindowSeconds: 86400, MaxAmount: 10000, OnExceed: "require_approval"},
	}}
	cfg := Config{Autonomy: map[string]Autonomy{"sales": AutonomyRules, "ops": AutonomyRules},
		Grants: []Grant{{ID: "d", Agent: "*", Tool: "*", Action: "*", Effect: EffectAllow}}, Limits: g.EngineLimits()}
	e, err := NewEngine(cfg, nil, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	sales := AgentIdentity{OrgID: "o", AgentID: "sales"}
	send := ToolCall{Tool: "email", Action: "send", Args: map[string]any{"to": "a@x.com"}}
	for i := 0; i < 2; i++ {
		if d := e.EvaluateAndReserve(t.Context(), sales, send); !d.Allow {
			t.Fatalf("call %d must pass: %+v", i+1, d)
		}
	}
	if d := e.EvaluateAndReserve(t.Context(), sales, send); !d.Deny || d.RuleID != "limit:mail" {
		t.Fatalf("third call in the window: %+v", d)
	}
	if d := e.EvaluateAndReserve(t.Context(), AgentIdentity{OrgID: "o", AgentID: "ops"}, send); !d.Allow {
		t.Fatalf("limits are counted per agent: %+v", d)
	}
	now = now.Add(61 * time.Minute)
	if d := e.EvaluateAndReserve(t.Context(), sales, send); !d.Allow {
		t.Fatalf("the window slid: %+v", d)
	}
	pay := func(a float64) Decision {
		return e.EvaluateAndReserve(t.Context(), sales, ToolCall{Tool: "ledger", Action: "make_payment", Args: map[string]any{"amount": a}})
	}
	if d := pay(6000); !d.Allow {
		t.Fatalf("first payment: %+v", d)
	}
	if d := pay(6000); !d.RequireApproval || d.RuleID != "limit:pay" {
		t.Fatalf("cumulative amount above the cap asks for approval: %+v", d)
	}
}
