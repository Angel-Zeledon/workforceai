package postgres

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/sandbox"
)

// Integration tests of migration 490 (code runs and the per-org setting); see
// store_rls_test.go for how to run them (TEST_DATABASE_URL).

func newRun(org, id, approval string) sandbox.Run {
	return sandbox.Run{ID: id, OrgID: org, AgentID: "analyst", TaskID: "t1", ApprovalID: approval, Language: "python", Code: "print(1)",
		CodeHash: "h", Hash: "ah", Purpose: "p", ExpectedOutputs: []string{}, Inputs: []sandbox.InputInfo{}, Status: sandbox.StatusAwaiting,
		Limits: sandbox.DefaultLimits(), CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
}

func TestCodeRunStoreLifecycle(t *testing.T) {
	env := testStore(t)
	seedOrg(t, env.Store, "org-a")
	ctx := context.Background()
	cs := &CodeRunStore{S: env.Store}
	r := newRun("org-a", "run_1", "ap1")
	if err := cs.CreateRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := cs.CreateRun(ctx, r); err == nil {
		t.Fatal("a duplicate run id must fail")
	}
	got, err := cs.GetRunByApproval(ctx, "org-a", "ap1")
	if err != nil || got.ID != "run_1" || got.Code != "print(1)" {
		t.Fatalf("by approval: %+v %v", got, err)
	}
	if _, err := cs.GetRun(ctx, "org-a", "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing run: %v", err)
	}
	// An approval id that is not the run's cannot start it.
	if ok, err := cs.BeginRun(ctx, "org-a", "run_1", "other", []string{"u"}, time.Now()); err != nil || ok {
		t.Fatalf("begin with another approval: %v %v", ok, err)
	}
	if ok, err := cs.BeginRun(ctx, "org-a", "run_1", "ap1", []string{"u1"}, time.Now().UTC()); err != nil || !ok {
		t.Fatalf("begin: %v %v", ok, err)
	}
	if n, _ := cs.CountStartedSince(ctx, "org-a", time.Now().Add(-time.Hour)); n != 1 {
		t.Fatalf("started count = %d", n)
	}
	if ok, _ := cs.CloseRun(ctx, "org-a", "run_1", sandbox.StatusRejected, "x", time.Now()); ok {
		t.Fatal("a running run cannot be closed as rejected")
	}
	code := 0
	fin := time.Now().UTC()
	r.Status, r.ExitCode, r.Stdout, r.FinishedAt = sandbox.StatusSucceeded, &code, "ok\n", &fin
	r.Outputs = []sandbox.OutputInfo{{Name: "a.csv", Size: 3, FileRef: "/Agents/analyst/outputs/run_1/a.csv"}}
	if err := cs.FinishRun(ctx, "org-a", r); err != nil {
		t.Fatal(err)
	}
	if err := cs.FinishRun(ctx, "org-a", r); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("finishing twice: %v", err)
	}
	got, _ = cs.GetRun(ctx, "org-a", "run_1")
	if got.Status != sandbox.StatusSucceeded || got.Stdout != "ok\n" || len(got.Outputs) != 1 || got.Approvers[0] != "u1" || got.ExitCode == nil {
		t.Fatalf("finished run: %+v", got)
	}
	// Close an awaiting run once.
	_ = cs.CreateRun(ctx, newRun("org-a", "run_2", "ap2"))
	if ok, err := cs.CloseRun(ctx, "org-a", "run_2", sandbox.StatusRejected, "rejected", time.Now()); err != nil || !ok {
		t.Fatalf("close: %v %v", ok, err)
	}
	if ok, _ := cs.CloseRun(ctx, "org-a", "run_2", sandbox.StatusDenied, "x", time.Now()); ok {
		t.Fatal("a closed run cannot be closed again")
	}
	if ok, _ := cs.BeginRun(ctx, "org-a", "run_2", "ap2", nil, time.Now()); ok {
		t.Fatal("a rejected run cannot start")
	}
	list, err := cs.ListRuns(ctx, "org-a", 10)
	if err != nil || len(list) != 2 || list[0].ID != "run_2" {
		t.Fatalf("list (newest first): %+v %v", list, err)
	}
}

func TestCodeRunBeginRunStartsAtMostOnceUnderConcurrency(t *testing.T) {
	env := testStore(t)
	seedOrg(t, env.Store, "org-a")
	ctx := context.Background()
	cs := &CodeRunStore{S: env.Store}
	if err := cs.CreateRun(ctx, newRun("org-a", "run_c", "apc")); err != nil {
		t.Fatal(err)
	}
	var started, errs atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := cs.BeginRun(ctx, "org-a", "run_c", "apc", []string{"u"}, time.Now().UTC())
			if err != nil {
				errs.Add(1)
			} else if ok {
				started.Add(1)
			}
		}()
	}
	wg.Wait()
	if started.Load() != 1 || errs.Load() != 0 {
		t.Fatalf("started %d times (errors %d), want exactly once", started.Load(), errs.Load())
	}
}

func TestCodeRunsAndSettingsAreIsolatedByOrg(t *testing.T) {
	env := testStore(t)
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	ctx := context.Background()
	cs := &CodeRunStore{S: env.Store}
	if err := cs.CreateRun(ctx, newRun("org-a", "run_a", "apa")); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.GetRun(ctx, "org-b", "run_a"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("org-b read a run of org-a: %v", err)
	}
	if l, _ := cs.ListRuns(ctx, "org-b", 10); len(l) != 0 {
		t.Fatal("org-b lists runs of org-a")
	}
	if ok, _ := cs.BeginRun(ctx, "org-b", "run_a", "apa", nil, time.Now()); ok {
		t.Fatal("org-b started a run of org-a")
	}
	// Raw statements as the app role inside the wrong org: RLS hides and refuses.
	if err := env.exec(ctx, "org-b", `UPDATE code_runs SET status='running'`); err != nil {
		t.Fatal(err)
	}
	if got, _ := cs.GetRun(ctx, "org-a", "run_a"); got.Status != sandbox.StatusAwaiting {
		t.Fatalf("org-b changed a run of org-a: %s", got.Status)
	}
	if err := env.exec(ctx, "org-b", `INSERT INTO code_runs (org_id, id, approval_id, status, created_at, doc) VALUES ('org-a','x','x','awaiting_approval',now(),'{}')`); err == nil {
		t.Fatal("org-b wrote a run of org-a (WITH CHECK)")
	}
	// Settings: off by default, per org.
	if s, err := cs.GetSettings(ctx, "org-a"); err != nil || s.Enabled {
		t.Fatalf("default settings must be disabled: %+v %v", s, err)
	}
	if err := cs.PutSettings(ctx, "org-a", sandbox.Settings{Enabled: true, MaxRunsPerDay: 5, MaxRuntimeSeconds: 30, UpdatedBy: "u"}); err != nil {
		t.Fatal(err)
	}
	if err := cs.PutSettings(ctx, "org-a", sandbox.Settings{Enabled: true, MaxRunsPerDay: 7}); err != nil {
		t.Fatal(err)
	}
	a, _ := cs.GetSettings(ctx, "org-a")
	b, _ := cs.GetSettings(ctx, "org-b")
	if !a.Enabled || a.MaxRunsPerDay != 7 || b.Enabled {
		t.Fatalf("settings a=%+v b=%+v", a, b)
	}
}
