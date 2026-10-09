package application_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/roles"
)

// process is one backend process over a shared store: stop() simulates the
// process dying (its runs are cancelled without touching the store).
type process struct {
	*harness
	stop func()
}

func startProcess(t *testing.T, store *memory.Store, pub *capture, rt application.Runtime, mutate func(*application.Config)) *process {
	t.Helper()
	cfg := application.DefaultConfig()
	cfg.IdleDelay, cfg.RetryBase, cfg.MaxRetries = 0, time.Millisecond, 3
	if mutate != nil {
		mutate(&cfg)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: store, Pub: pub, Log: log}
	appr := application.NewApprovals(cfg, store, rec)
	q := &application.Queries{Store: store, Cfg: cfg}
	ctx, cancel := context.WithCancel(context.Background())
	orch := application.NewOrchestrator(ctx, cfg, store, rt, memory.NewLocker(), rec, appr, q, log)
	p := &process{harness: &harness{t, store, pub, orch, appr, q}}
	p.stop = func() { cancel(); orch.Wait() }
	t.Cleanup(p.stop)
	return p
}

func newStore(t *testing.T) *memory.Store {
	t.Helper()
	store := memory.New()
	if err := store.Seed(context.Background(), domain.SeedOrg(25), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	return store
}

// pendingBeforeRestart submits the proposal scenario, waits for its approval
// and kills the process. It returns the store, the request and the approval.
func pendingBeforeRestart(t *testing.T) (*memory.Store, *capture, string, domain.Approval) {
	t.Helper()
	store, pub := newStore(t), &capture{}
	p1 := startProcess(t, store, pub, sendProposalRuntime(proposalPlan()), nil)
	reqID, err := p1.orch.Submit(context.Background(), "enviar propuesta de $50,000")
	if err != nil {
		t.Fatal(err)
	}
	ap := p1.awaitApproval()
	p1.stop()
	return store, pub, reqID, ap
}

func (h *harness) auditCount(action string) int {
	n := 0
	for _, a := range h.store.Audit() {
		if a.Action == action {
			n++
		}
	}
	return n
}

func TestRecoverResumesPendingApprovalAndFinishes(t *testing.T) {
	store, pub, reqID, ap := pendingBeforeRestart(t)
	if cp, err := store.GetCheckpoint(context.Background(), domain.DemoOrgID, ap.TaskID); err != nil || cp.ApprovalID != ap.ID {
		t.Fatalf("checkpoint before restart: %+v %v", cp, err)
	}

	p2 := startProcess(t, store, pub, sendProposalRuntime(proposalPlan()), nil)
	n, err := p2.orch.Recover(context.Background(), nil)
	if err != nil || n != 1 {
		t.Fatalf("recover = %d, %v", n, err)
	}
	p2.waitFor("agent waiting again", func() bool { return p2.agent("sales").State == domain.StateAwaitingApproval })
	if _, err := p2.appr.Decide(context.Background(), ap.ID, "approve", "ok"); err != nil {
		t.Fatal(err)
	}
	p2.orch.Wait()

	r := p2.request(reqID)
	if r.Status != domain.RequestDone || r.ReportID == nil {
		t.Fatalf("request after resume: %+v", r)
	}
	for title, task := range p2.tasks(reqID) {
		if task.Status != domain.TaskDone {
			t.Fatalf("task %q = %s", title, task.Status)
		}
	}
	if c := p2.auditCount("tool.executed"); c != 1 {
		t.Fatalf("tool.executed = %d, want exactly 1", c)
	}
	if pub.count(domain.EvRequestResumed) != 1 {
		t.Fatal("request.resumed not emitted")
	}
	if _, err := store.GetCheckpoint(context.Background(), domain.DemoOrgID, ap.TaskID); err == nil {
		t.Fatal("checkpoint must be dropped when the task ends")
	}
}

// The server lists organizations through memberships, which never include the
// demo organization: once anyone registers, Recover gets a non-empty list
// without it and must still resume the demo organization's requests.
func TestRecoverIncludesDefaultOrgWhenOthersAreListed(t *testing.T) {
	store, pub, reqID, ap := pendingBeforeRestart(t)
	p2 := startProcess(t, store, pub, sendProposalRuntime(proposalPlan()), nil)
	n, err := p2.orch.Recover(context.Background(), []string{"11111111-1111-1111-1111-111111111111"})
	if err != nil || n != 1 {
		t.Fatalf("recover = %d, %v", n, err)
	}
	p2.waitFor("agent waiting again", func() bool { return p2.agent("sales").State == domain.StateAwaitingApproval })
	if _, err := p2.appr.Decide(context.Background(), ap.ID, "approve", "ok"); err != nil {
		t.Fatal(err)
	}
	p2.orch.Wait()
	if r := p2.request(reqID); r.Status != domain.RequestDone {
		t.Fatalf("request after resume: %+v", r)
	}
}

func TestWithDefaultOrg(t *testing.T) {
	if got := application.WithDefaultOrg(nil, "d"); len(got) != 1 || got[0] != "d" {
		t.Fatalf("nil list: %v", got)
	}
	if got := application.WithDefaultOrg([]string{"a", "d"}, "d"); len(got) != 2 {
		t.Fatalf("already listed: %v", got)
	}
	if got := application.WithDefaultOrg([]string{"a"}, "d"); len(got) != 2 || got[1] != "d" {
		t.Fatalf("missing: %v", got)
	}
}

func TestRecoverRejectAfterRestartBlocksTask(t *testing.T) {
	store, pub, reqID, ap := pendingBeforeRestart(t)
	p2 := startProcess(t, store, pub, sendProposalRuntime(proposalPlan()), nil)
	if n, _ := p2.orch.Recover(context.Background(), nil); n != 1 {
		t.Fatalf("recover = %d", n)
	}
	p2.waitFor("agent waiting again", func() bool { return p2.agent("sales").State == domain.StateAwaitingApproval })
	if _, err := p2.appr.Decide(context.Background(), ap.ID, "reject", "no"); err != nil {
		t.Fatal(err)
	}
	p2.orch.Wait()
	ts := p2.tasks(reqID)
	if ts["Preparar propuesta"].Status != domain.TaskBlocked {
		t.Fatalf("rejected task = %s", ts["Preparar propuesta"].Status)
	}
	if p2.auditCount("tool.executed") != 0 {
		t.Fatal("a rejected action must not execute")
	}
}

func TestRecoverDecisionTakenWhileDown(t *testing.T) {
	store, pub, reqID, ap := pendingBeforeRestart(t)
	// A human decides while no process is waiting (e.g. on another instance's API).
	p2 := startProcess(t, store, pub, sendProposalRuntime(proposalPlan()), nil)
	if _, err := p2.appr.Decide(context.Background(), ap.ID, "approve", "ok"); err != nil {
		t.Fatal(err)
	}
	if n, _ := p2.orch.Recover(context.Background(), nil); n != 1 {
		t.Fatalf("recover = %d", n)
	}
	p2.orch.Wait()
	if r := p2.request(reqID); r.Status != domain.RequestDone {
		t.Fatalf("request = %s", r.Status)
	}
	if p2.auditCount("tool.executed") != 1 {
		t.Fatal("approved action must execute once")
	}
}

func TestRecoverKeepsOriginalDeadline(t *testing.T) {
	store, pub, reqID, ap := pendingBeforeRestart(t)
	// The new process' timeout already elapsed since the approval was created.
	p2 := startProcess(t, store, pub, sendProposalRuntime(proposalPlan()), func(c *application.Config) { c.ApprovalTimeout = time.Millisecond })
	if n, _ := p2.orch.Recover(context.Background(), nil); n != 1 {
		t.Fatalf("recover = %d", n)
	}
	p2.orch.Wait()
	got, err := store.GetApproval(context.Background(), domain.DemoOrgID, ap.ID)
	if err != nil || got.Status != domain.ApprovalRejected || !strings.Contains(got.Note, "tiempo") {
		t.Fatalf("expired approval: %+v %v", got, err)
	}
	if p2.tasks(reqID)["Preparar propuesta"].Status != domain.TaskBlocked {
		t.Fatal("timed-out task must be blocked")
	}
}

func TestRecoverWithoutCheckpointSupersedesAndAsksAgain(t *testing.T) {
	store, pub, reqID, ap := pendingBeforeRestart(t)
	if err := store.DeleteCheckpoint(context.Background(), domain.DemoOrgID, ap.TaskID); err != nil {
		t.Fatal(err)
	}
	p2 := startProcess(t, store, pub, sendProposalRuntime(proposalPlan()), nil)
	if n, _ := p2.orch.Recover(context.Background(), nil); n != 1 {
		t.Fatalf("recover = %d", n)
	}
	var fresh domain.Approval
	p2.waitFor("a fresh approval", func() bool {
		var ok bool
		fresh, ok = p2.pendingApproval()
		return ok && fresh.ID != ap.ID && p2.agent(fresh.AgentID).State == domain.StateAwaitingApproval
	})
	old, _ := store.GetApproval(context.Background(), domain.DemoOrgID, ap.ID)
	if old.Status != domain.ApprovalRejected || !strings.Contains(old.Note, "superseded") {
		t.Fatalf("old approval: %+v", old)
	}
	if _, err := p2.appr.Decide(context.Background(), fresh.ID, "approve", "ok"); err != nil {
		t.Fatal(err)
	}
	p2.orch.Wait()
	if r := p2.request(reqID); r.Status != domain.RequestDone {
		t.Fatalf("request = %s", r.Status)
	}
}

func TestConcurrentRecoverRunsOnce(t *testing.T) {
	store, pub, reqID, ap := pendingBeforeRestart(t)
	p2 := startProcess(t, store, pub, sendProposalRuntime(proposalPlan()), nil)
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, _ := p2.orch.Recover(context.Background(), nil)
			mu.Lock()
			total += n
			mu.Unlock()
		}()
	}
	wg.Wait()
	if total != 1 {
		t.Fatalf("resumed %d times, want 1", total)
	}
	p2.waitFor("agent waiting again", func() bool { return p2.agent("sales").State == domain.StateAwaitingApproval })
	if _, err := p2.appr.Decide(context.Background(), ap.ID, "approve", "ok"); err != nil {
		t.Fatal(err)
	}
	p2.orch.Wait()
	if r := p2.request(reqID); r.Status != domain.RequestDone {
		t.Fatalf("request = %s", r.Status)
	}
	if p2.auditCount("tool.executed") != 1 {
		t.Fatal("approved action must execute once")
	}
}

