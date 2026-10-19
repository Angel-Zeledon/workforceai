package orgchart

import (
	"strings"
	"testing"
)

func ag(id, dept string) Actor { return Actor{Kind: KindAgent, ID: id, DepartmentID: dept} }
func pe(id, dept string) Actor { return Actor{Kind: KindPerson, ID: id, DepartmentID: dept} }

func testDir() Directory {
	return Directory{
		Departments: []Department{
			{ID: "hq", Name: "HQ"},
			{ID: "fin", Name: "Finance", ParentID: "hq", Head: Ref{Kind: KindAgent, ID: "cfo"}},
			{ID: "tax", Name: "Tax", ParentID: "fin"},
			{ID: "ops", Name: "Ops", ParentID: "hq"},
		},
		ReportsTo: map[string]Ref{"agent:clerk": {Kind: KindAgent, ID: "lead"}},
	}
}

func TestCheckNoRulesKeepsTodaysBehaviour(t *testing.T) {
	d := Check(nil, pe("ana", ""), ag("clerk", "tax"), testDir())
	if !d.Allowed || d.NeedsApproval || d.Reason != "no_rule" {
		t.Fatalf("%+v", d)
	}
}

func TestCheckSelfIsNotDelegation(t *testing.T) {
	rules := []Rule{{ID: "r", From: Selector{Type: SelAny}, To: Selector{Type: SelAny}, Effect: EffectDeny}}
	if d := Check(rules, ag("a", ""), ag("a", ""), testDir()); !d.Allowed {
		t.Fatalf("%+v", d)
	}
}

func TestCheckStrictDefaultWithExceptions(t *testing.T) {
	rules := []Rule{
		{ID: "default", From: Selector{Type: SelAny}, To: Selector{Type: SelAny}, Effect: EffectDeny},
		{ID: "people", From: Selector{Type: SelAnyPerson}, To: Selector{Type: SelAnyAgent}, Effect: EffectAllow},
	}
	dir := testDir()
	if d := Check(rules, pe("ana", ""), ag("clerk", "tax"), dir); !d.Allowed || d.RuleID != "people" {
		t.Fatalf("a person may assign to any agent: %+v", d)
	}
	if d := Check(rules, ag("clerk", "tax"), pe("ana", ""), dir); d.Allowed || d.RuleID != "default" {
		t.Fatalf("an agent may not assign to a person under the strict default: %+v", d)
	}
	if d := Check(rules, ag("clerk", "tax"), ag("buyer", "ops"), dir); d.Allowed {
		t.Fatalf("agent to agent denied by default: %+v", d)
	}
}

func TestCheckMostSpecificWins(t *testing.T) {
	rules := []Rule{
		{ID: "dept-deny", From: Selector{Type: SelDepartment, ID: "fin"}, To: Selector{Type: SelAny}, Effect: EffectDeny},
		{ID: "agent-allow", From: Selector{Type: SelAgent, ID: "cfo"}, To: Selector{Type: SelAny}, Effect: EffectAllow},
	}
	dir := testDir()
	// the department rule covers sub-departments (tax is under fin)
	if d := Check(rules, ag("clerk", "tax"), ag("buyer", "ops"), dir); d.Allowed || d.RuleID != "dept-deny" {
		t.Fatalf("%+v", d)
	}
	// the rule about one agent beats the rule about its department
	if d := Check(rules, ag("cfo", "fin"), ag("buyer", "ops"), dir); !d.Allowed || d.RuleID != "agent-allow" {
		t.Fatalf("%+v", d)
	}
	// an actor outside the department is untouched
	if d := Check(rules, ag("buyer", "ops"), ag("clerk", "tax"), dir); !d.Allowed || d.Reason != "no_rule" {
		t.Fatalf("%+v", d)
	}
}

func TestCheckTieBreakDenyBeatsApprovalBeatsAllow(t *testing.T) {
	sel := func(id string) Rule {
		return Rule{ID: id, From: Selector{Type: SelAnyAgent}, To: Selector{Type: SelAnyAgent}}
	}
	allow, appr, deny := sel("a-allow"), sel("b-approval"), sel("c-deny")
	allow.Effect, appr.Effect, deny.Effect = EffectAllow, EffectAllow, EffectDeny
	appr.RequireApproval = true
	dir := testDir()
	if d := Check([]Rule{allow, appr}, ag("x", ""), ag("y", ""), dir); !d.Allowed || !d.NeedsApproval {
		t.Fatalf("approval beats plain allow at equal specificity: %+v", d)
	}
	if d := Check([]Rule{allow, appr, deny}, ag("x", ""), ag("y", ""), dir); d.Allowed {
		t.Fatalf("deny beats everything at equal specificity: %+v", d)
	}
}

