package api_test

import (
	"io"
	"log/slog"
	"testing"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/infrastructure/memory"
)

// D3 (pendientes, área B, punto 1): "approve and do not ask again" and the devil's advocate review follow who decides
// the approval (ApprovalPolicy.CanDecide), not the role: the responsible of the office uses them without
// approvals:decide, and an admin who is not the responsible may not.
func TestApprovalExtrasFollowTheOfficeRule(t *testing.T) {
	p := newOfficeApprovals(t, func(o *opts) {
		o.advocate = &advocateStub{}
		o.deps = func(d *api.Deps) {
			d.Permissions = &application.PermissionsService{Store: memory.New(), Agents: d.Store, OrgID: d.Cfg.OrgID,
				Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		}
	})
	p.approval("ap-legal", "legal")
	p.approval("ap-gen", "sales")
	post := func(path, token string) int {
		t.Helper()
		resp, _ := p.e.do("POST", api1+"/approvals/"+path, token, map[string]any{}, nil)
		return resp.StatusCode
	}
	for _, extra := range []string{"counterargument", "remember"} {
		// ana leads Legal (a member, no approvals:decide): she decides its approvals, so she may use the extras
		if c := post("ap-legal/"+extra, p.ana); c == 403 || c == 404 {
			t.Fatalf("the responsible's %s = %d", extra, c)
		}
		// beto is an admin outside Legal: he does not decide it
		if c := post("ap-legal/"+extra, p.beto); c != 403 {
			t.Fatalf("an admin outside the office: %s = %d, want 403", extra, c)
		}
		// General keeps the role rule
		if c := post("ap-gen/"+extra, p.ana); c != 403 {
			t.Fatalf("a member on a General approval: %s = %d, want 403", extra, c)
		}
		if c := post("ap-gen/"+extra, p.beto); c == 403 || c == 404 {
			t.Fatalf("an admin on a General approval: %s = %d", extra, c)
		}
	}
}
