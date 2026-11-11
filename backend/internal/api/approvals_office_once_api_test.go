package api_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/offices"
)

// D3 (pendientes, área B, punto 2): the inbox, the list of approvals and the away summary ask the office rule about
// every pending approval; the roles of the organization (a database query) and the offices model are read once per
// request, not once per item.
func TestApprovalListingsReadTheRuleOnce(t *testing.T) {
	var roleReads atomic.Int32
	p := newOfficeApprovals(t, func(o *opts) {
		o.deps = func(d *api.Deps) {
			d.Orch.SetApproverPolicy(offices.ApprovalPolicy{Svc: d.Offices, Roles: func(ctx context.Context, _ string) (map[string]string, error) {
				roleReads.Add(1)
				pr, _ := auth.PrincipalFrom(ctx)
				ms, err := d.Auth.ListMembers(ctx, pr)
				out := map[string]string{}
				for _, m := range ms {
					out[m.UserID] = string(m.Role)
				}
				return out, err
			}})
		}
	})
	now := time.Now().UTC()
	for i := range 6 {
		if err := p.e.store.CreateApproval(context.Background(), p.org, domain.Approval{ID: fmt.Sprintf("ap-tier-%d", i), TaskID: "t", AgentID: "legal",
			Action: "send", Title: "x", Risk: "low", Status: domain.ApprovalPending, CreatedAt: now, RequiredApprovals: 1, RequiredRole: "member",
			Decisions: []domain.ApprovalDecision{}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/approvals/inbox", "/approvals?status=pending"} {
		roleReads.Store(0)
		resp, raw := p.e.do("GET", api1+path, p.ana, nil, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s = %d %s", path, resp.StatusCode, raw)
		}
		if n := roleReads.Load(); n > 1 {
			t.Fatalf("GET %s read the roles %d times for 6 approvals", path, n)
		}
	}
}
