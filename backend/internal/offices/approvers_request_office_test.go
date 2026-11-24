package offices

import (
	"context"
	"slices"
	"testing"

	"aiworkforce/backend/internal/domain"
)

// An approval waiting for a task of a request asked from a private office belongs to that office, whatever the base
// office of the agent (a General one): only its members see it and its responsibles decide it.
func TestApprovalPolicyFollowsTheOfficeOfTheRequest(t *testing.T) {
	ctx := context.Background()
	f := newFix(t)
	f.enable(t, "d-legal", Private) // led by ana, luis is a member, the owner is not
	roles := map[string]string{"u-owner": "owner", "u-ana": "member", "u-luis": "member"}
	taskOffice := map[string]string{"t-legal": "d-legal", "t-base": ""}
	p := ApprovalPolicy{Svc: f.svc, Roles: func(context.Context, string) (map[string]string, error) { return roles, nil },
		TaskOffice: func(_ context.Context, _, id string) (string, error) { return taskOffice[id], nil }}
	asked := domain.Approval{ID: "ap-1", AgentID: "a-general", TaskID: "t-legal"}
	base := domain.Approval{ID: "ap-2", AgentID: "a-general", TaskID: "t-base"}
	if p.Sees(ctx, org, "u-owner", asked) || !p.Sees(ctx, org, "u-luis", asked) || !p.Sees(ctx, org, "u-owner", base) {
		t.Fatal("who sees an approval of a private office's request")
	}
	if ok, _ := p.CanDecide(ctx, org, "u-owner", asked); ok {
		t.Fatal("an admin outside the office decides its approval")
	}
	if ok, _ := p.CanDecide(ctx, org, "u-ana", asked); !ok {
		t.Fatal("the responsible does not decide")
	}
	if got := p.Approvers(ctx, org, asked); !slices.Equal(got, []string{"u-ana"}) {
		t.Fatalf("approvers = %v", got)
	}
	if o, ok, _, err := p.Audience(ctx, org, asked); err != nil || !ok || o.ID != "d-legal" {
		t.Fatalf("audience = %v %v %v", o.ID, ok, err)
	}
	if _, ok, _, _ := p.Audience(ctx, org, base); ok {
		t.Fatal("an approval of General's own work has an office audience")
	}
	failing := ApprovalPolicy{Svc: f.svc, TaskOffice: func(context.Context, string, string) (string, error) { return "", context.Canceled }}
	if failing.Sees(ctx, org, "u-owner", asked) {
		t.Fatal("an unreadable task must fail closed")
	}
}
