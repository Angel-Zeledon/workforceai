package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/offices"
	"aiworkforce/backend/internal/vault"
)

// D3: a connection may belong to a department (office). The private office Legal (ana leads it, the agent legal is
// in it) keeps its connections to itself: beto, an admin outside it, neither lists nor opens them (404, like a
// missing one). Only the office's agents may be granted one. The office's lead manages its connections even without
// the admin permissions; nobody else outside the admins does.

type officeConns struct {
	*privateWork
	office string
	carla  string // a plain member, lead of Legal
	dora   string // a plain member outside Legal
}

func newOfficeConns(t *testing.T) *officeConns {
	t.Helper()
	p := newPrivateWork(t, func(o *opts) {
		prev := o.deps
		o.deps = func(d *api.Deps) {
			prev(d)
			cs, err := connections.NewService(connections.Config{Store: connections.NewMemStore(), Vault: vault.New(vault.NewMemRepo(), nil)})
			if err != nil {
				t.Fatal(err)
			}
			svc := d.Offices
			cs.AgentOffice = func(ctx context.Context, org, agent string) (string, error) {
				m, err := svc.Model(ctx, org)
				if err != nil {
					return "", err
				}
				if id := m.Agent(agent).OfficeID; id != offices.GeneralID {
					return id, nil
				}
				return "", nil
			}
			d.Conns = cs
		}
	})
	e := p.e
	oc := &officeConns{privateWork: p,
		carla: e.member(p.owner, "carla-oc@example.com", auth.RoleMember),
		dora:  e.member(p.owner, "dora-oc@example.com", auth.RoleMember)}
	var list struct {
		Offices []struct {
			ID      string `json:"id"`
			General bool   `json:"general"`
		} `json:"offices"`
	}
	if err := json.Unmarshal([]byte(p.body("GET", "/offices", p.owner.AccessToken, 200)), &list); err != nil {
		t.Fatal(err)
	}
	for _, o := range list.Offices {
		if !o.General {
			oc.office = o.ID
		}
	}
	if oc.office == "" {
		t.Fatal("no Legal office")
	}
	e.json("PUT", api1+"/offices/"+oc.office+"/members/"+e.userID(oc.carla), p.owner.AccessToken, map[string]any{"role": "lead"}, 200)
	return oc
}

func (o *officeConns) create(token, label, office string, want int) string {
	o.e.t.Helper()
	body := map[string]any{"provider": connections.DatabaseProvider, "label": label, "mode": "simulated",
		"database": map[string]any{"host": "db.example.test", "user": "reader"}}
	if office != "" {
		body["office_id"] = office
	}
	m := o.e.json("POST", api1+"/connections", token, body, want)
	if want != 201 {
		return ""
	}
	if got, _ := m["office_id"].(string); got != office {
		o.e.t.Fatalf("office_id = %q, want %q", got, office)
	}
	return m["id"].(string)
}

func (o *officeConns) code(method, path, token string, body any) (int, string) {
	o.e.t.Helper()
	resp, raw := o.e.do(method, api1+path, token, body, nil)
	return resp.StatusCode, string(raw)
}

