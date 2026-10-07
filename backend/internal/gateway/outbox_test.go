package gateway

import (
	"context"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/connections/gmail"
	"aiworkforce/backend/internal/controls"
)

func pendingSend(t *testing.T, e *env) (connections.Connection, application.GatewayPending) {
	t.Helper()
	c := writeConn(e)
	out := e.exec(call("email", "send", map[string]any{"to": "laura@acme.com", "subject": "Propuesta", "body": "Adjunto la propuesta."}))
	if out.Pending == nil || out.Pending.OutboxID == "" {
		t.Fatalf("pending send must create an outbox item: %+v", out)
	}
	return c, *out.Pending
}

func TestOutboxListsEditsAndApprovesWithVersionCheck(t *testing.T) {
	e := newEnv(t, gmail.Config{})
	ctx := context.Background()
	c, p := pendingSend(t, e)

	items, err := e.g.Outbox(ctx, org, "")
	if err != nil || len(items) != 1 {
		t.Fatalf("outbox: %v %+v", err, items)
	}
	it := items[0]
	if it.ID != p.OutboxID || it.Status != OutboxPendingApproval || it.Version != 1 || it.Subject != "Propuesta" || it.Body == "" ||
		len(it.To) != 1 || it.HoldSeconds != 60 || it.Reversibility != "none" || it.AccountLabel == "" {
		t.Fatalf("item %+v", it)
	}

	// editing bumps the version and the hash: the old approval context is void
	subj := "Propuesta revisada"
	edited, err := e.g.OutboxEdit(ctx, org, it.ID, OutboxPatch{Subject: &subj})
	if err != nil || edited.Version != 2 || edited.ArgsHash == it.ArgsHash || edited.Subject != subj {
		t.Fatalf("edit: %v %+v", err, edited)
	}
	if _, err := e.g.OutboxApprove(ctx, org, it.ID, 1, "admin", nil); err != ErrVersionMismatch {
		t.Fatalf("approving a stale version must fail, got %v", err)
	}
	var decided []string
	got, err := e.g.OutboxApprove(ctx, org, it.ID, 2, "admin", func(id string) error { decided = append(decided, id); return nil })
	if err != nil || got.Status != OutboxHeld || got.ID != it.ID || got.HoldUntil == nil {
		t.Fatalf("approve: %v %+v", err, got)
	}
	if want := e.clk.Now().Add(60 * time.Second); !got.HoldUntil.Equal(want) {
		t.Fatalf("hold_until %v want %v", got.HoldUntil, want)
	}
	// the same id now cancels the held send
	fake := e.gm.FakeFor(c.ID)
	if len(fake.SentMessages()) != 0 {
		t.Fatal("sent inside the window")
	}
	e.clk.Add(61 * time.Second)
	if n := e.g.ProcessDue(ctx); n != 1 {
		t.Fatalf("send after window: %d", n)
	}
	sent := fake.SentMessages()
	if len(sent) != 1 || sent[0]["subject"] != subj {
		t.Fatalf("the EDITED content must be what is sent: %v", sent)
	}
	done, _ := e.g.Outbox(ctx, org, OutboxSent)
	if len(done) != 1 || done[0].ID != it.ID || done[0].Body != "" {
		t.Fatalf("sent item keeps no body: %+v", done)
	}
	if _, err := e.g.OutboxApprove(ctx, org, it.ID, 2, "admin", nil); err == nil {
		t.Fatal("an item cannot be approved twice")
	}
}

func TestOutboxEditRejectsSecretsAndEmptyRecipients(t *testing.T) {
	e := newEnv(t, gmail.Config{})
	ctx := context.Background()
	_, p := pendingSend(t, e)
	bad := "key AKIAIOSFODNN7EXAMPLE"
	if _, err := e.g.OutboxEdit(ctx, org, p.OutboxID, OutboxPatch{Body: &bad}); err == nil || !strings.Contains(err.Error(), connections.CodeSecretDetected) {
		t.Fatalf("secret in an edit: %v", err)
	}
	none := []string{}
	if _, err := e.g.OutboxEdit(ctx, org, p.OutboxID, OutboxPatch{To: &none}); err == nil {
		t.Fatal("an item without recipients was accepted")
	}
	to := []string{"nueva@otra.com"}
	it, err := e.g.OutboxEdit(ctx, org, p.OutboxID, OutboxPatch{To: &to})
	if err != nil || it.To[0] != "nueva@otra.com" {
		t.Fatalf("%v %+v", err, it)
	}
}

