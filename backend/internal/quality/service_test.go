package quality_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/quality"
)

var t0 = time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)

type audited struct{ action, id, actor string }

type env struct {
	svc   *quality.Service
	store *memory.Store
	audit *[]audited
}

func newEnv(t *testing.T) env {
	t.Helper()
	st := memory.New()
	ctx := context.Background()
	agents := []domain.Agent{{ID: "ana", Name: "Ana", Role: "sales"}, {ID: "bob", Name: "Bob", Role: "analyst"}, {ID: "cy", Name: "Cy", Role: "ops"}}
	if err := st.Seed(ctx, domain.Organization{ID: "org-a", Name: "A", Slug: "a"}, agents); err != nil {
		t.Fatal(err)
	}
	log := &[]audited{}
	svc := quality.New(quality.Config{
		Store: quality.NewMemStore(), Signals: quality.StoreSignals{Store: st}, OrgID: "org-a",
		Agents: func(ctx context.Context, org string) ([]domain.Agent, error) { return st.ListAgents(ctx, org) },
		Audit: func(_ context.Context, _, actor, action, id string, _ map[string]any) {
			*log = append(*log, audited{action, id, actor})
		},
		Now: func() time.Time { return t0 },
	})
	return env{svc, st, log}
}

func (e env) task(t *testing.T, id, agent string, st domain.TaskStatus, ago time.Duration, review *domain.Review) {
	t.Helper()
	fin := t0.Add(-ago)
	tk := domain.Task{ID: id, AgentID: agent, Status: st, CreatedAt: fin.Add(-time.Minute), FinishedAt: &fin}
	if review != nil {
		tk.Output = &domain.StructuredOutput{Review: review}
	}
	if err := e.store.CreateTask(context.Background(), "org-a", tk); err != nil {
		t.Fatal(err)
	}
}

func (e env) approval(t *testing.T, id, agent string, st domain.ApprovalStatus, note string, ago time.Duration) {
	t.Helper()
	at := t0.Add(-ago)
	if err := e.store.CreateApproval(context.Background(), "org-a", domain.Approval{ID: id, AgentID: agent, Status: st, Note: note, CreatedAt: at.Add(-time.Hour), ResolvedAt: &at}); err != nil {
		t.Fatal(err)
	}
}

const day = 24 * time.Hour

func TestOverviewRanksAndHidesInsufficient(t *testing.T) {
	e := newEnv(t)
	ctx := application.WithOrg(context.Background(), "org-a")
	for i := 0; i < 12; i++ {
		e.approval(t, fmt.Sprintf("a-ana-%d", i), "ana", domain.ApprovalApproved, "", day)
	}
	for i := 0; i < 12; i++ {
		e.approval(t, fmt.Sprintf("a-bob-%d", i), "bob", domain.ApprovalRejected, "no", day)
	}
	e.approval(t, "a-cy", "cy", domain.ApprovalApproved, "", day) // a single observation

	rep, err := e.svc.Overview(ctx, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Agents) != 3 || rep.Methodology.WindowDays != 30 || !rep.Methodology.Advisory {
		t.Fatalf("report: %+v", rep)
	}
	got := []string{rep.Agents[0].AgentID, rep.Agents[1].AgentID, rep.Agents[2].AgentID}
	if got[0] != "ana" || got[1] != "bob" || got[2] != "cy" {
		t.Fatalf("order = %v", got)
	}
	if *rep.Agents[0].Score != 100 || *rep.Agents[1].Score != 0 {
		t.Fatalf("scores: %d %d", *rep.Agents[0].Score, *rep.Agents[1].Score)
	}
	if cy := rep.Agents[2]; cy.Score != nil || cy.Status != quality.StatusInsufficient || cy.Trend != quality.TrendUnknown {
		t.Fatalf("cy must have no score: %+v", cy)
	}
	for _, c := range rep.Agents[0].Components {
		if c.Key == quality.CompRetries && c.Included {
			t.Fatal("the memory source cannot count retries, so the component must stay out")
		}
	}
}

