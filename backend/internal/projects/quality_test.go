package projects_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/projects"
	"aiworkforce/backend/internal/roles"
)

// Review implements application.Reviewer on the shared fake runtime.
func (f *fakeRuntime) Review(_ context.Context, in application.ReviewRequest) (application.ReviewResponse, error) {
	f.mu.Lock()
	n := len(f.reviews)
	f.reviews = append(f.reviews, in)
	hook := f.review
	f.mu.Unlock()
	if hook != nil {
		return hook(n, in), nil
	}
	return application.ReviewResponse{Verdict: domain.VerdictPass, Reasons: []string{"ok"}}, nil
}

func hireAuditorInStore(t *testing.T, store *memory.Store) string {
	t.Helper()
	tp, _ := roles.Get("internal_auditor")
	a := roles.Instantiate(tp, "en", "internal_auditor", "Alan Brooks")
	if err := store.CreateAgent(context.Background(), domain.DemoOrgID, a); err != nil {
		t.Fatal(err)
	}
	return a.ID
}

func isAuditTitle(t string) bool {
	return strings.HasPrefix(t, "Auditoría de la fase") || strings.HasPrefix(t, "Audit of phase")
}

func nodesOf(d projects.Detail, kind string) (out []projects.Node) {
	for _, n := range d.Nodes {
		if n.Kind == kind {
			out = append(out, n)
		}
	}
	return
}

func TestPlannerProposesAcceptanceAndHierarchicalProjectsDefaultToLowConfidence(t *testing.T) {
	rt := &hierRT{fakeRuntime: newRT(), accept: true}
	svc, _, _ := newPlannerEnv(t, rt, projects.Limits{})
	ctx := context.Background()
	rec, err := svc.CreateDraft(ctx, projects.NewProject{Goal: "Launch the new product line in three countries"})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := svc.Get(ctx, rec.ID)
	if d.Quality == nil || d.Quality.Review != application.ReviewLowConfidence {
		t.Fatalf("quality = %+v, want low_confidence for a hierarchical project", d.Quality)
	}
	for _, n := range nodesOf(d, projects.KindTask) {
		// blanks and duplicates the planner sent are cleaned
		if len(n.Acceptance) != 2 || n.Acceptance[0] != "Includes a summary" || n.Acceptance[1] != "Cites its sources" {
			t.Fatalf("node %q acceptance = %q", n.Title, n.Acceptance)
		}
	}
}

