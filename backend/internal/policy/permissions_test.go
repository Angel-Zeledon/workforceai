package policy

import (
	"context"
	"testing"
	"time"
)

func TestActionKind(t *testing.T) {
	cases := []struct {
		tool, action string
		args         map[string]any
		want         string
	}{
		{"email", "send", nil, KindMessages},
		{"gmail", "send_email", nil, KindMessages},
		{"slack", "post_message", nil, KindMessages},
		{"email", "send_reply", nil, KindMessages},
		{"documents", "share", nil, KindMessages},
		{"documents", "send_proposal", nil, KindContracts},
		{"legal", "send_contract", nil, KindContracts},
		{"ledger", "make_payment", map[string]any{"amount": 10}, KindPayments},
		{"billing", "send_invoice", nil, KindPayments},
		{"fs", "delete", nil, KindDelete},
		{"crm", "remove_contact", nil, KindDelete},
		{"social", "publish_post", nil, KindPublish},
		{"legal", "draft_contract", nil, KindInternal},
		{"email", "read", nil, KindInternal},
		{"documents", "create", nil, KindInternal},
		{"fs", "write", nil, KindInternal},
		{"spreadsheet", "set_cell", nil, KindInternal},
		{"crm", "log_note", nil, KindInternal},
		{"calendar", "schedule", nil, KindOther},
		{"crm", "update_deal", nil, KindOther},
		{"purchasing", "create_purchase_order", map[string]any{"amount_usd": 900}, KindSpend},
		{"travel", "book_flight", map[string]any{"total": "1.200"}, KindSpend},
	}
	for _, c := range cases {
		if got := ActionKind(c.tool, c.action, c.args); got != c.want {
			t.Errorf("ActionKind(%s.%s) = %s, want %s", c.tool, c.action, got, c.want)
		}
	}
}

