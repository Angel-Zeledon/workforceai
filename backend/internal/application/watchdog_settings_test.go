package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

func TestStalledWorkSettingsDefaultsRangesAndOldRows(t *testing.T) {
	svc, mem, _, _ := newInitiative(t)
	ctx := context.Background()
	st, err := svc.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Signals.StalledWork; got != application.DefaultStalledWork() || !got.Enabled || got.TaskMinutes != 30 || got.ProjectMinutes != 120 {
		t.Fatalf("defaults = %+v", got)
	}
	for _, bad := range []application.StalledWorkSignal{
		{Enabled: true, TaskMinutes: 4, ProjectMinutes: 120},
		{Enabled: true, TaskMinutes: 1441, ProjectMinutes: 120},
		{Enabled: true, TaskMinutes: 30, ProjectMinutes: 14},
		{Enabled: true, TaskMinutes: 30, ProjectMinutes: 4321},
	} {
		sig := application.DefaultSignalSettings()
		sig.StalledWork = bad
		if _, err := svc.PutSettings(ctx, application.InitiativeSettings{Enabled: true, DailyLimit: 3, MonthlyLimit: 30, Signals: &sig}); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%+v accepted: %v", bad, err)
		}
	}
	sig := application.DefaultSignalSettings()
	sig.StalledWork = application.StalledWorkSignal{Enabled: false, TaskMinutes: 45, ProjectMinutes: 240}
	if _, err := svc.PutSettings(ctx, application.InitiativeSettings{Enabled: true, DailyLimit: 3, MonthlyLimit: 30, Signals: &sig}); err != nil {
		t.Fatal(err)
	}
	st, _ = svc.Settings(ctx)
	if st.Signals.StalledWork != sig.StalledWork {
		t.Fatalf("not kept: %+v", st.Signals.StalledWork)
	}
	// A row written before the watchdog existed has no stalled_work: it reads (and saves) with the defaults.
	old := application.DefaultSignalSettings()
	old.StalledWork = application.StalledWorkSignal{}
	if err := mem.PutInitiativeSettings(ctx, domain.DemoOrgID, application.InitiativeSettings{Enabled: true, DailyLimit: 3, MonthlyLimit: 30, Signals: &old}); err != nil {
		t.Fatal(err)
	}
	if st, _ = svc.Settings(ctx); st.Signals.StalledWork != application.DefaultStalledWork() {
		t.Fatalf("old row = %+v", st.Signals.StalledWork)
	}
	if _, err := svc.PutSettings(ctx, application.InitiativeSettings{Enabled: true, DailyLimit: 3, MonthlyLimit: 30, Signals: &old}); err != nil {
		t.Fatalf("an older client resaving its signals must not fail: %v", err)
	}
}

func TestStalledWorkThresholds(t *testing.T) {
	s := application.StalledWorkSignal{Enabled: true, TaskMinutes: 30, ProjectMinutes: 120}
	p := s.Policy(time.Unix(0, 0))
	if p.Task != 30*time.Minute || p.Project != 120*time.Minute || !p.Now.Equal(time.Unix(0, 0)) {
		t.Fatalf("policy = %+v", p)
	}
}
