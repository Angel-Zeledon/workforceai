package application_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
)

// orgSpy records which org id reaches the store on every write.
type orgSpy struct {
	application.Store
	mu   sync.Mutex
	orgs map[string]int
}

func (s *orgSpy) note(org string) {
	s.mu.Lock()
	s.orgs[org]++
	s.mu.Unlock()
}
func (s *orgSpy) CreateRequest(ctx context.Context, org string, r domain.Request) error {
	s.note(org)
	return s.Store.CreateRequest(ctx, org, r)
}
func (s *orgSpy) CreateTask(ctx context.Context, org string, t domain.Task) error {
	s.note(org)
	return s.Store.CreateTask(ctx, org, t)
}
func (s *orgSpy) UpdateTask(ctx context.Context, org string, t domain.Task) error {
	s.note(org)
	return s.Store.UpdateTask(ctx, org, t)
}
func (s *orgSpy) UpdateAgentState(ctx context.Context, org, id string, st domain.AgentState, a string, tid *string, p int) error {
	s.note(org)
	return s.Store.UpdateAgentState(ctx, org, id, st, a, tid, p)
}
func (s *orgSpy) SaveEvent(ctx context.Context, org string, e domain.Event) error {
	s.note(org)
	return s.Store.SaveEvent(ctx, org, e)
}
func (s *orgSpy) AddAudit(ctx context.Context, org string, a domain.AuditLog) error {
	s.note(org)
	return s.Store.AddAudit(ctx, org, a)
}
func (s *orgSpy) CreateReport(ctx context.Context, org string, r domain.Report) error {
	s.note(org)
	return s.Store.CreateReport(ctx, org, r)
}

// The tenant of the request (from ctx, i.e. from the JWT) must reach every
// store call made by the background run, and the configured default org must
// never be used when ctx carries one.
func TestTenantFromContextReachesBackgroundWork(t *testing.T) {
	cfg := application.DefaultConfig()
	cfg.IdleDelay, cfg.RetryBase = 0, time.Millisecond
	spy := &orgSpy{Store: memory.New(), orgs: map[string]int{}}
	_ = spy.Seed(context.Background(), domain.SeedOrg(cfg.BudgetUSD), domain.SeedAgents())
	pub := &capture{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: spy, Pub: pub, Log: log}
	appr := application.NewApprovals(cfg, spy, rec)
	q := &application.Queries{Store: spy, Cfg: cfg}
	rt := &fakeRuntime{plan: func(application.PlanRequest) (application.PlanResponse, error) {
		return application.PlanResponse{Tasks: []application.PlannedTask{{Key: "a", Title: "A", AgentID: "sales"}}}, nil
	}}
	rctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	orch := application.NewOrchestrator(rctx, cfg, spy, rt, memory.NewLocker(), rec, appr, q, log)

	ctx := application.WithOrg(context.Background(), "tenant-42")
	if _, err := orch.Submit(ctx, "hola"); err != nil {
		t.Fatal(err)
	}
	orch.Wait()

	spy.mu.Lock()
	defer spy.mu.Unlock()
	if spy.orgs["tenant-42"] == 0 {
		t.Fatalf("no writes recorded for tenant-42: %v", spy.orgs)
	}
	for org, n := range spy.orgs {
		if org != "tenant-42" {
			t.Errorf("%d store calls leaked to org %q", n, org)
		}
	}
	pub.mu.Lock()
	defer pub.mu.Unlock()
	if len(pub.events) == 0 {
		t.Fatal("no events published")
	}
	for _, e := range pub.events {
		if e.OrgID != "tenant-42" {
			t.Errorf("event %s has org %q", e.Type, e.OrgID)
		}
	}
}

func TestOrgFromFallsBackToDefault(t *testing.T) {
	if got := application.OrgFrom(context.Background(), "demo"); got != "demo" {
		t.Fatal(got)
	}
	if got := application.OrgFrom(application.WithOrg(context.Background(), ""), "demo"); got != "demo" {
		t.Fatal("empty org must fall back")
	}
}