func TestCheckApproverResolution(t *testing.T) {
	dir := testDir()
	base := Rule{ID: "r", From: Selector{Type: SelAny}, To: Selector{Type: SelAnyAgent}, Effect: EffectAllow, RequireApproval: true}

	explicit := base
	explicit.Approver = Ref{Kind: KindPerson, ID: "ana"}
	if d := Check([]Rule{explicit}, pe("bob", ""), ag("clerk", "tax"), dir); !d.NeedsApproval || d.Approver != explicit.Approver {
		t.Fatalf("explicit approver: %+v", d)
	}
	// the head of the target's department (cfo heads fin; tax is below fin but clerk is IN tax whose head is empty)
	if d := Check([]Rule{base}, pe("bob", ""), ag("cfo2", "fin"), dir); d.Approver != (Ref{Kind: KindAgent, ID: "cfo"}) {
		t.Fatalf("department head: %+v", d)
	}
	// no department head: the target's boss
	if d := Check([]Rule{base}, pe("bob", ""), ag("clerk", "tax"), dir); d.Approver != (Ref{Kind: KindAgent, ID: "lead"}) {
		t.Fatalf("boss: %+v", d)
	}
	// nothing else: the owner
	if d := Check([]Rule{base}, pe("bob", ""), ag("loner", ""), dir); d.Approver != (Ref{Kind: KindPerson, ID: OwnerID}) {
		t.Fatalf("owner: %+v", d)
	}
	// a head is not asked to approve work assigned to themselves
	if d := Check([]Rule{base}, pe("bob", ""), ag("cfo", "fin"), dir); d.Approver == (Ref{Kind: KindAgent, ID: "cfo"}) {
		t.Fatalf("the head must not approve their own work: %+v", d)
	}
}

func TestCheckPersonAndAgentSelectorsDoNotCollide(t *testing.T) {
	rules := []Rule{{ID: "r", From: Selector{Type: SelPerson, ID: "x"}, To: Selector{Type: SelAny}, Effect: EffectDeny}}
	if d := Check(rules, ag("x", ""), pe("y", ""), testDir()); !d.Allowed {
		t.Fatalf("an agent with the same id as a denied person is a different actor: %+v", d)
	}
	if d := Check(rules, pe("x", ""), pe("y", ""), testDir()); d.Allowed {
		t.Fatalf("%+v", d)
	}
}

func TestValidateRule(t *testing.T) {
	ok := Rule{From: Selector{Type: SelDepartment, ID: "fin"}, To: Selector{Type: SelAnyAgent}, Effect: EffectAllow, RequireApproval: true, Approver: Ref{Kind: KindPerson, ID: "ana"}}
	if err := ValidateRule(ok); err != nil {
		t.Fatal(err)
	}
	mut := func(f func(*Rule)) Rule { r := ok; f(&r); return r }
	bad := []struct {
		name string
		r    Rule
		want string
	}{
		{"unknown selector", mut(func(r *Rule) { r.From.Type = "everyone" }), "unknown selector"},
		{"any with id", mut(func(r *Rule) { r.To = Selector{Type: SelAny, ID: "x"} }), "no id"},
		{"department without id", mut(func(r *Rule) { r.From.ID = "" }), "needs an id"},
		{"bad effect", mut(func(r *Rule) { r.Effect = "maybe" }), "effect"},
		{"deny with approval", mut(func(r *Rule) { r.Effect = EffectDeny }), "deny"},
		{"approver without approval", mut(func(r *Rule) { r.RequireApproval = false }), "only makes sense"},
		{"bad approver", mut(func(r *Rule) { r.Approver = Ref{Kind: "robot", ID: "x"} }), "approver"},
		{"long note", mut(func(r *Rule) { r.Note = strings.Repeat("x", 201) }), "note"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidateRule(c.r); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v want %q", err, c.want)
			}
		})
	}
}
