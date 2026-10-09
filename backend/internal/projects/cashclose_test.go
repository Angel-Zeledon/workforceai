package projects_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/projects"
	"aiworkforce/backend/internal/roles"
)

// Q3: the "Quarterly cash close" template runs end to end in simulation (fake
// runtime): parallel reconciliations per account, AR aging, 13-week forecast,
// consolidation, memo, alerts and the PROPOSED payment list behind a human gate.

type cashEnv struct {
	svc  *projects.Service
	rt   *fakeRuntime
	appr *application.Approvals
	st   *memory.Store
	ctx  context.Context
	org  string
}

func newCashEnv(t *testing.T, hireTreasury bool) *cashEnv {
	t.Helper()
	rt := newRT()
	rt.onRun = func(application.RunTaskRequest) { time.Sleep(20 * time.Millisecond) }
	cfg := application.DefaultConfig()
	cfg.IdleDelay, cfg.RetryBase, cfg.TaskRetryBackoff = 0, time.Millisecond, time.Millisecond
	cfg.MaxParallelPerOrg = 32
	store := memory.New()
	if err := store.Seed(context.Background(), domainSeedOrg(cfg.BudgetUSD), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: store, Pub: &evLog{}, Log: log}
	appr := application.NewApprovals(cfg, store, rec)
	q := &application.Queries{Store: store, Cfg: cfg}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	orch := application.NewOrchestrator(ctx, cfg, store, rt, memory.NewLocker(), rec, appr, q, log)
	if hireTreasury {
		if _, err := orch.HireFromTemplate(ctx, application.HireInput{TemplateID: "finance_treasury", Locale: "es"}); err != nil {
			t.Fatal(err)
		}
	}
	svc := projects.New(ctx, projects.Config{Store: projects.NewMemStore(), Orch: orch, Core: store, Approvals: appr, Rec: rec, Runtime: rt,
		Catalog: catalog.Default(), OrgID: cfg.OrgID, Poll: 10 * time.Millisecond, Log: log})
	return &cashEnv{svc: svc, rt: rt, appr: appr, st: store, ctx: ctx, org: cfg.OrgID}
}

func (e *cashEnv) runToDone(t *testing.T, params map[string]string, parallel int) (projects.Detail, int) {
	t.Helper()
	r, err := e.svc.CreateDraft(e.ctx, projects.NewProject{TemplateID: "quarterly_cash_close", Params: params, Locale: "es"})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := e.svc.Get(e.ctx, r.ID)
	total := leafCount(d)
	if _, err := e.svc.Launch(e.ctx, r.ID, projects.LaunchBody{ApprovedBudgetUSD: 50, AcknowledgeUnderbudget: true, MaxParallel: parallel}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		// the human reviews each proposed payment list (a gate); nothing is paid by the system
		all, _ := e.st.ListApprovals(e.ctx, e.org, "")
		for _, a := range all {
			if a.Status == domain.ApprovalPending {
				_, _ = e.appr.Decide(e.ctx, a.ID, "approve", "reviewed")
			}
		}
		dd, _ := e.svc.Get(e.ctx, r.ID)
		if dd.Project.Status == "done" {
			return dd, total
		}
		time.Sleep(10 * time.Millisecond)
	}
	dd, _ := e.svc.Get(e.ctx, r.ID)
	t.Fatalf("timeout: %d/%d done, status %s", dd.Project.TasksDone, total, dd.Project.Status)
	return dd, total
}