func permEngine(t *testing.T, level Autonomy, a AgentPermissions, tool, action string, args map[string]any) (*Engine, ToolCall) {
	t.Helper()
	g, l := PermissionRules("ana", level, a, ActionKind(tool, action, args))
	cfg := Config{Autonomy: map[string]Autonomy{"ana": level},
		Grants: append([]Grant{{ID: "base", Agent: "*", Tool: "*", Action: "*", Effect: EffectAllow}}, g...), Limits: l}
	e, err := NewEngine(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return e, ToolCall{Tool: tool, Action: action, Args: args, Risk: "medium"}
}

func permDecide(t *testing.T, level Autonomy, a AgentPermissions, tool, action string, args map[string]any) Decision {
	t.Helper()
	e, c := permEngine(t, level, a, tool, action, args)
	return e.EvaluateAndReserve(context.Background(), AgentIdentity{OrgID: "o", AgentID: "ana"}, c)
}

func TestPermissionRulesThroughEngine(t *testing.T) {
	none := AgentPermissions{}
	amt := 500.0
	tests := []struct {
		name         string
		level        Autonomy
		perms        AgentPermissions
		tool, action string
		args         map[string]any
		want, rule   string
	}{
		{"important: internal runs alone", AutonomyRules, none, "documents", "create", nil, "allow", ""},
		{"important: messages ask", AutonomyRules, none, "email", "send", nil, "require_approval", "perm.important:messages"},
		{"important: unknown asks", AutonomyRules, none, "calendar", "schedule", nil, "require_approval", "perm.important:other"},
		{"important: never asking for messages", AutonomyRules, AgentPermissions{Exceptions: []Exception{{Category: KindMessages, Mode: ModeNever}}}, "email", "send", nil, "allow", ""},
		{"alone: messages run alone", AutonomyAutonomous, none, "email", "send", nil, "allow", ""},
		{"alone: always ask for publish", AutonomyAutonomous, AgentPermissions{Exceptions: []Exception{{Category: KindPublish, Mode: ModeAsk}}}, "social", "publish_post", nil, "require_approval", "perm.ask:publish"},
		{"alone: floor for payments", AutonomyAutonomous, none, "ledger", "make_payment", nil, "require_approval", "perm.floor:payments"},
		{"alone: never on a floor category is ignored", AutonomyAutonomous, AgentPermissions{Exceptions: []Exception{{Category: KindDelete, Mode: ModeNever}}}, "fs", "delete", nil, "require_approval", "perm.floor:delete"},
		{"alone: floor lifted", AutonomyAutonomous, AgentPermissions{FloorLifts: []FloorLift{{Category: KindPayments, By: "owner"}}}, "ledger", "make_payment", nil, "allow", ""},
		{"ask if amount above, above", AutonomyRules, AgentPermissions{Exceptions: []Exception{{Category: KindMessages, Mode: ModeAskIf, If: &AskIf{AmountOver: &amt}}}}, "email", "send", map[string]any{"amount": 800}, "require_approval", "perm.if_amount:messages"},
		{"ask if amount above, below", AutonomyRules, AgentPermissions{Exceptions: []Exception{{Category: KindMessages, Mode: ModeAskIf, If: &AskIf{AmountOver: &amt}}}}, "email", "send", map[string]any{"amount": 100}, "allow", ""},
		{"ask if new recipient", AutonomyRules, AgentPermissions{Exceptions: []Exception{{Category: KindMessages, Mode: ModeAskIf, If: &AskIf{NewRecipient: true}}}}, "email", "send", map[string]any{"to": "x@y.com"}, "require_approval", "perm.if_new_recipient:messages"},
		{"ask_each still asks", AutonomyApproveEach, AgentPermissions{Exceptions: []Exception{{Category: KindMessages, Mode: ModeNever}}}, "email", "send", nil, "require_approval", "autonomy.approve_each"},
	}
	for _, tc := range tests {
		d := permDecide(t, tc.level, tc.perms, tc.tool, tc.action, tc.args)
		if d.Verdict() != tc.want || (tc.rule != "" && d.RuleID != tc.rule) {
			t.Errorf("%s: got %s (%s), want %s (%s)", tc.name, d.Verdict(), d.RuleID, tc.want, tc.rule)
		}
	}
}

func TestPermissionPerDayLimitAsksAfterN(t *testing.T) {
	a := AgentPermissions{Exceptions: []Exception{{Category: KindMessages, Mode: ModeAskIf, If: &AskIf{PerDayOver: 2}}}}
	e, c := permEngine(t, AutonomyRules, a, "email", "send", nil)
	who := AgentIdentity{OrgID: "o", AgentID: "ana"}
	for i := 0; i < 2; i++ {
		if d := e.EvaluateAndReserve(context.Background(), who, c); !d.Allow {
			t.Fatalf("call %d: %s (%s)", i+1, d.Verdict(), d.RuleID)
		}
	}
	if d := e.EvaluateAndReserve(context.Background(), who, c); !d.RequireApproval || d.RuleID != "limit:perm.per_day:messages" {
		t.Fatalf("third call: %s (%s)", d.Verdict(), d.RuleID)
	}
}

func TestExceptionValidate(t *testing.T) {
	neg := -1.0
	bad := []Exception{
		{Category: "nope", Mode: ModeAsk},
		{Category: KindMessages, Mode: "maybe"},
		{Category: KindMessages, Mode: ModeAskIf},
		{Category: KindMessages, Mode: ModeAskIf, If: &AskIf{}},
		{Category: KindMessages, Mode: ModeAskIf, If: &AskIf{AmountOver: &neg}},
		{Category: KindMessages, Mode: ModeAskIf, If: &AskIf{PerDayOver: 100000}},
	}
	for _, e := range bad {
		if e.Validate() == nil {
			t.Errorf("%+v should be invalid", e)
		}
	}
	if err := (Exception{Category: KindPayments, Mode: ModeNever}).Validate(); err != nil {
		t.Errorf("never on a floor category is storable (it applies once the floor is lifted): %v", err)
	}
}

func TestPermissionsLookups(t *testing.T) {
	p := &Permissions{Agents: map[string]AgentPermissions{"ana": {Since: time.Now(),
		Exceptions: []Exception{{Category: KindPublish, Mode: ModeAsk}}, FloorLifts: []FloorLift{{Category: KindDelete}}}}}
	a, ok := p.For("ana")
	if !ok || !a.Lifted(KindDelete) || a.Lifted(KindPayments) {
		t.Fatalf("lookup: %+v %v", a, ok)
	}
	if e, ok := a.Exception(KindPublish); !ok || e.Mode != ModeAsk {
		t.Fatalf("exception lookup: %+v", e)
	}
	if _, ok := (*Permissions)(nil).For("ana"); ok {
		t.Fatal("nil permissions has no agents")
	}
	if !IsFloor(KindContracts) || IsFloor(KindMessages) {
		t.Fatal("floor categories")
	}
}

func TestScopeGrants(t *testing.T) {
	now := time.Now()
	p := &Permissions{Scopes: []ScopeGrant{
		{ID: "s1", Kind: ScopeBatch, ScopeID: "b1", Category: KindMessages, ExpiresAt: now.Add(time.Hour)},
		{ID: "s2", Kind: ScopeProject, ScopeID: "p1", AgentID: "ana", Category: KindPublish, ExpiresAt: now.Add(time.Hour)},
		{ID: "s3", Kind: ScopeProject, ScopeID: "p1", Category: KindSpend, ExpiresAt: now.Add(-time.Hour)},
	}}
	if g, ok := p.ScopeAllows("bob", "p9", "b1", KindMessages, now); !ok || g.ID != "s1" {
		t.Fatal("a batch grant covers every project of the batch and any agent")
	}
	if _, ok := p.ScopeAllows("bob", "p1", "", KindPublish, now); ok {
		t.Fatal("a grant for one agent does not cover another")
	}
	if _, ok := p.ScopeAllows("ana", "p1", "", KindSpend, now); ok {
		t.Fatal("expired grants do not apply")
	}
	if _, ok := p.ScopeAllows("ana", "", "", KindMessages, now); ok {
		t.Fatal("outside a project nothing applies")
	}
	// The floor still asks inside a scope.
	a := AgentPermissions{}.WithNever(KindPayments)
	if g, _ := PermissionRules("ana", AutonomyRules, a, KindPayments); len(g) != 1 || g[0].ID != "perm.floor:payments" {
		t.Fatalf("floor inside a scope: %+v", g)
	}
	if g, _ := PermissionRules("ana", AutonomyRules, AgentPermissions{}.WithNever(KindMessages), KindMessages); len(g) != 0 {
		t.Fatalf("a scope lets the category run: %+v", g)
	}
}
