package application_test

import (
	"context"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// A schedule created from a request or a conversation asked from an office keeps it, and every request it starts is
// asked from that office whatever runs it (the scheduler has no office of its own); one created with none starts
// requests with none, even if the one who runs the pass happens to look at an office.
func TestScheduleInheritsTheOfficeItWasCreatedFrom(t *testing.T) {
	h := newCfgHarness(t, &fakeRuntime{})
	ctx := context.Background()
	fromLegal, err := h.svc.CreateSchedule(application.WithRequestOffice(ctx, "dept-legal"), application.ScheduleInput{Hour: 8, Minute: 0, Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if fromLegal.OfficeID != "dept-legal" {
		t.Fatalf("the schedule forgot its office: %+v", fromLegal)
	}
	got, _ := h.svc.ListSchedules(ctx)
	if len(got) != 1 || got[0].OfficeID != "dept-legal" {
		t.Fatalf("stored schedule = %+v", got)
	}
	*h.now = h.now.Add(90 * time.Minute)
	if n := h.svc.RunDue(application.WithRequestOffice(ctx, "dept-sales"), []string{domain.DemoOrgID}); n != 1 {
		t.Fatalf("ran %d", n)
	}
	h.waitFor("the request of the schedule", func() bool {
		rs, _ := h.store.ListRequests(ctx, domain.DemoOrgID)
		return len(rs) == 1
	})
	rs, _ := h.store.ListRequests(ctx, domain.DemoOrgID)
	if rs[0].OfficeID != "dept-legal" {
		t.Fatalf("the request of the schedule is asked from %q, want dept-legal", rs[0].OfficeID)
	}

	// one created with no office starts requests with none, whoever runs the pass
	h2 := newCfgHarness(t, &fakeRuntime{})
	if _, err := h2.svc.CreateSchedule(ctx, application.ScheduleInput{Hour: 8, Minute: 0, Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	*h2.now = h2.now.Add(90 * time.Minute)
	h2.svc.RunDue(application.WithRequestOffice(ctx, "dept-sales"), []string{domain.DemoOrgID})
	h2.waitFor("the request of the plain schedule", func() bool {
		rs, _ := h2.store.ListRequests(ctx, domain.DemoOrgID)
		return len(rs) == 1
	})
	if rs, _ := h2.store.ListRequests(ctx, domain.DemoOrgID); rs[0].OfficeID != "" {
		t.Fatalf("a schedule with no office starts a request from %q", rs[0].OfficeID)
	}
}
