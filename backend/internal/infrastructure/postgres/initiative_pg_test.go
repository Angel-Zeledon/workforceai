package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

func initiativeRow(id, agent, key string, at time.Time) application.Initiative {
	return application.Initiative{ID: id, AgentID: agent, Signal: "margin_drop", SignalKey: key, Title: "El margen bajó", Body: "¿Lo reviso?",
		Prompt: "Revisa " + key, Status: application.InitiativePending, CreatedAt: at}
}

// Proactive suggestions (migration 787): round trip, compare-and-set resolution, counts, dedupe lookups, settings
// upsert and tenant isolation under RLS.
func TestInitiativeStorePostgres(t *testing.T) {
	st := testStore(t).Store
	ctx := context.Background()
	seedOrg(t, st, "org-i1")
	seedOrg(t, st, "org-i2")
	at := time.Now().UTC().Truncate(time.Microsecond)

	for _, it := range []application.Initiative{
		initiativeRow("i1", "sales", "a", at.Add(-2*time.Hour)),
		initiativeRow("i2", "sales", "b", at.Add(-time.Hour)),
		initiativeRow("i3", "finance", "c", at),
	} {
		if err := st.CreateInitiative(ctx, "org-i1", it); err != nil {
			t.Fatal(err)
		}
	}
	list, err := st.ListInitiatives(ctx, "org-i1", "", 0)
	if err != nil || len(list) != 3 || list[0].ID != "i3" || list[2].ID != "i1" || list[0].Status != application.InitiativePending {
		t.Fatalf("list = %+v %v", list, err)
	}
	if one, _ := st.ListInitiatives(ctx, "org-i1", application.InitiativePending, 1); len(one) != 1 {
		t.Fatalf("limit: %+v", one)
	}

	if n, _ := st.CountInitiatives(ctx, "org-i1", "", at.Add(-90*time.Minute)); n != 2 {
		t.Errorf("count since -90m = %d", n)
	}
	if n, _ := st.CountInitiatives(ctx, "org-i1", "sales", at.Add(-3*time.Hour)); n != 2 {
		t.Errorf("count sales = %d", n)
	}
	if ok, _ := st.HasInitiativeSignal(ctx, "org-i1", "margin_drop", "a", at.Add(-3*time.Hour)); !ok {
		t.Error("signal a should be found")
	}
	if ok, _ := st.HasInitiativeSignal(ctx, "org-i1", "margin_drop", "a", at.Add(-time.Hour)); ok {
		t.Error("signal a is older than the window")
	}

	// Compare-and-set: only one resolution wins; moving back to pending clears it.
	got, err := st.ResolveInitiative(ctx, "org-i1", "i1", application.InitiativePending, application.InitiativeAccepted, "ana", "", at)
	if err != nil || got.Status != application.InitiativeAccepted || got.ResolvedBy != "ana" || got.ResolvedAt == nil {
		t.Fatalf("resolve = %+v %v", got, err)
	}
	if _, err := st.ResolveInitiative(ctx, "org-i1", "i1", application.InitiativePending, application.InitiativeDismissed, "bob", "", at); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second resolve = %v, want conflict", err)
	}
	if _, err := st.ResolveInitiative(ctx, "org-i1", "nope", application.InitiativePending, application.InitiativeDismissed, "bob", "", at); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing resolve = %v, want not found", err)
	}
	got, err = st.ResolveInitiative(ctx, "org-i1", "i1", application.InitiativeAccepted, application.InitiativeAccepted, "ana", "req-9", at)
	if err != nil || got.RequestID != "req-9" {
		t.Fatalf("attach request = %+v %v", got, err)
	}
	got, err = st.ResolveInitiative(ctx, "org-i1", "i1", application.InitiativeAccepted, application.InitiativePending, "", "", at)
	if err != nil || got.Status != application.InitiativePending || got.ResolvedAt != nil || got.RequestID != "" || got.ResolvedBy != "" {
		t.Fatalf("revert = %+v %v", got, err)
	}

	// Settings: none stored, then upsert with agent overrides.
	if _, ok, err := st.GetInitiativeSettings(ctx, "org-i1"); err != nil || ok {
		t.Fatalf("no settings yet: ok=%v err=%v", ok, err)
	}
	if err := st.PutInitiativeSettings(ctx, "org-i1", application.InitiativeSettings{Enabled: true, DailyLimit: 2, MonthlyLimit: 9, AgentDaily: map[string]int{"sales": 1}}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutInitiativeSettings(ctx, "org-i1", application.InitiativeSettings{Enabled: false, DailyLimit: 4, MonthlyLimit: 10}); err != nil {
		t.Fatal(err)
	}
	s, ok, err := st.GetInitiativeSettings(ctx, "org-i1")
	if err != nil || !ok || s.Enabled || s.DailyLimit != 4 || s.MonthlyLimit != 10 || len(s.AgentDaily) != 0 {
		t.Fatalf("settings = %+v %v %v", s, ok, err)
	}

	// Tenant isolation.
	if other, _ := st.ListInitiatives(ctx, "org-i2", "", 0); len(other) != 0 {
		t.Fatal("org-i2 sees suggestions of org-i1")
	}
	if _, ok, _ := st.GetInitiativeSettings(ctx, "org-i2"); ok {
		t.Fatal("org-i2 sees the settings of org-i1")
	}
	if _, err := st.GetInitiative(ctx, "org-i2", "i1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-org get = %v", err)
	}
	if _, err := st.ResolveInitiative(ctx, "org-i2", "i1", application.InitiativePending, application.InitiativeDismissed, "x", "", at); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-org resolve = %v", err)
	}
}
