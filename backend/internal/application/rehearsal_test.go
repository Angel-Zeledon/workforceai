package application_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// withRehearsal installs the rehearsal service in the orchestrator AND the gateway (as in production).
func withRehearsal(w *wired) *application.RehearsalService {
	svc := &application.RehearsalService{Store: w.store, Cfg: application.DefaultConfig()} // no Rec: the service's own events are not under test
	w.orch.SetRehearsal(svc)
	w.gw.Rehearsal = svc
	return svc
}

func sendRuntime(agent string, extra ...application.ToolRequest) *spyRuntime {
	rt := &fakeRuntime{plan: onePlan(agent)}
	rt.runTask = func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		res := okResult("hecho")
		res.ToolRequests = append([]application.ToolRequest{{Tool: "email", Action: "send", Risk: "low",
			Args: map[string]any{"to": "laura@acme.com", "subject": "Propuesta", "body": "Adjunto la propuesta de $50,000."}}}, extra...)
		return res, nil
	}
	return &spyRuntime{fakeRuntime: rt}
}

func (w *wired) rehearsed(reqID string) []domain.RehearsalAction {
	as, _ := w.store.ListRehearsalActions(context.Background(), org, domain.RehearsalQuery{RequestID: reqID})
	return as
}

func approvePlanIfAsked(w *wired, reqID string) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := w.gw.PlanGet(org, reqID); err == nil {
			_, _ = w.gw.PlanDecide(context.Background(), org, reqID, "u1", true)
			return
		}
		if st := w.request(reqID).Status; st != domain.RequestPlanning {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Agent switch on: the send is recorded as "would have needed approval", nothing is sent, held or asked.
func TestAgentRehearsalRecordsSendWithoutExecutingIt(t *testing.T) {
	w := wire(t, sendRuntime("sales"))
	svc := withRehearsal(w)
	write := w.conn("Gmail escritura", "mail.send")
	w.grant(write, "sales", "mail.send")
	if _, err := svc.SetAgent(context.Background(), "sales", true, "u1"); err != nil {
		t.Fatal(err)
	}
	reqID, _ := w.orch.Submit(context.Background(), "envía la propuesta")
	approvePlanIfAsked(w, reqID)
	w.waitFor("request done", func() bool { return w.request(reqID).Status == domain.RequestDone })

	if _, ok := w.pendingApproval(); ok {
		t.Fatal("a rehearsal must not ask for approvals")
	}
	w.now.Add(10 * time.Minute)
	w.gw.ProcessDue(context.Background())
	if n := len(w.gm.FakeFor(write.ID).SentMessages()); n != 0 {
		t.Fatalf("a rehearsal sent %d message(s)", n)
	}
	if hs, _ := w.gw.Holds(context.Background(), org, ""); len(hs) != 0 {
		t.Fatalf("a rehearsal scheduled holds: %+v", hs)
	}
	as := w.rehearsed(reqID)
	if len(as) != 1 {
		t.Fatalf("rehearsed actions = %+v", as)
	}
	a := as[0]
	if a.Tool != "email" || a.Action != "send" || a.Scope != application.RehearsalScopeAgent || a.Source != application.RehearsalSourceGateway ||
		a.WouldDecision != domain.WouldApprove || len(a.Recipients) != 1 || a.Recipients[0] != "laura@acme.com" ||
		a.Preview["subject"] != "Propuesta" || a.Verdict != "" || a.Account == "" {
		t.Fatalf("action = %+v", a)
	}
	if !w.hasAudit("tool.rehearsed") || w.hasAudit("tool.executed") {
		t.Fatal("audit must say rehearsed, never executed")
	}
	if !strings.Contains(strings.Join(w.tasks(reqID)["Responder correos"].Output.Evidence, "\n"), "Ensayo") {
		t.Fatal("the task output must say what was rehearsed")
	}
}

// A rehearsal request: simulated (legacy) tools are rehearsed too, with the amount; reads keep running.
func TestRehearsalRequestRecordsPaymentAndStillReads(t *testing.T) {
	rt := &fakeRuntime{plan: onePlan("sales")}
	var sawData bool
	rt.runTask = func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		res := okResult("hecho")
		if len(in.ExternalContent) == 0 {
			res.ToolRequests = []application.ToolRequest{{Tool: "email", Action: "search", Args: map[string]any{"q": ""}, Risk: "low"}}
			return res, nil
		}
		sawData = true
		res.ToolRequests = []application.ToolRequest{payment(5000)}
		return res, nil
	}
	w := wire(t, &spyRuntime{fakeRuntime: rt})
	withRehearsal(w)
	read := w.conn("Gmail lectura", "mail.read")
	w.grant(read, "sales", "mail.read")
	reqID, _ := w.orch.Submit(application.WithRehearsal(context.Background(), true), "paga la factura")
	approvePlanIfAsked(w, reqID)
	w.waitFor("request done", func() bool { return w.request(reqID).Status == domain.RequestDone })
	if !sawData {
		t.Fatal("reads must still run during a rehearsal")
	}
	// The flag is durable: a restart resumes the request as a rehearsal (RunMeta.Rehearsal).
	if m, err := w.store.GetRunMeta(context.Background(), org, reqID); err != nil || !m.Rehearsal {
		t.Fatalf("run meta = %+v %v", m, err)
	}
	if _, ok := w.pendingApproval(); ok {
		t.Fatal("a rehearsal must not ask for approvals")
	}
	as := w.rehearsed(reqID)
	if len(as) != 1 || as[0].Tool != "ledger" || as[0].Source != application.RehearsalSourceSimulated || as[0].Scope != application.RehearsalScopeRequest ||
		as[0].Amount == nil || *as[0].Amount != 5000 {
		t.Fatalf("rehearsed = %+v", as)
	}
	for _, e := range w.auditEntries("tool.executed") {
		if d, _ := e.Details.(map[string]any); d["tool"] == "ledger" {
			t.Fatal("the payment was recorded as executed")
		}
	}
}

