package projects_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/projects"
	"aiworkforce/backend/internal/roles"
)

// goalOK accepts only "goal-ok" (both for the project and, at launch, for its request).
type goalOK struct{}

func (goalOK) CheckLink(_ context.Context, _, _, id string) error {
	if id != "goal-ok" {
		return domain.ErrInvalid
	}
	return nil
}

func (goalOK) AgentNote(context.Context, string, string, string) (application.RecalledMemory, bool) {
	return application.RecalledMemory{}, false
}

// F1: a project created for a goal keeps it, shows it, and the request it launches inherits it.
func TestProjectCarriesItsGoalToItsRequest(t *testing.T) {
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
	orch.SetGoals(goalOK{})
	svc := projects.New(ctx, projects.Config{Store: projects.NewMemStore(), Orch: orch, Core: store, Approvals: appr, Rec: rec, Runtime: rt,
		Catalog: catalog.Default(), OrgID: cfg.OrgID, Poll: 10 * time.Millisecond, Log: log, GoalCheck: goalOK{}.CheckLink})
	bg := context.Background()
	if _, err := svc.CreateDraft(bg, projects.NewProject{Goal: "cierre", TemplateID: "tpl-generic", GoalID: "goal-other-org"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("a goal that is not linkable is refused: %v", err)
	}
	r, err := svc.CreateDraft(bg, projects.NewProject{Goal: "cierre del trimestre", TemplateID: "tpl-generic", GoalID: " goal-ok "})
	if err != nil || r.GoalID != "goal-ok" {
		t.Fatalf("draft keeps its goal: %q %v", r.GoalID, err)
	}
	if sums, _ := svc.List(bg); len(sums) != 1 || sums[0].GoalID != "goal-ok" {
		t.Fatalf("the summary carries the goal: %+v", sums)
	}
	if _, err := svc.Launch(bg, r.ID, projects.LaunchBody{ApprovedBudgetUSD: 1000, AcknowledgeUnderbudget: true, ProceedWithoutInputs: true}); err != nil {
		t.Fatal(err)
	}
	d, err := svc.Get(bg, r.ID)
	if err != nil || d.Project.RequestID == "" {
		t.Fatalf("launched: %+v %v", d.Project, err)
	}
	req, err := store.GetRequest(bg, cfg.OrgID, d.Project.RequestID)
	if err != nil || req.GoalID != "goal-ok" {
		t.Fatalf("the project's request inherits the goal: %q %v", req.GoalID, err)
	}
	// without goals configured a goal id is refused, not ignored; without a goal nothing changes
	plain := projects.New(ctx, projects.Config{Store: projects.NewMemStore(), Orch: orch, Core: store, Approvals: appr, Rec: rec, Runtime: rt,
		Catalog: catalog.Default(), OrgID: cfg.OrgID, Log: log})
	if _, err := plain.CreateDraft(bg, projects.NewProject{Goal: "x", TemplateID: "tpl-generic", GoalID: "goal-ok"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("goals unavailable: %v", err)
	}
	if p, err := plain.CreateDraft(bg, projects.NewProject{Goal: "x", TemplateID: "tpl-generic"}); err != nil || p.GoalID != "" {
		t.Fatalf("no goal: %q %v", p.GoalID, err)
	}
	orch.Wait()
}
