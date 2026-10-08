package application_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/connections/gmail"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/gateway"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/sanitize"
	"aiworkforce/backend/internal/vault"
)

// Restart recovery, second delivery (A1b): human gates before the start, Tool
// Gateway approvals from their exact point, the execution ledger and the chat
// echo of a resumed request.

// durableWorld is what survives a restart: the core store and the stores of
// connections (with their holds), controls and the vault, as Postgres would.
type durableWorld struct {
	store *memory.Store
	pub   *capture
	conns connections.Store
	ctl   controls.Store
	vault *vault.MemRepo
	gm    *gmail.Provider
}

func newWorld(t *testing.T) *durableWorld {
	t.Helper()
	gm, _ := gmail.New(gmail.Config{})
	return &durableWorld{store: newStore(t), pub: &capture{}, conns: connections.NewMemStore(), ctl: controls.NewMemStore(), vault: vault.NewMemRepo(), gm: gm}
}

// wiredProcess is one backend process with the Tool Gateway over a durableWorld.
type wiredProcess struct {
	*process
	conns *connections.Service
	gw    *gateway.Gateway
	now   *fakeNow
}

func (w *durableWorld) start(t *testing.T, rt application.Runtime, mutate func(*application.Config)) *wiredProcess {
	t.Helper()
	p := startProcess(t, w.store, w.pub, rt, mutate)
	p.orch.SetGuardPoll(5 * time.Millisecond)
	now := &fakeNow{t: time.Now()}
	kw, _ := vault.NewEnvKeyWrapper(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)))
	sus := sanitize.NewSuspects()
	cs, err := connections.NewService(connections.Config{Store: w.conns, Vault: vault.New(w.vault, kw), Suspects: sus,
		Providers: map[string]connections.Provider{"google_gmail": w.gm}, Now: now.Now})
	if err != nil {
		t.Fatal(err)
	}
	ctl := controls.New(w.ctl, nil, nil)
	gw := gateway.New(cs, ctl, sus, nil)
	gw.Now = now.Now
	gw.Executions = w.store // the durable at-most-once ledger (Postgres in production)
	p.orch.SetConnections(gateway.Guard{G: gw}, gw, gw)
	return &wiredProcess{process: p, conns: cs, gw: gw, now: now}
}

