package api_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/offices"
)

// withApprovers installs the D3 approval rule of the offices (equipos-oficinas.md §4.2) on the orchestrator's
// approvals, like cmd/server does. It goes after withOffices.
func withApprovers(d *api.Deps) {
	d.Orch.SetApproverPolicy(offices.ApprovalPolicy{Svc: d.Offices, Roles: func(ctx context.Context, _ string) (map[string]string, error) {
		p, _ := auth.PrincipalFrom(ctx)
		ms, err := d.Auth.ListMembers(ctx, p)
		out := map[string]string{}
		for _, m := range ms {
			out[m.UserID] = string(m.Role)
		}
		return out, err
	}})
}

// officeApprovals (D3): Legal is an open office led by ana (a member: no approvals:decide) with the agent legal;
// Finanzas is an open office without a responsible with the agent accounting; Privada is a private office led by
// carla (an admin) with the agent hr; the agent sales stays in General. beto is an admin outside every office.
type officeApprovals struct {
	e                *env
	owner            *auth.Session
	ana, beto, carla string
	org              string
}

func newOfficeApprovals(t *testing.T) *officeApprovals {
	t.Helper()
	e := newEnv(t, opts{auth: true, orgchart: true, deps: func(d *api.Deps) { withOffices(d); withApprovers(d) }})
	owner := e.register("owner-ap@example.com", "OwnerAP")
	p := &officeApprovals{e: e, owner: owner, org: owner.OrgID,
		ana:   e.member(owner, "ana-ap@example.com", auth.RoleMember),
		beto:  e.member(owner, "beto-ap@example.com", auth.RoleAdmin),
		carla: e.member(owner, "carla-ap@example.com", auth.RoleAdmin)}
	office := func(name, head, agent, vis string) {
		body := map[string]any{"name": name}
		if head != "" {
			body["head"] = map[string]any{"kind": "person", "id": head}
		}
		dep := e.json("POST", api1+"/org/departments", owner.AccessToken, body, 201)
		id := dep["id"].(string)
		e.json("PUT", api1+"/agents/"+agent+"/job", owner.AccessToken, map[string]any{"department_id": id}, 200)
		e.json("POST", api1+"/offices", owner.AccessToken, map[string]any{"department_id": id, "visibility": vis}, 201)
	}
	office("Legal", e.userID(p.ana), "legal", "open")
	office("Finanzas", "", "accounting", "open")
	office("Privada", e.userID(p.carla), "hr", "private")
	return p
}

func (p *officeApprovals) approval(id, agent string) {
	p.e.t.Helper()
	now := time.Now().UTC()
	if err := p.e.store.CreateApproval(context.Background(), p.org, domain.Approval{ID: id, TaskID: "t-" + id, AgentID: agent, Action: "send",
		Title: "aprobación " + id, Risk: "low", Status: domain.ApprovalPending, CreatedAt: now, RequiredApprovals: 1,
		Decisions: []domain.ApprovalDecision{}}); err != nil {
		p.e.t.Fatal(err)
	}
}

func (p *officeApprovals) decide(id, token string, want int) {
	p.e.t.Helper()
	resp, raw := p.e.do("POST", api1+"/approvals/"+id+"/decision", token, map[string]any{"decision": "approve"}, nil)
	if resp.StatusCode != want {
		p.e.t.Fatalf("decide %s = %d, want %d: %s", id, resp.StatusCode, want, raw)
	}
}

func (p *officeApprovals) inbox(token, mine string) application.ApprovalInbox {
	p.e.t.Helper()
	path := api1 + "/approvals/inbox"
	if mine != "" {
		path += "?mine=" + mine
	}
	resp, raw := p.e.do("GET", path, token, nil, nil)
	var in application.ApprovalInbox
	if resp.StatusCode != 200 || json.Unmarshal(raw, &in) != nil {
		p.e.t.Fatalf("GET inbox = %d %s", resp.StatusCode, raw)
	}
	return in
}

func inboxIDs(in application.ApprovalInbox) []string {
	out := []string{}
	for _, it := range in.Items {
		out = append(out, it.ID)
	}
	return out
}

