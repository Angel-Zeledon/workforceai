package orgchart

import (
	"context"
	"testing"

	"aiworkforce/backend/internal/application"
)

func TestCheckerSeesRuleChangesAndWakesOnChange(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newSvc(t)
	woke := 0
	svc.OnChange = func() { woke++ }
	fin, _ := svc.CreateDepartment(ctx, DepartmentInput{Name: "Finance", Head: Ref{Kind: KindPerson, ID: "u2"}})
	if _, err := svc.PutJob(ctx, "accounting", JobInput{DepartmentID: fin.ID, AllowedTools: []string{"fs"}}); err != nil {
		t.Fatal(err)
	}
	c := svc.Checker()
	from := application.DelegationActor{Kind: "agent", ID: "sales"}
	if v := c.CheckDelegation(ctx, svc.DefaultOrg, from, "accounting"); !v.Allowed || v.NeedsApproval {
		t.Fatalf("no rules: %+v", v)
	}
	before := woke
	r, err := svc.CreateRule(ctx, RuleInput{From: Selector{Type: SelAnyAgent}, To: Selector{Type: SelDepartment, ID: fin.ID}, Effect: EffectAllow, RequireApproval: true})
	if err != nil {
		t.Fatal(err)
	}
	if woke == before {
		t.Fatal("a rule change must call OnChange")
	}
	v := c.CheckDelegation(ctx, svc.DefaultOrg, from, "accounting") // cached snapshot must have been dropped
	if !v.NeedsApproval || v.ApproverKind != "person" || v.ApproverID != "u2" || v.RuleID != r.ID {
		t.Fatalf("approval by the head of the department: %+v", v)
	}
	_ = svc.DeleteRule(ctx, r.ID)
	if v := c.CheckDelegation(ctx, svc.DefaultOrg, from, "accounting"); v.NeedsApproval {
		t.Fatalf("rule deleted: %+v", v)
	}
}
