package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/skills"
)

// Integration test of migration 829 (see store_rls_test.go: TEST_DATABASE_URL). Not run without a database.

func TestSkillTestStoreRLSAndAppendOnlyRuns(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	svc := skillSvc(env)
	ctxA := application.WithOrg(ctx, "org-a")
	d, err := svc.Create(ctxA, skills.Actor{ID: "maria"}, skills.Input{Name: "pg-tested", Description: "d", Instructions: "# Body\n"})
	if err != nil {
		t.Fatal(err)
	}
	st := &SkillTestStore{S: env.Store}
	now := time.Now().UTC().Truncate(time.Millisecond)
	c := skills.TestCase{ID: "stc_1", SkillID: d.ID, Title: "t", Prompt: "p", Expect: "e", CreatedBy: "maria", CreatedAt: now, UpdatedAt: now}
	if err := st.PutCase(ctx, "org-a", c); err != nil {
		t.Fatal(err)
	}
	c.Title = "t2"
	if err := st.PutCase(ctx, "org-a", c); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got, err := st.GetCase(ctx, "org-a", d.ID, "stc_1"); err != nil || got.Title != "t2" {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := st.GetCase(ctx, "org-b", d.ID, "stc_1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("another organization must not see the case: %v", err)
	}
	r := skills.TestRun{ID: "str_1", SkillID: d.ID, CaseID: "stc_1", CaseTitle: "t2", Version: 1, AgentID: "sales", Status: skills.RunDone, Output: "ok",
		UsedSkill: true, Blocked: []string{"email.send"}, EstimateUSD: 0.01, CostUSD: 0.004, CreatedBy: "boss", CreatedAt: now}
	if err := st.AddRun(ctx, "org-a", r); err != nil {
		t.Fatal(err)
	}
	runs, err := st.ListRuns(ctx, "org-a", d.ID, "", 10)
	if err != nil || len(runs) != 1 || !runs[0].UsedSkill || len(runs[0].Blocked) != 1 || runs[0].CostUSD != 0.004 {
		t.Fatalf("runs: %+v %v", runs, err)
	}
	if other, _ := st.ListRuns(ctx, "org-b", d.ID, "", 10); len(other) != 0 {
		t.Fatalf("another organization must not see the runs: %+v", other)
	}
	// the runs are append-only for the application role; a deleted case leaves its history
	if err := st.DeleteCase(ctx, "org-a", d.ID, "stc_1"); err != nil {
		t.Fatal(err)
	}
	if runs, _ := st.ListRuns(ctx, "org-a", d.ID, "stc_1", 10); len(runs) != 1 {
		t.Fatalf("history after deleting the case: %+v", runs)
	}
}