func TestOtherProjectsKeepReviewOffAndOldRecordsAreValid(t *testing.T) {
	e := newEnv(t, newRT(), nil, nil)
	d := e.detail(e.draft(tFC, budgetBig))
	if d.Quality != nil {
		t.Fatalf("a template project must not enable the review: %+v", d.Quality)
	}
	for _, n := range d.Nodes {
		if len(n.Acceptance) != 0 || n.Review != nil {
			t.Fatalf("node %q carries quality data", n.Title)
		}
	}
	// A record written before Q1 (no acceptance, no quality) still loads and is "off".
	var rec projects.Record
	if err := jsonUnmarshal(`{"id":"p","org_id":"o","nodes":[{"id":"n1","kind":"task","title":"x","depends_on":[]}],"status":"draft"}`, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Quality != nil || len(rec.Nodes[0].Acceptance) != 0 {
		t.Fatalf("old record = %+v", rec)
	}
}

func TestDraftEditorEditsAcceptanceAndQualitySetting(t *testing.T) {
	e := newEnv(t, newRT(), nil, nil)
	id := e.draft(tFC, budgetBig)
	d := e.detail(id)
	task := nodesOf(d, projects.KindTask)[0]
	group := nodesOf(d, projects.KindGroup)[0]
	patch := func(op projects.PlanOp) error {
		_, _, err := e.svc.PatchPlan(e.ctx, id, []projects.PlanOp{op})
		return err
	}
	upd := func(id string, ac []string) projects.PlanOp {
		op := projects.PlanOp{Op: "update", ID: id}
		op.Fields.Acceptance = &ac
		return op
	}
	many := []string{" a ", "A", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	if err := patch(upd(task.ID, many)); err != nil {
		t.Fatal(err)
	}
	got := nodeByID(e.detail(id), task.ID).Acceptance
	if len(got) != projects.MaxAcceptance || got[0] != "a" || got[1] != "b" {
		t.Fatalf("acceptance = %q (trimmed, deduplicated, capped at %d)", got, projects.MaxAcceptance)
	}
	if err := patch(upd(task.ID, []string{})); err != nil || len(nodeByID(e.detail(id), task.ID).Acceptance) != 0 {
		t.Fatalf("clearing: %v", err)
	}
	if err := patch(upd(group.ID, []string{"x"})); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("a group has no criteria: %v", err)
	}
	set := func(v string) error {
		op := projects.PlanOp{Op: "set_quality"}
		op.Fields.Review = &v
		return patch(op)
	}
	if err := set("sometimes"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("invalid mode: %v", err)
	}
	if err := set("always"); err != nil {
		t.Fatal(err)
	}
	if q := e.detail(id).Quality; q == nil || q.Review != "always" {
		t.Fatalf("quality = %+v", q)
	}
}

func nodeByID(d projects.Detail, id string) projects.Node {
	for _, n := range d.Nodes {
		if n.ID == id {
			return n
		}
	}
	return projects.Node{}
}

func TestReviewedProjectReworksAndShowsVerdictOnTheNode(t *testing.T) {
	rt := newRT()
	rt.review = func(n int, in application.ReviewRequest) application.ReviewResponse {
		if n == 0 { // first review: rework; the second one passes
			return application.ReviewResponse{Verdict: domain.VerdictRework, Reasons: []string{"falta el total"}}
		}
		return application.ReviewResponse{Verdict: domain.VerdictPass, Reasons: []string{"ok"}}
	}
	e := newEnv(t, rt, nil, nil)
	id := e.draft(tFC, budgetBig)
	d := e.detail(id)
	var target projects.Node
	for _, n := range nodesOf(d, projects.KindTask) {
		if n.AgentID != nil && n.ApprovalAction == "" {
			target = n
			break
		}
	}
	ac := []string{"incluye el total"}
	op := projects.PlanOp{Op: "update", ID: target.ID}
	op.Fields.Acceptance = &ac
	rv := "always"
	q := projects.PlanOp{Op: "set_quality"}
	q.Fields.Review = &rv
	if _, _, err := e.svc.PatchPlan(e.ctx, id, []projects.PlanOp{op, q}); err != nil {
		t.Fatal(err)
	}
	e.launch(id, budgetBig)
	e.approveAll(id, func(d projects.Detail) bool { return d.Project.Status == "done" })
	d = e.detail(id)
	n := nodeByID(d, target.ID)
	if n.Review == nil || n.Review.Verdict != domain.VerdictPass || n.Review.Reworks != 1 || len(n.Review.Reasons) == 0 {
		t.Fatalf("node review = %+v", n.Review)
	}
	if len(rt.reviews) != 2 {
		t.Fatalf("only the node with criteria is reviewed (twice, once per output): %d reviews", len(rt.reviews))
	}
	runs := 0
	for _, s := range rt.started {
		if s == target.Title {
			runs++
		}
	}
	if runs != 2 {
		t.Fatalf("the reviewed task ran %d times, want 2 (one rework)", runs)
	}
	for _, o := range d.Nodes {
		if o.ID != target.ID && o.Review != nil {
			t.Fatalf("node %q was reviewed without criteria", o.Title)
		}
	}
}

func TestProjectWithoutQualitySettingNeverReviews(t *testing.T) {
	rt := newRT()
	e := newEnv(t, rt, nil, nil)
	id := e.draft(tFC, budgetBig)
	for _, n := range nodesOf(e.detail(id), projects.KindTask) { // criteria alone do nothing: the setting is off
		ac := []string{"x"}
		op := projects.PlanOp{Op: "update", ID: n.ID}
		op.Fields.Acceptance = &ac
		if n.AgentID != nil {
			if _, _, err := e.svc.PatchPlan(e.ctx, id, []projects.PlanOp{op}); err != nil {
				t.Fatal(err)
			}
		}
	}
	e.launch(id, budgetBig)
	e.approveAll(id, func(d projects.Detail) bool { return d.Project.Status == "done" })
	if len(rt.reviews) != 0 {
		t.Fatalf("%d reviews with the setting off", len(rt.reviews))
	}
}

func TestPlannerInsertsOneAuditNodePerPhaseWhenAnAuditorExists(t *testing.T) {
	ctx := context.Background()
	rt := &hierRT{fakeRuntime: newRT()}
	svc, store, _ := newPlannerEnv(t, rt, projects.Limits{})
	aud := hireAuditorInStore(t, store)
	rec, err := svc.CreateDraft(ctx, projects.NewProject{Goal: "Launch the new product line in three countries"})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := svc.Get(ctx, rec.ID)
	if issues, err := svc.Validate(ctx, rec.ID); err != nil || len(issues) != 0 {
		t.Fatalf("the plan with audit nodes must stay a valid DAG: %v %v", issues, err)
	}
	perObjective := map[string][]projects.Node{}
	audits := map[string]projects.Node{}
	for _, n := range nodesOf(d, projects.KindTask) {
		perObjective[n.ObjectiveID] = append(perObjective[n.ObjectiveID], n)
		if n.AgentID != nil && *n.AgentID == aud {
			if _, dup := audits[n.ObjectiveID]; dup {
				t.Fatalf("two audit nodes in phase %s", n.ObjectiveID)
			}
			audits[n.ObjectiveID] = n
		}
	}
	if len(audits) != len(d.Objectives) || len(audits) != 5 {
		t.Fatalf("audits = %d, phases = %d", len(audits), len(d.Objectives))
	}
	for obj, a := range audits {
		if !isAuditTitle(a.Title) || len(a.DependsOn) != len(perObjective[obj])-1 {
			t.Fatalf("audit %q depends on %d, phase has %d tasks", a.Title, len(a.DependsOn), len(perObjective[obj]))
		}
	}
	// the audit is the exit of its phase: the next phase waits for it
	next := 0
	for _, n := range nodesOf(d, projects.KindTask) {
		for _, dep := range n.DependsOn {
			for _, a := range audits {
				if dep == a.ID && n.ObjectiveID != a.ObjectiveID {
					next++
				}
			}
		}
	}
	if next == 0 {
		t.Fatal("no task of a later phase depends on an audit node")
	}
}

func TestAuditNodesAreOptionalAndBounded(t *testing.T) {
	ctx := context.Background()
	// no auditor: none
	svc, _, _ := newPlannerEnv(t, &hierRT{fakeRuntime: newRT()}, projects.Limits{})
	rec, _ := svc.CreateDraft(ctx, projects.NewProject{Goal: "Launch the new product line in three countries"})
	d, _ := svc.Get(ctx, rec.ID)
	for _, n := range nodesOf(d, projects.KindTask) {
		if isAuditTitle(n.Title) {
			t.Fatalf("audit node without an auditor: %q", n.Title)
		}
	}
	// disabled by configuration
	svc, store, _ := newPlannerEnv(t, &hierRT{fakeRuntime: newRT()}, projects.Limits{NoAuditNodes: true})
	hireAuditorInStore(t, store)
	rec, _ = svc.CreateDraft(ctx, projects.NewProject{Goal: "Launch the new product line in three countries"})
	d, _ = svc.Get(ctx, rec.ID)
	for _, n := range nodesOf(d, projects.KindTask) {
		if isAuditTitle(n.Title) {
			t.Fatalf("audit node although QUALITY_AUDIT_NODES=false: %q", n.Title)
		}
	}
	// the per-phase cap includes the audit
	svc, store, _ = newPlannerEnv(t, &hierRT{fakeRuntime: newRT()}, projects.Limits{MaxTasksPerPhase: 6})
	hireAuditorInStore(t, store)
	rec, _ = svc.CreateDraft(ctx, projects.NewProject{Goal: "Launch the new product line in three countries"})
	d, _ = svc.Get(ctx, rec.ID)
	per := map[string]int{}
	audits := 0
	for _, n := range nodesOf(d, projects.KindTask) {
		per[n.ObjectiveID]++
		if isAuditTitle(n.Title) {
			audits++
		}
	}
	for obj, c := range per {
		if c > 6 {
			t.Fatalf("phase %s has %d tasks, cap 6 (audit included)", obj, c)
		}
	}
	if audits != 5 {
		t.Fatalf("audits = %d", audits)
	}
}
