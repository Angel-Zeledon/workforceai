package postgres

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/audit"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/push"
)

// Integration tests of migration 250 (tamper-evident audit trail and
// multi-approver approvals). See store_rls_test.go for how to run them.

var _ application.AuditStore = (*Store)(nil)

func (e *testEnv) owner(t *testing.T) *pgx.Conn {
	t.Helper()
	c, err := pgx.Connect(context.Background(), e.rawURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func addAudits(t *testing.T, st *Store, org string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		actor, action := "user-a", "approval.approved"
		if i%2 == 1 {
			actor, action = "agent-x", "tool.executed"
		}
		err := st.AddAudit(context.Background(), org, domain.AuditLog{ID: fmt.Sprintf("%s-%03d", org, i), Actor: actor, Action: action, Entity: "task",
			EntityID: fmt.Sprint("t", i), RequestID: "req-1", Details: map[string]any{"i": i, "n": 1.5, "nested": map[string]any{"z": 1, "a": []any{"x"}}}})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func verifyOrg(t *testing.T, st *Store, org string, anchor *domain.AuditHead) audit.Report {
	t.Helper()
	rep, err := application.VerifyChain(context.Background(), st, org, anchor)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestAuditChainOnPostgresVerifiesAfterARoundTrip(t *testing.T) {
	st := testStore(t).Store
	seedOrg(t, st, "org-a")
	addAudits(t, st, "org-a", 25)
	rep := verifyOrg(t, st, "org-a", nil)
	if !rep.OK || rep.Checked != 25 || rep.HeadSeq != 25 {
		t.Fatalf("report = %+v", rep)
	}
	rows, _ := st.AuditChain(context.Background(), "org-a", 0, 100)
	for i, r := range rows {
		if r.Seq != int64(i+1) || r.OrgID != "org-a" || len(r.Hash) != 64 {
			t.Fatalf("row %d = %+v", i, r)
		}
		if i > 0 && !r.TS.After(rows[i-1].TS) {
			t.Fatal("timestamps strictly increase along the chain")
		}
	}
	head, ok, err := st.AuditHead(context.Background(), "org-a")
	if err != nil || !ok || head.Seq != 25 || head.Hash != rows[24].Hash {
		t.Fatalf("head = %+v %v %v", head, ok, err)
	}
	if _, ok, _ := st.AuditHead(context.Background(), "org-none"); ok {
		t.Fatal("an organization without entries has no head")
	}
}

func TestConcurrentAppendsProduceOneGaplessChain(t *testing.T) {
	st := testStore(t).Store
	seedOrg(t, st, "org-a")
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 15; i++ {
				if err := st.AddAudit(context.Background(), "org-a", domain.AuditLog{ID: fmt.Sprintf("w%d-%d", w, i), Actor: "u", Action: "x", Details: map[string]any{"w": w}}); err != nil {
					t.Error(err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	rep := verifyOrg(t, st, "org-a", nil)
	if !rep.OK || rep.Checked != 120 {
		t.Fatalf("120 concurrent appends must form a single valid chain: %+v", rep)
	}
}

func TestAuditOrganizationsAreIsolatedByRLS(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	seedOrg(t, st, "org-b")
	addAudits(t, st, "org-a", 5)
	addAudits(t, st, "org-b", 3)
	if p, _ := st.QueryAudit(ctx, "org-b", domain.AuditQuery{Limit: 100}); len(p.Items) != 3 {
		t.Fatalf("org-b sees %d entries", len(p.Items))
	}
	if rep := verifyOrg(t, st, "org-b", nil); !rep.OK || rep.Checked != 3 {
		t.Fatalf("org-b chain restarts at 1: %+v", rep)
	}
	// Through the wrong tenant context the database itself hides and refuses rows.
	for _, tb := range []string{"audit_logs", "audit_chain_heads"} {
		var n int
		err := st.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, "SELECT count(*) FROM "+tb+" WHERE org_id='org-a'").Scan(&n)
		})
		if err != nil || n != 0 {
			t.Errorf("%s: org-b sees %d rows of org-a (%v)", tb, n, err)
		}
	}
	err := st.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO audit_logs (id, org_id, actor, action) VALUES ('evil','org-a','u','a')`)
		return e
	})
	if err == nil {
		t.Fatal("RLS must refuse writing an entry of another organization")
	}
}

func TestAuditTrailIsAppendOnlyForEveryone(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	addAudits(t, st, "org-a", 4)

	// The application role: privileges.
	for _, q := range []string{
		`UPDATE audit_logs SET actor='evil'`, `DELETE FROM audit_logs`, `TRUNCATE audit_logs`,
		`DELETE FROM audit_chain_heads`, `UPDATE audit_chain_heads SET seq=0, hash=''`,
	} {
		err := st.WithOrgTx(ctx, "org-a", func(tx pgx.Tx) error { _, e := tx.Exec(ctx, q); return e })
		if err == nil {
			t.Errorf("app role: %q must fail", q)
		}
	}
	// The table owner: triggers (nobody rewrites the trail by accident or by a careless script).
	owner := env.owner(t)
	for _, q := range []string{
		`UPDATE audit_logs SET actor='evil'`, `DELETE FROM audit_logs`, `TRUNCATE audit_logs`,
		`UPDATE audit_chain_heads SET seq=1, hash='x'`, // rewinding the head
	} {
		if _, err := owner.Exec(ctx, q); err == nil || !strings.Contains(err.Error(), "append-only") && !strings.Contains(err.Error(), "only advance") {
			t.Errorf("owner: %q must be stopped by a trigger, got %v", q, err)
		}
	}
	if rep := verifyOrg(t, st, "org-a", nil); !rep.OK || rep.Checked != 4 {
		t.Fatalf("nothing changed: %+v", rep)
	}
}

func TestAuditTamperingBehindTheTriggersIsStillDetected(t *testing.T) {
	// A superuser who disables the triggers can edit rows; the chain exposes it.
	cases := []struct {
		name   string
		sql    string
		reason string
		at     int64
	}{
		{"modified entry", `UPDATE audit_logs SET actor='mallory' WHERE org_id='org-a' AND seq=3`, "hash_mismatch", 3},
		{"modified details", `UPDATE audit_logs SET details='{"i": 999}' WHERE org_id='org-a' AND seq=2`, "hash_mismatch", 2},
		{"modified timestamp", `UPDATE audit_logs SET ts = ts + interval '1 hour' WHERE org_id='org-a' AND seq=4`, "hash_mismatch", 4},
		{"deleted middle entry", `DELETE FROM audit_logs WHERE org_id='org-a' AND seq=3`, "gap", 4},
		{"deleted tail", `DELETE FROM audit_logs WHERE org_id='org-a' AND seq=6`, "head_mismatch", 6},
		{"deleted first", `DELETE FROM audit_logs WHERE org_id='org-a' AND seq=1`, "gap", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := testStore(t)
			st := env.Store
			seedOrg(t, st, "org-a")
			addAudits(t, st, "org-a", 6)
			owner := env.owner(t)
			ctx := context.Background()
			if _, err := owner.Exec(ctx, `ALTER TABLE audit_logs DISABLE TRIGGER USER`); err != nil {
				t.Fatal(err)
			}
			if _, err := owner.Exec(ctx, tc.sql); err != nil {
				t.Fatal(err)
			}
			rep := verifyOrg(t, st, "org-a", nil)
			if rep.OK || rep.Reason != tc.reason || rep.BrokenAtSeq != tc.at {
				t.Fatalf("%+v, want %s at %d", rep, tc.reason, tc.at)
			}
		})
	}
}

func TestLegacyRowsWithoutChainAreKeptAndIgnoredByVerification(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	// Rows written before migration 250 have no seq/hash.
	err := st.WithOrgTx(ctx, "org-a", func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO audit_logs (id, org_id, actor, action, ts) VALUES ('legacy-1','org-a','u','old.action', now() - interval '1 day')`)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	addAudits(t, st, "org-a", 3)
	if rep := verifyOrg(t, st, "org-a", nil); !rep.OK || rep.Checked != 3 || rep.FirstSeq != 1 {
		t.Fatalf("legacy rows are outside the chain: %+v", rep)
	}
	p, _ := st.QueryAudit(ctx, "org-a", domain.AuditQuery{Limit: 100})
	if len(p.Items) != 4 || p.Items[0].ID != "legacy-1" || p.Items[0].Seq != 0 {
		t.Fatalf("legacy rows still show up first in the trail: %+v", p.Items)
	}
}

func TestAuditQueryFiltersPaginationAndSnapshotOnPostgres(t *testing.T) {
	st := testStore(t).Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	addAudits(t, st, "org-a", 20)
	// A literal underscore or percent in a type must not behave as a LIKE wildcard.
	_ = st.AddAudit(ctx, "org-a", domain.AuditLog{ID: "odd-1", Actor: "u", Action: "tool%executed", RequestID: "req-2"})
	_ = st.AddAudit(ctx, "org-a", domain.AuditLog{ID: "odd-2", Actor: "u", Action: "toolXexecuted", RequestID: "req-2"})
	q := func(q domain.AuditQuery) []domain.AuditLog {
		t.Helper()
		p, err := st.QueryAudit(ctx, "org-a", q)
		if err != nil {
			t.Fatal(err)
		}
		return p.Items
	}
	if n := len(q(domain.AuditQuery{Actor: "agent-x", Limit: 100})); n != 10 {
		t.Fatalf("by actor: %d", n)
	}
	if n := len(q(domain.AuditQuery{Action: "tool.executed", Limit: 100})); n != 10 {
		t.Fatalf("by type: %d", n)
	}
	if n := len(q(domain.AuditQuery{Action: "approval.*", Limit: 100})); n != 10 {
		t.Fatalf("by prefix: %d", n)
	}
	if n := len(q(domain.AuditQuery{Action: "tool%*", Limit: 100})); n != 1 {
		t.Fatalf("a percent sign is literal: %d", n)
	}
	if n := len(q(domain.AuditQuery{RequestID: "req-2", Limit: 100})); n != 2 {
		t.Fatalf("by request: %d", n)
	}
	all := q(domain.AuditQuery{Limit: 100})
	from, to := all[5].TS, all[15].TS
	if n := len(q(domain.AuditQuery{From: &from, To: &to, Limit: 100})); n != 10 {
		t.Fatalf("time range [from,to): %d", n)
	}
	for _, desc := range []bool{false, true} {
		seen, cursor := map[string]bool{}, ""
		for i := 0; i < 20; i++ {
			p, err := st.QueryAudit(ctx, "org-a", domain.AuditQuery{Limit: 7, Cursor: cursor, Desc: desc})
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range p.Items {
				if seen[e.ID] {
					t.Fatalf("desc=%v: %s twice", desc, e.ID)
				}
				seen[e.ID] = true
			}
			if p.NextCursor == "" {
				break
			}
			cursor = p.NextCursor
		}
		if len(seen) != 22 {
			t.Fatalf("desc=%v: paged %d of 22", desc, len(seen))
		}
	}
	bound := int64(10)
	if n := len(q(domain.AuditQuery{UpToSeq: &bound, Limit: 100})); n != 10 {
		t.Fatalf("snapshot up to seq 10: %d", n)
	}
	// An export through the service reads the same chain and verifies offline.
	svc := &application.AuditService{Store: st, Rec: &application.Recorder{OrgID: "org-a", Store: st, Log: nil}, Cfg: application.Config{OrgID: "org-a"}}
	svc.Rec.Log = discardLogger()
	var sb strings.Builder
	res, err := svc.Export(application.WithOrg(ctx, "org-a"), domain.AuditQuery{}, "jsonl", &sb)
	if err != nil || res.Entries != 22 {
		t.Fatalf("export: %+v %v", res, err)
	}
	recs, err := audit.ReadJSONL([]byte(sb.String()))
	if err != nil {
		t.Fatal(err)
	}
	if rep := audit.VerifyRecords(recs, true); !rep.OK || rep.Checked != 22 {
		t.Fatalf("postgres export verifies offline: %+v", rep)
	}
}

func TestApprovalsKeepTheirGovernanceAndDecisions(t *testing.T) {
	st := testStore(t).Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	taskID, _ := populate(t, st, "org-a")
	now := time.Now().UTC().Truncate(time.Microsecond)
	ap := domain.Approval{ID: "ap-dual", TaskID: taskID, AgentID: "sales", Action: "make_payment", Title: "t", Risk: "high", Status: domain.ApprovalPending, CreatedAt: now,
		RequiredApprovals: 2, RequiredRole: "admin", RequestedBy: "user-ana", NoSelfApproval: true, PolicyRule: "dual_approval.action:make_payment"}
	if err := st.CreateApproval(ctx, "org-a", ap); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetApproval(ctx, "org-a", "ap-dual")
	if err != nil || got.RequiredApprovals != 2 || got.RequiredRole != "admin" || got.RequestedBy != "user-ana" || !got.NoSelfApproval ||
		got.PolicyRule != ap.PolicyRule || got.Decisions == nil || len(got.Decisions) != 0 {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	got.Decisions = []domain.ApprovalDecision{{By: "bob", Role: "admin", Note: "uno", TS: now}}
	if err := st.UpdateApproval(ctx, "org-a", got); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetApproval(ctx, "org-a", "ap-dual")
	if got.Status != domain.ApprovalPending || len(got.Decisions) != 1 || got.Decisions[0].By != "bob" {
		t.Fatalf("partial approval persisted: %+v", got)
	}
	fin := now.Add(time.Minute)
	got.Status, got.ResolvedAt = domain.ApprovalApproved, &fin
	got.Decisions = append(got.Decisions, domain.ApprovalDecision{By: "dave", Role: "owner", TS: fin})
	if err := st.UpdateApproval(ctx, "org-a", got); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateApproval(ctx, "org-a", got); err == nil {
		t.Fatal("a resolved approval cannot be changed again")
	}
	got, _ = st.GetApproval(ctx, "org-a", "ap-dual")
	if got.Status != domain.ApprovalApproved || len(got.Decisions) != 2 {
		t.Fatalf("resolved: %+v", got)
	}
	// Approvals created before the migration read back as single approvals.
	old, err := st.GetApproval(ctx, "org-a", "ap-org-a")
	if err != nil || old.RequiredApprovals != 1 || len(old.Decisions) != 0 {
		t.Fatalf("a plain approval: %+v %v", old, err)
	}
}

func TestMigration250DownThenUpAgain(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	addAudits(t, st, "org-a", 3)
	down, err := os.ReadFile("../../../migrations/down/250_audit_chain_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	owner := env.owner(t)
	if _, err := owner.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	var n int
	_ = owner.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=$1 AND table_name='audit_logs' AND column_name IN ('seq','hash','prev_hash','request_id')`, env.schema).Scan(&n)
	if n != 0 {
		t.Fatalf("down left %d audit columns", n)
	}
	var reg *string
	_ = owner.QueryRow(ctx, `SELECT to_regclass('audit_chain_heads')::text`).Scan(&reg)
	if reg != nil {
		t.Fatalf("down left the heads table: %s", *reg)
	}
	// Rows survive the rollback; the up migration applies again (idempotent).
	var rows int
	_ = owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs`).Scan(&rows)
	if rows != 3 {
		t.Fatalf("audit rows after down = %d", rows)
	}
	up, err := migrationsFS.ReadFile("migrations/250_audit_chain.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, string(up)); err != nil {
		t.Fatalf("up after down: %v", err)
	}
	if _, err := owner.Exec(ctx, string(up)); err != nil {
		t.Fatalf("the up migration must be idempotent: %v", err)
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestPushSubscriptionsAreIsolatedByRLS(t *testing.T) {
	env := testStore(t)
	ps := &PushStore{S: env.Store}
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	for _, org := range []string{"org-a", "org-b"} {
		if _, err := ps.SavePushSubscription(ctx, push.Subscription{ID: "id-" + org, OrgID: org, UserID: "u-" + org,
			Endpoint: "https://fcm.googleapis.com/x/" + org, P256dh: "k", Auth: "a", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if l, _ := ps.ListPushSubscriptions(ctx, "org-b"); len(l) != 1 || l[0].OrgID != "org-b" {
		t.Fatalf("org-b sees %+v", l)
	}
	// Deleting another org's endpoint through my tenant context removes nothing.
	_ = ps.DeletePushSubscription(ctx, "org-b", "", "https://fcm.googleapis.com/x/org-a")
	if l, _ := ps.ListPushSubscriptions(ctx, "org-a"); len(l) != 1 {
		t.Fatal("org-a subscription must survive")
	}
	// A user cannot delete a subscription of another user.
	_ = ps.DeletePushSubscription(ctx, "org-a", "someone-else", "https://fcm.googleapis.com/x/org-a")
	if l, _ := ps.ListPushSubscriptions(ctx, "org-a"); len(l) != 1 {
		t.Fatal("only the owner may delete")
	}
	err := env.Store.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO push_subscriptions (id, org_id, endpoint, p256dh, auth) VALUES ('evil','org-a','https://x','k','a')`)
		return e
	})
	if err == nil {
		t.Fatal("RLS must refuse writing a subscription of another organization")
	}
}
