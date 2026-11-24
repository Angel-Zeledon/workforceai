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

// submitSpy records what accepting a suggestion submits (the existing request path).
type submitSpy struct {
	texts []string
	err   error
}

func (s *submitSpy) Submit(_ context.Context, text string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	s.texts = append(s.texts, text)
	return "req-1", nil
}

func newInitiative(t *testing.T) (*application.InitiativeService, *memory.Store, *submitSpy, *time.Time) {
	t.Helper()
	mem := memory.New()
	spy := &submitSpy{}
	now := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	svc := &application.InitiativeService{Store: mem, Submit: spy, Cfg: application.DefaultConfig(), Now: func() time.Time { return now }}
	return svc, mem, spy, &now
}

func raise(t *testing.T, svc *application.InitiativeService, agent, key string) application.RaiseOutcome {
	t.Helper()
	out, err := svc.Raise(context.Background(), application.RaiseInput{AgentID: agent, Signal: "margin_drop", SignalKey: key,
		Title: "El margen bajó 3 %", Body: "¿Lo reviso?", Prompt: "Revisa el margen de " + key})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestInitiativeRaiseAppliesDailyAndMonthlyBudget(t *testing.T) {
	svc, _, _, now := newInitiative(t)
	ctx := context.Background()
	if _, err := svc.PutSettings(ctx, application.InitiativeSettings{Enabled: true, DailyLimit: 2, MonthlyLimit: 3}); err != nil {
		t.Fatal(err)
	}
	if o := raise(t, svc, "sales", "a"); !o.Raised {
		t.Fatalf("first: %+v", o)
	}
	if o := raise(t, svc, "sales", "b"); !o.Raised {
		t.Fatalf("second: %+v", o)
	}
	if o := raise(t, svc, "sales", "c"); o.Raised || o.Reason != "daily_limit" {
		t.Fatalf("third must hit the daily limit: %+v", o)
	}
	*now = now.Add(24 * time.Hour) // next day: the day counter resets, the month keeps counting
	if o := raise(t, svc, "sales", "c"); !o.Raised {
		t.Fatalf("next day: %+v", o)
	}
	if o := raise(t, svc, "sales", "d"); o.Raised || o.Reason != "monthly_limit" {
		t.Fatalf("fourth in the month must hit the monthly limit: %+v", o)
	}
	*now = time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC) // next month
	if o := raise(t, svc, "sales", "d"); !o.Raised {
		t.Fatalf("next month: %+v", o)
	}
}

func TestInitiativeDedupeWindowAndPerAgentOverride(t *testing.T) {
	svc, _, _, now := newInitiative(t)
	ctx := context.Background()
	if _, err := svc.PutSettings(ctx, application.InitiativeSettings{Enabled: true, DailyLimit: 10, MonthlyLimit: 100,
		AgentDaily: map[string]int{"sales": 1, "muted": 0}}); err != nil {
		t.Fatal(err)
	}
	if o := raise(t, svc, "sales", "margin"); !o.Raised {
		t.Fatalf("first: %+v", o)
	}
	if o := raise(t, svc, "sales", "MARGIN "); o.Raised || o.Reason != "duplicate" {
		t.Fatalf("same signal must not repeat inside the window: %+v", o)
	}
	if o := raise(t, svc, "sales", "other"); o.Raised || o.Reason != "agent_limit" {
		t.Fatalf("agent override of 1/day: %+v", o)
	}
	if o := raise(t, svc, "muted", "x"); o.Raised || o.Reason != "agent_limit" {
		t.Fatalf("muted agent: %+v", o)
	}
	if o := raise(t, svc, "finance", "x"); !o.Raised {
		t.Fatalf("an agent without override uses the org limit: %+v", o)
	}
	*now = now.Add(25 * time.Hour) // window elapsed
	if o := raise(t, svc, "sales", "margin"); !o.Raised {
		t.Fatalf("after the dedupe window: %+v", o)
	}
}