func TestSystemRejectionsAndWindowCutoff(t *testing.T) {
	e := newEnv(t)
	ctx := application.WithOrg(context.Background(), "org-a")
	for i := 0; i < 6; i++ {
		e.approval(t, fmt.Sprintf("ok-%d", i), "ana", domain.ApprovalApproved, "", 2*day)
	}
	for i := 0; i < 5; i++ { // not the agent's fault: timeouts and restarts
		e.approval(t, fmt.Sprintf("to-%d", i), "ana", domain.ApprovalRejected, "sin respuesta: tiempo de espera agotado", 2*day)
		e.approval(t, fmt.Sprintf("sp-%d", i), "ana", domain.ApprovalRejected, "superseded: server restarted", 2*day)
	}
	for i := 0; i < 5; i++ { // outside the 30 day window, inside the previous one
		e.approval(t, fmt.Sprintf("old-%d", i), "ana", domain.ApprovalRejected, "bad", 31*day)
	}
	for i := 0; i < 5; i++ { // older than two windows: ignored
		e.approval(t, fmt.Sprintf("ancient-%d", i), "ana", domain.ApprovalRejected, "bad", 61*day)
	}
	rep, err := e.svc.ForAgent(ctx, "ana", 30)
	if err != nil {
		t.Fatal(err)
	}
	a := rep.Agents[0]
	if a.Counts.Approved != 6 || a.Counts.Rejected != 0 {
		t.Fatalf("counts: %+v", a.Counts)
	}
	if a.Score != nil { // 6 approvals alone are under the total sample of 10
		t.Fatalf("score with 6 observations: %d", *a.Score)
	}
	if _, err := e.svc.ForAgent(ctx, "ghost", 30); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown agent: %v", err)
	}
}

func TestTrendAndReviewSignals(t *testing.T) {
	e := newEnv(t)
	ctx := application.WithOrg(context.Background(), "org-a")
	pass := &domain.Review{Verdict: domain.VerdictPass}
	reworked := &domain.Review{Verdict: domain.VerdictPass, Reworks: 1}
	for i := 0; i < 5; i++ { // previous window: 5 done + 5 failed -> 50
		e.task(t, fmt.Sprintf("p-done-%d", i), "ana", domain.TaskDone, 40*day, nil)
		e.task(t, fmt.Sprintf("p-fail-%d", i), "ana", domain.TaskFailed, 40*day, nil)
	}
	for i := 0; i < 10; i++ { // current window: all done; half the reviews needed a rework
		r := pass
		if i%2 == 1 {
			r = reworked
		}
		e.task(t, fmt.Sprintf("c-%d", i), "ana", domain.TaskDone, 3*day, r)
	}
	rep, err := e.svc.ForAgent(ctx, "ana", 30)
	if err != nil {
		t.Fatal(err)
	}
	a := rep.Agents[0]
	if a.Counts.Reviewed != 10 || a.Counts.ReviewFirstPass != 5 || a.Counts.TasksDone != 10 {
		t.Fatalf("counts: %+v", a.Counts)
	}
	// tasks 10/10 (0.25) and review 5/10 (0.15): (0.25 + 0.075) / 0.40 = 0.8125
	if a.Score == nil || *a.Score != 81 {
		t.Fatalf("score: %v", a.Score)
	}
	if a.Previous == nil || *a.Previous != 50 || a.Trend != quality.TrendUp || *a.TrendDelta != 31 {
		t.Fatalf("trend: prev %v %s %v", a.Previous, a.Trend, a.TrendDelta)
	}
}

