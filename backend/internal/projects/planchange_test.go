package projects_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/projects"
	"aiworkforce/backend/internal/roles"
)

func agentOf(n projects.Node) string {
	if n.AgentID == nil {
		return ""
	}
	return *n.AgentID
}

func hasAudit(e *env, action string) bool {
	for _, a := range e.store.Audit() {
		if a.Action == action {
			return true
		}
	}
	return false
}

func TestHumanReplacesAFailedNodeAndDependentsProceed(t *testing.T) {
	e, id, _ := failedProject(t) // stays broken: the node is replaced, not retried
	failed := nodeByTitle(e.detail(id), failingNode)
	ctx := application.WithActor(e.ctx, "alice")
	ch, err := e.svc.ProposeChange(ctx, id, projects.NewPlanChange{Reason: "otra via", Ops: []projects.PlanChangeOp{
		{Op: projects.OpReplaceTask, NodeID: failed.ID, Tasks: []projects.ProposedTask{
			{Key: "a", Title: "Reunir datos por otra via", AgentID: agentOf(failed)},
			{Key: "b", Title: "Validar los datos reunidos", AgentID: agentOf(failed), DependsOn: []string{"a"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if ch.Status != projects.ChangeApplied || ch.ProposedBy != projects.ProposerHuman || ch.DecidedBy != "alice" || ch.Impact.Replaced != 1 || ch.Impact.Added != 2 {
		t.Fatalf("change: %+v", ch)
	}
	e.approveAll(id, func(d projects.Detail) bool { return d.Project.Status == projects.StatusDone })
	d := e.detail(id)
	old := nodeByTitle(d, failingNode)
	if old.State != projects.StateCancelled || old.Superseded != "replaced" {
		t.Fatalf("replaced node: %+v", old)
	}
	for _, n := range d.Nodes {
		if n.Kind != projects.KindGroup && n.Superseded == "" && n.State != projects.StateDone {
			t.Fatalf("node %q ended %s", n.Title, n.State)
		}
	}
	if nodeByTitle(d, "Validar los datos reunidos").PlanChangeID != ch.ID {
		t.Fatal("added nodes must point to their plan change")
	}
	if d.Project.Failed != 0 {
		t.Fatalf("a replaced node does not count as failed: %d", d.Project.Failed)
	}
	list, _ := e.svc.ListChanges(e.ctx, id)
	if len(list) != 1 || !hasAudit(e, "project.plan_change_applied") || !hasAudit(e, "project.plan_change_proposed") {
		t.Fatalf("changes=%d, audit missing", len(list))
	}
}

func TestPlanChangeValidation(t *testing.T) {
	e, id, _ := failedProject(t)
	d := e.detail(id)
	failed := nodeByTitle(d, failingNode)
	ag := agentOf(failed)
	propose := func(ops ...projects.PlanChangeOp) error {
		_, err := e.svc.ProposeChange(application.WithActor(e.ctx, "alice"), id, projects.NewPlanChange{Ops: ops})
		return err
	}
	add := func(ts ...projects.ProposedTask) projects.PlanChangeOp {
		return projects.PlanChangeOp{Op: projects.OpAddTask, Tasks: ts}
	}
	if err := propose(add(projects.ProposedTask{Key: "x", Title: "X", AgentID: ag, DependsOn: []string{"y"}}, projects.ProposedTask{Key: "y", Title: "Y", AgentID: ag, DependsOn: []string{"x"}})); !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle: %v", err)
	}
	// A done node (the gate/first nodes that ran) cannot be edited; the failed node is fine, a blocked one's dependency is not.
	var doneNode projects.Node
	for _, n := range d.Nodes {
		if n.State == projects.StateDone && n.Kind != projects.KindGroup {
			doneNode = n
		}
	}
	if doneNode.ID != "" {
		if err := propose(projects.PlanChangeOp{Op: projects.OpRemovePending, NodeID: doneNode.ID}); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("editing a finished node: %v", err)
		}
	}
	if err := propose(add(projects.ProposedTask{Key: "x", Title: "X", AgentID: ag, DependsOn: []string{failed.ID}})); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("depending on a failed node: %v", err)
	}
	if err := propose(add(projects.ProposedTask{Key: "x", Title: "X", AgentID: "nobody"})); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown agent: %v", err)
	}
	var many []projects.ProposedTask
	for i := 0; i < 41; i++ {
		many = append(many, projects.ProposedTask{Key: fmt.Sprintf("k%d", i), Title: fmt.Sprintf("T%d", i), AgentID: ag})
	}
	if err := propose(add(many...)); !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "limit_exceeded") {
		t.Fatalf("size limit: %v", err)
	}
	if err := propose(); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("empty change: %v", err)
	}
	if _, err := e.svc.ProposeChange(application.WithActor(e.ctx, "agent:analyst"), id, projects.NewPlanChange{Ops: []projects.PlanChangeOp{add(projects.ProposedTask{Key: "x", Title: "X", AgentID: ag})}}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("an agent cannot change the plan directly: %v", err)
	}
	if list, _ := e.svc.ListChanges(e.ctx, id); len(list) != 0 {
		t.Fatalf("refused changes leave no trace in the plan: %d", len(list))
	}
}

func TestSuggestedTasksBecomeOneBoundedProposalAndNeedAHuman(t *testing.T) {
	rt := newRT()
	rt.suggest = func(title string) []string {
		if title == "Investigar el contexto y los datos" {
			return []string{"Revisar fuentes externas", "revisar  fuentes externas", "Consolidar el informe final", "A", "B", "C", "D", "E"}
		}
		return nil
	}
	e := newEnv(t, rt, nil, nil)
	id := e.draft("tpl-generic", 0)
	e.launch(id, budgetBig)
	var list []projects.PlanChange
	e.waitFor("a proposal", func() bool {
		e.approveGates(id)
		list, _ = e.svc.ListChanges(e.ctx, id)
		return len(list) > 0
	})
	if len(list) != 1 {
		t.Fatalf("one proposal per source node, got %d", len(list))
	}
	ch := list[0]
	if ch.ProposedBy != projects.ProposerAgent || ch.Status != projects.ChangePending || ch.Source != projects.SourceSuggested || ch.ApprovalID == "" {
		t.Fatalf("proposal: %+v", ch)
	}
	tasks := ch.Ops[0].Tasks
	if len(tasks) > 5 {
		t.Fatalf("bounded: %d tasks", len(tasks))
	}
	seen := map[string]bool{}
	for _, tk := range tasks {
		k := strings.ToLower(strings.Join(strings.Fields(tk.Title), " "))
		if seen[k] || tk.Title == "Consolidar el informe final" {
			t.Fatalf("duplicate or already planned task proposed: %q", tk.Title)
		}
		seen[k] = true
	}
	// The project is not blocked by the pending proposal.
	e.approveAll(id, func(d projects.Detail) bool { return d.Project.Status == projects.StatusDone })
	before := len(e.detail(id).Nodes)
	// An agent cannot approve; neither can the system.
	for _, who := range []string{"agent:analyst", "system", "analyst"} {
		if _, err := e.svc.DecideChange(application.WithActor(e.ctx, who), id, ch.ID, "approve", ""); err == nil {
			t.Fatalf("%s must not approve a plan change", who)
		}
	}
	if got, _ := e.svc.ListChanges(e.ctx, id); got[0].Status != projects.ChangePending || len(e.detail(id).Nodes) != before {
		t.Fatal("the plan must not change without a human")
	}
	out, err := e.svc.DecideChange(application.WithActor(e.ctx, "alice"), id, ch.ID, "approve", "ok")
	if err != nil || out.Status != projects.ChangeApplied || out.DecidedBy != "alice" {
		t.Fatalf("human approval: %+v %v", out, err)
	}
	e.approveAll(id, func(d projects.Detail) bool { return d.Project.Status == projects.StatusDone && len(d.Nodes) > before })
	for _, n := range e.detail(id).Nodes {
		if n.Kind != projects.KindGroup && n.Superseded == "" && n.State != projects.StateDone {
			t.Fatalf("node %q ended %s", n.Title, n.State)
		}
	}
}

func TestRejectedProposalLeavesThePlanUntouched(t *testing.T) {
	rt := newRT()
	rt.suggest = func(title string) []string { return []string{"Extra " + title} }
	e := newEnv(t, rt, nil, nil)
	id := e.draft("tpl-generic", 0)
	e.launch(id, budgetBig)
	var list []projects.PlanChange
	e.waitFor("proposals", func() bool {
		e.approveGates(id)
		list, _ = e.svc.ListChanges(e.ctx, id)
		return len(list) >= 3
	})
	// Bounded: never more than 3 open proposals, however many nodes suggest.
	e.approveAll(id, func(d projects.Detail) bool { return d.Project.Status == projects.StatusDone })
	list, _ = e.svc.ListChanges(e.ctx, id)
	open := 0
	for _, c := range list {
		if c.Status == projects.ChangePending {
			open++
		}
	}
	if open > 3 || open == 0 {
		t.Fatalf("open proposals = %d, want 1..3", open)
	}
	n := len(e.detail(id).Nodes)
	out, err := e.svc.DecideChange(application.WithActor(e.ctx, "alice"), id, list[0].ID, "reject", "no")
	if err != nil || out.Status != projects.ChangeRejected || len(e.detail(id).Nodes) != n {
		t.Fatalf("reject: %+v %v", out, err)
	}
	if !hasAudit(e, "project.plan_change_rejected") {
		t.Fatal("rejection must be audited")
	}
}

func TestReplanProposesAndAHumanApproves(t *testing.T) {
	e, id, _ := failedProject(t) // the runtime has no replanner: the deterministic fallback proposes
	failed := nodeByTitle(e.detail(id), failingNode)
	if _, err := e.svc.ProposeReplan(application.WithActor(e.ctx, "agent:analyst"), id, failed.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("agent: %v", err)
	}
	ch, err := e.svc.ProposeReplan(application.WithActor(e.ctx, "alice"), id, failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Status != projects.ChangePending || ch.ProposedBy != projects.ProposerAgent || ch.Source != projects.SourceReplan || ch.Ops[0].Op != projects.OpReplaceTask {
		t.Fatalf("proposal: %+v", ch)
	}
	if _, err := e.svc.ProposeReplan(application.WithActor(e.ctx, "alice"), id, failed.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a second replan of the same node while one waits: %v", err)
	}
	if st := e.detail(id).Project.Status; st != projects.StatusFailed {
		t.Fatalf("a pending proposal changes nothing: %s", st)
	}
	// Decided in the approvals inbox (not through the project endpoint): picked up all the same.
	if _, err := e.appr.Decide(application.WithActor(e.ctx, "alice"), ch.ApprovalID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	e.waitFor("change applied", func() bool {
		l, _ := e.svc.ListChanges(e.ctx, id)
		return len(l) == 1 && l[0].Status == projects.ChangeApplied
	})
	e.approveAll(id, func(d projects.Detail) bool { return d.Project.Status == projects.StatusDone })
}

func TestAppliedChangeSurvivesARestart(t *testing.T) {
	store := memory.New()
	if err := store.Seed(context.Background(), domain.SeedOrg(25), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	pstore := projects.NewMemStore()
	e1, stop1 := processOver(t, newRT(), store, pstore)
	id := e1.draft("tpl-generic", 0)
	e1.launch(id, budgetBig)
	e1.waitFor("plan gate", func() bool { return pendingGate(e1, id) != "" })
	first := e1.detail(id).Nodes
	var ag string
	for _, n := range first {
		if n.AgentID != nil {
			ag = *n.AgentID
		}
	}
	ch, err := e1.svc.ProposeChange(application.WithActor(e1.ctx, "alice"), id, projects.NewPlanChange{Ops: []projects.PlanChangeOp{
		{Op: projects.OpAddTask, Tasks: []projects.ProposedTask{{Key: "n", Title: "Tarea agregada en vuelo", AgentID: ag}}}}})
	if err != nil || ch.Status != projects.ChangeApplied {
		t.Fatalf("apply: %+v %v", ch, err)
	}
	stop1()

	e2, _ := processOver(t, newRT(), store, pstore)
	if n, _ := e2.orch.Recover(context.Background(), nil); n != 1 {
		t.Fatalf("recover = %d", n)
	}
	e2.svc.Recover(context.Background(), nil)
	if nodeByTitle(e2.detail(id), "Tarea agregada en vuelo").PlanChangeID != ch.ID {
		t.Fatal("the applied change is part of the persisted plan")
	}
	e2.approveAll(id, func(d projects.Detail) bool { return d.Project.Status == projects.StatusDone })
	if n := nodeByTitle(e2.detail(id), "Tarea agregada en vuelo"); n.State != projects.StateDone {
		t.Fatalf("the added task must run after the restart: %s", n.State)
	}
}