func TestCashCloseSixAccountsRunsInParallelToCompletion(t *testing.T) {
	e := newCashEnv(t, true)
	dd, total := e.runToDone(t, map[string]string{"accounts": "6"}, 16)
	if total != 29 {
		t.Fatalf("6 accounts must give 29 tasks (18 per-account + aging 2 + forecast 3 + consolidation 2 + memo/alerts 2 + payments 2), got %d", total)
	}
	if dd.Project.TasksDone != dd.Project.TasksTotal || dd.Project.Failed != 0 {
		t.Fatalf("all tasks must be done: %d/%d failed %d", dd.Project.TasksDone, dd.Project.TasksTotal, dd.Project.Failed)
	}
	if e.rt.peak <= 4 {
		t.Fatalf("peak parallelism = %d, want > 4", e.rt.peak)
	}
	agents := map[string]bool{}
	var gate *projects.Node
	for i, n := range dd.Nodes {
		if n.Kind == projects.KindGroup {
			continue
		}
		if n.State != "done" {
			t.Fatalf("node %q is %s", n.Title, n.State)
		}
		if n.AgentID != nil {
			agents[*n.AgentID] = true
		}
		if n.Kind == projects.KindGate {
			gate = &dd.Nodes[i]
		}
	}
	for a := range agents {
		if a != "finance_treasury" && a != "accounting" && a != "assistant" {
			t.Fatalf("unexpected agent %q in the cash close", a)
		}
	}
	if gate == nil || !strings.Contains(strings.ToLower(gate.Title), "pagos") {
		t.Fatalf("the proposed payment list must end in a human gate: %+v", gate)
	}
	// The payment node only PROPOSES: the project has no tool that pays (the fake runtime saw task titles only).
	if !e.rt.hasStarted("Preparar la lista de pagos propuesta (nunca se ejecuta)") {
		t.Fatal("the proposed payment list task did not run")
	}
}

func TestCashCloseFirmVariantScalesWithinLimits(t *testing.T) {
	e := newCashEnv(t, true)
	dd, total := e.runToDone(t, map[string]string{"accounts": "6", "clients": "3"}, 24)
	if total != 3*29+1 {
		t.Fatalf("3 clients x 6 accounts = %d tasks, want %d", total, 3*29+1)
	}
	if dd.Project.TasksDone != dd.Project.TasksTotal || dd.Project.Failed != 0 {
		t.Fatalf("%d/%d done, failed %d", dd.Project.TasksDone, dd.Project.TasksTotal, dd.Project.Failed)
	}
	if e.rt.peak <= 4 || e.rt.peak > 24 {
		t.Fatalf("peak = %d, want 5..24", e.rt.peak)
	}
	// per-client titles keep their client and every node key is unique
	clients := map[string]bool{}
	for _, o := range dd.Objectives {
		for _, c := range []string{"Cliente 1", "Cliente 2", "Cliente 3"} {
			if strings.HasPrefix(o.Title, c+":") {
				clients[c] = true
			}
		}
	}
	if len(clients) != 3 {
		t.Fatalf("objectives per client: %v", clients)
	}
}

func TestCashCloseParamsAreClampedAndLocalized(t *testing.T) {
	e := newCashEnv(t, true)
	r, err := e.svc.CreateDraft(e.ctx, projects.NewProject{TemplateID: "tpl-quarterly-cash-close", Params: map[string]string{"accounts": "999", "clients": "-4"}, Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := e.svc.Get(e.ctx, r.ID)
	if n := leafCount(d); n != 12*3+2+3+2+2+2 {
		t.Fatalf("12 accounts (clamped) x 1 client = %d tasks", n)
	}
	found := false
	for _, n := range d.Nodes {
		if n.Title == "Reconcile account C" {
			found = true
		}
	}
	if !found {
		t.Fatal("English titles with per-account parameters expected")
	}
	if d.Estimate == nil || d.Estimate.Breakdown == nil || d.Estimate.Breakdown.SynthesisCalls < 1 {
		t.Fatalf("estimate breakdown: %+v", d.Estimate)
	}
}

func TestCashCloseNeedsTheTreasuryRoleHired(t *testing.T) {
	e := newCashEnv(t, false)
	r, err := e.svc.CreateDraft(e.ctx, projects.NewProject{TemplateID: "quarterly_cash_close"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Launch(e.ctx, r.ID, projects.LaunchBody{ApprovedBudgetUSD: 50, AcknowledgeUnderbudget: true})
	if !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "unknown_agent") {
		t.Fatalf("an org without the treasury role cannot launch: %v", err)
	}
}
