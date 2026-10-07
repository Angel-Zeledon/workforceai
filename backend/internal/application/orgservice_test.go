package application_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

type cfgHarness struct {
	*harness
	svc *application.OrgConfig
	now *time.Time
}

// newCfgHarness wires an OrgConfig on top of the orchestrator harness. The
// fake runtime fails the test if the planner is called (templates must skip it).
func newCfgHarness(t *testing.T, rt *fakeRuntime) *cfgHarness {
	t.Helper()
	if rt.plan == nil {
		rt.plan = func(application.PlanRequest) (application.PlanResponse, error) {
			t.Error("planner must not be called for template runs")
			return application.PlanResponse{}, errors.New("unexpected plan")
		}
	}
	h := newHarness(t, rt, nil)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rec := &application.Recorder{OrgID: domain.DemoOrgID, Store: h.store, Pub: &capture{}, Log: log}
	now := time.Date(2026, 10, 5, 7, 0, 0, 0, time.UTC) // a Monday
	svc := &application.OrgConfig{Cfg: application.DefaultConfig(), Store: h.store, Core: h.store, Orch: h.orch, Rec: rec, Log: log,
		Now: func() time.Time { return now }}
	h.orch.SetStyle(svc)
	return &cfgHarness{harness: h, svc: svc, now: &now}
}

