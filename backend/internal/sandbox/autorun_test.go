package sandbox

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Desk mode (autorun.go). The default (mode off) must behave exactly as before.

type auditEntry struct {
	Action string
	D      map[string]any
}

func deskEnv(t *testing.T, mode string) (*env, *[]auditEntry) {
	t.Helper()
	e := newEnv(t, true)
	e.svc.MaxRunsPerDay = 50
	var mu sync.Mutex
	log := &[]auditEntry{}
	e.svc.Audit = func(_ context.Context, _, _, action, _, _ string, d map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		*log = append(*log, auditEntry{action, d})
	}
	if _, err := e.svc.PutSettings(context.Background(), org, "owner", Settings{Enabled: true, AutoRun: mode, AcknowledgeRisk: true}); err != nil {
		t.Fatal(err)
	}
	return e, log
}

func cleanScope(agent, task string) *application.CodeScope {
	return &application.CodeScope{AgentID: agent, TaskID: task, Desk: "/Agents/" + agent, Project: "/Projects/Q1", PolicyClean: true}
}

func (e *env) prepareScoped(t *testing.T, apID, code string, sc *application.CodeScope, files ...string) (application.CodePlan, error) {
	t.Helper()
	return e.svc.Prepare(context.Background(), application.CodeRunInput{Org: org, AgentID: "analyst", TaskID: "t1", ApprovalID: apID,
		Args: args(code, files...), Scope: sc})
}

func (e *env) execAuto(apID, hash string, sc *application.CodeScope) application.CodeOutcome {
	return e.svc.Execute(context.Background(), org, application.CodeExec{ApprovalID: apID, ArgsHash: hash,
		Approvers: []string{application.AutoApprover}, Scope: sc})
}

func find(log []auditEntry, action string) (auditEntry, bool) {
	for _, a := range log {
		if a.Action == action {
			return a, true
		}
	}
	return auditEntry{}, false
}

func TestDeskModeOffNeverAutoRuns(t *testing.T) {
	e := newEnv(t, true) // default settings: AutoRun "" = off
	e.files.PutAt(org, "f1", "/Agents/analyst/data.csv", []byte("a\n1\n"))
	p, err := e.prepareScoped(t, "ap1", "print(1)", cleanScope("analyst", "t1"), "f1")
	if err != nil || p.Auto {
		t.Fatalf("mode off must always need approval: auto=%v err=%v", p.Auto, err)
	}
	if _, has := p.Card["approval_reason"]; has || p.Card["auto_run"] != nil {
		t.Fatalf("mode off must not change the card: %v", p.Card)
	}
	// Even an explicit "off" in the settings.
	_, _ = e.svc.PutSettings(context.Background(), org, "owner", Settings{Enabled: true, AutoRun: "off"})
	if p, _ = e.prepareScoped(t, "ap2", "print(2)", cleanScope("analyst", "t1"), "f1"); p.Auto {
		t.Fatal("explicit off must need approval")
	}
	r, _ := e.store.GetRunByApproval(context.Background(), org, "ap1")
	if r.AutoRun || r.Status != StatusAwaiting {
		t.Fatalf("%+v", r)
	}
	// Execute of a normal run ignores a scope: the human approval path is unchanged.
	if out := e.svc.Execute(context.Background(), org, application.CodeExec{ApprovalID: "ap1", ArgsHash: p.ArgsHash, Approvers: []string{"alice"}, Scope: cleanScope("analyst", "t1")}); out.Status == "denied" && out.DenyReason == CodeApprovalMismatch {
		t.Logf("hash of ap2 used for ap1: mismatch expected") // different code on purpose
	}
}

