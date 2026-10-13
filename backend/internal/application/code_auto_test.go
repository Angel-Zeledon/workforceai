package application_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/sandbox"
)

// Desk mode of the code sandbox (code_auto.go). Mode off is covered by every
// other test of code_flow_test.go (approval always required).

type deskWS struct{}

func (deskWS) Workspace(_ context.Context, _, agentID, _ string) *application.RunWorkspace {
	return &application.RunWorkspace{Desk: "/Agents/" + agentID, Project: "/Projects/P1", Shared: "/Shared"}
}

func deskArgs(files ...any) map[string]any {
	a := goodArgs()
	a["input_files"] = files
	return a
}

func newDeskEnv(t *testing.T, args map[string]any, mode string) *codeEnv {
	t.Helper()
	e := newCodeEnv(t, args, "high", true, true)
	e.orch.SetWorkspaceSource(deskWS{})
	if _, err := e.svc.PutSettings(context.Background(), org, "owner", sandbox.Settings{Enabled: true, AutoRun: mode, AcknowledgeRisk: mode == sandbox.AutoDesk}); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestDeskModeRunsWithoutAnApprovalOnDeskFiles(t *testing.T) {
	e := newDeskEnv(t, deskArgs("f1"), sandbox.AutoDesk)
	e.files.PutAt(org, "f1", "/Agents/"+codeAgent+"/inbox/data.csv", []byte("a\n1\n"))
	reqID, _ := e.orch.Submit(context.Background(), "analiza")
	e.waitFor("request done", func() bool { return e.request(reqID).Status == domain.RequestDone })
	if _, ok := e.pendingApproval(); ok {
		t.Fatal("a desk-confined run must not create an approval in desk mode")
	}
	calls := e.runner.Calls()
	if len(calls) != 1 || calls[0].Code != goodArgs()["code"] || len(calls[0].Files) != 1 {
		t.Fatalf("runner calls %+v", calls)
	}
	seen := e.spy.seen()
	if !strings.Contains(seen, "untrusted_data") || !strings.Contains(seen, "status=succeeded") || !strings.Contains(seen, `"code_auto_run":"desk"`) {
		t.Fatalf("the agent must get the result in the same turn and be told the mode: %s", seen)
	}
	var executed domain.AuditLog
	for _, a := range e.auditEntries("tool.executed") {
		executed = a
	}
	if d, _ := executed.Details.(map[string]any); d["auto_run"] != true {
		t.Fatalf("tool.executed must carry auto_run: %+v", executed.Details)
	}
	if len(e.auditEntries("tool.auto_approved")) != 1 {
		t.Fatal("the skipped approval must be audited")
	}
	runs, _ := e.store.ListRuns(context.Background(), org, 10)
	if len(runs) != 1 || !runs[0].AutoRun || runs[0].Status != sandbox.StatusSucceeded || runs[0].Code != goodArgs()["code"] || runs[0].Stdout == "" {
		t.Fatalf("the code and its output must stay reviewable: %+v", runs)
	}
	if len(runs[0].Outputs) != 1 || runs[0].Outputs[0].FileRef == "" {
		t.Fatalf("outputs must go to the desk: %+v", runs[0].Outputs)
	}
	if _, agent, folder, _, ok := e.files.Output(org, runs[0].Outputs[0].FileRef); !ok || agent != codeAgent || !strings.HasPrefix(folder, "outputs/") {
		t.Fatalf("output must be written to the agent's desk outputs: %s %s", agent, folder)
	}
}

func TestDeskModeForeignInputNeedsApprovalAndSaysWhy(t *testing.T) {
	for name, path := range map[string]string{"other agent": "/Agents/rival/data.csv", "shared": "/Shared/Finance/data.csv", "other project": "/Projects/P2/data.csv"} {
		t.Run(name, func(t *testing.T) {
			e := newDeskEnv(t, deskArgs("f1"), sandbox.AutoDesk)
			e.files.PutAt(org, "f1", path, []byte("a\n1\n"))
			e.orch.Submit(context.Background(), "analiza")
			ap := e.awaitApproval()
			if ap.Action != "code.run" || ap.Risk != "high" {
				t.Fatalf("approval must be code.run/high: %+v", ap)
			}
			if ap.Context["approval_reason"] != sandbox.AutoInputOutsideScope || !strings.Contains(ap.Details, "approval needed because") {
				t.Fatalf("the card must say why: %v / %s", ap.Context, ap.Details)
			}
			time.Sleep(50 * time.Millisecond)
			if len(e.runner.Calls()) != 0 {
				t.Fatal("nothing may run before the approval")
			}
			if _, err := e.appr.Decide(context.Background(), ap.ID, "approve", ""); err != nil {
				t.Fatal(err)
			}
			e.waitFor("run done", func() bool { return len(e.runner.Calls()) == 1 })
			runs, _ := e.store.ListRuns(context.Background(), org, 10)
			if len(runs) != 1 || runs[0].AutoRun || len(runs[0].Approvers) == 0 || runs[0].Approvers[0] == application.AutoApprover {
				t.Fatalf("a human-approved run is not an auto run: %+v", runs)
			}
		})
	}
}

func TestDeskModeOversizedCodeNeedsApproval(t *testing.T) {
	a := deskArgs()
	a["code"] = strings.Repeat("# padding\n", sandbox.DefaultAutoMaxCodeBytes/10+5) + "print(1)"
	e := newDeskEnv(t, a, sandbox.AutoDesk)
	e.orch.Submit(context.Background(), "analiza")
	ap := e.awaitApproval()
	if ap.Context["approval_reason"] != sandbox.AutoCodeTooLarge {
		t.Fatalf("%v", ap.Context)
	}
}

func TestDeskModeOffIsIdenticalToToday(t *testing.T) {
	e := newDeskEnv(t, deskArgs("f1"), sandbox.AutoOff)
	e.files.PutAt(org, "f1", "/Agents/"+codeAgent+"/data.csv", []byte("a\n1\n"))
	e.orch.Submit(context.Background(), "analiza")
	ap := e.awaitApproval()
	if _, has := ap.Context["approval_reason"]; has {
		t.Fatalf("mode off must not add a reason: %v", ap.Context)
	}
	if strings.Contains(e.spy.seen(), "code_auto_run") {
		t.Fatal("mode off must not change the runtime payload")
	}
	if len(e.runner.Calls()) != 0 || len(e.auditEntries("tool.auto_approved")) != 0 {
		t.Fatal("nothing runs without approval when the mode is off")
	}
}

func TestDeskModeRespectsKillSwitchAndReadOnly(t *testing.T) {
	w, e := newGuardedCodeEnv(t)
	w.orch.SetWorkspaceSource(deskWS{})
	e.files.PutAt(org, "f1", "/Agents/"+codeAgent+"/data.csv", []byte("a\n1\n"))
	_, _ = e.svc.PutSettings(context.Background(), org, "owner", sandbox.Settings{Enabled: true, AutoRun: sandbox.AutoDesk, AcknowledgeRisk: true})
	if _, err := w.ctl.SetMode(context.Background(), org, controls.ModeReadOnly, "admin", "audit"); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Prepare(context.Background(), application.CodeRunInput{Org: org, AgentID: codeAgent, TaskID: "t", ApprovalID: "a1",
		Args: deskArgs("f1"), Scope: &application.CodeScope{AgentID: codeAgent, TaskID: "t", Desk: "/Agents/" + codeAgent, PolicyClean: true}})
	if sandbox.CodeOf(err) != "read_only_mode" {
		t.Fatalf("read-only mode must block desk-mode runs: %v", err)
	}
}