func TestOfficeConnectionsHiddenFromNonMembers(t *testing.T) {
	o := newOfficeConns(t)
	owner := o.owner.AccessToken
	legal := o.create(o.ana, "Base de Legal", o.office, 201)
	shared := o.create(owner, "Base común", "", 201)

	if got := o.body("GET", "/connections", o.ana, 200); !strings.Contains(got, legal) || !strings.Contains(got, shared) {
		t.Fatalf("ana lists both: %s", got)
	}
	o.body("GET", "/connections/"+legal, o.ana, 200)
	for _, tok := range []string{o.beto, owner, o.dora} { // admins outside the office included
		if got := o.body("GET", "/connections", tok, 200); strings.Contains(got, legal) || !strings.Contains(got, shared) {
			t.Fatalf("a non member lists the office's connection: %s", got)
		}
		o.body("GET", "/connections/"+legal, tok, 404)
		o.body("GET", "/connections/"+legal+"/grants", tok, 404)
		o.body("GET", "/connections/"+legal+"/usage", tok, 404)
	}
	for _, tok := range []string{o.beto, owner} {
		if c, raw := o.code("PATCH", "/connections/"+legal, tok, map[string]any{"label": "x"}); c != 404 {
			t.Fatalf("edit by a non member = %d %s", c, raw)
		}
		if c, raw := o.code("PUT", "/connections/"+legal+"/grants/legal", tok, map[string]any{"capabilities": []string{"db.read"}}); c != 404 {
			t.Fatalf("grant by a non member = %d %s", c, raw)
		}
		if c, raw := o.code("POST", "/connections/"+legal+"/revoke", tok, map[string]any{"confirm_name": "Base de Legal"}); c != 404 {
			t.Fatalf("revoke by a non member = %d %s", c, raw)
		}
	}

	// the office's agent can be granted, an agent of another office cannot
	if c, raw := o.code("PUT", "/connections/"+legal+"/grants/sales", o.ana, map[string]any{"capabilities": []string{"db.read"}}); c != 400 ||
		!strings.Contains(raw, connections.CodeAgentOtherOffice) {
		t.Fatalf("grant to another office's agent = %d %s", c, raw)
	}
	o.e.json("PUT", api1+"/connections/"+legal+"/grants/legal", o.ana, map[string]any{"capabilities": []string{"db.read"}}, 200)
	if got := o.body("GET", "/connections/"+legal+"/grants", o.ana, 200); !strings.Contains(got, `"agent_id":"legal"`) {
		t.Fatalf("grants: %s", got)
	}
	// the agent's connections leave out what the person may not see
	if got := o.body("GET", "/agents/legal/connections", o.ana, 200); !strings.Contains(got, legal) {
		t.Fatalf("ana: %s", got)
	}
	if c, raw := o.code("GET", "/agents/legal/connections", o.beto, nil); c == 200 && strings.Contains(raw, legal) {
		t.Fatalf("beto sees the office's connection through the agent: %s", raw)
	}
	// moving it to General shows it to everybody again
	o.e.json("PATCH", api1+"/connections/"+legal, o.ana, map[string]any{"office_id": ""}, 200)
	o.body("GET", "/connections/"+legal, o.beto, 200)
}

func TestOfficeLeadManagesOfficeConnections(t *testing.T) {
	o := newOfficeConns(t)
	owner := o.owner.AccessToken
	shared := o.create(owner, "Base común", "", 201)

	// carla (a plain member) leads Legal: she creates and manages Legal's connections, nothing else
	legal := o.create(o.carla, "Base de Legal", o.office, 201)
	o.create(o.carla, "Sin oficina", "", 403)
	o.create(o.dora, "De Dora", o.office, 403)
	o.e.json("PATCH", api1+"/connections/"+legal, o.carla, map[string]any{"label": "Base Legal"}, 200)
	o.e.json("PUT", api1+"/connections/"+legal+"/grants/legal", o.carla, map[string]any{"capabilities": []string{"db.read"}}, 200)
	o.body("GET", "/connections/"+legal+"/usage", o.carla, 200)
	if c, raw := o.code("PATCH", "/connections/"+legal, o.carla, map[string]any{"office_id": ""}); c != 403 {
		t.Fatalf("a lead cannot hand the connection to General = %d %s", c, raw)
	}
	if c, raw := o.code("PATCH", "/connections/"+shared, o.carla, map[string]any{"label": "x"}); c != 403 {
		t.Fatalf("a lead cannot edit a connection outside the office = %d %s", c, raw)
	}
	if c, raw := o.code("PATCH", "/connections/"+shared, o.carla, map[string]any{"office_id": o.office}); c != 403 {
		t.Fatalf("a lead cannot take a General connection = %d %s", c, raw)
	}
	if c, raw := o.code("PUT", "/connections/"+shared+"/grants/legal", o.carla, map[string]any{"capabilities": []string{"db.read"}}); c != 403 {
		t.Fatalf("a lead cannot grant outside the office = %d %s", c, raw)
	}
	// an unknown office is refused; General means no office
	o.create(owner, "Rara", "no-such-office", 400)
	gen := o.e.json("POST", api1+"/connections", owner, map[string]any{"provider": connections.DatabaseProvider, "label": "General",
		"mode": "simulated", "office_id": "general", "database": map[string]any{"host": "db.example.test", "user": "reader"}}, 201)
	if gen["office_id"] != nil && gen["office_id"] != "" {
		t.Fatalf("general is no office: %v", gen["office_id"])
	}
	// the admin moves a connection into the office (it then leaves the admin's view: ana sees it)
	o.e.json("PATCH", api1+"/connections/"+shared, owner, map[string]any{"office_id": o.office}, 200)
	o.body("GET", "/connections/"+shared, o.ana, 200)
	o.body("GET", "/connections/"+shared, owner, 404)
}
