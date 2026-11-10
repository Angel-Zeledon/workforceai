package offices

import (
	"context"
	"slices"
	"testing"

	"aiworkforce/backend/internal/domain"
)

// D3 (equipos-oficinas.md §4.2): who decides the approvals of an office's agent.
func TestApprovalPolicy(t *testing.T) {
	ctx := context.Background()
	f := newFix(t)
	f.enable(t, "d-legal", Private) // led by ana, luis is a member, the owner is not
	f.enable(t, "d-sales", Open)    // no responsible: the admins
	roles := map[string]string{"u-owner": "owner", "u-ana": "member", "u-luis": "member", "u-marta": "member"}
	p := ApprovalPolicy{Svc: f.svc, Roles: func(context.Context, string) (map[string]string, error) { return roles, nil }}
	can := func(user string, ap domain.Approval) bool {
		t.Helper()
		ok, err := p.CanDecide(ctx, org, user, ap)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	legal := domain.Approval{ID: "ap-legal", AgentID: "a-elena"}
	if got := p.Approvers(ctx, org, legal); !slices.Equal(got, []string{"u-ana"}) {
		t.Fatalf("Legal's approvers = %v", got)
	}
	if !can("u-ana", legal) || can("u-luis", legal) || can("u-owner", legal) {
		t.Fatal("only Legal's responsible decides its approvals")
	}
	if !p.Sees(ctx, org, "u-luis", legal) || p.Sees(ctx, org, "u-owner", legal) {
		t.Fatal("a private office's approvals are its members'")
	}

	// a minimum role the responsible does not reach: today's rules (the admins with that role who see the work)
	tier := domain.Approval{ID: "ap-tier", AgentID: "a-elena", RequiredRole: "admin"}
	if got := p.Approvers(ctx, org, tier); len(got) != 0 || !can("u-luis", tier) {
		t.Fatalf("an approval above the responsible's role: approvers %v", got)
	}

	// an office without a responsible: its admins
	sales := domain.Approval{ID: "ap-sales", AgentID: "a-valeria"}
	if got := p.Approvers(ctx, org, sales); !slices.Equal(got, []string{"u-owner"}) || can("u-marta", sales) || !can("u-owner", sales) {
		t.Fatalf("Ventas' approvers = %v", got)
	}

	// General: today's rules for everybody
	gen := domain.Approval{ID: "ap-gen", AgentID: "a-sofia"}
	if got := p.Approvers(ctx, org, gen); got != nil || !can("u-marta", gen) || !p.Sees(ctx, org, "u-marta", gen) {
		t.Fatalf("General's approvers = %v", got)
	}

	// without offices nothing changes
	none := ApprovalPolicy{}
	if ok, err := none.CanDecide(ctx, org, "u-luis", legal); !ok || err != nil || none.Approvers(ctx, org, legal) != nil || !none.Sees(ctx, org, "u-luis", legal) {
		t.Fatal("without offices the policy must not narrow anything")
	}
}