func TestDeskModeAutoRunsDeskInputsAndAuditsIt(t *testing.T) {
	e, log := deskEnv(t, AutoDesk)
	e.files.PutAt(org, "f1", "/Agents/analyst/inbox/data.csv", []byte("a\n1\n"))
	sc := cleanScope("analyst", "t1")
	p, err := e.prepareScoped(t, "auto1", "print(sum([1,2,3]))", sc, "f1")
	if err != nil || !p.Auto {
		t.Fatalf("desk inputs must auto-run: auto=%v err=%v card=%v", p.Auto, err, p.Card)
	}
	if len(e.runner.Calls()) != 0 {
		t.Fatal("Prepare must not run")
	}
	out := e.execAuto("auto1", p.ArgsHash, sc)
	if out.Status != StatusSucceeded {
		t.Fatalf("outcome %+v", out)
	}
	run, _ := e.store.GetRunByApproval(context.Background(), org, "auto1")
	if !run.AutoRun || run.Status != StatusSucceeded || len(run.Approvers) != 1 || run.Approvers[0] != application.AutoApprover {
		t.Fatalf("run %+v", run)
	}
	if run.Code != "print(sum([1,2,3]))" || run.CodeHash != CodeHash(run.Code) {
		t.Fatal("the code must stay stored to be reviewed afterwards")
	}
	for _, act := range []string{"code.run.requested", "code.run.started", "code.run.finished"} {
		a, ok := find(*log, act)
		if !ok || a.D["auto_run"] != true || a.D["code_hash"] == nil && act != "code.run.requested" {
			t.Fatalf("%s audit missing auto_run/code_hash: %+v", act, a)
		}
	}
	if a, _ := find(*log, "code.run.started"); a.D["code_hash"] != CodeHash(run.Code) {
		t.Fatalf("started must carry the full code hash: %v", a.D)
	}
	// outputs go to the agent's desk only (the sink is keyed by the agent)
	if len(e.runner.Calls()) != 1 {
		t.Fatalf("calls %d", len(e.runner.Calls()))
	}
}

func TestDeskModeProjectAndAttachmentInputs(t *testing.T) {
	e, _ := deskEnv(t, AutoDesk)
	e.files.PutAt(org, "p1", "/Projects/Q1/inputs/tb.csv", []byte("x\n"))
	e.files.PutAt(org, "s1", "/Shared/Chat/2026-10-13/mine.xlsx", []byte("x\n"))
	e.files.PutAt(org, "s2", "/Shared/Finance/other.xlsx", []byte("x\n"))
	sc := cleanScope("analyst", "t1")
	sc.Attached = []application.CodeAttachment{{Path: "/Shared/Chat/2026-10-13/mine.xlsx", FileID: "s1"}}
	if p, err := e.prepareScoped(t, "a1", "print(1)", sc, "p1"); err != nil || !p.Auto {
		t.Fatalf("project folder input: %v %v", p.Card, err)
	}
	if p, err := e.prepareScoped(t, "a2", "print(2)", sc, "s1"); err != nil || !p.Auto {
		t.Fatalf("user attachment by id: %v %v", p.Card, err)
	}
	if p, err := e.prepareScoped(t, "a3", "print(3)", sc, "/Shared/Chat/2026-10-13/mine.xlsx"); err != nil || !p.Auto {
		t.Fatalf("user attachment by path: %v %v", p.Card, err)
	}
	// a /Shared file that is NOT an attachment of this chat needs a human
	p, err := e.prepareScoped(t, "a4", "print(4)", sc, "s2")
	if err != nil || p.Auto || p.Card["approval_reason"] != AutoInputOutsideScope {
		t.Fatalf("shared non-attachment: auto=%v %v %v", p.Auto, p.Card, err)
	}
}