func TestOutboxEditedItemInvalidatesTheWaitingApproval(t *testing.T) {
	e := newEnv(t, gmail.Config{})
	ctx := context.Background()
	c, p := pendingSend(t, e)
	e.g.RegisterApproval("ap-1", p) // the orchestrator registers the approval card
	body := "otro texto"
	if _, err := e.g.OutboxEdit(ctx, org, p.OutboxID, OutboxPatch{Body: &body}); err != nil {
		t.Fatal(err)
	}
	// The human approves through the plain approvals endpoint: the task executes
	// with the ORIGINAL args/hash, which must not send the edited-away content.
	cl := call("email", "send", map[string]any{"to": "laura@acme.com", "subject": "Propuesta", "body": "Adjunto la propuesta."})
	cl.ApprovalID, cl.ApprovedArgsHash = "ap-1", p.ArgsHash
	out := e.exec(cl)
	if out.Decision != "denied" || out.DenyReason != CodeApprovalMismatch {
		t.Fatalf("stale approval executed: %+v", out)
	}
	e.clk.Add(2 * time.Minute)
	e.g.ProcessDue(ctx)
	if len(e.gm.FakeFor(c.ID).SentMessages()) != 0 {
		t.Fatal("sent")
	}
}

func TestOutboxApprovalIsIdempotentWithTheWaitingTask(t *testing.T) {
	e := newEnv(t, gmail.Config{})
	ctx := context.Background()
	c, p := pendingSend(t, e)
	e.g.RegisterApproval("ap-1", p)
	if _, err := e.g.OutboxApprove(ctx, org, p.OutboxID, 1, "admin", func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	// the orchestrator wakes up and executes the same approval: same outcome, no second hold
	cl := call("email", "send", map[string]any{"to": "laura@acme.com", "subject": "Propuesta", "body": "Adjunto la propuesta."})
	cl.ApprovalID, cl.ApprovedArgsHash = "ap-1", p.ArgsHash
	out := e.exec(cl)
	if out.Status != "scheduled" {
		t.Fatalf("%+v", out)
	}
	holds, _ := e.g.Holds(ctx, org, "held")
	if len(holds) != 1 {
		t.Fatalf("%d holds for one approval", len(holds))
	}
	e.clk.Add(61 * time.Second)
	e.g.ProcessDue(ctx)
	if n := len(e.gm.FakeFor(c.ID).SentMessages()); n != 1 {
		t.Fatalf("sent %d times", n)
	}
}

func TestOutboxRejectClosesTheItemAndResolvesTheApproval(t *testing.T) {
	e := newEnv(t, gmail.Config{})
	ctx := context.Background()
	c, p := pendingSend(t, e)
	e.g.RegisterApproval("ap-1", p)
	var decided string
	it, err := e.g.OutboxReject(ctx, org, p.OutboxID, "admin", func(id string) error { decided = id; return nil })
	if err != nil || it.Status != OutboxRejected || decided != "ap-1" {
		t.Fatalf("%v %+v %q", err, it, decided)
	}
	if _, err := e.g.OutboxApprove(ctx, org, p.OutboxID, 1, "admin", nil); err == nil {
		t.Fatal("a rejected item cannot be approved")
	}
	if len(e.gm.FakeFor(c.ID).SentMessages()) != 0 {
		t.Fatal("sent")
	}
	// an approval that times out or is rejected elsewhere closes the item too
	_, p2 := func() (connections.Connection, application.GatewayPending) {
		out := e.exec(call("email", "send", map[string]any{"to": "b@c.com", "subject": "x", "body": "y"}))
		return connections.Connection{}, *out.Pending
	}()
	e.g.RegisterApproval("ap-2", p2)
	e.g.ApprovalResolved("ap-2", false)
	rej, _ := e.g.Outbox(ctx, org, OutboxRejected)
	if len(rej) != 2 {
		t.Fatalf("rejected items: %d", len(rej))
	}
}

func TestKillSwitchReturnsHeldSendsToTheApprovalQueue(t *testing.T) {
	e := newEnv(t, gmail.Config{})
	ctx := context.Background()
	c, p := pendingSend(t, e)
	if _, err := e.g.OutboxApprove(ctx, org, p.OutboxID, 1, "admin", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ctl.KillSwitch(ctx, org, controls.LevelFreeze, "drill", "admin"); err != nil {
		t.Fatal(err)
	}
	items, _ := e.g.Outbox(ctx, org, "")
	if len(items) != 1 || items[0].Status != OutboxPendingApproval || items[0].ID != p.OutboxID || items[0].BlockReason != "kill_switch" {
		t.Fatalf("a held send must go back to pending_approval, got %+v", items)
	}
	if items[0].Body == "" {
		t.Fatal("the content must survive the requeue so the human can decide again")
	}
	// nothing runs by inertia: not while frozen, not after the release
	e.clk.Add(5 * time.Minute)
	e.g.ProcessDue(ctx)
	if _, err := e.ctl.Release(ctx, org, "all clear", "owner", false); err != nil {
		t.Fatal(err)
	}
	e.g.ProcessDue(ctx)
	if len(e.gm.FakeFor(c.ID).SentMessages()) != 0 {
		t.Fatal("a requeued send executed without a new approval")
	}
	// the human approves again: a NEW 60 s window starts
	again, err := e.g.OutboxApprove(ctx, org, p.OutboxID, items[0].Version, "admin", nil)
	if err != nil || again.Status != OutboxHeld {
		t.Fatalf("re-approve: %v %+v", err, again)
	}
	e.clk.Add(30 * time.Second)
	e.g.ProcessDue(ctx)
	if len(e.gm.FakeFor(c.ID).SentMessages()) != 0 {
		t.Fatal("sent inside the new window")
	}
	e.clk.Add(31 * time.Second)
	e.g.ProcessDue(ctx)
	if len(e.gm.FakeFor(c.ID).SentMessages()) != 1 {
		t.Fatal("not sent after the new window")
	}
}

func TestReadOnlyModeAtSendTimeRequeuesInsteadOfSending(t *testing.T) {
	e := newEnv(t, gmail.Config{})
	ctx := context.Background()
	c, p := pendingSend(t, e)
	if _, err := e.g.OutboxApprove(ctx, org, p.OutboxID, 1, "admin", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ctl.SetMode(ctx, org, controls.ModeReadOnly, "admin", "audit"); err != nil {
		t.Fatal(err)
	}
	e.clk.Add(2 * time.Minute)
	e.g.ProcessDue(ctx)
	if len(e.gm.FakeFor(c.ID).SentMessages()) != 0 {
		t.Fatal("sent in read-only mode")
	}
	items, _ := e.g.Outbox(ctx, org, OutboxPendingApproval)
	if len(items) != 1 {
		t.Fatalf("the send must wait for a human: %+v", items)
	}
}

func TestPlanListReturnsPendingReviews(t *testing.T) {
	e := newEnv(t, gmail.Config{})
	writeConn(e, "mail.send")
	ctx := context.Background()
	tasks := []application.PlanTaskInfo{{ID: "t1", Title: "x", AgentID: "assistant"}}
	if _, req, _, _ := e.g.Begin(ctx, org, "r1", tasks); !req {
		t.Fatal("review required")
	}
	if l := e.g.PlanList(org); len(l) != 1 || l[0].RequestID != "r1" {
		t.Fatalf("%+v", l)
	}
	if l := e.g.PlanList("other-org"); len(l) != 0 {
		t.Fatal("reviews leaked across orgs")
	}
}