// blockingPlanner never answers the plan until the process stops.
type blockingPlanner struct{ fakeRuntime }

func (b *blockingPlanner) Plan(ctx context.Context, _ application.PlanRequest) (application.PlanResponse, error) {
	<-ctx.Done()
	return application.PlanResponse{}, ctx.Err()
}

func TestRecoverFailsRequestsInterruptedBeforeStart(t *testing.T) {
	store, pub := newStore(t), &capture{}
	p1 := startProcess(t, store, pub, &blockingPlanner{}, nil)
	reqID, err := p1.orch.Submit(context.Background(), "algo")
	if err != nil {
		t.Fatal(err)
	}
	p1.stop()
	if r := p1.request(reqID); r.Status != domain.RequestPlanning {
		t.Fatalf("before restart = %s", r.Status)
	}
	p2 := startProcess(t, store, pub, sendProposalRuntime(proposalPlan()), nil)
	if n, _ := p2.orch.Recover(context.Background(), nil); n != 0 {
		t.Fatalf("resumed %d, want 0", n)
	}
	if r := p2.request(reqID); r.Status != domain.RequestFailed {
		t.Fatalf("after restart = %s", r.Status)
	}
	if p2.auditCount("request.interrupted") != 1 {
		t.Fatal("interruption must be audited")
	}
}

func TestRunMetaIsPersisted(t *testing.T) {
	store, _, reqID, _ := pendingBeforeRestart(t)
	m, err := store.GetRunMeta(context.Background(), domain.DemoOrgID, reqID)
	if err != nil || m.ConversationID == "" {
		t.Fatalf("run meta: %+v %v", m, err)
	}
	m.ReadOnly, m.RemovedTaskIDs = true, []string{"x"}
	if err := store.PutRunMeta(context.Background(), domain.DemoOrgID, m); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetRunMeta(context.Background(), domain.DemoOrgID, reqID); !got.ReadOnly || len(got.RemovedTaskIDs) != 1 {
		t.Fatalf("read_only not kept: %+v", got)
	}
}