// The guarantee end to end: the agent goes into rehearsal while its send waits for approval; the human approves; the
// gateway still does not send (the approval never overrides a rehearsal), and the action is recorded instead.
func TestRehearsalNeverExecutesAWriteEvenIfApproved_EndToEnd(t *testing.T) {
	w := wire(t, sendRuntime("sales"))
	svc := withRehearsal(w)
	write := w.conn("Gmail escritura", "mail.send")
	w.grant(write, "sales", "mail.send")
	reqID, _ := w.orch.Submit(context.Background(), "envía la propuesta")
	approvePlanIfAsked(w, reqID)
	ap := w.awaitApproval()
	if _, err := svc.SetAgent(context.Background(), "sales", true, "u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.appr.Decide(context.Background(), ap.ID, "approve", "ok"); err != nil {
		t.Fatal(err)
	}
	w.waitFor("request done", func() bool { return w.request(reqID).Status == domain.RequestDone })
	w.now.Add(10 * time.Minute)
	w.gw.ProcessDue(context.Background())
	if n := len(w.gm.FakeFor(write.ID).SentMessages()); n != 0 {
		t.Fatalf("an approved send went out during a rehearsal (%d)", n)
	}
	if hs, _ := w.gw.Holds(context.Background(), org, ""); len(hs) != 0 {
		t.Fatalf("holds = %+v", hs)
	}
	if as := w.rehearsed(reqID); len(as) != 1 || as[0].Tool != "email" {
		t.Fatalf("rehearsed = %+v", as)
	}
}

// Verdicts are the human's: they are recorded, validated and summarized per agent (evidence for trust promotions).
func TestRehearsalVerdictsAndAgentSummary(t *testing.T) {
	w := wire(t, sendRuntime("sales", payment(300)))
	svc := withRehearsal(w)
	write := w.conn("Gmail escritura", "mail.send")
	w.grant(write, "sales", "mail.send")
	reqID, _ := w.orch.Submit(application.WithRehearsal(context.Background(), true), "envía y paga")
	approvePlanIfAsked(w, reqID)
	w.waitFor("request done", func() bool { return w.request(reqID).Status == domain.RequestDone })
	rep, err := svc.Report(context.Background(), reqID)
	if err != nil || !rep.Rehearsal || rep.Summary.Total != 2 || rep.Summary.Undecided != 2 {
		t.Fatalf("report = %+v %v", rep, err)
	}
	if _, err := svc.SetVerdict(context.Background(), rep.Actions[0].ID, "maybe", "u1"); err == nil {
		t.Fatal("an unknown verdict must be refused")
	}
	if _, err := svc.SetVerdict(context.Background(), rep.Actions[0].ID, domain.VerdictWouldApprove, "u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetVerdict(context.Background(), rep.Actions[1].ID, domain.VerdictWouldReject, "u1"); err != nil {
		t.Fatal(err)
	}
	ags, _ := svc.Agents(context.Background())
	if len(ags) != 1 || ags[0].AgentID != "sales" || ags[0].Actions != 2 || ags[0].WouldApprove != 1 || ags[0].WouldReject != 1 || ags[0].Enabled {
		t.Fatalf("agents = %+v", ags)
	}
}
