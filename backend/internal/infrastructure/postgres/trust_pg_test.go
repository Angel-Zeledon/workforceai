package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/trust"
)

// Integration test of migration 791 and of the trust evidence queries (see store_rls_test.go: TEST_DATABASE_URL).
func TestTrustStorePostgres(t *testing.T) {
	env := testStore(t)
	st := &TrustStore{S: env.Store}
	ctx := context.Background()
	seedOrg(t, env.Store, "org-t1")
	seedOrg(t, env.Store, "org-t2")
	now := time.Now().UTC().Truncate(time.Microsecond)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	if _, ok, err := st.GetRecord(ctx, "org-t1", "sales"); ok || err != nil {
		t.Fatalf("missing record: %v %v", ok, err)
	}
	must(st.PutRecord(ctx, "org-t1", trust.Record{AgentID: "sales", StartLevel: "rules", Level: "rules", LevelSince: now, TrackingSince: now, UpdatedAt: now}))
	must(st.PutRecord(ctx, "org-t1", trust.Record{AgentID: "sales", StartLevel: "ignored", Level: "approve_each", LevelSince: now.Add(time.Hour), TrackingSince: now, UpdatedAt: now}))
	r, ok, err := st.GetRecord(ctx, "org-t1", "sales")
	if err != nil || !ok || r.StartLevel != "rules" || r.Level != "approve_each" || !r.LevelSince.Equal(now.Add(time.Hour)) {
		t.Fatalf("record upsert keeps the start level: %+v %v", r, err)
	}

	p := trust.Proposal{ID: "tp1", AgentID: "sales", Kind: trust.KindPromotion, FromLevel: "approve_each", ToLevel: "rules", Status: trust.StatusPending,
		Reasons: []string{"criteria_met"}, Evidence: trust.Evidence{Counts: trust.Counts{Approved: 20}, Ceiling: "rules", Version: 1}, CreatedAt: now}
	must(st.CreateProposal(ctx, "org-t1", p))
	// one pending proposal per agent
	dup := p
	dup.ID = "tp-dup"
	if err := st.CreateProposal(ctx, "org-t1", dup); err == nil {
		t.Fatal("a second pending proposal for the same agent was accepted")
	}
	got, err := st.GetProposal(ctx, "org-t1", "tp1")
	if err != nil || got.Evidence.Counts.Approved != 20 || got.Reasons[0] != "criteria_met" || got.ResolvedAt != nil {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	if _, err := st.ResolveProposal(ctx, "org-t1", "tp1", trust.StatusApplied, trust.StatusReverted, "x", "", now); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("wrong from-status: %v", err)
	}
	if _, err := st.ResolveProposal(ctx, "org-t1", "nope", trust.StatusPending, trust.StatusAccepted, "x", "", now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	got, err = st.ResolveProposal(ctx, "org-t1", "tp1", trust.StatusPending, trust.StatusAccepted, "owner-1", "ok", now)
	if err != nil || got.Status != trust.StatusAccepted || got.ResolvedBy != "owner-1" || got.ResolvedAt == nil {
		t.Fatalf("resolve: %+v %v", got, err)
	}
	if l, _ := st.ListProposals(ctx, "org-t1", "sales", "", 10); len(l) != 1 {
		t.Fatalf("list: %d", len(l))
	}

	// RLS: org-t2 sees nothing of org-t1.
	if _, ok, _ := st.GetRecord(ctx, "org-t2", "sales"); ok {
		t.Fatal("record leaked across organizations")
	}
	if l, _ := st.ListProposals(ctx, "org-t2", "", "", 10); len(l) != 0 {
		t.Fatal("proposals leaked across organizations")
	}

	// Evidence: approvals (human, high risk, system), tasks and policy denials.
	since := now.Add(-time.Hour)
	n := 0
	approval := func(st domain.ApprovalStatus, risk, note string, at time.Time) {
		n++
		id := fmt.Sprintf("tap-%d", n)
		must(env.Store.CreateApproval(ctx, "org-t1", domain.Approval{ID: id, TaskID: "t", AgentID: "sales", Action: "send", Title: "t", Risk: risk, Status: domain.ApprovalPending, CreatedAt: since}))
		must(env.Store.UpdateApproval(ctx, "org-t1", domain.Approval{ID: id, Status: st, Note: note, ResolvedAt: &at}))
	}
	for i := 0; i < 5; i++ {
		approval(domain.ApprovalApproved, "low", "", now.Add(-30*time.Minute))
	}
	approval(domain.ApprovalRejected, "high", "no", now.Add(-10*time.Minute))
	approval(domain.ApprovalRejected, "low", "no", now.Add(-50*time.Minute))
	approval(domain.ApprovalRejected, "high", "sin respuesta: timeout", now.Add(-10*time.Minute))
	approval(domain.ApprovalApproved, "low", "", now.Add(-2*time.Hour)) // before since
	for i, s := range []domain.TaskStatus{domain.TaskDone, domain.TaskDone, domain.TaskFailed, domain.TaskBlocked} {
		fin := now.Add(-5 * time.Minute)
		must(env.Store.CreateTask(ctx, "org-t1", domain.Task{ID: fmt.Sprintf("ttk-%d", i), AgentID: "sales", Status: s, CreatedAt: since, FinishedAt: &fin}))
	}
	must(env.Store.AddAudit(ctx, "org-t1", domain.AuditLog{Actor: "sales", Action: "tool.denied", Entity: "task", EntityID: "t", TS: now.Add(-time.Minute),
		Details: map[string]any{"reason": "policy:no_payments"}}))
	must(env.Store.AddAudit(ctx, "org-t1", domain.AuditLog{Actor: "sales", Action: "tool.denied", Entity: "task", EntityID: "t", TS: now.Add(-time.Minute),
		Details: map[string]any{"reason": "kill_switch_active"}}))
	c, err := st.Collect(ctx, "org-t1", "sales", since, now.Add(-20*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	want := trust.Counts{Approved: 5, Rejected: 2, HighRiskRejected: 1, RecentRejected: 1, PolicyDenials: 1, TasksDone: 2, TasksFailed: 1, TasksBlocked: 1}
	if c != want {
		t.Fatalf("collect = %+v, want %+v", c, want)
	}
	if c2, _ := st.Collect(ctx, "org-t2", "sales", since, since); c2 != (trust.Counts{}) {
		t.Fatalf("evidence leaked across organizations: %+v", c2)
	}

	must(st.Reset(ctx, "org-t1"))
	if l, _ := st.ListRecords(ctx, "org-t1"); len(l) != 0 {
		t.Fatal("reset left records")
	}
}

func TestMigration791DownThenUpAgain(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-t1")
	down, err := os.ReadFile("../../../migrations/down/791_agent_trust_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	owner := env.owner(t)
	if _, err := owner.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	for _, tbl := range []string{"agent_trust", "trust_proposals"} {
		var reg *string
		_ = owner.QueryRow(ctx, `SELECT to_regclass($1)::text`, tbl).Scan(&reg)
		if reg != nil {
			t.Fatalf("down left %s", *reg)
		}
	}
	up, err := migrationsFS.ReadFile("migrations/791_agent_trust.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := owner.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up after down (%d): %v", i, err)
		}
	}
	for _, tbl := range []string{"agent_trust", "trust_proposals"} {
		var forced bool
		_ = owner.QueryRow(ctx, `SELECT relforcerowsecurity FROM pg_class WHERE relname=$1`, tbl).Scan(&forced)
		if !forced {
			t.Errorf("row level security is not forced on %s", tbl)
		}
	}
}
