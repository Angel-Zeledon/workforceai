package orgchart

import (
	"context"
	"slices"
	"testing"
)

func TestAssignAgentMovesItToTheTeamAndKeepsTheRestOfTheJob(t *testing.T) {
	svc, st, _ := newSvc(t)
	ctx := context.Background()
	d, err := svc.CreateDepartment(ctx, DepartmentInput{Name: "Legal"})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := svc.Job(ctx, "legal")
	if err := svc.AssignAgent(ctx, "legal", d.ID); err != nil {
		t.Fatal(err)
	}
	j, _ := svc.Job(ctx, "legal")
	if j.DepartmentID != d.ID || !slices.Equal(j.AllowedTools, before.AllowedTools) || !slices.Equal(j.Responsibilities, before.Responsibilities) {
		t.Fatalf("job after = %+v, before = %+v", j, before)
	}
	a, _ := st.GetAgent(ctx, "demo", "legal")
	if a.Department == nil || *a.Department != "Legal" {
		t.Fatalf("department tag = %v", a.Department)
	}
	if err := svc.AssignAgent(ctx, "legal", "dept_nope"); err == nil {
		t.Fatal("unknown department must fail")
	}
}
