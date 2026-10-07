package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Integration test (needs TEST_DATABASE_URL, see store_rls_test.go): organization
// configuration tables of migration 230 are isolated per organization by RLS.
func TestOrgConfigRLSAndClaim(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	const a, b = "cfg-org-a", "cfg-org-b"
	seedOrg(t, st, a)
	seedOrg(t, st, b)

	// Unconfigured org: defaults, not an error.
	got, err := st.GetOrgSettings(ctx, a)
	if err != nil || got.Configured || got.Locale != "es" || got.Tone != "neutral" {
		t.Fatalf("defaults = %+v, %v", got, err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	set := application.DefaultOrgSettings()
	set.Configured, set.Tone, set.BusinessType, set.Onboarded, set.OnboardedAt = true, "mx", "agency", true, &now
	if err := st.PutOrgSettings(ctx, a, set); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetOrgSettings(ctx, a); got.Tone != "mx" || !got.Onboarded || got.BusinessType != "agency" {
		t.Fatalf("org a settings = %+v", got)
	}
	if got, _ := st.GetOrgSettings(ctx, b); got.Configured {
		t.Fatalf("org b must not see org a settings: %+v", got)
	}

	// The tone check constraint rejects unknown codes.
	set.Tone = "pirate"
	if err := st.PutOrgSettings(ctx, a, set); err == nil {
		t.Fatal("invalid tone accepted by the database")
	}

	if err := st.SetAgentTone(ctx, a, "sales", "ar"); err != nil {
		t.Fatal(err)
	}
	if tones, _ := st.ListAgentTones(ctx, b); len(tones) != 0 {
		t.Fatalf("org b sees org a tones: %v", tones)
	}
	if tones, _ := st.ListAgentTones(ctx, a); tones["sales"] != "ar" {
		t.Fatalf("tones = %v", tones)
	}
	if err := st.SetAgentAutonomy(ctx, a, "sales", "autonomous"); err != nil {
		t.Fatal(err)
	}
	if ag, _ := st.GetAgent(ctx, a, "sales"); ag.Autonomy != "autonomous" {
		t.Fatalf("autonomy = %q", ag.Autonomy)
	}
	if ag, _ := st.GetAgent(ctx, b, "sales"); ag.Autonomy == "autonomous" {
		t.Fatal("autonomy leaked to org b")
	}
	if err := st.SetAgentAutonomy(ctx, a, "ghost", "rules"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown agent: %v", err)
	}

	next := now.Add(time.Hour)
	sc := application.Schedule{ID: "sched-1", TemplateKey: "daily_briefing", Params: map[string]string{}, Hour: 8, Timezone: "UTC",
		Weekdays: []int{1, 2}, Enabled: true, NextRunAt: &next, CreatedAt: now}
	if err := st.PutSchedule(ctx, a, sc); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.ListSchedules(ctx, b); len(list) != 0 {
		t.Fatalf("org b sees org a schedules: %v", list)
	}
	if _, err := st.GetSchedule(ctx, b, "sched-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-org get: %v", err)
	}
	if err := st.DeleteSchedule(ctx, b, "sched-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-org delete: %v", err)
	}

	// Compare-and-set claim: only one of two racing instances wins.
	after := next.Add(24 * time.Hour)
	ok1, err := st.ClaimSchedule(ctx, a, "sched-1", next, after, now)
	if err != nil || !ok1 {
		t.Fatalf("first claim = %v, %v", ok1, err)
	}
	if ok2, _ := st.ClaimSchedule(ctx, a, "sched-1", next, after, now); ok2 {
		t.Fatal("second claim of the same occurrence must fail")
	}
	if ok3, _ := st.ClaimSchedule(ctx, b, "sched-1", after, after, now); ok3 {
		t.Fatal("cross-org claim must fail")
	}
}
