package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/offices"
	"aiworkforce/backend/internal/orgchart"
)

// withOffices mounts the E2 offices over the org chart and the auth members of the caller's organization.
func withOffices(d *api.Deps) { withOfficesAs(d, nil) }

// withOfficesAs is withOffices reading the people as reader when the request has no person (the public API's token;
// the server reads them by organization).
func withOfficesAs(d *api.Deps, reader func() auth.Principal) {
	members := func(ctx context.Context) ([]auth.Member, error) {
		p, ok := auth.PrincipalFrom(ctx)
		if !ok && reader != nil {
			p = reader()
		}
		return d.Auth.ListMembers(ctx, p)
	}
	d.OrgChart.People = func(ctx context.Context, _ string) ([]orgchart.Person, error) {
		ms, err := members(ctx)
		out := []orgchart.Person{}
		for _, m := range ms {
			out = append(out, orgchart.Person{UserID: m.UserID, Name: m.Name, Role: string(m.Role)})
		}
		return out, err
	}
	st := d.OrgChart.Store
	d.Offices = offices.New(offices.Config{Store: offices.NewMemStore(), TTL: 1,
		Departments: func(ctx context.Context, org string) ([]offices.Dept, error) {
			ds, err := st.ListDepartments(ctx, org)
			out := []offices.Dept{}
			for _, x := range ds {
				dd := offices.Dept{ID: x.ID, Name: x.Name}
				if x.Head.Kind == orgchart.KindPerson {
					dd.HeadUser = x.Head.ID
				}
				out = append(out, dd)
			}
			return out, err
		},
		Placements: func(ctx context.Context, org string) (map[string]string, error) {
			ps, err := st.ListPlacements(ctx, org)
			out := map[string]string{}
			for _, p := range ps {
				out[p.UserID] = p.DepartmentID
			}
			return out, err
		},
		AgentDepts: func(ctx context.Context, org string) (map[string]string, error) {
			js, err := st.ListJobs(ctx, org)
			out := map[string]string{}
			for _, j := range js {
				out[j.AgentID] = j.DepartmentID
			}
			return out, err
		},
		People: func(ctx context.Context, _ string) ([]offices.Person, error) {
			ms, err := members(ctx)
			out := []offices.Person{}
			for _, m := range ms {
				out = append(out, offices.Person{UserID: m.UserID, Name: m.Name, Admin: m.Role == auth.RoleOwner || m.Role == auth.RoleAdmin})
			}
			return out, err
		},
		Agents: func(ctx context.Context, org string) ([]offices.AgentRef, error) {
			as, err := d.Store.ListAgents(ctx, org)
			out := []offices.AgentRef{}
			for _, a := range as {
				out = append(out, offices.AgentRef{ID: a.ID, Name: a.Name, Role: a.Role, Title: a.Title})
			}
			return out, err
		},
	})
}

func (e *env) userID(token string) string {
	e.t.Helper()
	return e.json("GET", api1+"/auth/me", token, nil, 200)["user"].(map[string]any)["id"].(string)
}

func (e *env) agentIDs(token string) []string {
	e.t.Helper()
	resp, raw := e.do("GET", api1+"/agents", token, nil, nil)
	if resp.StatusCode != 200 {
		e.t.Fatalf("GET /agents: %d %s", resp.StatusCode, raw)
	}
	var agents []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &agents); err != nil {
		e.t.Fatal(err)
	}
	var ids []string
	for _, a := range agents {
		ids = append(ids, a.ID)
	}
	return ids
}

