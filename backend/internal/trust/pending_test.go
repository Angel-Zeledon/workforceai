package trust_test

import (
	"context"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/trust"
)

// "Te necesitan" (needs-you.md) lists the pending proposals of every agent and asks who may decide them: the same
// rule as accepting one (admin or owner, never an agent or the system).
func TestPendingAndMayDecide(t *testing.T) {
	f := newFixture(t)
	st := trust.NewMemStore()
	svc := trust.New(trust.Config{Store: st, Agents: f.mem, OrgID: f.org, Now: f.clk.now})
	for _, p := range []trust.Proposal{
		{ID: "p1", AgentID: "sales", Kind: trust.KindPromotion, Status: trust.StatusPending, CreatedAt: f.clk.now()},
		{ID: "p2", AgentID: "legal", Kind: trust.KindDemotion, Status: trust.StatusPending, CreatedAt: f.clk.now()},
		{ID: "p3", AgentID: "hr", Kind: trust.KindPromotion, Status: trust.StatusDeclined, CreatedAt: f.clk.now()},
	} {
		if err := st.CreateProposal(context.Background(), f.org, p); err != nil {
			t.Fatal(err)
		}
	}
	got, err := svc.Pending(f.ctx, 50)
	if err != nil || len(got) != 2 {
		t.Fatalf("pending = %v %v", got, err)
	}
	for _, p := range got {
		if p.Status != trust.StatusPending {
			t.Fatalf("not pending: %+v", p)
		}
	}
	role := func(r string) context.Context { return application.WithActorRole(f.ctx, r) }
	if !svc.MayDecide(role("admin"), "sales") || !svc.MayDecide(role("owner"), "sales") {
		t.Fatal("an admin or owner may decide")
	}
	if svc.MayDecide(role("member"), "sales") {
		t.Fatal("a member may not decide a change of autonomy")
	}
	agent := application.WithActorRole(application.WithActor(f.ctx, "agent:sales"), "owner")
	if svc.MayDecide(agent, "sales") || svc.MayDecide(application.WithActor(f.ctx, "sales"), "sales") {
		t.Fatal("an agent never decides")
	}
}