func TestGoldenCasesLifecycleAndRuns(t *testing.T) {
	e := newEnv(t)
	ctx := application.WithOrg(context.Background(), "org-a")
	in := quality.CaseInput{Name: "Invoice total", Input: "Sum the invoice", Checks: []quality.Check{{Kind: "contains", Value: "1,200"}, {Kind: "not_contains", Value: "error"}}}

	for name, bad := range map[string]quality.CaseInput{
		"no name":    {Input: "x", Checks: in.Checks},
		"no input":   {Name: "x", Checks: in.Checks},
		"no checks":  {Name: "x", Input: "x"},
		"bad kind":   {Name: "x", Input: "x", Checks: []quality.Check{{Kind: "regex", Value: "a"}}},
		"empty valu": {Name: "x", Input: "x", Checks: []quality.Check{{Kind: "contains", Value: " "}}},
	} {
		if _, err := e.svc.CreateCase(ctx, "u1", "ana", bad); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := e.svc.CreateCase(ctx, "u1", "ghost", in); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown agent: %v", err)
	}
	c, err := e.svc.CreateCase(ctx, "u1", "ana", in)
	if err != nil || !c.Active || c.CreatedBy != "u1" {
		t.Fatalf("create: %v %+v", err, c)
	}

	// A run with the output is evaluated here; one with a verdict is taken as is.
	out := "Total: 1,200 USD"
	r1, err := e.svc.RecordRun(ctx, "u1", c.ID, quality.RunInput{Output: &out})
	if err != nil || !r1.Passed || r1.ChecksPassed != 2 || r1.ChecksTotal != 2 {
		t.Fatalf("passing run: %v %+v", err, r1)
	}
	bad := "ERROR: nothing found"
	r2, err := e.svc.RecordRun(ctx, "u1", c.ID, quality.RunInput{Output: &bad})
	if err != nil || r2.Passed || r2.ChecksPassed != 0 {
		t.Fatalf("failing run: %v %+v", err, r2)
	}
	no := false
	if r3, err := e.svc.RecordRun(ctx, "u1", c.ID, quality.RunInput{Passed: &no, TaskID: "tk-1", Note: "wrong total"}); err != nil || r3.Passed || r3.TaskID != "tk-1" {
		t.Fatalf("verdict run: %v %+v", err, r3)
	}
	if _, err := e.svc.RecordRun(ctx, "u1", c.ID, quality.RunInput{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("empty run: %v", err)
	}
	runs, err := e.svc.ListRuns(ctx, c.ID, 10)
	if err != nil || len(runs) != 3 {
		t.Fatalf("runs: %v %d", err, len(runs))
	}

	// Golden runs feed the score once there are enough of them.
	rep, _ := e.svc.ForAgent(ctx, "ana", 30)
	if rep.Agents[0].Counts.GoldenRuns != 3 || rep.Agents[0].Counts.GoldenPassed != 1 || rep.Agents[0].GoldenSet != 1 {
		t.Fatalf("golden counts: %+v", rep.Agents[0].Counts)
	}
	if g := component(rep.Agents[0], quality.CompGolden); !g.Included || g.Sample != 3 {
		t.Fatalf("golden component: %+v", g)
	}

	// Update, deactivate (inactive cases refuse runs), delete (runs stay in the history).
	off := false
	upd := in
	upd.Name, upd.Active = "Invoice total v2", &off
	if u, err := e.svc.UpdateCase(ctx, "u2", c.ID, upd); err != nil || u.Name != "Invoice total v2" || u.Active {
		t.Fatalf("update: %v %+v", err, u)
	}
	if _, err := e.svc.RecordRun(ctx, "u1", c.ID, quality.RunInput{Output: &out}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("inactive run: %v", err)
	}
	if err := e.svc.DeleteCase(ctx, "u2", c.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteCase(ctx, "u2", c.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	rep, _ = e.svc.ForAgent(ctx, "ana", 30)
	if rep.Agents[0].Counts.GoldenRuns != 3 || rep.Agents[0].GoldenSet != 0 {
		t.Fatalf("history after delete: %+v", rep.Agents[0].Counts)
	}

	want := []string{"quality.case_created", "quality.run_recorded", "quality.run_recorded", "quality.run_recorded", "quality.case_updated", "quality.case_deleted"}
	if len(*e.audit) != len(want) {
		t.Fatalf("audit: %+v", *e.audit)
	}
	for i, a := range *e.audit {
		if a.action != want[i] || a.id != c.ID {
			t.Fatalf("audit[%d] = %+v", i, a)
		}
	}
}

func TestCaseLimitPerAgent(t *testing.T) {
	e := newEnv(t)
	ctx := application.WithOrg(context.Background(), "org-a")
	in := quality.CaseInput{Name: "n", Input: "i", Checks: []quality.Check{{Kind: "contains", Value: "v"}}}
	for i := 0; i < quality.MaxCasesPerAgent; i++ {
		if _, err := e.svc.CreateCase(ctx, "u", "ana", in); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.CreateCase(ctx, "u", "ana", in); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("limit: %v", err)
	}
	if _, err := e.svc.CreateCase(ctx, "u", "bob", in); err != nil {
		t.Fatalf("the limit is per agent: %v", err)
	}
}

func TestEvaluate(t *testing.T) {
	checks := []quality.Check{{Kind: quality.CheckContains, Value: "Hola"}, {Kind: quality.CheckNotContains, Value: "secret"}}
	for out, want := range map[string]int{"hola mundo": 2, "hola secret": 1, "adios secret": 0, "": 1} {
		if got := quality.Evaluate(checks, out); got != want {
			t.Fatalf("Evaluate(%q) = %d, want %d", out, got, want)
		}
	}
}

func component(a quality.AgentQuality, key string) quality.Component {
	for _, c := range a.Components {
		if c.Key == key {
			return c
		}
	}
	return quality.Component{}
}
