package api_test

import (
	"context"
	"testing"

	"aiworkforce/backend/internal/trust"
)

// D3: a trust proposal of an agent of a private office is decided only by the members of that office. For anyone else
// (an admin outside it, even holding agents:write and approvals:decide) it does not exist: 404 on accept, decline and
// revert, and the proposal stays pending.
func TestTrustProposalsOfPrivateOfficeAreSealed(t *testing.T) {
	n := newNeedsYou(t)
	n.proposal("tp-hr", "hr")       // Privada (private, led by carla)
	n.proposal("tp-sales", "sales") // General
	base := api1 + "/trust/proposals/"
	for _, act := range []string{"accept", "decline", "revert"} {
		if s := n.e.status("POST", base+"tp-hr/"+act, n.beto, nil); s != 404 {
			t.Fatalf("non-member admin %s = %d, want 404", act, s)
		}
	}
	if s := n.e.status("POST", base+"ghost/decline", n.beto, nil); s != 404 {
		t.Fatalf("unknown proposal = %d", s)
	}
	p, err := n.trust.GetProposal(context.Background(), n.org, "tp-hr")
	if err != nil || p.Status != trust.StatusPending {
		t.Fatalf("proposal must stay pending: %+v %v", p, err)
	}
	// A proposal of General is decided by any admin.
	if s := n.e.status("POST", base+"tp-sales/decline", n.beto, nil); s != 200 {
		t.Fatalf("admin decline General = %d", s)
	}
	// A member of the private office with the role (carla, admin) decides it.
	if s := n.e.status("POST", base+"tp-hr/decline", n.carla, nil); s != 200 {
		t.Fatalf("member admin decline = %d", s)
	}
	if p, _ = n.trust.GetProposal(context.Background(), n.org, "tp-hr"); p.Status != trust.StatusDeclined {
		t.Fatalf("status = %s", p.Status)
	}
	// The agent routes were already sealed.
	for _, path := range []string{"/agents/hr/trust", "/agents/hr/trust/proposals"} {
		if s := n.e.status("GET", api1+path, n.beto, nil); s != 404 {
			t.Fatalf("GET %s = %d", path, s)
		}
	}
	if s := n.e.status("POST", api1+"/agents/hr/trust/evaluate", n.beto, nil); s != 404 {
		t.Fatalf("evaluate = %d", s)
	}
}
