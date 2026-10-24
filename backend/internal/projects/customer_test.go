package projects_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
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

// artTags is a mutable artifact -> customer table (ArtifactCustomer).
type artTags struct {
	mu sync.Mutex
	m  map[string]string
}

func (a *artTags) lookup(_ context.Context, _, id string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, ok := a.m[id]
	if !ok {
		return "", domain.ErrNotFound
	}
	return c, nil
}

func (a *artTags) set(id, c string) { a.mu.Lock(); a.m[id] = c; a.mu.Unlock() }

// B12: a customer's project plan may only reference ("artifact:<id>") artifacts of that customer. Another customer's
// artifact, or one without a customer, is refused when the plan is written (goal, edits, plan changes) and again at
// launch. (The task-time filter is tested in internal/customers/artifact_isolation_test.go.)
func TestCustomerPlanRefusesOtherCustomersArtifacts(t *testing.T) {
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
	orch.SetCustomerScope(&scopeSpy{links: map[string]string{}})
	tags := &artTags{m: map[string]string{"art_aaaaaa1": "cus_a", "art_bbbbbb1": "cus_b", "art_gggggg1": ""}}
	active := func(_ context.Context, _, id string) error {
		if id != "cus_a" && id != "cus_b" {
			return domain.ErrNotFound
		}
		return nil
	}
	svc := projects.New(ctx, projects.Config{Store: projects.NewMemStore(), Orch: orch, Core: store, Approvals: appr, Rec: rec, Runtime: rt,
		Catalog: catalog.Default(), OrgID: cfg.OrgID, Poll: 10 * time.Millisecond, Log: log, CustomerActive: active, ArtifactCustomer: tags.lookup})
	bg := context.Background()
	isolation := func(err error) bool {
		return errors.Is(err, domain.ErrInvalid) && strings.Contains(err.Error(), projects.CodeCustomerIsolation)
	}

	// The goal of a customer's project.
	for _, ref := range []string{"art_bbbbbb1", "art_gggggg1"} {
		if _, err := svc.CreateDraft(bg, projects.NewProject{Goal: "cierre usando artifact:" + ref, TemplateID: "tpl-generic", CustomerID: "cus_a"}); !isolation(err) {
			t.Fatalf("a draft of A referencing %s must be refused: %v", ref, err)
		}
	}
	if all, _ := svc.List(bg); len(all) != 0 {
		t.Fatalf("a refused draft must not be stored: %+v", all)
	}
	own, err := svc.CreateDraft(bg, projects.NewProject{Goal: "cierre usando artifact:art_aaaaaa1 y artifact:art_missing1", TemplateID: "tpl-generic", CustomerID: "cus_a"})
	if err != nil {
		t.Fatalf("A's own artifact (and a missing one) are fine: %v", err)
	}
	// Without a customer the old behaviour holds.
	plain, err := svc.CreateDraft(bg, projects.NewProject{Goal: "resumen con artifact:art_bbbbbb1", TemplateID: "tpl-generic"})
	if err != nil {
		t.Fatalf("an unscoped project keeps the old rule: %v", err)
	}
	// Assigning a customer to that draft later (history.go) applies the same rule.
	if _, err := svc.SetCustomer(bg, plain.ID, "cus_a"); !isolation(err) {
		t.Fatalf("a draft referencing B's artifact cannot become A's: %v", err)
	}
	if prev, err := svc.SetCustomer(bg, plain.ID, "cus_b"); err != nil || prev != "" {
		t.Fatalf("a draft referencing B's artifact can become B's: %q %v", prev, err)
	}
	if prev, err := svc.SetCustomer(bg, plain.ID, ""); err != nil || prev != "cus_b" {
		t.Fatalf("and back (undo): %q %v", prev, err)
	}
	if _, err := svc.SetCustomer(bg, plain.ID, "cus_zzz"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an unknown customer is refused: %v", err)
	}

	// A plan edit (a title reaches the task description).
	leaf, agent := "", ""
	for _, n := range own.Nodes {
		if n.AgentID != nil && *n.AgentID != "" && *n.AgentID != "assistant" {
			leaf, agent = n.ID, *n.AgentID
		}
	}
	if leaf == "" {
		t.Fatalf("the generic plan has an agent task: %+v", own.Nodes)
	}
	title := "Revisar artifact:art_bbbbbb1"
	op := projects.PlanOp{Op: "update", ID: leaf}
	op.Fields.Title = &title
	if _, _, err := svc.PatchPlan(bg, own.ID, []projects.PlanOp{op}); !isolation(err) {
		t.Fatalf("an edit referencing B's artifact must be refused: %v", err)
	}
	d, _ := svc.Get(bg, own.ID)
	for _, n := range d.Nodes {
		if strings.Contains(n.Title, "art_bbbbbb1") {
			t.Fatalf("the refused edit must not be stored: %+v", n)
		}
	}

	// The artifact changed hands after the draft was written: the launch refuses.
	tags.set("art_aaaaaa1", "cus_b")
	if _, err := svc.Launch(bg, own.ID, projects.LaunchBody{ApprovedBudgetUSD: 1000, AcknowledgeUnderbudget: true, ProceedWithoutInputs: true}); !isolation(err) {
		t.Fatalf("the launch must re-check the references: %v", err)
	}
	tags.set("art_aaaaaa1", "cus_a")
	if _, err := svc.Launch(bg, own.ID, projects.LaunchBody{ApprovedBudgetUSD: 1000, AcknowledgeUnderbudget: true, ProceedWithoutInputs: true}); err != nil {
		t.Fatal(err)
	}

	// A plan change of the launched project.
	_, err = svc.ProposeChange(application.WithActor(bg, "alice"), own.ID, projects.NewPlanChange{Reason: "más datos", Ops: []projects.PlanChangeOp{
		{Op: projects.OpAddTask, Tasks: []projects.ProposedTask{{Key: "x", Title: "Cruzar saldos", Description: "Usa artifact:art_bbbbbb1", AgentID: agent}}}}})
	if !isolation(err) {
		t.Fatalf("a plan change referencing B's artifact must be refused: %v", err)
	}

	// Without the artifact lookup a customer's plan with references cannot be checked: refused (fail closed).
	blind := projects.New(ctx, projects.Config{Store: projects.NewMemStore(), Orch: orch, Core: store, Approvals: appr, Rec: rec, Runtime: rt,
		Catalog: catalog.Default(), OrgID: cfg.OrgID, Log: log, CustomerActive: active})
	if _, err := blind.CreateDraft(bg, projects.NewProject{Goal: "cierre con artifact:art_aaaaaa1", TemplateID: "tpl-generic", CustomerID: "cus_a"}); !isolation(err) {
		t.Fatalf("unchecked references must be refused: %v", err)
	}
	orch.Wait()
}
