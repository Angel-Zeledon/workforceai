package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/connections/gmail"
	"aiworkforce/backend/internal/domain"
)

type rehearsalPolicy struct {
	agents map[string]bool
	err    error
}

func (p *rehearsalPolicy) Rehearsing(_ context.Context, _, agentID, _ string) (bool, error) {
	return p.agents[agentID], p.err
}

func noEffects(t *testing.T, e *env, connID string) {
	t.Helper()
	fake := e.gm.FakeFor(connID)
	if n := len(fake.SentMessages()); n != 0 {
		t.Fatalf("a rehearsal sent %d message(s)", n)
	}
	if n := len(fake.DraftMessages()); n != 0 {
		t.Fatalf("a rehearsal created %d draft(s)", n)
	}
	hs, _ := e.g.Holds(context.Background(), org, "")
	if len(hs) != 0 {
		t.Fatalf("a rehearsal scheduled holds: %+v", hs)
	}
	items, _ := e.g.Outbox(context.Background(), org, "")
	if len(items) != 0 {
		t.Fatalf("a rehearsal queued outbox items: %+v", items)
	}
}

// The core guarantee of B9: a rehearsal never executes a write, EVEN WITH a valid approval for those exact args.
func TestRehearsalNeverExecutesAWriteEvenIfApproved(t *testing.T) {
	e := newEnv(t, gmail.Config{})
	c := writeConn(e)
	// The approval of a real (non-rehearsal) call, bound to the exact args...
	ac, _ := approvedCall(t, e, sendArgs())
	// ...forget the outbox item that real call created: this test is about the rehearsal only.
	e.g.ApprovalResolved("ap1", false)
	ac.Rehearsal = true
	out := e.exec(ac)
	if out.Decision != application.DecisionRehearsed || out.Status != "not_executed" || out.HoldID != "" || out.Rehearsal == nil {
		t.Fatalf("outcome = %+v", out)
	}
	if out.Rehearsal.WouldDecision != domain.WouldApprove || len(out.Rehearsal.Recipients) != 1 || out.Rehearsal.Recipients[0] != "laura@acme.com" ||
		!contains(out.Rehearsal.Flags, "always_approval") {
		t.Fatalf("preview = %+v", out.Rehearsal)
	}
	e.clk.Add(10 * time.Minute)
	if n := e.g.ProcessDue(context.Background()); n != 0 {
		t.Fatalf("ProcessDue sent %d", n)
	}
	if n := len(e.gm.FakeFor(c.ID).SentMessages()); n != 0 {
		t.Fatalf("a rehearsal sent %d message(s)", n)
	}
	hs, _ := e.g.Holds(context.Background(), org, "")
	if len(hs) != 0 {
		t.Fatalf("holds = %+v", hs)
	}
	if !strings.Contains(e.sink.text(), `"action":"tool.rehearsed"`) {
		t.Fatal("tool.rehearsed audit entry missing")
	}
}

// A reversible write an autonomous agent could do alone (a draft) is not done either: the preview says "allowed".
func TestRehearsalDoesNotCreateDraftThatWouldBeAllowed(t *testing.T) {
	e := newEnv(t, gmail.Config{})
	c := writeConn(e, "mail.draft")
	dc := call("email", "draft", map[string]any{"to": "laura@acme.com", "subject": "Re", "body": "Hola"})
	dc.Rehearsal = true
	out := e.exec(dc)
	if out.Decision != application.DecisionRehearsed || out.Rehearsal.WouldDecision != domain.WouldAllow {
		t.Fatalf("outcome = %+v / %+v", out, out.Rehearsal)
	}
	noEffects(t, e, c.ID)
	rows, _ := e.cs.Usage(context.Background(), org, connections.UsageFilter{ConnectionID: c.ID})
	if len(rows) != 0 {
		t.Fatalf("a rehearsal counted usage against the limits: %+v", rows)
	}
}

