package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/quality"
)

// Integration test of migration 789 and of the score queries (see store_rls_test.go: TEST_DATABASE_URL).

func qualitySvc(env *testEnv, now time.Time) *quality.Service {
	qs := &QualityStore{S: env.Store}
	return quality.New(quality.Config{Store: qs, Signals: qs, OrgID: "org-a", Now: func() time.Time { return now },
		Agents: func(ctx context.Context, org string) ([]domain.Agent, error) { return env.Store.ListAgents(ctx, org) }})
}

func TestQualitySignalsAndGoldenCasesPostgres(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	now := time.Now().UTC().Truncate(time.Second)
	svc := qualitySvc(env, now)
	ctxA, ctxB := application.WithOrg(ctx, "org-a"), application.WithOrg(ctx, "org-b")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	const agent = "sales"
	ago := func(d time.Duration) *time.Time { x := now.Add(-d); return &x }
	const day = 24 * time.Hour

	// Approvals: 8 approved, 2 rejected by a human, 3 system rejections (ignored), 2 pending (ignored),
	// and 4 rejected 40 days ago (previous window).
	n := 0
	approval := func(st domain.ApprovalStatus, note string, resolved *time.Time) {
		n++
		must(env.Store.CreateApproval(ctx, "org-a", domain.Approval{ID: fmt.Sprintf("ap-%d", n), TaskID: "t", AgentID: agent, Action: "send", Title: "t", Risk: "low",
			Status: domain.ApprovalPending, CreatedAt: now.Add(-50 * day)}))
		if resolved != nil {
			must(env.Store.UpdateApproval(ctx, "org-a", domain.Approval{ID: fmt.Sprintf("ap-%d", n), Status: st, Note: note, ResolvedAt: resolved}))
		}
	}
	for i := 0; i < 8; i++ {
		approval(domain.ApprovalApproved, "", ago(2*day))
	}
	for i := 0; i < 2; i++ {
		approval(domain.ApprovalRejected, "not like this", ago(2*day))
	}
	approval(domain.ApprovalRejected, "sin respuesta: tiempo de espera agotado", ago(2*day))
	approval(domain.ApprovalRejected, "Superseded: server restarted", ago(2*day))
	approval(domain.ApprovalRejected, "superseded: server restarted", ago(2*day))
	approval(domain.ApprovalPending, "", nil)
	approval(domain.ApprovalPending, "", nil)
	for i := 0; i < 4; i++ {
		approval(domain.ApprovalRejected, "old", ago(40*day))
	}

	// Tasks: 6 done (3 reviewed: 2 first pass, 1 reworked), 2 failed (1 reviewed fail), and 1 running (ignored).
	tn := 0
	task := func(st domain.TaskStatus, fin *time.Time, rv *domain.Review) {
		tn++
		tk := domain.Task{ID: fmt.Sprintf("tk-%d", tn), RequestID: "rq", Title: "t", AgentID: agent, Status: st, CreatedAt: now.Add(-50 * day), FinishedAt: fin, Depth: 1}
		if rv != nil {
			tk.Output = &domain.StructuredOutput{Summary: "s", Review: rv}
		}
		must(env.Store.CreateTask(ctx, "org-a", tk))
	}
	for i := 0; i < 3; i++ {
		task(domain.TaskDone, ago(3*day), nil)
	}
	task(domain.TaskDone, ago(3*day), &domain.Review{Verdict: domain.VerdictPass})
	task(domain.TaskDone, ago(3*day), &domain.Review{Verdict: domain.VerdictPass})
	task(domain.TaskDone, ago(3*day), &domain.Review{Verdict: domain.VerdictPass, Reworks: 1})
	task(domain.TaskFailed, ago(3*day), &domain.Review{Verdict: domain.VerdictFail})
	task(domain.TaskFailed, ago(3*day), nil)
	task(domain.TaskRunning, nil, nil)
	// Retries: 4 in the window, 1 before it.
	for i := 0; i < 5; i++ {
		at := now.Add(-time.Duration(i+1) * day)
		if i == 4 {
			at = now.Add(-45 * day)
		}
		must(env.Store.SaveEvent(ctx, "org-a", domain.Event{ID: fmt.Sprintf("ev-%d", i), Type: domain.EvTaskRetrying, AgentID: agent, TS: at}))
	}
	// Another tenant's data must not leak in.
	must(env.Store.CreateApproval(ctx, "org-b", domain.Approval{ID: "ap-b", TaskID: "t", AgentID: agent, Action: "send", Title: "t", Risk: "low", Status: domain.ApprovalPending, CreatedAt: now}))
	must(env.Store.UpdateApproval(ctx, "org-b", domain.Approval{ID: "ap-b", Status: domain.ApprovalRejected, ResolvedAt: ago(day)}))

	rep, err := svc.ForAgent(ctxA, agent, 30)
	if err != nil {
		t.Fatal(err)
	}
	a := rep.Agents[0]
	want := quality.Counts{Approved: 8, Rejected: 2, TasksDone: 6, TasksFailed: 2, Reviewed: 4, ReviewFirstPass: 2, Retries: 4, RetriesTracked: true}
	if a.Counts != want {
		t.Fatalf("counts = %+v\nwant     %+v", a.Counts, want)
	}
	if a.Score == nil || a.Previous != nil || a.Trend != quality.TrendUnknown {
		t.Fatalf("score %v previous %v trend %s (the previous window has too little data)", a.Score, a.Previous, a.Trend)
	}
	// Window cut-off: 40 days ago is outside 30 days but inside a 60 day window.
	rep60, err := svc.ForAgent(ctxA, agent, 60)
	if err != nil {
		t.Fatal(err)
	}
	if got := rep60.Agents[0].Counts.Rejected; got != 6 {
		t.Fatalf("60 day window rejected = %d", got)
	}

	// Golden cases: CRUD, runs and the counts that feed the score.
	c, err := svc.CreateCase(ctxA, "boss", agent, quality.CaseInput{Name: "case", Input: "do it", Checks: []quality.Check{{Kind: "contains", Value: "ok"}}})
	must(err)
	if _, err := svc.CreateCase(ctxA, "boss", "ghost", quality.CaseInput{Name: "case", Input: "do it", Checks: c.Checks}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown agent: %v", err)
	}
	pass, fail := "all ok", "nope"
	_, err = svc.RecordRun(ctxA, "boss", c.ID, quality.RunInput{Output: &pass})
	must(err)
	_, err = svc.RecordRun(ctxA, "boss", c.ID, quality.RunInput{Output: &fail, TaskID: "tk-1"})
	must(err)
	_, err = svc.RecordRun(ctxA, "boss", c.ID, quality.RunInput{Output: &pass})
	must(err)
	runs, err := svc.ListRuns(ctxA, c.ID, 10)
	if err != nil || len(runs) != 3 || runs[0].CreatedAt.Before(runs[2].CreatedAt) {
		t.Fatalf("runs: %v %+v", err, runs)
	}
	rep, _ = svc.ForAgent(ctxA, agent, 30)
	if g := rep.Agents[0].Counts; g.GoldenRuns != 3 || g.GoldenPassed != 2 || rep.Agents[0].GoldenSet != 1 {
		t.Fatalf("golden: %+v", g)
	}

	// RLS: org-b sees none of it, and cannot touch it.
	if _, err := svc.ListCases(application.WithOrg(ctx, "org-b"), agent); err != nil {
		t.Fatal(err)
	}
	cases, _ := svc.ListCases(ctxB, agent)
	if len(cases) != 0 {
		t.Fatalf("org-b sees %d cases", len(cases))
	}
	qs := &QualityStore{S: env.Store}
	if _, err := qs.GetCase(ctx, "org-b", c.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant get: %v", err)
	}
	if err := qs.DeleteCase(ctx, "org-b", c.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant delete: %v", err)
	}
	if err := qs.AddRun(ctx, "org-b", quality.Run{ID: "x", CaseID: c.ID, AgentID: agent, CreatedAt: now}); err != nil {
		t.Fatal(err) // allowed: it lands in org-b's own space and never touches org-a
	}
	if g, _ := qs.GoldenCounts(ctx, "org-a", now.Add(-day), now.Add(time.Second)); g[agent].Runs != 3 {
		t.Fatalf("org-a golden runs = %+v", g)
	}

	// Update and delete; the runs stay.
	off := false
	u, err := svc.UpdateCase(ctxA, "boss", c.ID, quality.CaseInput{Name: "renamed", Input: "do it", Checks: c.Checks, Active: &off})
	if err != nil || u.Name != "renamed" || u.Active {
		t.Fatalf("update: %v %+v", err, u)
	}
	must(svc.DeleteCase(ctxA, "boss", c.ID))
	if g, _ := qs.GoldenCounts(ctx, "org-a", now.Add(-day), now.Add(time.Second)); g[agent].Runs != 3 {
		t.Fatalf("runs after delete = %+v", g)
	}
}
