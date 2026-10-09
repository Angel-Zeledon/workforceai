package projects_test

import (
	"errors"
	"sync/atomic"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/projects"
)

const failingNode = "Investigar el contexto y los datos"

// failedProject launches the generic template with its first node failing.
func failedProject(t *testing.T) (*env, string, *atomic.Bool) {
	t.Helper()
	var broken atomic.Bool
	broken.Store(true)
	rt := newRT()
	rt.failFn = func(title string) error {
		if title == failingNode && broken.Load() {
			return errors.New("boom")
		}
		return nil
	}
	e := newEnv(t, rt, nil, func(c *application.Config) { c.MaxRetries, c.TaskMaxAttempts = 1, 1 })
	id := e.draft("tpl-generic", 0)
	e.launch(id, budgetBig)
	e.waitFor("failed project", func() bool { return e.detail(id).Project.Status == projects.StatusFailed })
	if n := nodeByTitle(e.detail(id), failingNode); n.State != projects.StateFailed {
		t.Fatalf("node = %s", n.State)
	}
	return e, id, &broken
}

func TestRetryNodeRevivesAFailedProject(t *testing.T) {
	e, id, broken := failedProject(t)
	broken.Store(false)
	n := nodeByTitle(e.detail(id), failingNode)
	res, err := e.svc.RetryNode(application.WithActor(e.ctx, "alice"), id, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Requeued) < 2 || res.Project.Status == projects.StatusFailed {
		t.Fatalf("requeued=%v status=%s: the node and what was blocked behind it must be back in the queue", res.Requeued, res.Project.Status)
	}
	e.approveAll(id, func(d projects.Detail) bool { return d.Project.Status == projects.StatusDone })
	d := e.detail(id)
	for _, nd := range d.Nodes {
		if nd.Kind != projects.KindGroup && nd.State != projects.StateDone {
			t.Fatalf("node %q ended %s", nd.Title, nd.State)
		}
	}
	var audited bool
	for _, a := range e.store.Audit() {
		if a.Action == "project.node_retried" && a.Actor == "alice" && a.EntityID == id {
			audited = true
		}
	}
	if !audited {
		t.Fatal("project.node_retried must be audited with the human actor")
	}
}

func TestSkipNodeLetsDependentsProceed(t *testing.T) {
	e, id, _ := failedProject(t) // stays broken: the human skips the node instead
	n := nodeByTitle(e.detail(id), failingNode)
	if _, err := e.svc.SkipNode(e.ctx, id, n.ID, ""); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("a skip needs a reason: %v", err)
	}
	if _, err := e.svc.SkipNode(application.WithActor(e.ctx, "alice"), id, n.ID, "no hay datos"); err != nil {
		t.Fatal(err)
	}
	e.approveAll(id, func(d projects.Detail) bool { return d.Project.Status == projects.StatusDone })
	got := nodeByTitle(e.detail(id), failingNode)
	if !got.Skipped || got.SkipReason != "no hay datos" || got.State != projects.StateDone {
		t.Fatalf("skipped node: %+v", got)
	}
	var audited bool
	for _, a := range e.store.Audit() {
		if a.Action == "project.node_skipped" && a.Actor == "alice" {
			audited = true
		}
	}
	if !audited {
		t.Fatal("project.node_skipped must be audited")
	}
}

func TestNodeRecoveryRespectsProjectState(t *testing.T) {
	e, id, _ := failedProject(t)
	n := nodeByTitle(e.detail(id), failingNode)
	if _, err := e.svc.RetryNode(application.WithActor(e.ctx, "agent:analyst"), id, n.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("an agent cannot recover a node: %v", err)
	}
	if _, err := e.svc.RetryNode(e.ctx, id, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown node: %v", err)
	}
	// A project that was never launched has nothing to recover.
	draft := e.draft("tpl-generic", 0)
	if _, err := e.svc.RetryNode(e.ctx, draft, n.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("draft: %v", err)
	}
}
