package application_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/gateway"
	"aiworkforce/backend/internal/sandbox"
)

const codeAgent = "coder"

// codeEnv is a harness with a "coder" agent (tool code granted) and a sandbox whose runner is fake.
type codeEnv struct {
	*harness
	svc    *sandbox.Service
	store  *sandbox.MemoryStore
	runner *sandbox.FakeRunner
	files  *sandbox.MemoryFiles
	spy    *spyRuntime
	reqs   func() []application.RunTaskRequest
}

func codeRuntime(args map[string]any, risk string) *spyRuntime {
	rt := &fakeRuntime{plan: onePlan(codeAgent)}
	rt.runTask = func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		res := okResult("hecho: " + in.Task.Title)
		if len(in.ExternalContent) == 0 { // first turn: ask to run code; after the result: summarize
			res.ToolRequests = []application.ToolRequest{{Tool: "code", Action: "run", Risk: risk, Args: args}}
		} else {
			res.Output.Summary = "resultado recibido"
			res.ToolRequests = nil
		}
		return res, nil
	}
	return &spyRuntime{fakeRuntime: rt}
}

func goodArgs() map[string]any {
	return map[string]any{"language": "python", "code": "print(sum([1,2,3]))", "purpose": "sum", "expected_outputs": []any{"out.csv"}}
}

func newCodeEnv(t *testing.T, args map[string]any, risk string, enabled, granted bool) *codeEnv {
	t.Helper()
	spy := codeRuntime(args, risk)
	h := newHarness(t, spy, nil)
	h.orch.SetGuardPoll(5 * time.Millisecond)
	tools := []string{"calculator"}
	if granted {
		tools = append(tools, "code")
	}
	if err := h.store.CreateAgent(context.Background(), org, domain.Agent{ID: codeAgent, Name: "Coder", Role: "analyst", Title: "Coder", Tools: tools,
		Autonomy: "autonomous", State: domain.StateIdle}); err != nil {
		t.Fatal(err)
	}
	return wireCode(t, h, spy, enabled, nil)
}

func wireCode(t *testing.T, h *harness, spy *spyRuntime, enabled bool, guard application.ExecutionGuard) *codeEnv {
	st, files, runner := sandbox.NewMemoryStore(), sandbox.NewMemoryFiles(), &sandbox.FakeRunner{}
	runner.Fn = func(context.Context, sandbox.RunRequest) (sandbox.RunResponse, error) {
		return sandbox.RunResponse{Status: sandbox.RunOK, Stdout: "6\n", Files: []sandbox.RunFile{{Name: "out.csv", Data: []byte("x\n6\n")}}}, nil
	}
	svc := &sandbox.Service{Store: st, Runner: runner, Files: files, Sink: files, Guard: guard, Ceiling: sandbox.DefaultLimits(), MaxRunsPerDay: 10, Poll: 5 * time.Millisecond}
	if enabled {
		_ = st.PutSettings(context.Background(), org, sandbox.Settings{Enabled: true})
	}
	h.orch.SetCodeRunner(svc)
	return &codeEnv{harness: h, svc: svc, store: st, runner: runner, files: files, spy: spy}
}

