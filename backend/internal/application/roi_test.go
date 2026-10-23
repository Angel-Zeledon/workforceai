package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
)

func roiTask(id, agent string, st domain.TaskStatus, at time.Time, out *domain.StructuredOutput) domain.Task {
	return domain.Task{ID: id, AgentID: agent, Status: st, FinishedAt: &at, Output: out}
}

func TestComputeROICountsOnlyCleanCompletedWorkInTheWindow(t *testing.T) {
	to := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	from := to.AddDate(0, 0, -30)
	in, old := to.Add(-time.Hour), from.Add(-time.Hour)
	agents := []domain.Agent{{ID: "acc", Role: "accounting"}, {ID: "leg", Role: "legal"}, {ID: "idle", Role: "hr"}}
	tasks := []domain.Task{
		roiTask("ok1", "acc", domain.TaskDone, in, &domain.StructuredOutput{}),
		roiTask("ok2", "acc", domain.TaskDone, in, &domain.StructuredOutput{Review: &domain.Review{Verdict: domain.VerdictPass}}),
		roiTask("rew", "acc", domain.TaskDone, in, &domain.StructuredOutput{Review: &domain.Review{Verdict: domain.VerdictPass, Reworks: 1}}),
		roiTask("retry", "acc", domain.TaskDone, in, &domain.StructuredOutput{Metrics: map[string]any{"attempts": float64(2)}}),
		roiTask("skip", "acc", domain.TaskDone, in, &domain.StructuredOutput{Metrics: map[string]any{"skipped": true}}),
		roiTask("fail", "leg", domain.TaskFailed, in, nil),
		roiTask("leg1", "leg", domain.TaskDone, in, nil),
		roiTask("old", "acc", domain.TaskDone, old, nil),
		{ID: "run", AgentID: "acc", Status: domain.TaskRunning},
	}
	usage := []domain.UsageEntry{{AgentID: "acc", CostUSD: 0.5, TS: in}, {AgentID: "acc", CostUSD: 9, TS: old}, {AgentID: "leg", CostUSD: 0.25, TS: in}}

	// Never configured: the 30-minute assumption, no money.
	rep := application.ComputeROI(application.DefaultROISettings(), false, agents, tasks, usage, from, to, 30)
	if rep.Methodology.Kind != "estimate" || rep.Methodology.DefaultMinutesSource != "assumption" || rep.Methodology.RateSource != "unset" {
		t.Fatalf("methodology = %+v", rep.Methodology)
	}
	if len(rep.Agents) != 2 { // the idle agent is left out
		t.Fatalf("agents = %+v", rep.Agents)
	}
	acc := rep.Agents[0]
	if acc.AgentID != "acc" || acc.TasksCounted != 2 || acc.ExcludedRework != 2 || acc.ExcludedSkipped != 1 || acc.EstMinutes != 60 || acc.EstHours != 1 ||
		acc.AICostUSD != 0.5 || acc.MinutesSource != "assumption" || acc.EstHumanCostUSD != nil || acc.AICostPerEstHourUSD == nil || *acc.AICostPerEstHourUSD != 0.5 {
		t.Fatalf("acc = %+v", acc)
	}
	if rep.Org.TasksCounted != 3 || rep.Org.ExcludedFailed != 1 || rep.Org.EstMinutes != 90 || rep.Org.AICostUSD != 0.75 || rep.Org.EstHumanCostUSD != nil {
		t.Fatalf("org = %+v", rep.Org)
	}

	// The org's own model: legal at 90 minutes, default 20, rate 40/h.
	rate := 40.0
	st := application.ROISettings{HourlyRateUSD: &rate, DefaultMinutes: 20, RoleMinutes: map[string]int{"legal": 90}}
	rep = application.ComputeROI(st, true, agents, tasks, usage, from, to, 30)
	byID := map[string]application.ROIAgent{}
	for _, a := range rep.Agents {
		byID[a.AgentID] = a
	}
	if a := byID["leg"]; a.MinutesPerTask != 90 || a.MinutesSource != "role" || a.EstMinutes != 90 || a.EstHumanCostUSD == nil || *a.EstHumanCostUSD != 60 {
		t.Fatalf("legal = %+v", a)
	}
	if a := byID["acc"]; a.MinutesPerTask != 20 || a.MinutesSource != "default" || a.EstMinutes != 40 {
		t.Fatalf("acc = %+v", a)
	}
	if rep.Org.EstHumanCostUSD == nil || *rep.Org.EstHumanCostUSD != 86.67 || rep.Methodology.RateSource != "org" {
		t.Fatalf("org = %+v", rep.Org)
	}
}

func TestROISettingsValidationAndAudit(t *testing.T) {
	mem := memory.New()
	svc := &application.ROIService{Store: mem, Core: mem, Cfg: application.DefaultConfig()}
	ctx := context.Background()
	st, saved, err := svc.Settings(ctx)
	if err != nil || saved || st.DefaultMinutes != application.DefaultROIMinutes || st.HourlyRateUSD != nil {
		t.Fatalf("defaults = %+v %v %v", st, saved, err)
	}
	bad := []application.ROISettings{
		{DefaultMinutes: -1},
		{DefaultMinutes: application.MaxROIMinutes + 1},
		{DefaultMinutes: 10, RoleMinutes: map[string]int{"legal": -5}},
		{DefaultMinutes: 10, RoleMinutes: map[string]int{" ": 5}},
		{DefaultMinutes: 10, HourlyRateUSD: ptrF(application.MaxROIHourlyRateUSD + 1)},
	}
	for i, in := range bad {
		if _, err := svc.PutSettings(ctx, in); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("bad[%d] = %v", i, err)
		}
	}
	out, err := svc.PutSettings(ctx, application.ROISettings{DefaultMinutes: 15, HourlyRateUSD: ptrF(25.555), RoleMinutes: map[string]int{" Legal ": 60}})
	if err != nil || out.RoleMinutes["legal"] != 60 || *out.HourlyRateUSD != 25.56 || out.UpdatedAt == nil {
		t.Fatalf("put = %+v %v", out, err)
	}
	// A zero rate clears it: hours only.
	out, err = svc.PutSettings(ctx, application.ROISettings{DefaultMinutes: 15, HourlyRateUSD: ptrF(0)})
	if err != nil || out.HourlyRateUSD != nil {
		t.Fatalf("clear rate = %+v %v", out, err)
	}
	if st, saved, _ := svc.Settings(ctx); !saved || st.DefaultMinutes != 15 || st.HourlyRateUSD != nil {
		t.Fatalf("stored = %+v %v", st, saved)
	}
}

func ptrF(v float64) *float64 { return &v }
