package api_test

import (
	"context"
	"slices"
	"sync"
	"testing"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
	rolecat "aiworkforce/backend/internal/roles"
)

// F2: ready-to-hire teams (GET /teams, GET /teams/{key}, POST /teams/{key}/install).

type teamAgentView struct {
	Role     string `json:"role"`
	Title    string `json:"title"`
	Does     string `json:"does"`
	Status   string `json:"status"`
	Name     string `json:"name"`
	Autonomy string `json:"autonomy"`
	Existing *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"existing"`
}

type teamPreviewBody struct {
	Team struct {
		Key        string `json:"key"`
		Name       string `json:"name"`
		For        string `json:"for"`
		Solves     string `json:"solves"`
		Department string `json:"department"`
	} `json:"team"`
	Destination struct {
		OfficeID          string `json:"office_id"`
		OfficeName        string `json:"office_name"`
		SuggestedOfficeID string `json:"suggested_office_id"`
	} `json:"destination"`
	Agents        []teamAgentView `json:"agents"`
	NewCount      int             `json:"new_count"`
	ExistingCount int             `json:"existing_count"`
	Skills        []string        `json:"skills"`
	Week          []struct {
		Template string `json:"template"`
		Name     string `json:"name"`
		Day      int    `json:"day"`
		Why      string `json:"why"`
	} `json:"week"`
	CanInstall bool `json:"can_install"`
}

type teamInstallBody struct {
	Created []struct {
		ID   string `json:"id"`
		Role string `json:"role"`
		Name string `json:"name"`
	} `json:"created"`
	Preview teamPreviewBody `json:"preview"`
}

type teamCard struct {
	Key           string `json:"key"`
	Name          string `json:"name"`
	NewCount      int    `json:"new_count"`
	ExistingCount int    `json:"existing_count"`
}

func (e *teamsEnv) addMember(owner *auth.Session, email string, role auth.Role) string {
	e.t.Helper()
	ctx := context.Background()
	e.signup(auth.RegisterInput{Email: email, Name: "Otra", OrgName: "Otra " + email, Audience: auth.AudienceTeam, Locale: "es"})
	p := auth.Principal{UserID: owner.User.ID, OrgID: owner.OrgID, Role: auth.RoleOwner}
	if _, err := e.svc.AddMember(ctx, p, email, role); err != nil {
		e.t.Fatalf("add member: %v", err)
	}
	s, err := e.svc.Login(ctx, auth.LoginInput{Email: email, Password: goodPW, OrgID: owner.OrgID}, auth.ClientMeta{})
	if err != nil {
		e.t.Fatalf("login: %v", err)
	}
	return s.AccessToken
}

func newTeamCatalogEnv(t *testing.T) (*teamsEnv, *auth.Session) {
	t.Helper()
	e := newTeamsEnv(t, withOffices)
	return e, e.signup(auth.RegisterInput{Email: "dueno@empresa.com", Name: "Dueño", OrgName: "Empresa", Audience: auth.AudienceTeam, Locale: "es"})
}

func TestTeamGalleryListsTheBusinessTeams(t *testing.T) {
	e, s := newTeamCatalogEnv(t)
	var cards []teamCard
	if c := e.call("GET", "/api/v1/teams", s.AccessToken, nil, &cards); c != 200 {
		t.Fatalf("GET /teams = %d", c)
	}
	var keys []string
	for _, c := range cards {
		if c.Name == "" {
			t.Fatalf("card without name: %+v", c)
		}
		keys = append(keys, c.Key)
	}
	for _, want := range []string{"month_close", "collections", "proposal", "agency", "online_store"} {
		if !slices.Contains(keys, want) {
			t.Fatalf("gallery lacks %q: %v", want, keys)
		}
	}
	var forPack []teamCard
	e.call("GET", "/api/v1/teams?pack=agency", s.AccessToken, nil, &forPack)
	if len(forPack) != 1 || forPack[0].Key != "agency" {
		t.Fatalf("pack filter = %+v", forPack)
	}
	var one teamPreviewBody
	if c := e.call("GET", "/api/v1/teams/collections?locale=en", s.AccessToken, nil, &one); c != 200 || one.Team.Name != "Collections" {
		t.Fatalf("english preview = %d %+v", c, one.Team)
	}
	if c := e.call("GET", "/api/v1/teams/nope", s.AccessToken, nil, nil); c != 404 {
		t.Fatalf("unknown team = %d", c)
	}
	if c := e.call("GET", "/api/v1/teams", "", nil, nil); c != 401 {
		t.Fatalf("anonymous = %d", c)
	}
}

func TestTeamPreviewWithAndWithoutExistingAgents(t *testing.T) {
	e, s := newTeamCatalogEnv(t)
	// a new organization only has its assistant: collections = accounting + sales + assistant
	var p teamPreviewBody
	if c := e.call("GET", "/api/v1/teams/collections", s.AccessToken, nil, &p); c != 200 {
		t.Fatalf("preview = %d", c)
	}
	if p.NewCount != 2 || p.ExistingCount != 1 || len(p.Agents) != 3 || !p.CanInstall {
		t.Fatalf("preview = %+v", p)
	}
	for _, a := range p.Agents {
		switch a.Role {
		case "assistant":
			if a.Status != "existing" || a.Existing == nil || a.Existing.ID == "" {
				t.Fatalf("assistant = %+v", a)
			}
		default:
			if a.Status != "new" || a.Name == "" || a.Does == "" || a.Autonomy == "" {
				t.Fatalf("%s = %+v", a.Role, a)
			}
		}
	}
	if len(p.Skills) == 0 || len(p.Week) < 3 || p.Week[0].Name == "" || p.Week[0].Why == "" {
		t.Fatalf("skills/week missing: %+v", p)
	}
	if p.Destination.OfficeID != "general" {
		t.Fatalf("destination = %+v", p.Destination)
	}
	// the gallery card says the same
	var cards []teamCard
	e.call("GET", "/api/v1/teams", s.AccessToken, nil, &cards)
	for _, c := range cards {
		if c.Key == "collections" && (c.NewCount != 2 || c.ExistingCount != 1) {
			t.Fatalf("card = %+v", c)
		}
	}
}

func TestTeamInstallIsIdempotentAndKeepsTheRoleRules(t *testing.T) {
	e, s := newTeamCatalogEnv(t)
	var first teamInstallBody
	if c := e.call("POST", "/api/v1/teams/collections/install", s.AccessToken, map[string]any{}, &first); c != 201 {
		t.Fatalf("install = %d", c)
	}
	if len(first.Created) != 2 || first.Preview.NewCount != 0 || first.Preview.ExistingCount != 3 {
		t.Fatalf("first install = %+v", first)
	}
	roles := e.roles(s.AccessToken)
	if !slices.Equal(roles, []string{"accounting", "assistant", "sales"}) {
		t.Fatalf("roles = %v", roles)
	}
	// same autonomy as hiring the role by hand: the role's default, never more
	var agents []struct {
		ID       string `json:"id"`
		Role     string `json:"role"`
		Autonomy string `json:"autonomy"`
	}
	e.call("GET", "/api/v1/agents", s.AccessToken, nil, &agents)
	for _, a := range agents {
		if tpl, ok := rolecat.Get(a.Role); a.Role == "sales" && (!ok || a.Autonomy != tpl.Autonomy.Default) {
			t.Fatalf("sales starts at %q, not at the role default", a.Autonomy)
		}
	}
	var second teamInstallBody
	if c := e.call("POST", "/api/v1/teams/collections/install", s.AccessToken, map[string]any{}, &second); c != 200 {
		t.Fatalf("second install = %d", c)
	}
	if len(second.Created) != 0 || len(e.roles(s.AccessToken)) != 3 {
		t.Fatalf("second install created %d, roles %v", len(second.Created), e.roles(s.AccessToken))
	}
	// installing a team that shares roles hires only what is missing (accounting already there)
	var third teamInstallBody
	e.call("POST", "/api/v1/teams/month_close/install", s.AccessToken, map[string]any{}, &third)
	got := []string{}
	for _, c := range third.Created {
		got = append(got, c.Role)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"analyst", "finance_treasury", "internal_auditor"}) {
		t.Fatalf("month_close created %v", got)
	}
}

func TestTeamInstallConcurrentCallsDoNotDuplicate(t *testing.T) {
	e, s := newTeamCatalogEnv(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.call("POST", "/api/v1/teams/collections/install", s.AccessToken, map[string]any{}, nil)
		}()
	}
	wg.Wait()
	if roles := e.roles(s.AccessToken); !slices.Equal(roles, []string{"accounting", "assistant", "sales"}) {
		t.Fatalf("roles after concurrent installs = %v", roles)
	}
}

func TestTeamPermissions(t *testing.T) {
	e, owner := newTeamCatalogEnv(t)
	admin := e.addMember(owner, "admin@empresa.com", auth.RoleAdmin)
	member := e.addMember(owner, "miembro@empresa.com", auth.RoleMember)
	viewer := e.addMember(owner, "lector@empresa.com", auth.RoleViewer)
	for _, tok := range []string{member, viewer} {
		if c := e.call("GET", "/api/v1/teams/collections", tok, nil, nil); c != 200 {
			t.Fatalf("read = %d", c)
		}
		if c := e.call("POST", "/api/v1/teams/collections/install", tok, map[string]any{}, nil); c != 403 {
			t.Fatalf("install without agents:write = %d", c)
		}
	}
	if len(e.roles(owner.AccessToken)) != 1 {
		t.Fatalf("a refused install hired agents: %v", e.roles(owner.AccessToken))
	}
	if c := e.call("POST", "/api/v1/teams/collections/install", admin, map[string]any{}, nil); c != 201 {
		t.Fatalf("admin install = %d", c)
	}
	if c := e.call("POST", "/api/v1/teams/nope/install", admin, map[string]any{}, nil); c != 404 {
		t.Fatalf("unknown team install = %d", c)
	}
	if c := e.call("POST", "/api/v1/teams/collections/install", "", map[string]any{}, nil); c != 401 {
		t.Fatalf("anonymous install = %d", c)
	}
}

type deptBody struct {
	ID string `json:"id"`
}

// officeFor makes a department an office; private ones have the owner as a member.
func officeFor(t *testing.T, e *teamsEnv, owner *auth.Session, name, visibility string) string {
	t.Helper()
	var d deptBody
	if c := e.call("POST", "/api/v1/org/departments", owner.AccessToken, map[string]any{"name": name}, &d); c != 201 {
		t.Fatalf("department = %d", c)
	}
	if c := e.call("PUT", "/api/v1/org/people/"+owner.User.ID, owner.AccessToken, map[string]any{"department_id": d.ID}, nil); c != 200 {
		t.Fatalf("placement = %d", c)
	}
	if c := e.call("POST", "/api/v1/offices", owner.AccessToken, map[string]any{"department_id": d.ID, "visibility": visibility}, nil); c != 201 {
		t.Fatalf("office = %d", c)
	}
	return d.ID
}

func jobDept(t *testing.T, e *teamsEnv, token, agent string) string {
	t.Helper()
	var j struct {
		DepartmentID string `json:"department_id"`
	}
	if c := e.call("GET", "/api/v1/agents/"+agent+"/job", token, nil, &j); c != 200 {
		t.Fatalf("job = %d", c)
	}
	return j.DepartmentID
}

func TestTeamInstallsIntoTheChosenOfficeWithoutDuplicatingThere(t *testing.T) {
	e, s := newTeamCatalogEnv(t)
	dept := officeFor(t, e, s, "Cobranza", "open")

	// the suggested department is the one named like the team's, and it is the default destination
	var p teamPreviewBody
	e.call("GET", "/api/v1/teams/collections", s.AccessToken, nil, &p)
	if p.Destination.SuggestedOfficeID != dept || p.Destination.OfficeID != dept || p.Destination.OfficeName != "Cobranza" {
		t.Fatalf("destination = %+v", p.Destination)
	}
	// the assistant lives in General: in Cobranza all three are new
	if p.NewCount != 3 || p.ExistingCount != 0 {
		t.Fatalf("preview for Cobranza = %d new / %d existing", p.NewCount, p.ExistingCount)
	}
	var in teamInstallBody
	if c := e.call("POST", "/api/v1/teams/collections/install", s.AccessToken, map[string]any{"office_id": dept}, &in); c != 201 || len(in.Created) != 3 {
		t.Fatalf("install = %d %+v", c, in)
	}
	for _, a := range in.Created {
		if d := jobDept(t, e, s.AccessToken, a.ID); d != dept {
			t.Fatalf("%s is in %q, want %q", a.Role, d, dept)
		}
	}
	// the same install again, with or without naming the office, adds nobody
	var again teamInstallBody
	if c := e.call("POST", "/api/v1/teams/collections/install", s.AccessToken, map[string]any{}, &again); c != 200 || len(again.Created) != 0 {
		t.Fatalf("again = %d %+v", c, again)
	}
	// General still has its own assistant and gets the two others when asked
	var gen teamInstallBody
	if c := e.call("POST", "/api/v1/teams/collections/install", s.AccessToken, map[string]any{"office_id": "general"}, &gen); c != 201 || len(gen.Created) != 2 {
		t.Fatalf("general install = %d %+v", c, gen)
	}
	if c := e.call("POST", "/api/v1/teams/collections/install", s.AccessToken, map[string]any{"office_id": "no-such-office"}, nil); c != 404 {
		t.Fatalf("unknown office = %d", c)
	}
}

func TestTeamInstallRespectsAPrivateDepartment(t *testing.T) {
	e, owner := newTeamCatalogEnv(t)
	dept := officeFor(t, e, owner, "Cobranza", "private")
	outsider := e.addMember(owner, "admin2@empresa.com", auth.RoleAdmin) // an admin who is not in the department
	before := len(e.roles(owner.AccessToken))

	// the outsider (even an admin) cannot see it: not as destination, not as suggestion, and it answers as if absent
	if c := e.call("GET", "/api/v1/teams/collections?office_id="+dept, outsider, nil, nil); c != 404 {
		t.Fatalf("preview into a private department = %d", c)
	}
	if c := e.call("POST", "/api/v1/teams/collections/install", outsider, map[string]any{"office_id": dept}, nil); c != 404 {
		t.Fatalf("install into a private department = %d", c)
	}
	var p teamPreviewBody
	e.call("GET", "/api/v1/teams/collections", outsider, nil, &p)
	if p.Destination.SuggestedOfficeID != "" || p.Destination.OfficeID != "general" {
		t.Fatalf("the private department leaked as a suggestion: %+v", p.Destination)
	}
	var in teamInstallBody
	if c := e.call("POST", "/api/v1/teams/collections/install", outsider, map[string]any{}, &in); c != 201 {
		t.Fatalf("default install = %d", c)
	}
	for _, a := range in.Created {
		if d := jobDept(t, e, owner.AccessToken, a.ID); d == dept {
			t.Fatalf("%s went into the private department", a.Role)
		}
	}
	if len(e.roles(owner.AccessToken)) != before+len(in.Created) {
		t.Fatalf("unexpected agents: %v", e.roles(owner.AccessToken))
	}
	// a member of the department sees and uses it
	if c := e.call("GET", "/api/v1/teams/collections?office_id="+dept, owner.AccessToken, nil, &p); c != 200 || p.Destination.OfficeID != dept {
		t.Fatalf("member preview = %d %+v", c, p.Destination)
	}
	if c := e.call("POST", "/api/v1/teams/collections/install", owner.AccessToken, map[string]any{"office_id": dept}, nil); c != 201 {
		t.Fatalf("member install = %d", c)
	}
}

func TestTeamPreviewCapacityStopsAFullOffice(t *testing.T) {
	e, s := newTeamCatalogEnv(t)
	for i := 0; i < 19; i++ { // the office holds 20 and already has its assistant
		a := domain.Agent{ID: "x" + string(rune('a'+i)), Name: "Extra " + string(rune('a'+i)), Role: "operations", Title: "Operaciones"}
		if err := e.store.CreateAgent(context.Background(), s.OrgID, a); err != nil {
			t.Fatal(err)
		}
	}
	var p teamPreviewBody
	e.call("GET", "/api/v1/teams/collections", s.AccessToken, nil, &p)
	if p.CanInstall {
		t.Fatalf("a full office must not offer the install: %+v", p)
	}
	if c := e.call("POST", "/api/v1/teams/collections/install", s.AccessToken, map[string]any{}, nil); c != 409 {
		t.Fatalf("install into a full office = %d", c)
	}
	if n := len(e.roles(s.AccessToken)); n != 20 {
		t.Fatalf("a refused install must hire nobody: %d agents", n)
	}
}

// orgRecordingStore notes which organization each agent listing asks for.
type orgRecordingStore struct {
	*memory.Store
	mu   sync.Mutex
	orgs map[string]int
}

func (o *orgRecordingStore) ListAgents(ctx context.Context, org string) ([]domain.Agent, error) {
	o.mu.Lock()
	o.orgs[org]++
	o.mu.Unlock()
	return o.Store.ListAgents(ctx, org)
}

func TestTeamPreviewAndInstallReadOnlyTheCallersOrganization(t *testing.T) {
	var rec *orgRecordingStore
	e := newTeamsEnv(t, withOffices, func(d *api.Deps) {
		rec = &orgRecordingStore{Store: d.Store.(*memory.Store), orgs: map[string]int{}}
		d.Store = rec
	})
	a := e.signup(auth.RegisterInput{Email: "a@uno.com", Name: "A", OrgName: "Uno", Audience: auth.AudienceTeam, Locale: "es"})
	b := e.signup(auth.RegisterInput{Email: "b@dos.com", Name: "B", OrgName: "Dos", Audience: auth.AudienceTeam, Locale: "es"})
	if a.OrgID == b.OrgID {
		t.Fatal("two organizations expected")
	}
	rec.mu.Lock()
	rec.orgs = map[string]int{}
	rec.mu.Unlock()
	e.call("GET", "/api/v1/teams/collections", a.AccessToken, nil, nil)
	e.call("POST", "/api/v1/teams/collections/install", b.AccessToken, map[string]any{}, nil)
	e.call("GET", "/api/v1/teams", a.AccessToken, nil, nil)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.orgs[a.OrgID] == 0 || rec.orgs[b.OrgID] == 0 {
		t.Fatalf("each caller's organization must be read: %v", rec.orgs)
	}
	for org := range rec.orgs {
		if org != a.OrgID && org != b.OrgID {
			t.Fatalf("read a foreign organization %q", org)
		}
	}
}
