package api_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/customers"
)

// customerTestWork reads the requests of the test store (no projects in these tests).
type customerTestWork struct{ store application.Store }

func (w customerTestWork) Projects(context.Context, string) ([]customers.ProjectInfo, error) {
	return nil, nil
}
func (w customerTestWork) ProjectCustomer(context.Context, string, string) (string, error) {
	return "", nil
}
func (w customerTestWork) Request(ctx context.Context, org, id string) (customers.RequestInfo, error) {
	r, err := w.store.GetRequest(ctx, org, id)
	return customers.RequestInfo{ID: r.ID, Text: r.Text, Status: string(r.Status), CreatedAt: r.CreatedAt}, err
}

// B12: the customer district over HTTP: roles, tenant isolation, scoped requests, clones, archive and audit.
func TestCustomerDistrictOverHTTP(t *testing.T) {
	e := newEnv(t, opts{auth: true, customers: true})
	owner := e.register("owner-c@example.com", "OwnerC")
	admin := e.member(owner, "admin-c@example.com", auth.RoleAdmin)
	member := e.member(owner, "member-c@example.com", auth.RoleMember)
	viewer := e.member(owner, "viewer-c@example.com", auth.RoleViewer)
	rival := e.register("rival-c@example.com", "RivalC")

	if c := e.status("POST", api1+"/customers", viewer, map[string]any{"name": "Ferretería López"}); c != 403 {
		t.Fatalf("a viewer cannot register customers: %d", c)
	}
	a := e.json("POST", api1+"/customers", member, map[string]any{"name": "  Ferretería López ", "tax_id": "LOFE800101AB1", "notes": "Cierre el día 5"}, 201)
	if a["name"] != "Ferretería López" || !strings.HasPrefix(a["color"].(string), "#") || a["archived"] != false {
		t.Fatalf("created customer: %v", a)
	}
	id := a["id"].(string)
	if c := e.status("POST", api1+"/customers", member, map[string]any{"name": "ferretería lópez"}); c != 409 {
		t.Fatalf("duplicate name must conflict: %d", c)
	}
	if c := e.status("POST", api1+"/customers", member, map[string]any{"name": "X", "color": "red"}); c != 400 {
		t.Fatalf("invalid color: %d", c)
	}
	b := e.json("POST", api1+"/customers", member, map[string]any{"name": "Panadería San José", "color": "#AA3300"}, 201)
	if b["color"] != "#aa3300" {
		t.Fatalf("color normalized: %v", b)
	}

	// Tenant isolation: another organization sees nothing.
	if got := e.list(api1+"/customers", rival.AccessToken); len(got) != 0 {
		t.Fatalf("rival org sees customers: %v", got)
	}
	if c := e.status("GET", api1+"/customers/"+id, rival.AccessToken, nil); c != 404 {
		t.Fatalf("rival org reads a customer: %d", c)
	}
	if got := e.list(api1+"/customers", viewer); len(got) != 2 || got[0]["name"] != "Ferretería López" {
		t.Fatalf("viewer lists customers by name: %v", got)
	}

	// A request scoped to the customer (unknown customer: refused, nothing created).
	before := len(e.list(api1+"/requests", owner.AccessToken))
	if c := e.status("POST", api1+"/requests", member, map[string]any{"text": "cierre", "customer_id": "cus_nope"}); c != 404 {
		t.Fatalf("unknown customer: %d", c)
	}
	if after := len(e.list(api1+"/requests", owner.AccessToken)); after != before {
		t.Fatalf("a refused scope created a request: %d -> %d", before, after)
	}
	rq := e.json("POST", api1+"/requests", member, map[string]any{"text": "Cierre de mes de la ferretería", "customer_id": id}, 202)
	det := e.json("GET", api1+"/customers/"+id, viewer, nil, 200)
	reqs := det["requests"].([]any)
	if len(reqs) != 1 || reqs[0].(map[string]any)["id"] != rq["request_id"] {
		t.Fatalf("the scoped request shows in the customer view: %v", det)
	}
	if st := det["status"].(string); !slices.Contains([]string{"on_track", "waiting_approval", "blocked", "idle"}, st) {
		t.Fatalf("status: %v", det)
	}

	// Clones: admins only; default display name; one per agent.
	if c := e.status("POST", api1+"/customers/"+id+"/assignments", member, map[string]any{"agent_id": "finance"}); c != 403 {
		t.Fatalf("a member cannot create clones: %d", c)
	}
	agents := e.list(api1+"/agents", admin)
	agentID := ""
	for _, ag := range agents {
		if ag["id"] != "assistant" {
			agentID = ag["id"].(string)
			break
		}
	}
	cl := e.json("POST", api1+"/customers/"+id+"/assignments", admin, map[string]any{"agent_id": agentID}, 201)
	if !strings.HasSuffix(cl["display_name"].(string), " · Ferretería López") || cl["customer_id"] != id {
		t.Fatalf("clone: %v", cl)
	}
	if c := e.status("POST", api1+"/customers/"+id+"/assignments", admin, map[string]any{"agent_id": agentID}); c != 409 {
		t.Fatalf("duplicate clone: %d", c)
	}
	if c := e.status("POST", api1+"/customers/"+id+"/assignments", admin, map[string]any{"agent_id": "nobody"}); c != 404 {
		t.Fatalf("clone of an unknown agent: %d", c)
	}
	dist := e.json("GET", api1+"/customers/district", viewer, nil, 200)
	entries := dist["customers"].([]any)
	if len(entries) != 2 {
		t.Fatalf("district: %v", dist)
	}
	for _, x := range entries {
		en := x.(map[string]any)
		if en["customer"].(map[string]any)["id"] == id && en["assignments"].(float64) != 1 {
			t.Fatalf("district counts clones: %v", en)
		}
	}
	if c := e.status("DELETE", api1+"/customers/"+id+"/assignments/"+cl["id"].(string), admin, nil); c != 204 {
		t.Fatalf("delete clone: %d", c)
	}

	// Edit, archive (admins), then no new work for an archived customer.
	up := e.json("PUT", api1+"/customers/"+id, member, map[string]any{"notes": "Cierre el día 7"}, 200)
	if up["notes"] != "Cierre el día 7" || up["name"] != "Ferretería López" {
		t.Fatalf("update: %v", up)
	}
	if c := e.status("DELETE", api1+"/customers/"+id, member, nil); c != 403 {
		t.Fatalf("a member cannot archive: %d", c)
	}
	if c := e.status("DELETE", api1+"/customers/"+id, admin, nil); c != 204 {
		t.Fatalf("archive: %d", c)
	}
	if c := e.status("POST", api1+"/requests", member, map[string]any{"text": "otra", "customer_id": id}); c != 409 {
		t.Fatalf("an archived customer takes no new work: %d", c)
	}
	if got := e.list(api1+"/customers", viewer); len(got) != 1 {
		t.Fatalf("archived customers are hidden by default: %v", got)
	}
	if got := e.list(api1+"/customers?include_archived=1", viewer); len(got) != 2 {
		t.Fatalf("include_archived lists them: %v", got)
	}
	for _, want := range []string{"customer.created", "customer.request.scoped", "customer.clone.created", "customer.clone.deleted", "customer.updated", "customer.archived"} {
		if !slices.Contains(*e.custAudit, want) {
			t.Fatalf("audit lacks %s: %v", want, *e.custAudit)
		}
	}
}