func TestDeskModeFallsBackToApprovalWithAReason(t *testing.T) {
	big := strings.Repeat("# x\n", DefaultAutoMaxCodeBytes/4+10) + "print(1)"
	bigInput := make([]byte, DefaultAutoMaxInputBytes+1)
	cases := []struct {
		name   string
		code   string
		file   string // id registered with the path below
		path   string
		data   []byte
		scope  func() *application.CodeScope
		reason string
	}{
		{"other agent desk", "print(1)", "f", "/Agents/someone_else/data.csv", []byte("x"), func() *application.CodeScope { return cleanScope("analyst", "t1") }, AutoInputOutsideScope},
		{"prefix lookalike", "print(1)", "f", "/Agents/analyst2/data.csv", []byte("x"), func() *application.CodeScope { return cleanScope("analyst", "t1") }, AutoInputOutsideScope},
		{"other project", "print(1)", "f", "/Projects/Q2/data.csv", []byte("x"), func() *application.CodeScope { return cleanScope("analyst", "t1") }, AutoInputOutsideScope},
		{"shared", "print(1)", "f", "/Shared/data.csv", []byte("x"), func() *application.CodeScope { return cleanScope("analyst", "t1") }, AutoInputOutsideScope},
		{"oversized code", big, "f", "/Agents/analyst/data.csv", []byte("x"), func() *application.CodeScope { return cleanScope("analyst", "t1") }, AutoCodeTooLarge},
		{"oversized input", "print(1)", "f", "/Agents/analyst/big.csv", bigInput, func() *application.CodeScope { return cleanScope("analyst", "t1") }, AutoInputsTooLarge},
		{"no scope", "print(1)", "f", "/Agents/analyst/data.csv", []byte("x"), func() *application.CodeScope { return nil }, AutoNoScope},
		{"policy wants a person", "print(1)", "f", "/Agents/analyst/data.csv", []byte("x"), func() *application.CodeScope {
			s := cleanScope("analyst", "t1")
			s.PolicyClean = false
			return s
		}, AutoPolicy},
		{"scope of another agent", "print(1)", "f", "/Agents/analyst/data.csv", []byte("x"), func() *application.CodeScope { return cleanScope("intruder", "t1") }, AutoScopeMismatch},
		{"scope of another task", "print(1)", "f", "/Agents/analyst/data.csv", []byte("x"), func() *application.CodeScope { return cleanScope("analyst", "t2") }, AutoScopeMismatch},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, _ := deskEnv(t, AutoDesk)
			e.files.PutAt(org, c.file, c.path, c.data)
			p, err := e.prepareScoped(t, "ap", c.code, c.scope(), c.file)
			if err != nil {
				t.Fatalf("must fall back to an approval, not fail: %v", err)
			}
			if p.Auto || p.Card["approval_reason"] != c.reason {
				t.Fatalf("want approval for %s, got auto=%v card=%v", c.reason, p.Auto, p.Card)
			}
			if txt, _ := p.Card["approval_reason_text"].(string); txt == "" || !strings.Contains(p.Details, "approval needed because") {
				t.Fatalf("the reason must be shown: %v / %s", p.Card, p.Details)
			}
			r, _ := e.store.GetRunByApproval(context.Background(), org, "ap")
			if r.AutoRun || r.Status != StatusAwaiting || r.AutoFallback != c.reason {
				t.Fatalf("run %+v", r)
			}
			if len(e.runner.Calls()) != 0 {
				t.Fatal("nothing may run before the approval")
			}
			// the human path still works for it
			if c.reason == AutoCodeTooLarge || c.reason == AutoInputsTooLarge {
				out := e.svc.Execute(context.Background(), org, application.CodeExec{ApprovalID: "ap", ArgsHash: p.ArgsHash, Approvers: []string{"alice"}})
				if out.Status != StatusSucceeded {
					t.Fatalf("a human approval must still run it: %+v", out)
				}
			}
		})
	}
}

