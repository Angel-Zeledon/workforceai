package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/rituals"
)

func ritualRow(id, origin, key string, at time.Time) rituals.Ritual {
	from := at.Add(-24 * time.Hour)
	return rituals.Ritual{ID: id, Kind: rituals.KindStandup, Origin: origin, PeriodKey: key, Status: rituals.StatusHeld, Locale: "es",
		WindowFrom: &from, WindowTo: &at, HeldAt: at, CreatedBy: "ana", Summary: "s", SummarySource: rituals.SummaryDeterministic,
		Entries: []rituals.Entry{{AgentID: "sales", Name: "Valeria", Role: "sales", Done: []rituals.Item{{TaskID: "t1", Title: "Cotizar", Status: "done"}},
			Next: []rituals.Item{}, Blockers: []rituals.Item{}, Lines: []string{"Hice: «Cotizar».", "Haré: nada."}}},
		Absent: []rituals.Absent{{AgentID: "hr", Name: "Marcos", Reason: "paused"}}}
}

// Rituals (migration 793): round trip, the scheduled claim, summaries, proposals with compare-and-set, settings and
// tenant isolation under RLS.
func TestRitualStorePostgres(t *testing.T) {
	st := testStore(t).Store
	q := &RitualStore{S: st}
	ctx := context.Background()
	seedOrg(t, st, "org-r1")
	seedOrg(t, st, "org-r2")
	at := time.Now().UTC().Truncate(time.Microsecond)

	ok, err := q.CreateRitual(ctx, "org-r1", ritualRow("r1", rituals.OriginScheduled, "2026-10-21", at.Add(-time.Hour)))
	if err != nil || !ok {
		t.Fatalf("create = %v %v", ok, err)
	}
	// the same scheduled occurrence cannot be held twice; manual runs have no period key and never collide
	if ok, err := q.CreateRitual(ctx, "org-r1", ritualRow("r2", rituals.OriginScheduled, "2026-10-21", at)); err != nil || ok {
		t.Fatalf("duplicate scheduled = %v %v", ok, err)
	}
	for _, id := range []string{"m1", "m2"} {
		if ok, err := q.CreateRitual(ctx, "org-r1", ritualRow(id, rituals.OriginManual, "", at)); err != nil || !ok {
			t.Fatalf("manual %s = %v %v", id, ok, err)
		}
	}
	// another organization has its own occurrence
	if ok, err := q.CreateRitual(ctx, "org-r2", ritualRow("r1", rituals.OriginScheduled, "2026-10-21", at)); err != nil || !ok {
		t.Fatalf("other org = %v %v", ok, err)
	}
	if has, _ := q.HasScheduled(ctx, "org-r1", rituals.KindStandup, "2026-10-21"); !has {
		t.Error("scheduled occurrence not found")
	}
	if has, _ := q.HasScheduled(ctx, "org-r1", rituals.KindRetro, "2026-10-21"); has {
		t.Error("kind is part of the key")
	}
	got, err := q.GetRitual(ctx, "org-r1", "r1")
	if err != nil || len(got.Entries) != 1 || got.Entries[0].Done[0].Title != "Cotizar" || len(got.Absent) != 1 || got.WindowFrom == nil || !got.HeldAt.Equal(at.Add(-time.Hour)) {
		t.Fatalf("get = %+v %v", got, err)
	}
	if err := q.SetRitualSummary(ctx, "org-r1", "r1", "modelo", rituals.SummaryRuntime); err != nil {
		t.Fatal(err)
	}
	if got, _ := q.GetRitual(ctx, "org-r1", "r1"); got.Summary != "modelo" || got.SummarySource != rituals.SummaryRuntime {
		t.Errorf("summary = %+v", got)
	}
	if err := q.SetRitualSummary(ctx, "org-r1", "missing", "x", "y"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing = %v", err)
	}
	list, err := q.ListRituals(ctx, "org-r1", rituals.KindStandup, at.Add(-30*time.Minute), 0)
	if err != nil || len(list) != 2 {
		t.Fatalf("list since = %+v %v", list, err)
	}
	if all, _ := q.ListRituals(ctx, "org-r1", "", time.Time{}, 0); len(all) != 3 || all[2].ID != "r1" {
		t.Errorf("list all = %+v", all)
	}

	ps := []rituals.Proposal{
		{ID: "p1", RitualID: "r1", AgentID: "sales", Kind: rituals.ProposalInstructionNote, Signal: rituals.SignalRejected, Title: "t", Text: "x", Evidence: []string{"a"}, Status: rituals.ProposalPending, CreatedAt: at.Add(-time.Minute)},
		{ID: "p2", RitualID: "r1", AgentID: "hr", Kind: rituals.ProposalSkillSuggestion, Signal: rituals.SignalQuality, Title: "t", Text: "y", Status: rituals.ProposalPending, CreatedAt: at},
	}
	if err := q.CreateProposals(ctx, "org-r1", ps); err != nil {
		t.Fatal(err)
	}
	if l, _ := q.ListProposals(ctx, "org-r1", rituals.ProposalQuery{RitualID: "r1"}); len(l) != 2 || l[0].ID != "p2" || l[1].Evidence[0] != "a" || l[0].Evidence == nil {
		t.Errorf("proposals = %+v", l)
	}
	p, err := q.ResolveProposal(ctx, "org-r1", "p1", rituals.ProposalPending, rituals.ProposalAccepted, "ana", true, at)
	if err != nil || p.Status != rituals.ProposalAccepted || !p.Applied || p.ResolvedBy != "ana" || p.ResolvedAt == nil {
		t.Fatalf("resolve = %+v %v", p, err)
	}
	if _, err := q.ResolveProposal(ctx, "org-r1", "p1", rituals.ProposalPending, rituals.ProposalDismissed, "bob", false, at); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("second resolve = %v", err)
	}
	if p, err := q.ResolveProposal(ctx, "org-r1", "p1", rituals.ProposalAccepted, rituals.ProposalPending, "", false, at); err != nil || p.ResolvedAt != nil || p.Applied {
		t.Errorf("back to pending = %+v %v", p, err)
	}
	if _, err := q.ResolveProposal(ctx, "org-r1", "nope", rituals.ProposalPending, rituals.ProposalAccepted, "a", false, at); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing = %v", err)
	}
	if pend, _ := q.ListProposals(ctx, "org-r1", rituals.ProposalQuery{Status: rituals.ProposalPending, AgentID: "hr"}); len(pend) != 1 {
		t.Errorf("filter = %+v", pend)
	}

	if _, ok, err := q.GetSettings(ctx, "org-r1"); ok || err != nil {
		t.Fatalf("no settings yet: %v %v", ok, err)
	}
	s := rituals.DefaultSettings()
	s.Standup.Enabled, s.Timezone = true, "America/Mexico_City"
	if err := q.PutSettings(ctx, "org-r1", s); err != nil {
		t.Fatal(err)
	}
	s.UseModel = true
	if err := q.PutSettings(ctx, "org-r1", s); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := q.GetSettings(ctx, "org-r1"); !ok || err != nil || !got.UseModel || !got.Standup.Enabled || got.Timezone != "America/Mexico_City" {
		t.Errorf("settings = %+v %v %v", got, ok, err)
	}

	// RLS: org-r2 sees only its own rows and cannot read org-r1's proposal.
	if l, _ := q.ListRituals(ctx, "org-r2", "", time.Time{}, 0); len(l) != 1 {
		t.Errorf("org-r2 rituals = %+v", l)
	}
	if _, err := q.GetProposal(ctx, "org-r2", "p2"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cross-org proposal = %v", err)
	}
	if _, ok, _ := q.GetSettings(ctx, "org-r2"); ok {
		t.Error("settings leaked across organizations")
	}
	if err := st.exec(ctx, "org-r2", `INSERT INTO rituals (org_id, id, kind, origin, status) VALUES ('org-r1','x','standup','manual','held')`); err == nil {
		t.Error("RLS let org-r2 write an org-r1 row")
	}
	// history is append-only for the application role
	if err := st.exec(ctx, "org-r1", `DELETE FROM ritual_proposals WHERE org_id='org-r1'`); err == nil {
		t.Error("the application role must not delete proposals")
	}
}

func TestMigration793DownThenUpAgain(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	seedOrg(t, st, "org-r1")
	q := &RitualStore{S: st}
	if _, err := q.CreateRitual(ctx, "org-r1", ritualRow("r1", rituals.OriginManual, "", time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../../migrations/down/793_rituals_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	owner := env.owner(t)
	if _, err := owner.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	for _, tbl := range []string{"rituals", "ritual_proposals", "ritual_settings"} {
		var reg *string
		_ = owner.QueryRow(ctx, `SELECT to_regclass($1)::text`, tbl).Scan(&reg)
		if reg != nil {
			t.Fatalf("down left %s", *reg)
		}
	}
	up, err := migrationsFS.ReadFile("migrations/793_rituals.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // idempotent
		if _, err := owner.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up after down (%d): %v", i, err)
		}
	}
	for _, tbl := range []string{"rituals", "ritual_proposals", "ritual_settings"} {
		var forced bool
		_ = owner.QueryRow(ctx, `SELECT relforcerowsecurity FROM pg_class WHERE relname=$1`, tbl).Scan(&forced)
		if !forced {
			t.Errorf("row level security is not forced on %s after the up migration", tbl)
		}
	}
}
