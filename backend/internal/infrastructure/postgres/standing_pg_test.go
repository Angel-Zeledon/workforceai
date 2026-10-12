package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

func standingRow(id, project, agent string) application.StandingApproval {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return application.StandingApproval{ID: id, ProjectID: project, AgentID: agent, Tool: "spreadsheet", Action: "update", ApprovalID: "ap1",
		CreatedBy: "ana", CreatedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour)}
}

// Standing approvals (migration 540): storage, revocation history, tenant
// isolation under RLS, no deletes for the app role, and the approval columns.
func TestStandingApprovalsStorageAndIsolation(t *testing.T) {
	st := testStore(t).Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	seedOrg(t, st, "org-b")

	a1, a2 := standingRow("s1", "p1", "sales"), standingRow("s2", "p1", "")
	a2.CreatedAt = a2.CreatedAt.Add(time.Second)
	for _, r := range []application.StandingApproval{a1, a2, standingRow("s3", "p2", "sales")} {
		if err := st.CreateStanding(ctx, "org-a", r); err != nil {
			t.Fatal(err)
		}
	}
	list, err := st.ListStanding(ctx, "org-a", "p1")
	if err != nil || len(list) != 2 || list[0].ID != "s2" || list[1].ID != "s1" || list[1].AgentID != "sales" || list[1].Tool != "spreadsheet" {
		t.Fatalf("list = %+v %v", list, err)
	}

	// Use counter and revocation (the first revocation wins; the row stays as history).
	at := time.Now().UTC().Truncate(time.Microsecond)
	if err := st.TouchStanding(ctx, "org-a", "s1", at); err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeStanding(ctx, "org-a", "s1", "ana", at); err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeStanding(ctx, "org-a", "s1", "bob", at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetStanding(ctx, "org-a", "s1")
	if err != nil || got.UseCount != 1 || got.RevokedBy != "ana" || got.RevokedAt == nil || got.LastUsedAt == nil || got.Status != "" {
		t.Fatalf("get = %+v %v", got, err)
	}
	if err := st.RevokeStanding(ctx, "org-a", "missing", "ana", at); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("revoke missing = %v", err)
	}

	// Tenant isolation.
	if l, _ := st.ListStanding(ctx, "org-b", "p1"); len(l) != 0 {
		t.Fatal("org-b lists org-a standing approvals")
	}
	if _, err := st.GetStanding(ctx, "org-b", "s1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("org-b get = %v", err)
	}
	if err := st.CreateStanding(ctx, "org-b", standingRow("s9", "p1", "")); err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeStanding(ctx, "org-b", "s1", "mallory", at); err != nil && !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
	if got, _ := st.GetStanding(ctx, "org-a", "s1"); got.RevokedBy != "ana" {
		t.Error("org-b changed an org-a row")
	}
	err = st.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM standing_approvals WHERE org_id='org-a'`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("org-b transaction sees %d rows of org-a", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.exec(ctx, "org-b", `INSERT INTO standing_approvals (id, org_id, project_id, tool, action, created_by, expires_at) VALUES ('x','org-a','p','t','a','m', now())`); err == nil {
		t.Error("org-b wrote a row for org-a")
	}
	// The application role can never erase the history.
	if err := st.exec(ctx, "org-a", `DELETE FROM standing_approvals`); err == nil {
		t.Error("the app role could delete standing approvals")
	}

	// The approval remembers what it is about.
	taskID, _ := populate(t, st, "org-a")
	ap := domain.Approval{ID: "ap-standing", TaskID: taskID, AgentID: "sales", Action: "update", Title: "t", Risk: "medium", Status: domain.ApprovalPending,
		CreatedAt: at, Tool: "spreadsheet", ProjectID: "p1", StandingEligible: true}
	if err := st.CreateApproval(ctx, "org-a", ap); err != nil {
		t.Fatal(err)
	}
	back, err := st.GetApproval(ctx, "org-a", "ap-standing")
	if err != nil || back.Tool != "spreadsheet" || back.ProjectID != "p1" || !back.StandingEligible {
		t.Fatalf("approval round trip: %+v %v", back, err)
	}
	old, err := st.GetApproval(ctx, "org-a", "ap-org-a")
	if err != nil || old.StandingEligible || old.Tool != "" || old.ProjectID != "" {
		t.Fatalf("a plain approval must not be eligible: %+v %v", old, err)
	}
}

func TestMigration540DownThenUpAgain(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	if err := st.CreateStanding(ctx, "org-a", standingRow("s1", "p1", "")); err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../../migrations/down/540_standing_approvals_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	owner := env.owner(t)
	if _, err := owner.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	var reg *string
	_ = owner.QueryRow(ctx, `SELECT to_regclass('standing_approvals')::text`).Scan(&reg)
	if reg != nil {
		t.Fatalf("down left the table: %s", *reg)
	}
	var cols int
	_ = owner.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name='approvals' AND column_name IN ('tool','project_id','standing_eligible')`).Scan(&cols)
	if cols != 0 {
		t.Fatalf("down left %d approval columns", cols)
	}
	up, err := migrationsFS.ReadFile("migrations/540_standing_approvals.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // idempotent
		if _, err := owner.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up after down (%d): %v", i, err)
		}
	}
}
