package projects_test

import (
	"context"
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

// Restart recovery of launched projects (A1b, application/durable.go).

// processOver starts one backend process (orchestrator + projects) over stores
// that survive it; stop simulates the process dying.
func processOver(t *testing.T, rt *fakeRuntime, store *memory.Store, pstore projects.Store) (*env, func()) {
	t.Helper()
	cfg := application.DefaultConfig()
	cfg.IdleDelay, cfg.RetryBase = 0, time.Millisecond
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: store, Pub: nopPub{}, Log: log}
	appr := application.NewApprovals(cfg, store, rec)
	q := &application.Queries{Store: store, Cfg: cfg}
	ctx, cancel := context.WithCancel(context.Background())
	orch := application.NewOrchestrator(ctx, cfg, store, rt, memory.NewLocker(), rec, appr, q, log)
	svc := projects.New(ctx, projects.Config{Store: pstore, Orch: orch, Core: store, Approvals: appr, Rec: rec, Runtime: rt,
		Catalog: catalog.Default(), OrgID: cfg.OrgID, Poll: 10 * time.Millisecond, Log: log})
	stop := func() { cancel(); orch.Wait() }
	t.Cleanup(stop)
	return &env{t: t, svc: svc, orch: orch, appr: appr, store: store, rt: rt, ctx: context.Background()}, stop
}

func pendingGate(e *env, id string) string {
	for _, a := range e.detail(id).Approvals {
		if a.Action == "approve_plan" && a.Status == "pending" {
			return a.ID
		}
	}
	return ""
}

func TestProjectResumesAfterRestartOnItsPendingGate(t *testing.T) {
	store := memory.New()
	if err := store.Seed(context.Background(), domain.SeedOrg(25), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	pstore := projects.NewMemStore()
	e1, stop1 := processOver(t, newRT(), store, pstore)
	id := e1.draft("tpl-generic", 0)
	e1.launch(id, budgetBig)
	var gate string
	e1.waitFor("plan gate", func() bool { gate = pendingGate(e1, id); return gate != "" })
	stop1()

	e2, _ := processOver(t, newRT(), store, pstore)
	n, err := e2.orch.Recover(context.Background(), nil)
	if err != nil || n != 1 {
		t.Fatalf("recover = %d, %v", n, err)
	}
	e2.svc.Recover(context.Background(), nil)
	d := e2.detail(id)
	if d.Project.Status == projects.StatusFailed || d.Project.Status == projects.StatusCancelled {
		t.Fatalf("a resumed project is not interrupted: %s", d.Project.Status)
	}
	// The gate approval is the same one (not superseded): the human decides it now.
	e2.waitFor("gate still pending", func() bool { return pendingGate(e2, id) == gate })
	e2.approveAll(id, func(d projects.Detail) bool { return d.Project.Status == projects.StatusDone })
	got, _ := store.GetApproval(context.Background(), domain.DemoOrgID, gate)
	if got.Status != domain.ApprovalApproved {
		t.Fatalf("the original gate approval decides the project: %+v", got)
	}
	for _, n := range e2.detail(id).Nodes {
		if n.Kind != projects.KindGroup && n.State != projects.StateDone {
			t.Fatalf("node %q ended %s", n.Title, n.State)
		}
	}
}

func TestProjectGateRejectedAfterRestartBlocksDependents(t *testing.T) {
	store := memory.New()
	if err := store.Seed(context.Background(), domain.SeedOrg(25), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	pstore := projects.NewMemStore()
	e1, stop1 := processOver(t, newRT(), store, pstore)
	id := e1.draft("tpl-generic", 0)
	e1.launch(id, budgetBig)
	var gate string
	e1.waitFor("plan gate", func() bool { gate = pendingGate(e1, id); return gate != "" })
	stop1()

	rt := newRT()
	e2, _ := processOver(t, rt, store, pstore)
	if n, _ := e2.orch.Recover(context.Background(), nil); n != 1 {
		t.Fatalf("recover = %d", n)
	}
	e2.svc.Recover(context.Background(), nil)
	e2.waitFor("gate still pending", func() bool { return pendingGate(e2, id) == gate })
	if _, err := e2.appr.Decide(e2.ctx, gate, "reject", "no"); err != nil {
		t.Fatal(err)
	}
	e2.waitFor("failed project", func() bool { return e2.detail(id).Project.Status == projects.StatusFailed })
	if rt.hasStarted("Consolidar el informe final") {
		t.Fatal("a task behind a rejected gate ran after the restart")
	}
}

// Without the restart recovery (Recover never ran) a launched project left
// without its process is still reported as interrupted.
func TestProjectWithoutRecoveryIsInterrupted(t *testing.T) {
	store := memory.New()
	if err := store.Seed(context.Background(), domain.SeedOrg(25), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	pstore := projects.NewMemStore()
	e1, stop1 := processOver(t, newRT(), store, pstore)
	id := e1.draft("tpl-generic", 0)
	e1.launch(id, budgetBig)
	e1.waitFor("plan gate", func() bool { return pendingGate(e1, id) != "" })
	stop1()
	e2, _ := processOver(t, newRT(), store, pstore)
	if st := e2.detail(id).Project.Status; st != projects.StatusFailed {
		t.Fatalf("status = %s", st)
	}
}
