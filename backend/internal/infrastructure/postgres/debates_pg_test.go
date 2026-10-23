package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/domain"
)

func debateRow(id, req string, at time.Time) domain.Debate {
	return domain.Debate{ID: id, Topic: "Precio 2027", Trigger: domain.DebateTriggerPlanner, RequestID: req, TaskID: "t2",
		Options: []string{"Subir", "Mantener"}, Participants: []string{"sales", "accounting"},
		Turns:     []domain.DebateTurn{{Seq: 1, AgentID: "sales", Kind: "position", Text: "subir"}, {Seq: 2, AgentID: "assistant", Kind: "synthesis", Text: "por fases"}},
		Synthesis: domain.DebateSynthesis{Choice: "Por fases", Rationale: "menos riesgo", Dissent: []string{"sales: subir ya"}},
		Status:    domain.DebateDone, MaxTurns: 8, Locale: "es", Model: "sim", Provider: "simulation", CostUSD: 0.006, CreatedBy: "ana",
		CreatedAt: at.UTC().Truncate(time.Microsecond)}
}

// Team debates (migration 799): JSON round trip, newest first, filter by request, tenant isolation under RLS and
// cleanup by Reset.
func TestDebateStorageAndIsolation(t *testing.T) {
	st := testStore(t).Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	seedOrg(t, st, "org-b")
	now := time.Now()

	if err := st.CreateDebate(ctx, "org-a", debateRow("d1", "r1", now.Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDebate(ctx, "org-a", debateRow("d2", "r2", now)); err != nil {
		t.Fatal(err)
	}
	failed := domain.Debate{ID: "d3", Topic: "x", Trigger: domain.DebateTriggerUser, Status: domain.DebateFailed, FailCode: "blocked", CreatedAt: now}
	if err := st.CreateDebate(ctx, "org-a", failed); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetDebate(ctx, "org-a", "d1")
	if err != nil || len(got.Turns) != 2 || got.Turns[1].Kind != "synthesis" || got.Synthesis.Choice != "Por fases" || len(got.Synthesis.Dissent) != 1 ||
		len(got.Options) != 2 || got.Participants[1] != "accounting" || !got.Advisory || !got.Untrusted || got.CostUSD != 0.006 {
		t.Fatalf("round trip = %+v %v", got, err)
	}
	if f, _ := st.GetDebate(ctx, "org-a", "d3"); f.FailCode != "blocked" || f.Turns == nil || len(f.Turns) != 0 {
		t.Fatalf("failed row = %+v", f)
	}
	all, err := st.ListDebates(ctx, "org-a", "", 10)
	if err != nil || len(all) != 3 || all[2].ID != "d1" {
		t.Fatalf("list = %+v %v", all, err)
	}
	if one, _ := st.ListDebates(ctx, "org-a", "r1", 10); len(one) != 1 || one[0].ID != "d1" {
		t.Fatalf("by request = %+v", one)
	}
	if lim, _ := st.ListDebates(ctx, "org-a", "", 1); len(lim) != 1 {
		t.Fatalf("limit = %d", len(lim))
	}

	// Tenant isolation.
	if _, err := st.GetDebate(ctx, "org-b", "d1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("org-b get = %v", err)
	}
	if ls, _ := st.ListDebates(ctx, "org-b", "", 10); len(ls) != 0 {
		t.Errorf("org-b sees %d debates", len(ls))
	}
	err = st.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM team_debates WHERE org_id='org-a'`).Scan(&n); err != nil {
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
	if err := st.exec(ctx, "org-b", `INSERT INTO team_debates (org_id, id, topic, trigger_kind, status) VALUES ('org-a','x','y','user_request','done')`); err == nil {
		t.Error("org-b wrote a row for org-a")
	}
	if err := st.CreateDebate(ctx, "org-b", debateRow("d1", "r1", now)); err != nil {
		t.Fatalf("org-b can reuse an id of org-a: %v", err)
	}

	if err := st.Reset(ctx, "org-a"); err != nil {
		t.Fatal(err)
	}
	if ls, _ := st.ListDebates(ctx, "org-a", "", 10); len(ls) != 0 {
		t.Errorf("after reset = %d", len(ls))
	}
	if _, err := st.GetDebate(ctx, "org-b", "d1"); err != nil {
		t.Errorf("reset of org-a removed an org-b row: %v", err)
	}
}

func TestMigration799DownThenUpAgain(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	if err := st.CreateDebate(ctx, "org-a", debateRow("d1", "r1", time.Now())); err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../../migrations/down/799_team_debates_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	owner := env.owner(t)
	if _, err := owner.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	var reg *string
	_ = owner.QueryRow(ctx, `SELECT to_regclass('team_debates')::text`).Scan(&reg)
	if reg != nil {
		t.Fatalf("down left the table: %s", *reg)
	}
	up, err := migrationsFS.ReadFile("migrations/799_team_debates.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // idempotent
		if _, err := owner.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up after down (%d): %v", i, err)
		}
	}
	var forced bool
	_ = owner.QueryRow(ctx, `SELECT relforcerowsecurity FROM pg_class WHERE relname='team_debates'`).Scan(&forced)
	if !forced {
		t.Error("row level security is not forced after the up migration")
	}
}