func TestOnboardSeedsAgentsMemoryRulesAndBriefing(t *testing.T) {
	h := newCfgHarness(t, &fakeRuntime{})
	ctx := context.Background()
	res, err := h.svc.Onboard(ctx, application.OnboardInput{PackKey: "professional_services", Locale: "es", Tone: "mx",
		Briefing: &application.BriefingInput{Enabled: true, Hour: 8, Minute: 0, Timezone: "America/Mexico_City"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.AgentsConfigured) != 7 || res.MemorySeeded == 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if got := h.agent("sales").Autonomy; got != "approve_each" {
		t.Fatalf("sales autonomy = %q", got)
	}
	if got := h.agent("analyst").Autonomy; got != "autonomous" {
		t.Fatalf("analyst autonomy = %q", got)
	}
	mem, _ := h.store.ListMemory(ctx, domain.DemoOrgID, "assistant")
	if len(mem) == 0 || mem[0].Key != "business_type" {
		t.Fatalf("assistant memory not seeded: %+v", mem)
	}
	st := res.Settings
	if !st.Onboarded || st.BusinessType != "professional_services" || st.Tone != "mx" || st.Rules.ApprovalAmountUSD != 5000 {
		t.Fatalf("settings = %+v", st)
	}
	if len(st.Recommendation) == 0 {
		t.Fatal("expected recommended templates")
	}
	if res.Schedule == nil || res.Schedule.NextRunAt == nil {
		t.Fatalf("briefing schedule missing: %+v", res.Schedule)
	}
	// 08:00 Mexico City (UTC-6, no DST in 2026) == 14:00 UTC the same day.
	if want := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC); !res.Schedule.NextRunAt.Equal(want) {
		t.Fatalf("next run = %v, want %v", res.Schedule.NextRunAt, want)
	}
	// A second onboarding is refused unless forced; forcing keeps a single briefing.
	if _, err := h.svc.Onboard(ctx, application.OnboardInput{PackKey: "general"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second onboarding: %v", err)
	}
	if _, err := h.svc.Onboard(ctx, application.OnboardInput{PackKey: "general", Force: true,
		Briefing: &application.BriefingInput{Enabled: true, Hour: 9, Timezone: "UTC"}}); err != nil {
		t.Fatal(err)
	}
	if sc, _ := h.svc.ListSchedules(ctx); len(sc) != 1 || sc[0].Hour != 9 {
		t.Fatalf("schedules after force = %+v", sc)
	}
}

func TestOnboardValidation(t *testing.T) {
	h := newCfgHarness(t, &fakeRuntime{})
	ctx := context.Background()
	for name, in := range map[string]application.OnboardInput{
		"unknown pack": {PackKey: "nope"},
		"bad tone":     {PackKey: "general", Tone: "klingon"},
		"bad locale":   {PackKey: "general", Locale: "fr"},
		"bad zone":     {PackKey: "general", Briefing: &application.BriefingInput{Enabled: true, Hour: 8, Timezone: "Mars/Base"}},
		"bad hour":     {PackKey: "general", Briefing: &application.BriefingInput{Enabled: true, Hour: 25, Timezone: "UTC"}},
		"bad weekday":  {PackKey: "general", Briefing: &application.BriefingInput{Enabled: true, Hour: 8, Timezone: "UTC", Weekdays: []int{7}}},
	} {
		if _, err := h.svc.Onboard(ctx, in); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	// Nothing was applied by the failed attempts.
	if st, _ := h.svc.Settings(ctx); st.Onboarded || st.Configured {
		t.Fatalf("failed onboarding must not persist settings: %+v", st)
	}
}

func TestTemplateRunsInParallelWithoutPlanner(t *testing.T) {
	var mu sync.Mutex
	running, peak := 0, 0
	rt := &fakeRuntime{runTask: func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		mu.Lock()
		running++
		peak = max(peak, running)
		mu.Unlock()
		time.Sleep(40 * time.Millisecond)
		mu.Lock()
		running--
		mu.Unlock()
		return okResult("hecho: " + in.Task.Title), nil
	}}
	h := newCfgHarness(t, rt)
	id, err := h.svc.InstantiateTemplate(context.Background(), "month_close", map[string]string{"month": "septiembre 2026"}, "es")
	if err != nil {
		t.Fatal(err)
	}
	h.waitFor("request done", func() bool { return h.request(id).Status == domain.RequestDone })
	tasks := h.tasks(id)
	if len(tasks) != 5 {
		t.Fatalf("expected 5 tasks, got %d", len(tasks))
	}
	bs, is := tasks["Balance general de septiembre 2026"], tasks["Estado de resultados de septiembre 2026"]
	if bs.ID == "" || is.ID == "" {
		t.Fatalf("tasks missing: %v", tasks)
	}
	if len(bs.DependsOn) != 1 || len(is.DependsOn) != 1 || bs.DependsOn[0] != is.DependsOn[0] {
		t.Fatalf("balance and income statement must share one dependency: %v %v", bs.DependsOn, is.DependsOn)
	}
	mu.Lock()
	defer mu.Unlock()
	if peak < 2 {
		t.Fatalf("expected parallel execution, peak concurrency = %d", peak)
	}
}

func TestTemplateRejectsBadParams(t *testing.T) {
	h := newCfgHarness(t, &fakeRuntime{})
	if _, err := h.svc.InstantiateTemplate(context.Background(), "new_client", map[string]string{}, ""); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("missing client: %v", err)
	}
	if _, err := h.svc.InstantiateTemplate(context.Background(), "ghost", nil, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown template: %v", err)
	}
}

func TestStyleIsOmittedUntilConfiguredThenReachesRuntime(t *testing.T) {
	var mu sync.Mutex
	var seen []application.RunTaskRequest
	rt := &fakeRuntime{runTask: func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		mu.Lock()
		seen = append(seen, in)
		mu.Unlock()
		return okResult("ok"), nil
	}}
	h := newCfgHarness(t, rt)
	ctx := context.Background()
	run := func() {
		id, err := h.svc.InstantiateTemplate(ctx, "daily_briefing", nil, "es")
		if err != nil {
			t.Fatal(err)
		}
		h.waitFor("done", func() bool { return h.request(id).Status == domain.RequestDone })
	}
	run()
	mu.Lock()
	if seen[0].Locale != "" || seen[0].Tone != "" {
		t.Fatalf("unconfigured org must send no style: %+v", seen[0])
	}
	seen = nil
	mu.Unlock()

	tone := "ar"
	if _, err := h.svc.UpdateSettings(ctx, application.SettingsUpdate{Tone: &tone}); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SetAgentTone(ctx, "assistant", "cl"); err != nil {
		t.Fatal(err)
	}
	run()
	mu.Lock()
	defer mu.Unlock()
	if seen[0].Locale != "es" || seen[0].Tone != "cl" {
		t.Fatalf("agent tone override should win: %+v", seen[0])
	}
	bad := "pirate"
	if _, err := h.svc.UpdateSettings(ctx, application.SettingsUpdate{Tone: &bad}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("invalid tone accepted: %v", err)
	}
	if err := h.svc.SetAgentTone(ctx, "ghost", "mx"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown agent: %v", err)
	}
}

