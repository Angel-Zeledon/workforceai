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

func rehearsalRow(id, req, agent string) domain.RehearsalAction {
	amt := 1200.5
	return domain.RehearsalAction{ID: id, RequestID: req, TaskID: "t1", AgentID: agent, Tool: "email", Action: "send", Source: "gateway", Scope: "agent",
		WouldDecision: domain.WouldApprove, Risk: "high", Reversibility: "none", Account: "ventas@acme.test", Recipients: []string{"laura@acme.com"},
		Amount: &amt, Flags: []string{"always_approval"}, Preview: map[string]string{"subject": "Propuesta"}, ArgsHash: "h",
		CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
}

// Rehearsal storage (migration 794): switches upsert, actions round trip with their verdict, filters, tenant
// isolation under RLS, and Reset clears the actions but keeps the switches (configuration).
func TestRehearsalStorageAndIsolation(t *testing.T) {
	st := testStore(t).Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	seedOrg(t, st, "org-b")

	if _, err := st.GetAgentRehearsal(ctx, "org-a", "sales"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get before put = %v", err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := st.PutAgentRehearsal(ctx, "org-a", domain.AgentRehearsal{AgentID: "sales", Enabled: true, SetBy: "ana", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutAgentRehearsal(ctx, "org-a", domain.AgentRehearsal{AgentID: "sales", Enabled: false, SetBy: "luis", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	r, err := st.GetAgentRehearsal(ctx, "org-a", "sales")
	if err != nil || r.Enabled || r.SetBy != "luis" {
		t.Fatalf("switch = %+v %v", r, err)
	}

	for _, a := range []domain.RehearsalAction{rehearsalRow("r1", "req1", "sales"), rehearsalRow("r2", "req2", "legal")} {
		if err := st.AddRehearsalAction(ctx, "org-a", a); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.GetRehearsalAction(ctx, "org-a", "r1")
	if err != nil || got.Recipients[0] != "laura@acme.com" || *got.Amount != 1200.5 || got.Preview["subject"] != "Propuesta" || got.Verdict != "" || got.VerdictAt != nil {
		t.Fatalf("round trip = %+v %v", got, err)
	}
	if l, _ := st.ListRehearsalActions(ctx, "org-a", domain.RehearsalQuery{RequestID: "req1"}); len(l) != 1 || l[0].ID != "r1" {
		t.Fatalf("by request = %+v", l)
	}
	if l, _ := st.ListRehearsalActions(ctx, "org-a", domain.RehearsalQuery{AgentID: "legal"}); len(l) != 1 || l[0].ID != "r2" {
		t.Fatalf("by agent = %+v", l)
	}
	v, err := st.SetRehearsalVerdict(ctx, "org-a", "r1", domain.VerdictWouldReject, "ana", now)
	if err != nil || v.Verdict != domain.VerdictWouldReject || v.VerdictBy != "ana" || v.VerdictAt == nil {
		t.Fatalf("verdict = %+v %v", v, err)
	}
	if v, _ := st.SetRehearsalVerdict(ctx, "org-a", "r1", "", "ana", now); v.Verdict != "" || v.VerdictBy != "" || v.VerdictAt != nil {
		t.Fatalf("cleared verdict = %+v", v)
	}
	if _, err := st.SetRehearsalVerdict(ctx, "org-a", "nope", domain.VerdictWouldApprove, "ana", now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("verdict on unknown id = %v", err)
	}

	// Tenant isolation.
	if _, err := st.GetRehearsalAction(ctx, "org-b", "r1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("org-b get = %v", err)
	}
	if _, err := st.SetRehearsalVerdict(ctx, "org-b", "r1", domain.VerdictWouldApprove, "x", now); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("org-b changed an org-a verdict: %v", err)
	}
	err = st.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM rehearsal_actions) + (SELECT count(*) FROM agent_rehearsal)`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("org-b sees %d rehearsal rows of org-a", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.exec(ctx, "org-b", `INSERT INTO rehearsal_actions (org_id, id, agent_id, tool, would_decision) VALUES ('org-a','x','a','t','denied')`); err == nil {
		t.Error("org-b wrote a row for org-a")
	}

	if err := st.Reset(ctx, "org-a"); err != nil {
		t.Fatal(err)
	}
	if l, _ := st.ListRehearsalActions(ctx, "org-a", domain.RehearsalQuery{}); len(l) != 0 {
		t.Errorf("reset kept %d actions", len(l))
	}
	if _, err := st.GetAgentRehearsal(ctx, "org-a", "sales"); err != nil {
		t.Errorf("reset removed the switch (configuration): %v", err)
	}
}

func TestMigration794DownThenUpAgain(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	if err := st.AddRehearsalAction(ctx, "org-a", rehearsalRow("r1", "req1", "sales")); err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../../migrations/down/794_rehearsal_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	owner := env.owner(t)
	if _, err := owner.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	for _, tbl := range []string{"rehearsal_actions", "agent_rehearsal"} {
		var reg *string
		_ = owner.QueryRow(ctx, `SELECT to_regclass($1)::text`, tbl).Scan(&reg)
		if reg != nil {
			t.Fatalf("down left the table: %s", *reg)
		}
	}
	up, err := migrationsFS.ReadFile("migrations/794_rehearsal.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // idempotent
		if _, err := owner.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up after down (%d): %v", i, err)
		}
	}
	for _, tbl := range []string{"rehearsal_actions", "agent_rehearsal"} {
		var forced bool
		_ = owner.QueryRow(ctx, `SELECT relforcerowsecurity FROM pg_class WHERE relname=$1`, tbl).Scan(&forced)
		if !forced {
			t.Errorf("row level security is not forced on %s after the up migration", tbl)
		}
	}
}
