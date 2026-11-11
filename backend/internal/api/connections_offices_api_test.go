package api_test

import (
	"bytes"
	"context"
	"encoding/base64"
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

func newOfficeConns(t *testing.T, more ...func(d *api.Deps)) *officeConns {
	t.Helper()
	p := newPrivateWork(t, func(o *opts) {
		prev := o.deps
		o.deps = func(d *api.Deps) {
			prev(d)
			kw, _ := vault.NewEnvKeyWrapper(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32)))
			cs, err := connections.NewService(connections.Config{Store: connections.NewMemStore(), Vault: vault.New(vault.NewMemRepo(), kw),
				AdminCount: func(context.Context, string) int { return 3 }}) // owner, ana and beto: write grants need a second human
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
			for _, f := range more {
				f(d)
			}
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

func (o *officeConns) conn(id, token string) map[string]any {
	o.e.t.Helper()
	return o.e.json("GET", api1+"/connections/"+id, token, nil, 200)
}

// Fix round 1: a responsible without connections:manage cannot send a credential an admin stored to another server
// (changing host, port, user or base_url needs the secret again in the same call), nor lift read only.
func TestOfficeLeadCannotRedirectACredential(t *testing.T) {
	o := newOfficeConns(t)
	owner := o.owner.AccessToken
	db := func(host string) map[string]any {
		return map[string]any{"host": host, "port": 5432, "database": "app", "user": "reader", "sslmode": "require"}
	}
	m := o.e.json("POST", api1+"/connections", owner, map[string]any{"provider": connections.DatabaseProvider, "label": "Base real",
		"database": db("db.example.test"), "secret": "admin-password-0001"}, 201)
	id := m["id"].(string)
	o.e.json("PATCH", api1+"/connections/"+id, owner, map[string]any{"office_id": o.office}, 200)
	version := func() float64 { return o.conn(id, o.carla)["credential"].(map[string]any)["version"].(float64) }
	host := func() string { return o.conn(id, o.carla)["database"].(map[string]any)["host"].(string) }

	// the responsible points it elsewhere without the secret: refused, nothing changes
	if c, raw := o.code("PATCH", "/connections/"+id, o.carla, map[string]any{"database": db("evil.example.test")}); c != 400 ||
		!strings.Contains(raw, connections.CodeCredentialRequired) {
		t.Fatalf("redirect without the secret = %d %s", c, raw)
	}
	if host() != "db.example.test" || version() != 1 {
		t.Fatalf("the connection changed: %s v%v", host(), version())
	}
	for field, val := range map[string]any{"port": 6543, "user": "admin"} {
		d := db("db.example.test")
		d[field] = val
		if c, raw := o.code("PATCH", "/connections/"+id, o.carla, map[string]any{"database": d}); c != 400 {
			t.Fatalf("changing %s without the secret = %d %s", field, c, raw)
		}
	}
	// what does not move the credential needs no secret
	d := db("db.example.test")
	d["description"] = "Pedidos"
	o.e.json("PATCH", api1+"/connections/"+id, o.carla, map[string]any{"database": d}, 200)
	// with its own secret it may: the admin's secret is replaced
	o.e.json("PATCH", api1+"/connections/"+id, o.carla, map[string]any{"database": db("other.example.test"), "secret": "lead-password-0002"}, 200)
	if host() != "other.example.test" || version() != 2 {
		t.Fatalf("with the secret: %s v%v", host(), version())
	}
	// an admin, as before
	o.e.json("PATCH", api1+"/connections/"+id, o.ana, map[string]any{"database": db("db.example.test")}, 200)

	// read only: the responsible may turn it on, never off
	o.e.json("PATCH", api1+"/connections/"+id, o.carla, map[string]any{"read_only": true}, 200)
	if c, raw := o.code("PATCH", "/connections/"+id, o.carla, map[string]any{"read_only": false}); c != 403 {
		t.Fatalf("lifting read only = %d %s", c, raw)
	}
	o.e.json("PATCH", api1+"/connections/"+id, o.ana, map[string]any{"read_only": false}, 200)
}

func TestOfficeCustomAPILeadNeedsSecretToMoveBaseURL(t *testing.T) {
	o := newOfficeConns(t)
	src := func(base string) map[string]any {
		return map[string]any{"base_url": base, "auth": map[string]any{"style": "bearer"}}
	}
	m := o.e.json("POST", api1+"/connections", o.ana, map[string]any{"provider": connections.SourceProvider, "label": "API Legal", "kind": "api_key",
		"source": src("https://api.example.test"), "secret": "admin-api-key-000000001", "office_id": o.office}, 201)
	id := m["id"].(string)
	if c, raw := o.code("PATCH", "/connections/"+id, o.carla, map[string]any{"source": src("https://evil.example.test")}); c != 400 ||
		!strings.Contains(raw, connections.CodeCredentialRequired) {
		t.Fatalf("moving base_url without the secret = %d %s", c, raw)
	}
	o.e.json("PATCH", api1+"/connections/"+id, o.carla, map[string]any{"source": src("https://other.example.test"), "secret": "lead-api-key-0000000002"}, 200)
}

// Fix round 1: the second human of a write grant on a department's connection may be its responsible (the first
// grant still needs connections:grant:write).
func TestOfficeLeadCoSignsAWriteGrant(t *testing.T) {
	o := newOfficeConns(t)
	m := o.e.json("POST", api1+"/connections", o.ana, map[string]any{"provider": "google_gmail", "label": "Correo Legal", "mode": "simulated",
		"capabilities": []string{"mail.draft", "mail.send"}, "office_id": o.office}, 201)
	id := m["id"].(string)
	write := map[string]any{"capabilities": []string{"mail.draft"}, "confirm_name": "Correo Legal"}
	if c, raw := o.code("PUT", "/connections/"+id+"/grants/legal", o.carla, write); c != 403 {
		t.Fatalf("the responsible cannot grant write access = %d %s", c, raw)
	}
	o.e.json("PUT", api1+"/connections/"+id+"/grants/legal", o.ana, write, 202)
	if c, raw := o.code("POST", "/connections/"+id+"/grants/legal/approve", o.ana, nil); c != 409 {
		t.Fatalf("the same human cannot co-sign = %d %s", c, raw)
	}
	o.body("POST", "/connections/"+id+"/grants/legal/approve", o.dora, 404)
	g := o.e.json("POST", api1+"/connections/"+id+"/grants/legal/approve", o.carla, nil, 200)
	if g["status"] != connections.GrantActive || g["approved_by"] != o.e.userID(o.carla) {
		t.Fatalf("co-signed grant: %v", g)
	}
}
