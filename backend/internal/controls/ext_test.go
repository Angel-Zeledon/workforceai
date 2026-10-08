package controls

import (
	"context"
	"testing"
	"time"
)

type captured struct{ audits, events []string }

func newSvc(t *testing.T, now *time.Time) (*Service, *captured) {
	c := &captured{}
	s := New(NewMemStore(),
		func(_ context.Context, _, _, action, _, _ string, _ map[string]any) {
			c.audits = append(c.audits, action)
		},
		func(_ context.Context, _, typ string, _ map[string]any) { c.events = append(c.events, typ) })
	s.SetEnvLevel(LevelNone)
	s.Now = func() time.Time { return *now }
	return s, c
}

func TestOperatingHoursDefaultAlwaysOpen(t *testing.T) {
	now := time.Date(2026, 10, 10, 3, 0, 0, 0, time.UTC) // Saturday night
	s, _ := newSvc(t, &now)
	if v := s.Admit(context.Background(), "o", "a"); !v.Allowed {
		t.Fatalf("default must be always open: %+v", v)
	}
}

func TestOperatingHoursBlockOutsideAndAllowInside(t *testing.T) {
	// 2026-10-07 is a Wednesday. Mexico City is UTC-6 in October (no DST since 2022).
	now := time.Date(2026, 10, 7, 14, 0, 0, 0, time.UTC) // 08:00 local
	s, c := newSvc(t, &now)
	ctx := context.Background()
	h := OperatingHours{Enabled: true, Timezone: "America/Mexico_City", Weekly: []DayHours{{Day: 3, Open: "09:00", Close: "18:00"}}}
	if _, err := s.SetOperatingHours(ctx, "o", h, "angel"); err != nil {
		t.Fatal(err)
	}
	if len(c.audits) != 1 || c.audits[0] != "control.operating_hours" {
		t.Fatalf("not audited: %v", c.audits)
	}
	if v := s.Admit(ctx, "o", "a"); v.Allowed || v.Code != CodeOutsideHours {
		t.Fatalf("08:00 local must be closed: %+v", v)
	}
	// approvals/tool checks are not hours-gated
	if v := s.Check(ctx, "o", "a", true); !v.Allowed {
		t.Fatalf("Check must ignore hours: %+v", v)
	}
	view, _ := s.OperatingHours(ctx, "o")
	if view.OpenNow || view.NextOpen == nil || !view.NextOpen.Equal(time.Date(2026, 10, 7, 15, 0, 0, 0, time.UTC)) {
		t.Fatalf("next open: %+v", view)
	}
	now = time.Date(2026, 10, 7, 16, 0, 0, 0, time.UTC) // 10:00 local
	if v := s.Admit(ctx, "o", "a"); !v.Allowed {
		t.Fatalf("10:00 local must be open: %+v", v)
	}
	now = time.Date(2026, 10, 8, 16, 0, 0, 0, time.UTC) // Thursday: no interval
	if v := s.Admit(ctx, "o", "a"); v.Allowed {
		t.Fatal("Thursday must be closed")
	}
}

func TestOperatingHoursValidation(t *testing.T) {
	bad := []OperatingHours{
		{Enabled: true, Timezone: "Nowhere/City", Weekly: []DayHours{{Day: 1, Open: "09:00", Close: "10:00"}}},
		{Enabled: true, Timezone: "UTC"},
		{Enabled: true, Timezone: "UTC", Weekly: []DayHours{{Day: 7, Open: "09:00", Close: "10:00"}}},
		{Enabled: true, Timezone: "UTC", Weekly: []DayHours{{Day: 1, Open: "18:00", Close: "09:00"}}},
		{Enabled: true, Timezone: "UTC", Weekly: []DayHours{{Day: 1, Open: "9:00", Close: "10:00"}}},
	}
	for i, h := range bad {
		if h.Validate() == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	if err := (OperatingHours{Enabled: true, Timezone: "UTC", Weekly: []DayHours{{Day: 1, Open: "00:00", Close: "24:00"}}}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAnomalyRulesAreExplainableAndAutoFreezeIsOptIn(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	s, c := newSvc(t, &now)
	ctx := context.Background()
	th := DefaultAnomalyThresholds()
	th.BurstCalls, th.RejectedMax = 3, 2
	s.SetAnomalyThresholds(th)

	for i := 0; i < 3; i++ {
		s.ObserveToolRequest(ctx, "o", "ag")
	}
	s.ObserveApprovalRejected(ctx, "o", "ag")
	s.ObserveApprovalRejected(ctx, "o", "ag")
	n := 0
	for _, e := range c.events {
		if e == "anomaly.detected" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("want 2 anomaly events, got %v", c.events)
	}
	if st, _ := s.State(ctx, "o"); st.KillSwitchLevel != LevelNone {
		t.Fatal("auto-freeze is off by default")
	}
	// cooldown: more requests do not repeat the alert
	s.ObserveToolRequest(ctx, "o", "ag")
	if len(c.events) != n {
		t.Fatalf("cooldown ignored: %v", c.events)
	}

	// auto-freeze on
	if _, err := s.SetAnomalySettings(ctx, "o", AnomalySettings{AutoFreeze: true}, "angel"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	for i := 0; i < 3; i++ {
		s.ObserveToolRequest(ctx, "o", "ag2")
	}
	if st, _ := s.State(ctx, "o"); st.KillSwitchLevel != LevelFreeze {
		t.Fatalf("auto-freeze did not freeze: %+v", st)
	}
}

func TestAnomalySpendSpikeAndNewDomainsAndDisabled(t *testing.T) {
	now := time.Date(2026, 10, 7, 0, 30, 0, 0, time.UTC)
	s, c := newSvc(t, &now)
	ctx := context.Background()
	// baseline of 4 hours at 1 USD/h (the spike floor is 2 USD)
	for h := 0; h < 4; h++ {
		s.ObserveSpend(ctx, "o", 1)
		now = now.Add(time.Hour)
	}
	s.ObserveSpend(ctx, "o", 10)
	if len(c.events) != 1 || c.events[0] != "anomaly.detected" {
		t.Fatalf("spend spike not detected: %v", c.events)
	}

	// new recipient domains: learn 5, then 3 new within the hour
	c.events = nil
	s.ObserveRecipients(ctx, "o", []string{"a@a.com", "a@b.com", "a@c.com", "a@d.com", "a@e.com"})
	s.ObserveRecipients(ctx, "o", []string{"x@n1.com", "x@n2.com"})
	if len(c.events) != 0 {
		t.Fatal("2 new domains must not fire")
	}
	s.ObserveRecipients(ctx, "o", []string{"x@n3.com", "x@a.com"})
	if len(c.events) != 1 {
		t.Fatalf("new domains not detected: %v", c.events)
	}

	// owner disabled detection
	_, _ = s.SetAnomalySettings(ctx, "o", AnomalySettings{Disabled: true}, "owner")
	c.events = nil
	now = now.Add(2 * time.Hour)
	s.ObserveSpend(ctx, "o", 1000)
	if len(c.events) != 0 {
		t.Fatalf("disabled detection fired: %v", c.events)
	}
}