func (w *wiredProcess) writeConn(t *testing.T, agent string) connections.Connection {
	t.Helper()
	c, err := w.conns.Create(context.Background(), org, connections.CreateInput{Provider: "google_gmail", Label: "Gmail escritura",
		Capabilities: []string{"mail.send"}, Mode: connections.ModeSimulated, Actor: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.conns.PutGrant(context.Background(), org, c.ID, agent, connections.GrantInput{Capabilities: []string{"mail.send"}}, "admin"); err != nil {
		t.Fatal(err)
	}
	return c
}

func sendMailRuntime() *fakeRuntime {
	return &fakeRuntime{plan: onePlan("sales"), runTask: func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		res := okResult("hecho")
		res.ToolRequests = []application.ToolRequest{{Tool: "email", Action: "send", Risk: "low",
			Args: map[string]any{"to": "laura@acme.com", "subject": "Propuesta", "body": "Adjunto."}}}
		return res, nil
	}}
}

func (w *wiredProcess) holds(t *testing.T) []connections.Hold {
	t.Helper()
	hs, err := w.gw.Holds(context.Background(), org, "")
	if err != nil {
		t.Fatal(err)
	}
	return hs
}

func (h *harness) approvalsOf(taskID string) []domain.Approval {
	all, _ := h.store.ListApprovals(context.Background(), domain.DemoOrgID, "")
	var out []domain.Approval
	for _, a := range all {
		if a.TaskID == taskID {
			out = append(out, a)
		}
	}
	return out
}

// ---- plan review ----

func TestRecoverResumesPlanReviewAndContinues(t *testing.T) {
	w := newWorld(t)
	p1 := w.start(t, sendMailRuntime(), nil)
	p1.writeConn(t, "sales")
	reqID, err := p1.orch.Submit(context.Background(), "envía la propuesta")
	if err != nil {
		t.Fatal(err)
	}
	p1.waitFor("plan review", func() bool { _, err := p1.gw.PlanGet(org, reqID); return err == nil })
	p1.stop()
	meta, err := w.store.GetRunMeta(context.Background(), org, reqID)
	if err != nil || meta.Gate == nil || meta.Gate.Kind != application.GatePlanReview || meta.Gate.StartedAt.IsZero() {
		t.Fatalf("the plan review must be in the run meta: %+v %v", meta, err)
	}

	p2 := w.start(t, sendMailRuntime(), nil)
	if n, err := p2.orch.Recover(context.Background(), nil); err != nil || n != 1 {
		t.Fatalf("recover = %d, %v", n, err)
	}
	p2.waitFor("plan review asked again", func() bool { _, err := p2.gw.PlanGet(org, reqID); return err == nil })
	if got := p2.request(reqID).Status; got != domain.RequestPlanning {
		t.Fatalf("nothing runs before the review: %s", got)
	}
	if _, err := p2.gw.PlanDecide(context.Background(), org, reqID, "u1", true); err != nil {
		t.Fatal(err)
	}
	ap := p2.awaitApproval()
	if _, err := p2.appr.Decide(context.Background(), ap.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	p2.waitFor("request done", func() bool { return p2.request(reqID).Status == domain.RequestDone })
	if hs := p2.holds(t); len(hs) != 1 {
		t.Fatalf("one held send, got %d", len(hs))
	}
	if m, _ := w.store.GetRunMeta(context.Background(), org, reqID); m.Gate != nil {
		t.Fatalf("the gate is cleared once decided: %+v", m.Gate)
	}
}

func TestRecoverPlanReviewKeepsOriginalDeadline(t *testing.T) {
	w := newWorld(t)
	p1 := w.start(t, sendMailRuntime(), nil)
	p1.writeConn(t, "sales")
	reqID, _ := p1.orch.Submit(context.Background(), "envía la propuesta")
	p1.waitFor("plan review", func() bool { _, err := p1.gw.PlanGet(org, reqID); return err == nil })
	p1.stop()

	p2 := w.start(t, sendMailRuntime(), func(c *application.Config) { c.ApprovalTimeout = time.Millisecond })
	if n, _ := p2.orch.Recover(context.Background(), nil); n != 1 {
		t.Fatalf("recover = %d", n)
	}
	p2.orch.Wait()
	if got := p2.request(reqID).Status; got != domain.RequestFailed {
		t.Fatalf("an expired review fails the request, got %s", got)
	}
	for title, tk := range p2.tasks(reqID) {
		if tk.Status != domain.TaskBlocked {
			t.Fatalf("task %q must never start: %s", title, tk.Status)
		}
	}
}

// ---- cost confirmation ----

func TestRecoverResumesCostConfirmationWithTheSameEstimate(t *testing.T) {
	store, pub := newStore(t), &capture{}
	rt := costRuntime(chainPlan("analyst", "sales"), 0.01, 0.80) // max total 1.61 > threshold 1.0
	p1 := startProcess(t, store, pub, rt, nil)
	reqID, _ := p1.orch.Submit(context.Background(), "x")
	p1.waitFor("awaiting confirmation", func() bool { return p1.request(reqID).Status == domain.RequestAwaitingConfirmation })
	p1.stop()
	meta, _ := store.GetRunMeta(context.Background(), org, reqID)
	if meta.Gate == nil || meta.Gate.Kind != application.GateCostConfirmation || meta.Gate.Estimate == nil {
		t.Fatalf("the cost confirmation must be in the run meta: %+v", meta.Gate)
	}

	// After the restart the runtime has no estimator (fallback): the human is
	// still asked, with the estimate they saw.
	p2 := startProcess(t, store, pub, &fakeRuntime{plan: chainPlan("analyst", "sales")}, nil)
	if n, _ := p2.orch.Recover(context.Background(), nil); n != 1 {
		t.Fatalf("recover = %d", n)
	}
	p2.waitFor("waiting for the confirmation again", func() bool {
		return p2.orch.Budget().Confirm(context.Background(), reqID, true, 0) == nil
	})
	p2.orch.Wait()
	if got := p2.request(reqID).Status; got != domain.RequestDone {
		t.Fatalf("status = %s", got)
	}
	ests := pub.of(domain.EvCostEstimated)
	last := ests[len(ests)-1].Payload.(map[string]any)["estimate"].(domain.CostEstimate)
	if !last.RequiresConfirmation || last.Total != meta.Gate.Estimate.Total {
		t.Fatalf("the same estimate is asked again: %+v", last)
	}
}

func TestRecoverCostConfirmationCancelBlocksTasks(t *testing.T) {
	store, pub := newStore(t), &capture{}
	rt := costRuntime(chainPlan("analyst"), 0.01, 1.20)
	p1 := startProcess(t, store, pub, rt, nil)
	reqID, _ := p1.orch.Submit(context.Background(), "x")
	p1.waitFor("awaiting confirmation", func() bool { return p1.request(reqID).Status == domain.RequestAwaitingConfirmation })
	p1.stop()
	p2 := startProcess(t, store, pub, rt, nil)
	if n, _ := p2.orch.Recover(context.Background(), nil); n != 1 {
		t.Fatalf("recover = %d", n)
	}
	p2.waitFor("cancel", func() bool { return p2.orch.Budget().Confirm(context.Background(), reqID, false, 0) == nil })
	p2.orch.Wait()
	if got := p2.request(reqID).Status; got != domain.RequestFailed {
		t.Fatalf("status = %s", got)
	}
	for _, tk := range p2.tasks(reqID) {
		if tk.Status != domain.TaskBlocked {
			t.Fatalf("a cancelled request runs nothing: %s", tk.Status)
		}
	}
}

// ---- Tool Gateway approvals ----

// gatewayPendingBeforeRestart reaches the approval of a Gmail send and stops.
func gatewayPendingBeforeRestart(t *testing.T) (*durableWorld, string, domain.Approval) {
	t.Helper()
	w := newWorld(t)
	p1 := w.start(t, sendMailRuntime(), nil)
	p1.writeConn(t, "sales")
	reqID, _ := p1.orch.Submit(context.Background(), "envía la propuesta")
	p1.waitFor("plan review", func() bool { _, err := p1.gw.PlanGet(org, reqID); return err == nil })
	_, _ = p1.gw.PlanDecide(context.Background(), org, reqID, "u1", true)
	ap := p1.awaitApproval()
	p1.waitFor("agent waiting", func() bool { return p1.agent("sales").State == domain.StateAwaitingApproval })
	p1.stop()
	return w, reqID, ap
}

func TestRecoverResumesGatewayApprovalFromItsExactPoint(t *testing.T) {
	w, reqID, ap := gatewayPendingBeforeRestart(t)
	cp, err := w.store.GetCheckpoint(context.Background(), org, ap.TaskID)
	if err != nil || cp.Gateway == nil || cp.ApprovalID != ap.ID || cp.Gateway.ArgsHash == "" {
		t.Fatalf("gateway checkpoint: %+v %v", cp, err)
	}

	p2 := w.start(t, sendMailRuntime(), nil)
	if n, _ := p2.orch.Recover(context.Background(), nil); n != 1 {
		t.Fatalf("recover = %d", n)
	}
	p2.waitFor("outbox item restored", func() bool {
		items, _ := p2.gw.Outbox(context.Background(), org, gateway.OutboxPendingApproval)
		return len(items) == 1
	})
	// The same approval is still the one pending (not superseded, no new one)
	// and the outbox shows it again with the same item.
	if aps := p2.approvalsOf(ap.TaskID); len(aps) != 1 || aps[0].ID != ap.ID || aps[0].Status != domain.ApprovalPending {
		t.Fatalf("approvals of the task: %+v", aps)
	}
	items, _ := p2.gw.Outbox(context.Background(), org, gateway.OutboxPendingApproval)
	if len(items) != 1 || items[0].ID != ap.Context["outbox_id"] || items[0].ApprovalID != ap.ID {
		t.Fatalf("restored outbox item: %+v", items)
	}
	if _, err := p2.appr.Decide(context.Background(), ap.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	p2.waitFor("request done", func() bool { return p2.request(reqID).Status == domain.RequestDone })
	if hs := p2.holds(t); len(hs) != 1 || hs[0].ApprovalID != ap.ID {
		t.Fatalf("exactly one held send for the approval: %+v", hs)
	}
	if c := p2.auditCount("tool.executed"); c != 1 {
		t.Fatalf("tool.executed = %d", c)
	}
}

func TestRecoverGatewayApprovalApprovedFromTheRestoredOutbox(t *testing.T) {
	w, reqID, ap := gatewayPendingBeforeRestart(t)
	p2 := w.start(t, sendMailRuntime(), nil)
	if n, _ := p2.orch.Recover(context.Background(), nil); n != 1 {
		t.Fatalf("recover = %d", n)
	}
	outboxID, _ := ap.Context["outbox_id"].(string)
	p2.waitFor("outbox item restored", func() bool {
		items, _ := p2.gw.Outbox(context.Background(), org, gateway.OutboxPendingApproval)
		return len(items) == 1
	})
	body := "Texto corregido tras el reinicio"
	if _, err := p2.gw.OutboxEdit(context.Background(), org, outboxID, gateway.OutboxPatch{Body: &body}); err != nil {
		t.Fatal(err)
	}
	if _, err := p2.gw.OutboxApprove(context.Background(), org, outboxID, 2, "admin", func(id string) error {
		_, e := p2.appr.Decide(context.Background(), id, "approve", "")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	p2.waitFor("request done", func() bool { return p2.request(reqID).Status == domain.RequestDone })
	hs := p2.holds(t)
	if len(hs) != 1 || hs[0].Payload["body"] != body {
		t.Fatalf("one hold with the human's edit: %+v", hs)
	}
}

func TestRecoverGatewayRejectedWhileDownSendsNothing(t *testing.T) {
	w, reqID, ap := gatewayPendingBeforeRestart(t)
	p2 := w.start(t, sendMailRuntime(), nil)
	if _, err := p2.appr.Decide(context.Background(), ap.ID, "reject", "no"); err != nil {
		t.Fatal(err)
	}
	if n, _ := p2.orch.Recover(context.Background(), nil); n != 1 {
		t.Fatalf("recover = %d", n)
	}
	p2.orch.Wait()
	if st := p2.tasks(reqID)["Responder correos"].Status; st != domain.TaskBlocked {
		t.Fatalf("rejected task = %s", st)
	}
	if hs := p2.holds(t); len(hs) != 0 {
		t.Fatalf("a rejected send must not be scheduled: %+v", hs)
	}
	if items, _ := p2.gw.Outbox(context.Background(), org, gateway.OutboxPendingApproval); len(items) != 0 {
		t.Fatalf("a decided approval is not restored as pending: %+v", items)
	}
}

// The approved action was executed (by the outbox) but the process died before
// the waiting task recorded it: after the restart it is never executed again.
func TestExecutedApprovalIsNeverExecutedAgainAfterRestart(t *testing.T) {
	w, reqID, ap := gatewayPendingBeforeRestart(t)
	cp, _ := w.store.GetCheckpoint(context.Background(), org, ap.TaskID)
	// Simulate the outbox path of the dead process: the send was scheduled
	// under this approval (ledger claimed) and the approval resolved.
	p0 := w.start(t, sendMailRuntime(), nil)
	call := application.GatewayCall{Org: org, AgentID: "sales", TaskID: ap.TaskID, RequestID: reqID, Tool: cp.Tools[cp.ToolIndex].Tool,
		Action: cp.Tools[cp.ToolIndex].Action, Args: cp.Tools[cp.ToolIndex].Args, Autonomy: "approve_each",
		ApprovalID: ap.ID, ApprovedArgsHash: cp.Gateway.ArgsHash}
	if out := p0.gw.Execute(context.Background(), call); out.Decision != "allowed" || out.Status != "scheduled" {
		t.Fatalf("first execution: %+v", out)
	}
	if _, err := p0.appr.Decide(context.Background(), ap.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	p0.stop()

	p2 := w.start(t, sendMailRuntime(), nil) // a fresh gateway: nothing in memory
	if n, _ := p2.orch.Recover(context.Background(), nil); n != 1 {
		t.Fatalf("recover = %d", n)
	}
	p2.waitFor("request done", func() bool { return p2.request(reqID).Status == domain.RequestDone })
	if hs := p2.holds(t); len(hs) != 1 {
		t.Fatalf("the approved send must exist once, got %d holds", len(hs))
	}
	if p2.auditCount("tool.execution_skipped") != 1 {
		t.Fatal("the duplicate must be audited as skipped")
	}
}

// ---- simulated tools and the execution ledger ----

func TestSimulatedApprovalAlreadyClaimedIsNotExecutedAgain(t *testing.T) {
	store, pub, reqID, ap := pendingBeforeRestart(t)
	// Another instance already executed this approval.
	if ok, _, err := store.ClaimExecution(context.Background(), org, application.ApprovalExecution{ApprovalID: ap.ID, TaskID: ap.TaskID}); !ok || err != nil {
		t.Fatalf("claim: %v %v", ok, err)
	}
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
	if p2.auditCount("tool.executed") != 0 || p2.auditCount("tool.execution_skipped") != 1 {
		t.Fatalf("executed=%d skipped=%d", p2.auditCount("tool.executed"), p2.auditCount("tool.execution_skipped"))
	}
}

func TestSimulatedApprovalIsRecordedInTheLedger(t *testing.T) {
	store, pub, _, ap := pendingBeforeRestart(t)
	p2 := startProcess(t, store, pub, sendProposalRuntime(proposalPlan()), nil)
	if _, err := p2.appr.Decide(context.Background(), ap.ID, "approve", "ok"); err != nil {
		t.Fatal(err)
	}
	p2.orch.Recover(context.Background(), nil) //nolint:errcheck
	p2.orch.Wait()
	ok, prev, _ := store.ClaimExecution(context.Background(), org, application.ApprovalExecution{ApprovalID: ap.ID})
	if ok || prev.Status != "simulated" || prev.ArgsHash == "" || prev.TaskID != ap.TaskID {
		t.Fatalf("ledger record: claimed=%v %+v", ok, prev)
	}
}

// ---- chat echo ----

func TestResumedChatRequestEchoesTheReportIntoItsChat(t *testing.T) {
	store, pub := newStore(t), &capture{}
	rt := newChatRuntime(fixedRoute("task", "sales", primary("assistant")))
	rt.fakeRuntime = sendProposalRuntime(proposalPlan())
	noStagger := func(c *application.Config) { c.ChatStagger = 0 }
	p1 := startProcess(t, store, pub, rt, noStagger)
	if _, err := p1.orch.PostChat(context.Background(), "agent:sales", "prepara la propuesta y envíala"); err != nil {
		t.Fatal(err)
	}
	ap := p1.awaitApproval()
	p1.stop()
	reqs, _ := store.ListRequests(context.Background(), org)
	if len(reqs) != 1 {
		t.Fatalf("requests: %d", len(reqs))
	}
	if m, _ := store.GetRunMeta(context.Background(), org, reqs[0].ID); m.Chat == nil || m.Chat.ConversationID != "agent:sales" {
		t.Fatalf("the chat link must be in the run meta: %+v", m.Chat)
	}

	p2 := startProcess(t, store, pub, rt, noStagger)
	if n, _ := p2.orch.Recover(context.Background(), nil); n != 1 {
		t.Fatalf("recover = %d", n)
	}
	p2.waitFor("agent waiting again", func() bool { return p2.agent("sales").State == domain.StateAwaitingApproval })
	if _, err := p2.appr.Decide(context.Background(), ap.ID, "approve", "ok"); err != nil {
		t.Fatal(err)
	}
	p2.orch.Wait()
	ms := p2.messages("agent:sales")
	last := ms[len(ms)-1]
	if last.RequestID == nil || *last.RequestID != reqs[0].ID || !strings.Contains(strings.ToLower(last.Text), "informe") {
		t.Fatalf("the chat that asked hears the report of the resumed request: %+v", last)
	}
}
