package api_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/connections/gmail"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/gateway"
	"aiworkforce/backend/internal/sanitize"
	"aiworkforce/backend/internal/vault"
)

// withGateway mounts the connections, controls and the Tool Gateway (the outbox) over in-memory stores and hands
// the gateway to *gw.
func withGateway(gw **gateway.Gateway) func(*opts) {
	return func(o *opts) {
		prev := o.deps
		o.deps = func(d *api.Deps) {
			if prev != nil {
				prev(d)
			}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			sus := sanitize.NewSuspects()
			kw, _ := vault.NewEnvKeyWrapper(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
			v := vault.New(vault.NewMemRepo(), kw)
			v.Suspects = sus
			gm, _ := gmail.New(gmail.Config{})
			cs, err := connections.NewService(connections.Config{Store: connections.NewMemStore(), Vault: v, Suspects: sus,
				Providers: map[string]connections.Provider{"google_gmail": gm}, AdminCount: func(context.Context, string) int { return 1 }})
			if err != nil {
				panic(err)
			}
			ctl := controls.New(controls.NewMemStore(), nil, nil)
			g := gateway.New(cs, ctl, sus, log)
			g.Audit = func(context.Context, string, string, string, string, string, map[string]any) {}
			g.Emit = func(context.Context, string, string, map[string]any) {}
			d.Orch.SetConnections(gateway.Guard{G: g}, g, g)
			d.Conns, d.Controls, d.Gateway = cs, ctl, g
			*gw = g
		}
	}
}

// outboxItem queues an e-mail of agent for approval (as a task of it would) with its approval record, and returns
// the outbox item id and the approval id.
func (p *officeApprovals) outboxItem(gw *gateway.Gateway, agent string, more ...func(*domain.Approval)) (string, string) {
	t := p.e.t
	t.Helper()
	c := p.e.json("POST", api1+"/connections", p.owner.AccessToken, map[string]any{"provider": "google_gmail", "label": "Gmail " + agent,
		"capabilities": []string{"mail.send"}, "mode": "simulated"}, 201)
	id := c["id"].(string)
	p.e.json("PUT", api1+"/connections/"+id+"/grants/"+agent, p.owner.AccessToken, map[string]any{"capabilities": []string{"mail.send"},
		"confirm_name": "Gmail " + agent}, 200)
	out := gw.Execute(context.Background(), application.GatewayCall{Org: p.org, AgentID: agent, TaskID: "t-" + agent, Tool: "email", Action: "send",
		Autonomy: "autonomous", Args: map[string]any{"to": "laura@acme.com", "subject": "Hola " + agent, "body": "Texto"}})
	if out.Pending == nil {
		t.Fatalf("no pending send: %+v", out)
	}
	want := domain.Approval{TaskID: "t-" + agent, AgentID: agent, Action: "send", Risk: "high", Title: "enviar " + agent}
	for _, f := range more {
		f(&want)
	}
	ap, _, err := p.e.appr.Request(application.WithOrg(context.Background(), p.org), want)
	if err != nil {
		t.Fatal(err)
	}
	gw.RegisterApproval(ap.ID, *out.Pending)
	return out.Pending.OutboxID, ap.ID
}

func (p *officeApprovals) outbox(token string) []map[string]any {
	p.e.t.Helper()
	var v struct {
		Items []map[string]any `json:"items"`
	}
	resp, raw := p.e.do("GET", api1+"/outbox", token, nil, nil)
	if resp.StatusCode != 200 || json.Unmarshal(raw, &v) != nil {
		p.e.t.Fatalf("GET /outbox = %d %s", resp.StatusCode, raw)
	}
	return v.Items
}

func outboxStatus(items []map[string]any, id string) string {
	for _, it := range items {
		if it["id"] == id {
			s, _ := it["status"].(string)
			return s
		}
	}
	return ""
}

// D3 round 1: approving a department's e-mail from the outbox goes through its responsible: an admin outside the
// office is refused and nothing is sent; a private office's entries are not even listed for a non-member.
func TestOutboxApprovalGoesThroughTheResponsible(t *testing.T) {
	var gw *gateway.Gateway
	p := newOfficeApprovals(t, withGateway(&gw))
	legal, legalAp := p.outboxItem(gw, "legal")
	priv, _ := p.outboxItem(gw, "hr")

	// beto (admin, not Legal's responsible): 403 approving and rejecting, and the e-mail does not leave
	for _, path := range []string{"/approve", "/reject"} {
		resp, raw := p.e.do("POST", api1+"/outbox/"+legal+path, p.beto, map[string]any{"version": 1}, nil)
		if resp.StatusCode != 403 {
			t.Fatalf("beto %s = %d %s", path, resp.StatusCode, raw)
		}
	}
	if st := outboxStatus(p.outbox(p.owner.AccessToken), legal); st != gateway.OutboxPendingApproval {
		t.Fatalf("after beto's refused approve the item is %q", st)
	}
	if ap, _ := p.e.store.GetApproval(context.Background(), p.org, legalAp); ap.Status != domain.ApprovalPending {
		t.Fatalf("the approval moved: %s", ap.Status)
	}

	// a private office's entry: absent for a non-member, 404 to edit or decide
	if st := outboxStatus(p.outbox(p.beto), priv); st != "" {
		t.Fatalf("beto sees the private entry (%s)", st)
	}
	if st := outboxStatus(p.outbox(p.beto), legal); st == "" {
		t.Fatal("beto must still see the open office's entry")
	}
	if resp, _ := p.e.do("PATCH", api1+"/outbox/"+priv, p.beto, map[string]any{"subject": "x"}, nil); resp.StatusCode != 404 {
		t.Fatalf("beto PATCH private = %d", resp.StatusCode)
	}
	if resp, _ := p.e.do("POST", api1+"/outbox/"+priv+"/approve", p.beto, map[string]any{"version": 1}, nil); resp.StatusCode != 404 {
		t.Fatalf("beto approve private = %d", resp.StatusCode)
	}
	if st := outboxStatus(p.outbox(p.carla), priv); st != gateway.OutboxPendingApproval {
		t.Fatalf("carla (its responsible) sees %q", st)
	}

	// ana (Legal's responsible, a member): she approves, the send is scheduled and the approval resolved
	resp, raw := p.e.do("POST", api1+"/outbox/"+legal+"/approve", p.ana, map[string]any{"version": 1}, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(raw), `"status":"held"`) {
		t.Fatalf("ana approve = %d %s", resp.StatusCode, raw)
	}
	if ap, _ := p.e.store.GetApproval(context.Background(), p.org, legalAp); ap.Status != domain.ApprovalApproved {
		t.Fatalf("the approval is %s", ap.Status)
	}
}

// D3 round 1: whoever asked for the action sees the approval of their request (not decide it).
func TestRequesterSeesButDoesNotDecide(t *testing.T) {
	p := newOfficeApprovals(t)
	beto := p.e.userID(p.beto)
	now := domain.Approval{ID: "ap-asked", TaskID: "t-asked", AgentID: "hr", Action: "send", Title: "pedida por beto", Risk: "low",
		Status: domain.ApprovalPending, RequiredApprovals: 1, RequestedBy: beto, Decisions: []domain.ApprovalDecision{}}
	if err := p.e.store.CreateApproval(context.Background(), p.org, now); err != nil {
		t.Fatal(err)
	}
	p.approval("ap-other", "hr")
	resp, raw := p.e.do("GET", api1+"/approvals", p.beto, nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(raw), "ap-asked") || strings.Contains(string(raw), "ap-other") {
		t.Fatalf("beto's approvals: %s", raw)
	}
	in := p.inbox(p.beto, "requested")
	if ids := inboxIDs(in); !slices.Equal(ids, []string{"ap-asked"}) || in.Items[0].CanDecide {
		t.Fatalf("beto's requested = %v", in.Items)
	}
	p.decide("ap-asked", p.beto, 403)
	p.decide("ap-other", p.beto, 404)
}

// D3 round 1: "always allow for every agent" needs approvals:decide; a responsible without it only decides this one.
func TestResponsibleCannotAllowEveryAgent(t *testing.T) {
	p := newOfficeApprovals(t, func(o *opts) { o.standing = true })
	p.approval("ap-legal", "legal")
	// the list says who may decide (Pendientes en el móvil, the project views)
	listed := func(token string) map[string]any {
		var v []map[string]any
		resp, raw := p.e.do("GET", api1+"/approvals?status=pending", token, nil, nil)
		if resp.StatusCode != 200 || json.Unmarshal(raw, &v) != nil || len(v) != 1 {
			t.Fatalf("GET /approvals = %d %s", resp.StatusCode, raw)
		}
		return v[0]
	}
	anaName := p.e.json("GET", api1+"/auth/me", p.ana, nil, 200)["user"].(map[string]any)["name"].(string)
	if it := listed(p.ana); it["can_decide"] != true {
		t.Fatalf("ana's item: %v", it)
	}
	if it := listed(p.beto); it["can_decide"] != false || len(it["approvers"].([]any)) != 1 || it["approvers"].([]any)[0] != anaName {
		t.Fatalf("beto's item: %v", it)
	}
	resp, raw := p.e.do("POST", api1+"/approvals/ap-legal/decision", p.ana, map[string]any{"decision": "approve", "always_allow": true,
		"always_allow_any_agent": true}, nil)
	if resp.StatusCode != 403 {
		t.Fatalf("ana always_allow_any_agent = %d %s", resp.StatusCode, raw)
	}
	if ap, _ := p.e.store.GetApproval(context.Background(), p.org, "ap-legal"); ap.Status != domain.ApprovalPending {
		t.Fatalf("the refused request decided it: %s", ap.Status)
	}
}

// D3 round 1: the batch of a project the person may not see answers like a missing project, without its counts.
func TestBatchOfAHiddenProjectIsNotFound(t *testing.T) {
	p := newPrivateWork(t)
	only := p.project(t, "solo legal", func(int) string { return "legal" })
	body := map[string]any{"filter": map[string]any{"project_id": only, "action": "send"}, "decision": "approve", "expected_count": 3}
	resp, raw := p.e.do("POST", api1+"/approvals/batch", p.beto, body, nil)
	if resp.StatusCode != 404 || strings.Contains(string(raw), "count") {
		t.Fatalf("beto's batch = %d %s", resp.StatusCode, raw)
	}
	if resp, raw := p.e.do("POST", api1+"/approvals/batch", p.ana, body, nil); resp.StatusCode != 409 {
		t.Fatalf("ana's batch = %d %s", resp.StatusCode, raw)
	}
}