func TestInitiativeDisabledAndValidation(t *testing.T) {
	svc, _, _, _ := newInitiative(t)
	ctx := context.Background()
	if s, err := svc.Settings(ctx); err != nil || !s.Enabled || s.DailyLimit != application.DefaultInitiativeDaily {
		t.Fatalf("defaults = %+v %v", s, err)
	}
	if _, err := svc.PutSettings(ctx, application.InitiativeSettings{Enabled: true, DailyLimit: -1}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("negative limit = %v", err)
	}
	if _, err := svc.PutSettings(ctx, application.InitiativeSettings{Enabled: false, DailyLimit: 3, MonthlyLimit: 30}); err != nil {
		t.Fatal(err)
	}
	if o := raise(t, svc, "sales", "a"); o.Raised || o.Reason != "disabled" {
		t.Fatalf("disabled org: %+v", o)
	}
	if _, err := svc.Raise(ctx, application.RaiseInput{AgentID: "sales"}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("incomplete input = %v", err)
	}
}

func TestInitiativeAcceptSubmitsANormalRequestOnce(t *testing.T) {
	svc, _, spy, _ := newInitiative(t)
	ctx := application.WithActor(context.Background(), "ana")
	it := raise(t, svc, "sales", "a").Initiative

	got, err := svc.Accept(ctx, it.ID)
	if err != nil || got.Status != application.InitiativeAccepted || got.RequestID != "req-1" || got.ResolvedBy != "ana" || got.ResolvedAt == nil {
		t.Fatalf("accept = %+v %v", got, err)
	}
	if len(spy.texts) != 1 || spy.texts[0] != it.Prompt {
		t.Fatalf("submitted = %q", spy.texts)
	}
	if _, err := svc.Accept(ctx, it.ID); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("second accept = %v (must conflict, one request only)", err)
	}
	if _, err := svc.Dismiss(ctx, it.ID); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("dismiss after accept = %v", err)
	}
	if len(spy.texts) != 1 {
		t.Fatalf("a request was submitted more than once: %q", spy.texts)
	}
	if _, err := svc.Accept(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown id = %v", err)
	}
}

func TestInitiativeAcceptRefusedByTheRequestPathStaysPending(t *testing.T) {
	svc, _, spy, _ := newInitiative(t)
	ctx := context.Background()
	it := raise(t, svc, "sales", "a").Initiative
	spy.err = domain.ErrForbidden // kill switch, budget, delegation rule...
	if _, err := svc.Accept(ctx, it.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("accept = %v", err)
	}
	pending, _ := svc.List(ctx, application.InitiativePending, 0)
	if len(pending) != 1 || pending[0].RequestID != "" || pending[0].ResolvedAt != nil {
		t.Fatalf("a refused accept must leave it pending: %+v", pending)
	}
	spy.err = nil
	if _, err := svc.Accept(ctx, it.ID); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestInitiativeDismissListAndTaskFailedHook(t *testing.T) {
	svc, _, spy, _ := newInitiative(t)
	ctx := context.Background()
	svc.TaskFailed(ctx, domain.Task{ID: "t1", AgentID: "sales", Title: "Enviar propuesta"}, errors.New("boom"))
	svc.TaskFailed(ctx, domain.Task{ID: "t2", AgentID: "sales", Title: "enviar propuesta"}, errors.New("boom")) // same signal: deduped
	svc.TaskFailed(ctx, domain.Task{ID: "t3"}, errors.New("no agent"))                                           // ignored
	list, err := svc.List(ctx, "", 0)
	if err != nil || len(list) != 1 || list[0].Signal != application.SignalTaskFailed || list[0].Status != application.InitiativePending {
		t.Fatalf("list = %+v %v", list, err)
	}
	if len(spy.texts) != 0 {
		t.Fatal("raising a suggestion must not submit anything")
	}
	d, err := svc.Dismiss(ctx, list[0].ID)
	if err != nil || d.Status != application.InitiativeDismissed {
		t.Fatalf("dismiss = %+v %v", d, err)
	}
	if len(spy.texts) != 0 {
		t.Fatal("dismissing must not submit anything")
	}
	if _, err := svc.List(ctx, "bogus", 0); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("bogus status = %v", err)
	}
	// A dismissed suggestion still counts against the budget of the day.
	if n, _ := svc.Store.CountInitiatives(ctx, application.DefaultConfig().OrgID, "", time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)); n != 1 {
		t.Errorf("count = %d", n)
	}
}
