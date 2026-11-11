package gateway

import (
	"context"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/connections/gmail"
)

// D3 (pendientes, área B, punto 6): an agent that moves to another office loses its office's connection also for
// what was already on its way: a send waiting out its window, and a write grant waiting for its second human.

func officeGmail(t *testing.T, e *env, who *officeOf) connections.Connection {
	t.Helper()
	e.cs.AgentOffice = who.fn
	c, err := e.cs.Create(context.Background(), org, connections.CreateInput{Provider: "google_gmail", Label: "Correo de Ventas",
		Capabilities: []string{"mail.send"}, Mode: connections.ModeSimulated, Actor: "u1", OfficeID: "ventas"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestHeldSendOfAMovedAgentIsCancelled(t *testing.T) {
	e := newEnv(t, gmail.Config{})
	ctx := context.Background()
	who := &officeOf{of: map[string]string{"assistant": "ventas"}, fail: map[string]bool{}}
	c := officeGmail(t, e, who)
	e.grant(c.ID, "assistant", connections.GrantInput{Capabilities: []string{"mail.send"}})
	ac, _ := approvedCall(t, e, sendArgs())
	if out := e.exec(ac); out.Status != "scheduled" {
		t.Fatalf("scheduled: %+v", out)
	}
	who.of["assistant"] = "compras" // it moved while its send waited out the window
	e.clk.Add(2 * time.Minute)
	if n := e.g.ProcessDue(ctx); n != 0 || len(e.gm.FakeFor(c.ID).SentMessages()) != 0 {
		t.Fatal("the send of an agent that left the office went out")
	}
	hs, _ := e.g.Holds(ctx, org, connections.HoldCancelled)
	if len(hs) != 1 || hs[0].Reason != connections.CodeScopeNotGranted {
		t.Fatalf("cancelled holds: %+v", hs)
	}
}

func TestWriteGrantOfAMovedAgentIsNotApproved(t *testing.T) {
	e := newEnv(t, gmail.Config{})
	ctx := context.Background()
	e.admin = 2 // a write grant waits for a second human
	who := &officeOf{of: map[string]string{"assistant": "ventas"}, fail: map[string]bool{}}
	c := officeGmail(t, e, who)
	g := e.grant(c.ID, "assistant", connections.GrantInput{Capabilities: []string{"mail.send"}})
	if g.Status != connections.GrantPendingApproval {
		t.Fatalf("grant status = %s", g.Status)
	}
	who.of["assistant"] = "compras"
	_, err := e.cs.ApproveGrant(ctx, org, c.ID, "assistant", "admin2")
	if err == nil || !strings.Contains(err.Error(), connections.CodeAgentOtherOffice) {
		t.Fatalf("approving the grant of an agent that left the office: %v", err)
	}
	if cur, _ := e.cs.Store.GetGrant(ctx, org, c.ID, "assistant"); cur.Status != connections.GrantPendingApproval {
		t.Fatalf("grant became %s", cur.Status)
	}
}