func TestDeskModePerTaskLimit(t *testing.T) {
	e, _ := deskEnv(t, AutoDesk)
	e.files.PutAt(org, "f", "/Agents/analyst/d.csv", []byte("x"))
	sc := cleanScope("analyst", "t1")
	for i := 0; i < DefaultAutoRunsPerTask; i++ {
		id := "auto" + string(rune('a'+i))
		p, err := e.prepareScoped(t, id, "print("+string(rune('0'+i))+")", sc, "f")
		if err != nil || !p.Auto {
			t.Fatalf("run %d must auto-run: %v %v", i, p.Card, err)
		}
		if out := e.execAuto(id, p.ArgsHash, sc); out.Status != StatusSucceeded {
			t.Fatalf("run %d: %+v", i, out)
		}
	}
	p, err := e.prepareScoped(t, "over", "print('over')", sc, "f")
	if err != nil || p.Auto || p.Card["approval_reason"] != AutoTaskRunLimit {
		t.Fatalf("the %dth run of a task needs a person: auto=%v %v %v", DefaultAutoRunsPerTask+1, p.Auto, p.Card, err)
	}
	// another task has its own budget
	if p, _ = e.svc.Prepare(context.Background(), application.CodeRunInput{Org: org, AgentID: "analyst", TaskID: "t2", ApprovalID: "other",
		Args: args("print('other')", "f"), Scope: cleanScope("analyst", "t2")}); !p.Auto {
		t.Fatalf("another task: %v", p.Card)
	}
	if out := e.execAuto("other", p.ArgsHash, cleanScope("analyst", "t2")); out.Status != StatusSucceeded {
		t.Fatalf("%+v", out)
	}
	// a configured lower limit
	_, _ = e.svc.PutSettings(context.Background(), org, "owner", Settings{Enabled: true, AutoRun: AutoDesk, AutoMaxRunsPerTask: 1})
	if p, _ = e.svc.Prepare(context.Background(), application.CodeRunInput{Org: org, AgentID: "analyst", TaskID: "t2", ApprovalID: "o2",
		Args: args("print('o2')", "f"), Scope: cleanScope("analyst", "t2")}); p.Auto {
		t.Fatal("configured per-task limit ignored")
	}
}

func TestDeskModeKillSwitchReadOnlyDailyLimitStillBlock(t *testing.T) {
	e, _ := deskEnv(t, AutoDesk)
	e.files.PutAt(org, "f", "/Agents/analyst/d.csv", []byte("x"))
	sc := cleanScope("analyst", "t1")
	e.guard.kill.Store(true)
	if _, err := e.prepareScoped(t, "k", "print(1)", sc, "f"); err == nil {
		t.Fatal("kill switch must block")
	}
	e.guard.kill.Store(false)
	e.guard.readOnly.Store(true)
	if _, err := e.prepareScoped(t, "r", "print(1)", sc, "f"); CodeOf(err) != "read_only_mode" {
		t.Fatalf("read-only mode must block: %v", err)
	}
	e.guard.readOnly.Store(false)

	// the controls change between Prepare and Execute: the run is denied
	p, err := e.prepareScoped(t, "late", "print(2)", sc, "f")
	if err != nil || !p.Auto {
		t.Fatal(err)
	}
	e.guard.kill.Store(true)
	if out := e.execAuto("late", p.ArgsHash, sc); out.Status != "denied" || len(e.runner.Calls()) != 0 {
		t.Fatalf("kill switch before execution: %+v", out)
	}
	e.guard.kill.Store(false)

	// daily limit
	e.svc.MaxRunsPerDay = 1
	p, _ = e.prepareScoped(t, "d1", "print(3)", sc, "f")
	if out := e.execAuto("d1", p.ArgsHash, sc); out.Status != StatusSucceeded {
		t.Fatalf("%+v", out)
	}
	if _, err := e.prepareScoped(t, "d2", "print(4)", sc, "f"); CodeOf(err) != CodeDailyLimit {
		t.Fatalf("daily limit must hold in desk mode: %v", err)
	}
}