func has(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func TestOfficesAPIFlow(t *testing.T) {
	e := newEnv(t, opts{auth: true, orgchart: true, deps: withOffices})
	owner := e.register("owner-of@example.com", "OwnerOF")
	ana := e.member(owner, "ana-of@example.com", auth.RoleMember)
	luis := e.member(owner, "luis-of@example.com", auth.RoleMember)
	marta := e.member(owner, "marta-of@example.com", auth.RoleMember)
	anaID, luisID, martaID := e.userID(ana), e.userID(luis), e.userID(marta)

	// nothing changes until there is a second office: General with everybody, every agent for everybody
	got := e.json("GET", api1+"/offices", marta, nil, 200)
	if offs := got["offices"].([]any); len(offs) != 1 || got["restricted"] != false {
		t.Fatalf("offices = %v", got)
	}
	before := e.agentIDs(marta)
	if !has(before, "sales") {
		t.Fatalf("agents = %v", before)
	}
	_, raw := e.do("GET", api1+"/agents", marta, nil, nil)
	if strings.Contains(string(raw), `"access"`) {
		t.Fatal("GET /agents must not change without offices")
	}

	// Ventas becomes a private office led by Ana, with Luis
	dep := e.json("POST", api1+"/org/departments", owner.AccessToken, map[string]any{"name": "Ventas", "head": map[string]any{"kind": "person", "id": anaID}}, 201)
	depID := dep["id"].(string)
	e.json("PUT", api1+"/org/people/"+luisID, owner.AccessToken, map[string]any{"department_id": depID}, 200)
	e.json("PUT", api1+"/agents/sales/job", owner.AccessToken, map[string]any{"department_id": depID}, 200)
	e.json("POST", api1+"/offices", ana, map[string]any{"department_id": depID}, 403)
	o := e.json("POST", api1+"/offices", owner.AccessToken, map[string]any{"department_id": depID, "visibility": "private"}, 201)
	if o["visibility"] != "private" || !strings.Contains(mustJSON(t, o["leads"]), anaID) || o["member_count"].(float64) != 2 {
		t.Fatalf("office = %v", o)
	}
	e.json("POST", api1+"/offices", owner.AccessToken, map[string]any{"department_id": depID}, 409)

	// Marta sees the door, not the inside, nor its agent
	for _, x := range e.json("GET", api1+"/offices", marta, nil, 200)["offices"].([]any) {
		ov := x.(map[string]any)
		if ov["id"] == depID && (ov["can_enter"] != false || len(ov["members"].([]any)) != 0) {
			t.Fatalf("private office from outside: %v", ov)
		}
	}
	if has(e.agentIDs(marta), "sales") || len(e.agentIDs(marta)) != len(before)-1 {
		t.Fatalf("marta sees %v", e.agentIDs(marta))
	}
	e.json("GET", api1+"/agents/sales", marta, nil, 404)
	e.json("GET", api1+"/agents/sales/detail", marta, nil, 404)
	e.json("POST", api1+"/messages", marta, map[string]any{"conversation": "agent:sales", "text": "hola"}, 403)
	if !has(e.agentIDs(luis), "sales") {
		t.Fatal("a member uses the office's agent")
	}
	_, raw = e.do("GET", api1+"/agents", luis, nil, nil)
	if !strings.Contains(string(raw), `"rights":{"see":true,"use":true,"edit":false}`) {
		t.Fatalf("access of luis: %s", raw)
	}

	// the responsible adds Marta; members cannot change who uses the agent, the responsible can
	e.json("PUT", api1+"/offices/"+depID+"/members/"+martaID, luis, map[string]any{"role": "member"}, 403)
	e.json("PUT", api1+"/offices/"+depID+"/members/"+martaID, ana, map[string]any{"role": "member"}, 200)
	if !has(e.agentIDs(marta), "sales") {
		t.Fatal("marta is a member now")
	}
	e.json("PUT", api1+"/agents/sales/access", marta, map[string]any{"visibility": "directory"}, 403)
	acc := e.json("PUT", api1+"/agents/sales/access", ana, map[string]any{"visibility": "directory"}, 200)
	if acc["visibility"] != "directory" || acc["office_name"] != "Ventas" {
		t.Fatalf("access = %v", acc)
	}
	dir := mustJSON(t, e.json("GET", api1+"/agents/directory", marta, nil, 200)["agents"])
	if !strings.Contains(dir, `"id":"sales"`) || !strings.Contains(dir, `"office_name":"Ventas"`) {
		t.Fatalf("directory = %s", dir)
	}
	// a personal private agent: only its owner uses it; the admin lists it but cannot chat with it
	e.json("PUT", api1+"/agents/accounting/access", owner.AccessToken, map[string]any{"owner_kind": "person", "owner_id": luisID, "visibility": "private"}, 200)
	if has(e.agentIDs(marta), "accounting") || !has(e.agentIDs(luis), "accounting") || !has(e.agentIDs(owner.AccessToken), "accounting") {
		t.Fatal("personal private agent visibility")
	}
	e.json("POST", api1+"/messages", owner.AccessToken, map[string]any{"conversation": "agent:accounting", "text": "hola"}, 403)

	// an office that still owns agents cannot be closed
	e.json("DELETE", api1+"/offices/"+depID, owner.AccessToken, nil, 409)
	e.json("PUT", api1+"/offices/"+depID, ana, map[string]any{"visibility": "open"}, 200)
	e.json("PUT", api1+"/offices/general", owner.AccessToken, map[string]any{"visibility": "private"}, 400)
}

func TestTeamNotesAPI(t *testing.T) {
	e := newEnv(t, opts{auth: true, orgchart: true, deps: withOffices})
	owner := e.register("owner-tn@example.com", "OwnerTN")
	ana := e.member(owner, "ana-tn@example.com", auth.RoleMember)
	marta := e.member(owner, "marta-tn@example.com", auth.RoleMember)
	anaID, martaID := e.userID(ana), e.userID(marta)
	dep := e.json("POST", api1+"/org/departments", owner.AccessToken, map[string]any{"name": "Legal", "head": map[string]any{"kind": "person", "id": anaID}}, 201)
	depID := dep["id"].(string)
	e.json("POST", api1+"/offices", owner.AccessToken, map[string]any{"department_id": depID, "visibility": "private"}, 201)

	n := e.json("POST", api1+"/memory/notes", ana, map[string]any{"scope": "office", "ref": depID, "text": "Plazo de respuesta: 48 horas"}, 201)
	if n["author_name"] == "" || n["can_delete"] != true {
		t.Fatalf("note = %v", n)
	}
	e.json("POST", api1+"/memory/notes", marta, map[string]any{"scope": "office", "ref": depID, "text": "x"}, 403)
	e.json("GET", api1+"/memory/notes?scope=office&ref="+depID, marta, nil, 403)
	l := e.json("GET", api1+"/memory/notes?scope=office&ref="+depID, ana, nil, 200)
	if len(l["notes"].([]any)) != 1 || l["can_promote"] != true {
		t.Fatalf("list = %v", l)
	}
	// promoted: the company reads it
	e.json("POST", api1+"/memory/notes/"+n["id"].(string)+"/promote", ana, nil, 201)
	if l := e.json("GET", api1+"/memory/notes?scope=office&ref="+depID, ana, nil, 200); l["notes"].([]any)[0].(map[string]any)["promoted"] != true {
		t.Fatalf("the office note says it was promoted: %v", l)
	}
	if c := e.json("GET", api1+"/memory/notes?scope=company", marta, nil, 200); len(c["notes"].([]any)) != 1 || c["can_write"] != false {
		t.Fatalf("company = %v", c)
	}
	// personal memory: only its owner
	p := e.json("POST", api1+"/memory/notes", marta, map[string]any{"scope": "person", "ref": anaID, "text": "Prefiero respuestas cortas"}, 201)
	if p["owner_ref"] != martaID {
		t.Fatalf("personal note owner = %v", p["owner_ref"])
	}
	if l := e.json("GET", api1+"/memory/notes?scope=person", ana, nil, 200); len(l["notes"].([]any)) != 0 {
		t.Fatal("ana reads marta's memory")
	}
	e.json("DELETE", api1+"/memory/notes/"+p["id"].(string), ana, nil, 404)
	e.json("DELETE", api1+"/memory/notes/"+p["id"].(string), marta, nil, 204)
	e.json("POST", api1+"/memory/notes", marta, map[string]any{"scope": "person", "text": "   "}, 400)
}

func TestOfficeLayoutPermission(t *testing.T) {
	e := newEnv(t, opts{auth: true, orgchart: true, rewards: true, deps: withOffices})
	owner := e.register("owner-ol@example.com", "OwnerOL")
	ana := e.member(owner, "ana-ol@example.com", auth.RoleMember)
	luis := e.member(owner, "luis-ol@example.com", auth.RoleMember)
	marta := e.member(owner, "marta-ol@example.com", auth.RoleMember)
	anaID, luisID := e.userID(ana), e.userID(luis)
	dep := e.json("POST", api1+"/org/departments", owner.AccessToken, map[string]any{"name": "Legal", "head": map[string]any{"kind": "person", "id": anaID}}, 201)
	depID := dep["id"].(string)
	e.json("POST", api1+"/offices", owner.AccessToken, map[string]any{"department_id": depID, "visibility": "private"}, 201)
	e.json("PUT", api1+"/offices/"+depID+"/members/"+luisID, ana, map[string]any{"role": "member"}, 200)

	path := api1 + "/office/layout?office=" + depID
	body := map[string]any{"layout": map[string]any{"v": 1, "floor": map[string]any{"kind": "wood"}}, "base_version": 0}
	if d := e.json("GET", path, luis, nil, 200); d["version"].(float64) != 0 {
		t.Fatalf("new office layout = %v", d)
	}
	e.json("GET", path, marta, nil, 404) // a private office you are not in
	e.json("PUT", path, luis, body, 403) // members look, the responsible arranges
	if d := e.json("PUT", path, ana, body, 200); d["version"].(float64) != 1 {
		t.Fatalf("saved = %v", d)
	}
	if d := e.json("GET", api1+"/office/layout", marta, nil, 200); d["version"].(float64) != 0 {
		t.Fatal("General's layout is untouched")
	}
	e.json("GET", api1+"/office/layout?office=ghost", ana, nil, 404)
}