// A permissive setup (autonomous agent, the runtime says "low" risk, no rules)
// must STILL produce an approval, and nothing runs before a human approves.
func TestCodeRunAlwaysNeedsApprovalEvenWithPermissiveSetup(t *testing.T) {
	e := newCodeEnv(t, goodArgs(), "low", true, true)
	reqID, _ := e.orch.Submit(context.Background(), "analiza")
	ap := e.awaitApproval()
	if ap.Action != "code.run" || ap.Risk != "high" {
		t.Fatalf("approval must be code.run/high: %+v", ap)
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(e.runner.Calls()); n != 0 {
		t.Fatalf("code ran %d times before approval", n)
	}
	run, err := e.store.GetRunByApproval(context.Background(), org, ap.ID)
	if err != nil || run.Code != goodArgs()["code"] || run.Status != sandbox.StatusAwaiting {
		t.Fatalf("the approval must show the exact code: %+v %v", run, err)
	}
	if _, err := e.appr.Decide(context.Background(), ap.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	e.waitFor("request done", func() bool { return e.request(reqID).Status == domain.RequestDone })
	calls := e.runner.Calls()
	if len(calls) != 1 || calls[0].Code != run.Code {
		t.Fatalf("runner calls %+v", calls)
	}
	// the agent got the result as delimited external content and used it
	seen := e.spy.seen()
	if !strings.Contains(seen, "untrusted_data") || !strings.Contains(seen, "status=succeeded") {
		t.Fatalf("the agent did not receive the delimited result: %s", seen)
	}
	if !e.hasAudit("tool.executed") {
		t.Fatal("tool.executed audit missing")
	}
	done, _ := e.store.GetRunByApproval(context.Background(), org, ap.ID)
	if done.Status != sandbox.StatusSucceeded || len(done.Approvers) == 0 || len(done.Outputs) != 1 || done.Outputs[0].FileRef == "" {
		t.Fatalf("run record %+v", done)
	}
}

func TestCodeRunApprovalIsNeverSkippedByPolicyEngine(t *testing.T) {
	e := newCodeEnv(t, goodArgs(), "low", true, true)
	// An organization with rules that would "allow" ordinary calls (huge approval threshold).
	e.orch.SetPolicy(&application.PolicyService{Store: e.store0(), Cfg: application.DefaultConfig()})
	e.orch.Submit(context.Background(), "analiza")
	ap := e.awaitApproval()
	if ap.Risk != "high" || len(e.runner.Calls()) != 0 {
		t.Fatalf("approval required: %+v", ap)
	}
	e.appr.Decide(context.Background(), ap.ID, "reject", "no")
	e.orch.Wait()
	if len(e.runner.Calls()) != 0 {
		t.Fatal("a rejected run must never execute")
	}
	run, _ := e.store.GetRunByApproval(context.Background(), org, ap.ID)
	if run.Status != sandbox.StatusRejected {
		t.Fatalf("status %s", run.Status)
	}
}

func (e *codeEnv) store0() application.ConfigStore { return e.harness.store }

func TestCodeRunDeniedWithoutToolGrantOrSetting(t *testing.T) {
	for name, c := range map[string]struct{ enabled, granted bool }{"setting off": {false, true}, "tool not granted": {true, false}} {
		t.Run(name, func(t *testing.T) {
			e := newCodeEnv(t, goodArgs(), "high", c.enabled, c.granted)
			reqID, _ := e.orch.Submit(context.Background(), "analiza")
			e.waitFor("request done", func() bool { return e.request(reqID).Status == domain.RequestDone })
			if _, ok := e.pendingApproval(); ok {
				t.Fatal("no approval must be created when the feature is off for this agent/org")
			}
			if len(e.runner.Calls()) != 0 || !e.hasAudit("tool.denied") {
				t.Fatal("must be denied and audited")
			}
		})
	}
}

func TestCodeRunWithoutRunnerInstalledIsDenied(t *testing.T) {
	spy := codeRuntime(goodArgs(), "high")
	h := newHarness(t, spy, nil)
	_ = h.store.CreateAgent(context.Background(), org, domain.Agent{ID: codeAgent, Name: "Coder", Role: "analyst", Title: "Coder", Tools: []string{"code"}, Autonomy: "autonomous", State: domain.StateIdle})
	reqID, _ := h.orch.Submit(context.Background(), "analiza")
	h.waitFor("request done", func() bool { return h.request(reqID).Status == domain.RequestDone })
	if _, ok := h.pendingApproval(); ok || !h.hasAudit("tool.denied") {
		t.Fatal("denied without a sandbox")
	}
}

func TestCodeRunInvalidArgsDenied(t *testing.T) {
	e := newCodeEnv(t, map[string]any{"language": "bash", "code": "ls", "purpose": "x"}, "high", true, true)
	reqID, _ := e.orch.Submit(context.Background(), "analiza")
	e.waitFor("request done", func() bool { return e.request(reqID).Status == domain.RequestDone })
	if _, ok := e.pendingApproval(); ok || len(e.runner.Calls()) != 0 {
		t.Fatal("invalid request must not reach approval")
	}
}

// wired variant: controls guard (kill switch, read-only) installed.
func newGuardedCodeEnv(t *testing.T) (*wired, *codeEnv) {
	spy := codeRuntime(goodArgs(), "high")
	w := wire(t, spy)
	_ = w.store.CreateAgent(context.Background(), org, domain.Agent{ID: codeAgent, Name: "Coder", Role: "analyst", Title: "Coder", Tools: []string{"code"}, Autonomy: "autonomous", State: domain.StateIdle})
	return w, wireCode(t, w.harness, spy, true, gateway.Guard{G: w.gw})
}

func TestReadOnlyModeBlocksCodeRunBeforeAndAfterApproval(t *testing.T) {
	w, e := newGuardedCodeEnv(t)
	// after approval: read-only mode switched on while the approval waits
	reqID, _ := w.orch.Submit(context.Background(), "analiza")
	ap := w.awaitApproval()
	if _, err := w.ctl.SetMode(context.Background(), org, controls.ModeReadOnly, "admin", "audit"); err != nil {
		t.Fatal(err)
	}
	w.appr.Decide(context.Background(), ap.ID, "approve", "")
	w.waitFor("request done", func() bool { return w.request(reqID).Status == domain.RequestDone })
	if len(e.runner.Calls()) != 0 {
		t.Fatal("read-only mode must block an approved run")
	}
	run, _ := e.store.GetRunByApproval(context.Background(), org, ap.ID)
	if run.Status != sandbox.StatusDenied {
		t.Fatalf("status %s", run.Status)
	}
	// before: a new request under read-only mode never reaches an approval
	req2, _ := w.orch.Submit(context.Background(), "otra vez")
	w.waitFor("second request done", func() bool { return w.request(req2).Status == domain.RequestDone })
	if _, ok := w.pendingApproval(); ok {
		t.Fatal("no approval under read-only mode")
	}
}

func TestKillSwitchBlocksApprovedCodeRun(t *testing.T) {
	w, e := newGuardedCodeEnv(t)
	w.orch.Submit(context.Background(), "analiza")
	ap := w.awaitApproval()
	if _, err := w.ctl.KillSwitch(context.Background(), org, controls.LevelFreeze, "drill", "admin"); err != nil {
		t.Fatal(err)
	}
	w.appr.Decide(context.Background(), ap.ID, "approve", "")
	w.waitFor("run closed", func() bool {
		r, err := e.store.GetRunByApproval(context.Background(), org, ap.ID)
		return err == nil && r.Status == sandbox.StatusDenied
	})
	if len(e.runner.Calls()) != 0 {
		t.Fatal("the kill switch must stop an approved run")
	}
}