func TestDeskModeExecuteRechecksEverything(t *testing.T) {
	e, _ := deskEnv(t, AutoDesk)
	e.files.PutAt(org, "f", "/Agents/analyst/d.csv", []byte("x"))
	sc := cleanScope("analyst", "t1")
	mk := func(id, code string) application.CodePlan {
		p, err := e.prepareScoped(t, id, code, sc, "f")
		if err != nil || !p.Auto {
			t.Fatalf("%v %v", p.Card, err)
		}
		return p
	}
	// no scope at execution time
	p := mk("x1", "print(1)")
	if out := e.execAuto("x1", p.ArgsHash, nil); out.Status != "denied" || !strings.HasPrefix(out.DenyReason, CodeAutoNotAllowed) {
		t.Fatalf("%+v", out)
	}
	// the file changed after the run was prepared (the hash binds the content)
	p = mk("x2", "print(2)")
	e.files.PutAt(org, "f", "/Agents/analyst/d.csv", []byte("CHANGED"))
	if out := e.execAuto("x2", p.ArgsHash, sc); out.Status != "denied" || out.DenyReason != CodeInputsChanged {
		t.Fatalf("%+v", out)
	}
	// the file moved out of the desk after Prepare
	e.files.PutAt(org, "f", "/Agents/analyst/d.csv", []byte("x"))
	p = mk("x3", "print(3)")
	e.files.PutAt(org, "f", "/Shared/d.csv", []byte("x"))
	if out := e.execAuto("x3", p.ArgsHash, sc); out.Status != "denied" || !strings.Contains(out.DenyReason, AutoInputOutsideScope) {
		t.Fatalf("a file that left the desk must not run: %+v", out)
	}
	e.files.PutAt(org, "f", "/Agents/analyst/d.csv", []byte("x"))
	// the organization turned desk mode off meanwhile
	p = mk("x4", "print(4)")
	_, _ = e.svc.PutSettings(context.Background(), org, "owner", Settings{Enabled: true, AutoRun: AutoOff})
	if out := e.execAuto("x4", p.ArgsHash, sc); out.Status != "denied" || out.DenyReason != CodeAutoNotAllowed {
		t.Fatalf("%+v", out)
	}
	_, _ = e.svc.PutSettings(context.Background(), org, "owner", Settings{Enabled: true, AutoRun: AutoDesk, AcknowledgeRisk: true})
	// a run cannot be started twice
	p = mk("x5", "print(5)")
	if out := e.execAuto("x5", p.ArgsHash, sc); out.Status != StatusSucceeded {
		t.Fatalf("%+v", out)
	}
	if out := e.execAuto("x5", p.ArgsHash, sc); out.Status != "denied" || out.DenyReason != "already_executed" {
		t.Fatalf("second start: %+v", out)
	}
	// wrong hash = different code/inputs = approval mismatch; modified code is a NEW plan with another hash
	p = mk("x6", "print(6)")
	if out := e.execAuto("x6", "deadbeef", sc); out.DenyReason != CodeApprovalMismatch {
		t.Fatalf("%+v", out)
	}
	p2 := mk("x7", "print(6) # edited")
	if p.ArgsHash == p2.ArgsHash {
		t.Fatal("modified code must have a different hash")
	}
}