func TestLeadDecidesDepartmentApprovals(t *testing.T) {
	p := newOfficeApprovals(t)
	for _, id := range []string{"ap-legal-1", "ap-legal-2"} {
		p.approval(id, "legal")
	}
	p.approval("ap-fin", "accounting")
	p.approval("ap-sales-1", "sales")
	p.approval("ap-sales-2", "sales")

	// an office with a responsible: only its responsible decides, even an admin outside it is refused
	p.decide("ap-legal-1", p.beto, 403)
	p.decide("ap-legal-1", p.owner.AccessToken, 403)
	p.decide("ap-legal-1", p.ana, 200) // a member: the responsible decides without approvals:decide
	resp, raw := p.e.do("POST", api1+"/approvals/ap-legal-2/decision", p.beto, map[string]any{"decision": "reject"}, nil)
	if resp.StatusCode != 403 {
		t.Fatalf("an admin outside the office rejected: %d %s", resp.StatusCode, raw)
	}

	// an office without a responsible: its admins decide
	p.decide("ap-fin", p.ana, 403)
	p.decide("ap-fin", p.beto, 200)

	// General: today's rules (approvals:decide)
	p.decide("ap-sales-1", p.ana, 403)
	p.decide("ap-sales-1", p.beto, 200)

	// the inbox: ana may decide the one of her office, not General's; beto the other way round, and he is told who decides
	in := p.inbox(p.ana, "decidable")
	if ids := inboxIDs(in); !slices.Equal(ids, []string{"ap-legal-2"}) {
		t.Fatalf("ana's decidable = %v", ids)
	}
	in = p.inbox(p.beto, "")
	var legal *application.InboxItem
	for i := range in.Items {
		if in.Items[i].ID == "ap-legal-2" {
			legal = &in.Items[i]
		}
		if in.Items[i].ID == "ap-sales-2" && !in.Items[i].CanDecide {
			t.Fatalf("beto may not decide a General approval: %+v", in.Items[i])
		}
	}
	anaName := p.e.json("GET", api1+"/auth/me", p.ana, nil, 200)["user"].(map[string]any)["name"].(string)
	if legal == nil || legal.CanDecide || anaName == "" || !slices.Equal(legal.Approvers, []string{anaName}) {
		t.Fatalf("beto's item of Legal = %+v", legal)
	}
	if ids := inboxIDs(p.inbox(p.beto, "decidable")); slices.Contains(ids, "ap-legal-2") || !slices.Contains(ids, "ap-sales-2") {
		t.Fatalf("beto's decidable = %v", ids)
	}
}

func TestApprovalsListHidesPrivateDepartments(t *testing.T) {
	p := newOfficeApprovals(t)
	p.approval("ap-priv", "hr")
	p.approval("ap-gen", "sales")

	list := func(token string) string {
		resp, raw := p.e.do("GET", api1+"/approvals", token, nil, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("GET /approvals = %d %s", resp.StatusCode, raw)
		}
		return string(raw)
	}
	// beto (admin, not a member): neither in the list nor in the inbox, and deciding it answers like a missing one
	if got := list(p.beto); strings.Contains(got, "ap-priv") || !strings.Contains(got, "ap-gen") {
		t.Fatalf("beto's approvals: %s", got)
	}
	if ids := inboxIDs(p.inbox(p.beto, "")); slices.Contains(ids, "ap-priv") || !slices.Contains(ids, "ap-gen") {
		t.Fatalf("beto's inbox = %v", ids)
	}
	if c := p.inbox(p.beto, ""); c.Counts.All != 1 {
		t.Fatalf("beto's counts = %+v", c.Counts)
	}
	p.decide("ap-priv", p.beto, 404)
	p.decide("ap-priv", p.owner.AccessToken, 404)

	// carla (its responsible) sees it and decides it
	if got := list(p.carla); !strings.Contains(got, "ap-priv") {
		t.Fatalf("carla's approvals: %s", got)
	}
	if ids := inboxIDs(p.inbox(p.carla, "decidable")); !slices.Contains(ids, "ap-priv") {
		t.Fatalf("carla's decidable = %v", ids)
	}
	p.decide("ap-priv", p.carla, 200)
	if ap, _ := p.e.store.GetApproval(context.Background(), p.org, "ap-priv"); ap.Status != domain.ApprovalApproved {
		t.Fatalf("ap-priv = %s", ap.Status)
	}
}
