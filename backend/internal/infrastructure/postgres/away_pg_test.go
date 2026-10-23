package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/away"
	"aiworkforce/backend/internal/domain"
)

// Integration test of migration 792 and of the history queries of the away summary (TEST_DATABASE_URL, see
// store_rls_test.go): marks round trip and stay inside their organization; events and spend are read by window.
func TestAwayMarksAndHistoryPostgres(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	seedOrg(t, st, "org-b")
	now := time.Now().UTC().Truncate(time.Microsecond)

	if _, found, err := st.GetAwayMark(ctx, "org-a", "u1"); err != nil || found {
		t.Fatalf("no mark yet = %v %v", found, err)
	}
	left := now.Add(-10 * time.Hour)
	dis := now.Add(-time.Minute)
	m := away.Mark{LastSeenAt: now, VisitStartedAt: now.Add(-2 * time.Minute), AwaySince: &left, DismissedAt: &dis}
	if err := st.PutAwayMark(ctx, "org-a", "u1", m); err != nil {
		t.Fatal(err)
	}
	got, found, err := st.GetAwayMark(ctx, "org-a", "u1")
	if err != nil || !found || !got.LastSeenAt.Equal(now) || got.AwaySince == nil || !got.AwaySince.Equal(left) || got.DismissedAt == nil || !got.DismissedAt.Equal(dis) {
		t.Fatalf("round trip = %+v %v %v", got, found, err)
	}
	m.DismissedAt = nil // upsert clears it
	if err := st.PutAwayMark(ctx, "org-a", "u1", m); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := st.GetAwayMark(ctx, "org-a", "u1"); got.DismissedAt != nil {
		t.Fatal("upsert kept dismissed_at")
	}
	// Tenant isolation: the same user id in another organization is another mark, and RLS hides org-a rows.
	if _, found, _ := st.GetAwayMark(ctx, "org-b", "u1"); found {
		t.Fatal("org-b reads the org-a mark")
	}
	err = st.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM away_marks WHERE org_id='org-a'`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("org-b sees %d org-a marks", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// History by window.
	ev := func(org, id, typ string, at time.Time) {
		t.Helper()
		if err := st.SaveEvent(ctx, org, domain.Event{ID: id, Type: typ, TS: at, AgentID: "sales", Payload: map[string]any{"agent_id": "sales", "state": "working"}}); err != nil {
			t.Fatal(err)
		}
	}
	ev("org-a", "e1", domain.EvAgentState, now.Add(-3*time.Hour))
	ev("org-a", "e2", domain.EvTaskFailed, now.Add(-2*time.Hour))
	ev("org-a", "e3", domain.EvAgentState, now.Add(-time.Hour))
	ev("org-a", "e0", domain.EvAgentState, now.Add(-20*time.Hour)) // before the window
	ev("org-b", "eb", domain.EvAgentState, now.Add(-time.Hour))    // another organization
	from, to := left, now
	list, err := st.ListEvents(ctx, "org-a", domain.EventQuery{From: from, To: to, Types: []string{domain.EvAgentState}})
	if err != nil || len(list) != 2 || list[0].ID != "e1" || list[1].ID != "e3" {
		t.Fatalf("list = %+v %v", list, err)
	}
	desc, _ := st.ListEvents(ctx, "org-a", domain.EventQuery{From: from, To: to, Desc: true, Limit: 1})
	if len(desc) != 1 || desc[0].ID != "e3" {
		t.Fatalf("desc = %+v", desc)
	}
	counts, err := st.CountEvents(ctx, "org-a", from, to)
	if err != nil || counts[domain.EvAgentState] != 2 || counts[domain.EvTaskFailed] != 1 {
		t.Fatalf("counts = %v %v", counts, err)
	}
	for i, c := range []float64{0.1, 0.2, 5} {
		at := now.Add(-time.Hour)
		if i == 2 {
			at = now.Add(-30 * time.Hour)
		}
		if err := st.AddUsage(ctx, "org-a", domain.UsageEntry{ID: "u" + string(rune('a'+i)), RequestID: "r", AgentID: "sales", Kind: domain.UsageRunTask, CostUSD: c, TS: at}); err != nil {
			t.Fatal(err)
		}
	}
	spend, err := st.SpendByAgent(ctx, "org-a", from, to)
	if err != nil || len(spend) != 1 || spend[0].Calls != 2 || spend[0].CostUSD < 0.299 || spend[0].CostUSD > 0.301 {
		t.Fatalf("spend = %+v %v", spend, err)
	}

	// The service end to end on Postgres.
	svc := away.New(away.Config{Marks: st, History: st, OrgID: "org-a", Now: func() time.Time { return now }})
	s, err := svc.Summary(application.WithOrg(ctx, "org-a"), away.Viewer{UserID: "u2", Role: "owner", CanDecide: true, ReadApprovals: true}, 0)
	if err != nil || !s.FirstVisit {
		t.Fatalf("service first visit = %+v %v", s, err)
	}
}

func TestMigration792DownThenUpAgain(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	if err := st.PutAwayMark(ctx, "org-a", "u1", away.Mark{LastSeenAt: time.Now().UTC(), VisitStartedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../../migrations/down/792_away_marks_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	owner := env.owner(t)
	if _, err := owner.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	var reg *string
	_ = owner.QueryRow(ctx, `SELECT to_regclass('away_marks')::text`).Scan(&reg)
	if reg != nil {
		t.Fatalf("down left the table: %s", *reg)
	}
	up, err := migrationsFS.ReadFile("migrations/792_away_marks.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // idempotent
		if _, err := owner.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up after down (%d): %v", i, err)
		}
	}
	var forced bool
	_ = owner.QueryRow(ctx, `SELECT relforcerowsecurity FROM pg_class WHERE relname='away_marks'`).Scan(&forced)
	if !forced {
		t.Error("row level security is not forced after the up migration")
	}
}