// Reads still run during a rehearsal, so the output is realistic.
func TestRehearsalStillRunsReads(t *testing.T) {
	e := newEnv(t, gmail.Config{})
	readConn(e)
	rc := call("email", "search", map[string]any{"q": ""})
	rc.Rehearsal = true
	if out := e.exec(rc); out.Decision != "allowed" || out.Status != "succeeded" || len(out.Blocks) == 0 {
		t.Fatalf("read during rehearsal = %+v", out)
	}
}

// The preview reports what the controls would have done: the kill switch would have denied it.
func TestRehearsalPreviewShowsDenials(t *testing.T) {
	e := newEnv(t, gmail.Config{})
	c := writeConn(e)
	sc := call("email", "send", map[string]any{"to": "a@b.com", "subject": "k", "body": "my key AKIAIOSFODNN7EXAMPLE"})
	sc.Rehearsal = true
	out := e.exec(sc)
	if out.Decision != application.DecisionRehearsed || out.Rehearsal.WouldDecision != domain.WouldDeny || out.Rehearsal.DenyReason != connections.CodeSecretDetected {
		t.Fatalf("outcome = %+v / %+v", out, out.Rehearsal)
	}
	noEffects(t, e, c.ID)
}

// The agent's switch is enforced by the gateway itself (the orchestrator flag is not needed), and an unknown state
// fails closed: nothing is written.
func TestRehearsalPolicyIsEnforcedAndFailsClosed(t *testing.T) {
	e := newEnv(t, gmail.Config{})
	c := writeConn(e, "mail.draft")
	pol := &rehearsalPolicy{agents: map[string]bool{"assistant": true}}
	e.g.Rehearsal = pol
	args := map[string]any{"to": "laura@acme.com", "subject": "Re", "body": "Hola"}
	if out := e.exec(call("email", "draft", args)); out.Decision != application.DecisionRehearsed {
		t.Fatalf("agent switch ignored: %+v", out)
	}
	pol.agents, pol.err = map[string]bool{}, errors.New("store down")
	if out := e.exec(call("email", "draft", args)); out.Decision != application.DecisionRehearsed {
		t.Fatalf("unknown rehearsal state must fail closed: %+v", out)
	}
	noEffects(t, e, c.ID)
	pol.err = nil
	if out := e.exec(call("email", "draft", args)); out.Decision != "allowed" || out.Status != "succeeded" {
		t.Fatalf("switch off: the draft runs again: %+v", out)
	}
}

// A send approved and held BEFORE the agent went into rehearsal does not go out while the switch is on: it returns to
// the queue, and approving it from the outbox is refused with rehearsal_mode until the switch is off.
func TestHeldSendIsRequeuedWhenAgentEntersRehearsal(t *testing.T) {
	e := newEnv(t, gmail.Config{})
	c := writeConn(e)
	ac, _ := approvedCall(t, e, sendArgs())
	out := e.exec(ac)
	if out.Status != "scheduled" {
		t.Fatalf("setup: %+v", out)
	}
	pol := &rehearsalPolicy{agents: map[string]bool{"assistant": true}}
	e.g.Rehearsal = pol
	e.clk.Add(2 * time.Minute)
	if n := e.g.ProcessDue(context.Background()); n != 0 || len(e.gm.FakeFor(c.ID).SentMessages()) != 0 {
		t.Fatal("a held send went out while the agent was in rehearsal")
	}
	all, _ := e.g.Outbox(context.Background(), org, OutboxPendingApproval)
	var items []OutboxItem
	for _, it := range all {
		if it.ID == out.HoldID {
			items = append(items, it)
		}
	}
	if len(items) != 1 || items[0].BlockReason != CodeRehearsal {
		t.Fatalf("requeued items = %+v", all)
	}
	_, err := e.g.OutboxApprove(context.Background(), org, items[0].ID, items[0].Version, "u1", nil)
	var de *DeniedError
	if !errors.As(err, &de) || de.Code != CodeRehearsal {
		t.Fatalf("approve during rehearsal = %v", err)
	}
	if len(e.gm.FakeFor(c.ID).SentMessages()) != 0 {
		t.Fatal("sent during rehearsal")
	}
}