func TestDeskModeSettingsNeedAcknowledgement(t *testing.T) {
	e := newEnv(t, true)
	ctx := context.Background()
	var log []auditEntry
	e.svc.Audit = func(_ context.Context, _, _, action, _, _ string, d map[string]any) {
		log = append(log, auditEntry{action, d})
	}
	if _, err := e.svc.PutSettings(ctx, org, "owner", Settings{Enabled: true, AutoRun: AutoDesk}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("desk mode without acknowledge_risk must be refused: %v", err)
	}
	if st, _ := e.svc.Settings(ctx, org); st.AutoRun == AutoDesk {
		t.Fatal("refused PUT must not change anything")
	}
	if _, err := e.svc.PutSettings(ctx, org, "owner", Settings{Enabled: true, AutoRun: "full"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown mode: %v", err)
	}
	if _, err := e.svc.PutSettings(ctx, org, "owner", Settings{Enabled: true, AutoRun: AutoDesk, AcknowledgeRisk: true, AutoMaxCodeBytes: MaxCodeBytes}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("caps above the desk ceilings: %v", err)
	}
	st, err := e.svc.PutSettings(ctx, org, "owner", Settings{Enabled: true, AutoRun: AutoDesk, AcknowledgeRisk: true})
	if err != nil || st.AutoRun != AutoDesk || st.AcknowledgeRisk {
		t.Fatalf("%+v %v", st, err)
	}
	stored, _ := e.svc.Settings(ctx, org)
	if stored.AcknowledgeRisk {
		t.Fatal("the acknowledgement is request-only and must not be stored")
	}
	a, ok := find(log, "code.settings.updated")
	if !ok || a.D["auto_run"] != AutoDesk || a.D["risk_acknowledged"] != true || a.D["previous_auto_run"] == AutoDesk {
		t.Fatalf("audit %+v", log)
	}
	// already on: saving other fields does not need a new acknowledgement; turning it off never does
	if _, err := e.svc.PutSettings(ctx, org, "owner", Settings{Enabled: true, AutoRun: AutoDesk, MaxRunsPerDay: 5}); err != nil {
		t.Fatal(err)
	}
	if st, err = e.svc.PutSettings(ctx, org, "owner", Settings{Enabled: true}); err != nil || st.AutoRun != AutoOff {
		t.Fatalf("a PUT without auto_run turns it OFF (safe direction): %+v %v", st, err)
	}
	if e.svc.AutoRunMode(ctx, org) != AutoOff {
		t.Fatal("mode")
	}
}

func TestDeskModeRequiresCodeExecutionEnabled(t *testing.T) {
	e := newEnv(t, false)
	_ = e.store.PutSettings(context.Background(), org, Settings{Enabled: false, AutoRun: AutoDesk})
	if e.svc.AutoRunMode(context.Background(), org) != AutoOff {
		t.Fatal("desk mode without code execution enabled is off")
	}
	if _, err := e.prepareScoped(t, "x", "print(1)", cleanScope("analyst", "t1")); CodeOf(err) != CodeDisabled {
		t.Fatalf("%v", err)
	}
}

func TestDeskModeTenantIsolation(t *testing.T) {
	e, _ := deskEnv(t, AutoDesk)
	e.files.PutAt(org, "f", "/Agents/analyst/d.csv", []byte("x"))
	// another organization: desk mode is not on for it, and org1's file does not exist for it
	_ = e.store.PutSettings(context.Background(), "org2", Settings{Enabled: true})
	if e.svc.AutoRunMode(context.Background(), "org2") != AutoOff {
		t.Fatal("desk mode is per organization")
	}
	p, err := e.svc.Prepare(context.Background(), application.CodeRunInput{Org: "org2", AgentID: "analyst", TaskID: "t1", ApprovalID: "z",
		Args: args("print(1)"), Scope: cleanScope("analyst", "t1")})
	if err != nil || p.Auto {
		t.Fatalf("org2 is not in desk mode: %v %v", p.Auto, err)
	}
	if _, err := e.svc.Prepare(context.Background(), application.CodeRunInput{Org: "org2", AgentID: "analyst", TaskID: "t1", ApprovalID: "z2",
		Args: args("print(1)", "f"), Scope: cleanScope("analyst", "t1")}); CodeOf(err) != CodeInvalid {
		t.Fatalf("org2 must not see org1's file: %v", err)
	}
	// the per-task counter is per organization
	_ = e.store.PutSettings(context.Background(), "org2", Settings{Enabled: true, AutoRun: AutoDesk})
	e.files.PutAt("org2", "f", "/Agents/analyst/d.csv", []byte("x"))
	for i := 0; i < DefaultAutoRunsPerTask; i++ {
		_ = e.store.CreateRun(context.Background(), Run{ID: "r" + string(rune('a'+i)), OrgID: org, TaskID: "t1", AutoRun: true, StartedAt: ptrTime(time.Now())})
	}
	p, err = e.svc.Prepare(context.Background(), application.CodeRunInput{Org: "org2", AgentID: "analyst", TaskID: "t1", ApprovalID: "z3",
		Args: args("print(1)", "f"), Scope: cleanScope("analyst", "t1")})
	if err != nil || !p.Auto {
		t.Fatalf("org1's runs must not count for org2: %v %v", p.Card, err)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