func TestNextRun(t *testing.T) {
	mon := time.Date(2026, 10, 5, 7, 0, 0, 0, time.UTC) // Monday 07:00 UTC
	cases := []struct {
		name  string
		after time.Time
		h, m  int
		tz    string
		days  []int
		want  time.Time
	}{
		{"later today", mon, 8, 0, "UTC", nil, time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)},
		{"already passed -> tomorrow", mon, 6, 30, "UTC", nil, time.Date(2026, 10, 6, 6, 30, 0, 0, time.UTC)},
		{"strictly after", time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC), 8, 0, "UTC", nil, time.Date(2026, 10, 6, 8, 0, 0, 0, time.UTC)},
		{"weekdays only from Friday evening", time.Date(2026, 10, 9, 20, 0, 0, 0, time.UTC), 8, 0, "UTC", []int{1, 2, 3, 4, 5}, time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)},
		{"time zone", mon, 8, 0, "America/Bogota", nil, time.Date(2026, 10, 5, 13, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		got, err := application.NextRun(c.after, c.h, c.m, c.tz, c.days)
		if err != nil || !got.Equal(c.want) {
			t.Errorf("%s: got %v (%v), want %v", c.name, got, err, c.want)
		}
	}
	if _, err := application.NextRun(mon, 8, 0, "Nope/Zone", nil); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("bad zone: %v", err)
	}
}

func TestRunDueRunsBriefingOnceAndAdvances(t *testing.T) {
	h := newCfgHarness(t, &fakeRuntime{})
	ctx := context.Background()
	sc, err := h.svc.CreateSchedule(ctx, application.ScheduleInput{Hour: 8, Minute: 0, Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if sc.TemplateKey != "daily_briefing" || !sc.Enabled {
		t.Fatalf("schedule = %+v", sc)
	}
	orgs := []string{domain.DemoOrgID}
	if n := h.svc.RunDue(ctx, orgs); n != 0 {
		t.Fatalf("not due yet, ran %d", n)
	}
	*h.now = h.now.Add(90 * time.Minute) // 08:30, due since 08:00
	if n := h.svc.RunDue(ctx, orgs); n != 1 {
		t.Fatalf("expected 1 run, got %d", n)
	}
	if n := h.svc.RunDue(ctx, orgs); n != 0 {
		t.Fatalf("an occurrence must not run twice, ran %d", n)
	}
	got, _ := h.svc.ListSchedules(ctx)
	if want := time.Date(2026, 10, 6, 8, 0, 0, 0, time.UTC); !got[0].NextRunAt.Equal(want) || got[0].LastRunAt == nil {
		t.Fatalf("schedule not advanced: %+v", got[0])
	}
	h.waitFor("briefing request", func() bool {
		rs, _ := h.store.ListRequests(ctx, domain.DemoOrgID)
		return len(rs) == 1 && rs[0].Status == domain.RequestDone
	})
}

func TestRunDueSkipsStaleOccurrences(t *testing.T) {
	h := newCfgHarness(t, &fakeRuntime{})
	ctx := context.Background()
	if _, err := h.svc.CreateSchedule(ctx, application.ScheduleInput{Hour: 8, Minute: 0, Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	*h.now = h.now.Add(14 * time.Hour) // 21:00: the 08:00 briefing is 13h overdue
	if n := h.svc.RunDue(ctx, []string{domain.DemoOrgID}); n != 0 {
		t.Fatalf("stale occurrence must not run, ran %d", n)
	}
	got, _ := h.svc.ListSchedules(ctx)
	if want := time.Date(2026, 10, 6, 8, 0, 0, 0, time.UTC); !got[0].NextRunAt.Equal(want) {
		t.Fatalf("stale schedule must still advance: %v", got[0].NextRunAt)
	}
}

func TestScheduleCRUD(t *testing.T) {
	h := newCfgHarness(t, &fakeRuntime{})
	ctx := context.Background()
	sc, err := h.svc.CreateSchedule(ctx, application.ScheduleInput{Hour: 8, Timezone: "UTC", Weekdays: []int{5, 1}})
	if err != nil {
		t.Fatal(err)
	}
	if sc.Weekdays[0] != 1 || sc.Weekdays[1] != 5 {
		t.Fatalf("weekdays must be sorted: %v", sc.Weekdays)
	}
	off := false
	up, err := h.svc.UpdateSchedule(ctx, sc.ID, application.ScheduleInput{Hour: 9, Minute: 15, Timezone: "UTC", Enabled: &off})
	if err != nil || up.Enabled || up.NextRunAt != nil || up.Hour != 9 {
		t.Fatalf("update: %+v %v", up, err)
	}
	if _, err := h.svc.CreateSchedule(ctx, application.ScheduleInput{TemplateKey: "new_client", Hour: 8, Timezone: "UTC"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("template with required params must be rejected: %v", err)
	}
	if err := h.svc.DeleteSchedule(ctx, sc.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.DeleteSchedule(ctx, sc.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("double delete: %v", err)
	}
}
