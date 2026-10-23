package projects_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/projects"
	"aiworkforce/backend/internal/roles"
)

// scopeSpy is an application.CustomerScope that records the request links.
type scopeSpy struct {
	mu    sync.Mutex
	links map[string]string
}

func (s *scopeSpy) LinkRequest(_ context.Context, _, requestID, customerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.links[requestID] = customerID
	return nil
}

func (s *scopeSpy) CustomerOfRequest(_ context.Context, _, requestID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.links[requestID], nil
}

func (s *scopeSpy) AssignedAgents(context.Context, string, string) ([]string, error) { return nil, nil }

// B12: a project created for a customer keeps it, shows it, and its request is scoped at launch.
func TestProjectScopedToCustomer(t *testing.T) {
	cfg := application.DefaultConfig()
	cfg.IdleDelay, cfg.RetryBase = 0, time.Millisecond
	store := memory.New()
	if err := store.Seed(context.Background(), domain.SeedOrg(cfg.BudgetUSD), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: store, Pub: nopPub{}, Log: log}
	appr := application.NewApprovals(cfg, store, rec)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	rt := newRT()
	orch := application.NewOrchestrator(ctx, cfg, store, rt, memory.NewLocker(), rec, appr, &application.Queries{Store: store, Cfg: cfg}, log)
	spy := &scopeSpy{links: map[string]string{}}
	orch.SetCustomerScope(spy)
	svc := projects.New(ctx, projects.Config{Store: projects.NewMemStore(), Orch: orch, Core: store, Approvals: appr, Rec: rec, Runtime: rt,
		Catalog: catalog.Default(), OrgID: cfg.OrgID, Poll: 10 * time.Millisecond, Log: log,
		CustomerActive: func(_ context.Context, _, id string) error {
			if id != "cus_a" {
				return domain.ErrNotFound
			}
			return nil
		}})
	bg := context.Background()
	if _, err := svc.CreateDraft(bg, projects.NewProject{Goal: "cierre", TemplateID: "tpl-generic", CustomerID: "cus_zzz"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an unknown customer is refused: %v", err)
	}
	rec2, err := svc.CreateDraft(bg, projects.NewProject{Goal: "cierre de la ferretería", TemplateID: "tpl-generic", CustomerID: " cus_a "})
	if err != nil || rec2.CustomerID != "cus_a" {
		t.Fatalf("draft keeps its customer: %+v %v", rec2.CustomerID, err)
	}
	sums, _ := svc.List(bg)
	if len(sums) != 1 || sums[0].CustomerID != "cus_a" {
		t.Fatalf("the summary carries the customer: %+v", sums)
	}
	if _, err := svc.Launch(bg, rec2.ID, projects.LaunchBody{ApprovedBudgetUSD: 1000, AcknowledgeUnderbudget: true, ProceedWithoutInputs: true}); err != nil {
		t.Fatal(err)
	}
	d, err := svc.Get(bg, rec2.ID)
	if err != nil || d.Project.RequestID == "" {
		t.Fatalf("launched: %+v %v", d.Project, err)
	}
	if got, _ := spy.CustomerOfRequest(bg, "", d.Project.RequestID); got != "cus_a" {
		t.Fatalf("the project's request must be scoped to its customer: %q", got)
	}
	// Without customers configured a customer id is refused, not ignored.
	plain := projects.New(ctx, projects.Config{Store: projects.NewMemStore(), Orch: orch, Core: store, Approvals: appr, Rec: rec, Runtime: rt,
		Catalog: catalog.Default(), OrgID: cfg.OrgID, Log: log})
	if _, err := plain.CreateDraft(bg, projects.NewProject{Goal: "x", TemplateID: "tpl-generic", CustomerID: "cus_a"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("customers unavailable: %v", err)
	}
	orch.Wait()
}
