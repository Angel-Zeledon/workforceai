package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"slices"
	"sort"
	"testing"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/events"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/orgchart"
)

// E1 "Alta y agentes por rol". The memory store holds ONE office, so every test starts from an empty one (no demo
// seed) and uses a single organization, the way a brand-new account starts.

type teamsEnv struct {
	t     *testing.T
	srv   *httptest.Server
	svc   *auth.Service
	store *memory.Store
	chart *orgchart.Service
}

func newTeamsEnv(t *testing.T, mods ...func(*api.Deps)) *teamsEnv {
	t.Helper()
	cfg := application.DefaultConfig()
	cfg.IdleDelay = 0
	mem := memory.New()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := events.NewHub(log)
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: mem, Pub: events.LocalBus{Hub: hub}, Log: log}
	appr := application.NewApprovals(cfg, mem, rec)
	q := &application.Queries{Store: mem, Cfg: cfg}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	orch := application.NewOrchestrator(ctx, cfg, mem, fakeRuntime{}, memory.NewLocker(), rec, appr, q, log)
	authStore := auth.NewMemoryStore()
	chart := &orgchart.Service{Store: orgchart.NewMemoryStore(), Agents: mem, Writer: mem, DefaultOrg: cfg.OrgID,
		Audit: func(c context.Context, a domain.AuditLog) { rec.Audit(c, a) },
		People: func(c context.Context, org string) ([]orgchart.Person, error) {
			ms, err := authStore.ListMembers(c, org)
			out := []orgchart.Person{}
			for _, m := range ms {
				out = append(out, orgchart.Person{UserID: m.UserID, Name: m.Name, Email: m.Email, Role: string(m.Role)})
			}
			return out, err
		}}
	svc, err := auth.NewService(auth.Config{
		Store: authStore, Secret: []byte("0123456789abcdef0123456789abcdef-test"),
		PasswordParams: auth.PasswordParams{Memory: 8, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32},
		DepartmentExists: func(c context.Context, org, id string) bool {
			ds, _ := chart.Store.ListDepartments(c, org)
			return slices.ContainsFunc(ds, func(d orgchart.Department) bool { return d.ID == id })
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h := auth.NewHandler(svc, auth.HandlerConfig{})
	d := api.Deps{Cfg: cfg, Queries: q, Orch: orch, Approvals: appr, Store: mem, Runtime: fakeRuntime{}, Hub: hub, Log: log,
		AuthEnabled: true, Auth: svc, AuthRoutes: h.Routes(), InvitationRoutes: h.InvitationRoutes(), OrgChart: chart}
	d.Audit = &application.AuditService{Store: mem, Rec: rec, Cfg: cfg}
	d.OrgConfig = &application.OrgConfig{Cfg: cfg, Store: mem, Core: mem, Orch: orch, Rec: rec, Log: log}
	for _, m := range mods {
		m(&d)
	}
	ts := httptest.NewServer(api.NewRouter(d))
	t.Cleanup(ts.Close)
	return &teamsEnv{t: t, srv: ts, svc: svc, store: mem, chart: chart}
}

func (e *teamsEnv) call(method, path, token string, body any, out any) int {
	e.t.Helper()
	ee := &env{t: e.t, srv: e.srv}
	resp, raw := ee.do(method, path, token, body, nil)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			e.t.Fatalf("%s %s: %v (%s)", method, path, err, raw)
		}
	}
	return resp.StatusCode
}

func (e *teamsEnv) signup(in auth.RegisterInput) *auth.Session {
	e.t.Helper()
	in.Password = goodPW
	s, err := e.svc.Register(context.Background(), in, auth.ClientMeta{})
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *teamsEnv) roles(token string) []string {
	e.t.Helper()
	var agents []struct {
		Role string `json:"role"`
	}
	if c := e.call("GET", "/api/v1/agents", token, nil, &agents); c != 200 {
		e.t.Fatalf("agents = %d", c)
	}
	out := []string{}
	for _, a := range agents {
		out = append(out, a.Role)
	}
	sort.Strings(out)
	return out
}

type welcomeView struct {
	Kind         string `json:"kind"`
	Audience     string `json:"audience"`
	OrgName      string `json:"org_name"`
	SetupPending bool   `json:"setup_pending"`
	Profession   string `json:"profession"`
	Team         *struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		HeadName string `json:"head_name"`
		HeadIsMe bool   `json:"head_is_me"`
	} `json:"team"`
	InvitedBy string `json:"invited_by"`
	Agents    []struct {
		ID   string `json:"id"`
		Role string `json:"role"`
		Name string `json:"name"`
	} `json:"agents"`
	Teammates []struct {
		Name string `json:"name"`
	} `json:"teammates"`
	CanPickTeam bool `json:"can_pick_team"`
	Teams       []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"teams"`
}

func (e *teamsEnv) welcome(token string) welcomeView {
	e.t.Helper()
	var w welcomeView
	if c := e.call("GET", "/api/v1/me/welcome", token, nil, &w); c != 200 {
		e.t.Fatalf("welcome = %d", c)
	}
	return w
}

func agentRoles(w welcomeView) []string {
	out := []string{}
	for _, a := range w.Agents {
		out = append(out, a.Role)
	}
	sort.Strings(out)
	return out
}

func TestTeamSignupStartsWithTheAgentsOfTheProfessionAndItsTeam(t *testing.T) {
	e := newTeamsEnv(t)
	s := e.signup(auth.RegisterInput{Email: "ana@despacho.com", Name: "Ana Ruiz", OrgName: "Despacho Ruiz", Audience: auth.AudienceTeam, Profession: "legal", TeamName: "Litigios"})
	if got := e.roles(s.AccessToken); !slices.Equal(got, []string{"assistant", "legal"}) {
		t.Fatalf("a lawyer's new office has %v, want only legal and the assistant", got)
	}
	var depList struct {
		Items []orgchart.Department `json:"items"`
	}
	c := e.call("GET", "/api/v1/org/departments", s.AccessToken, nil, &depList)
	deps := depList.Items
	if c != 200 || len(deps) != 1 || deps[0].Name != "Litigios" ||
		deps[0].Head != (orgchart.Ref{Kind: orgchart.KindPerson, ID: s.User.ID}) {
		t.Fatalf("departments = %d %+v", c, deps)
	}
	w := e.welcome(s.AccessToken)
	if w.Kind != "owner" || !w.SetupPending || w.Team == nil || w.Team.Name != "Litigios" || !w.Team.HeadIsMe || w.Profession != "legal" {
		t.Fatalf("welcome = %+v", w)
	}
	if got := agentRoles(w); !slices.Equal(got, []string{"assistant", "legal"}) {
		t.Fatalf("welcome agents = %v", got)
	}
	if j, _ := e.chart.Job(application.WithOrg(context.Background(), s.OrgID), "legal"); j.DepartmentID != deps[0].ID {
		t.Fatalf("the lawyer agent works in the team: %+v", j)
	}
	// A second request changes nothing (provisioning runs once).
	if got := e.roles(s.AccessToken); len(got) != 2 {
		t.Fatalf("agents after a second request = %v", got)
	}
	if c := e.call("POST", "/api/v1/teams/setup/done", s.AccessToken, map[string]any{}, nil); c != 204 {
		t.Fatalf("setup done = %d", c)
	}
	if w := e.welcome(s.AccessToken); w.SetupPending {
		t.Fatal("setup still pending")
	}
}

func TestSoloSignupHasNoTeamSteps(t *testing.T) {
	e := newTeamsEnv(t)
	s := e.signup(auth.RegisterInput{Email: "luis@example.com", Name: "Luis Mora", Audience: auth.AudienceSolo, Profession: "sales"})
	if got := e.roles(s.AccessToken); !slices.Equal(got, []string{"analyst", "assistant", "sales"}) {
		t.Fatalf("agents = %v", got)
	}
	w := e.welcome(s.AccessToken)
	if w.Kind != "solo" || w.SetupPending || w.Team != nil || w.OrgName != "Luis Mora" {
		t.Fatalf("welcome = %+v", w)
	}
}

func TestClassicSignupKeepsTheSevenAgents(t *testing.T) {
	e := newTeamsEnv(t)
	s := e.signup(auth.RegisterInput{Email: "old@example.com", Name: "Old", OrgName: "Old Inc"})
	if got := e.roles(s.AccessToken); len(got) != 7 {
		t.Fatalf("a classic sign-up keeps the 7 agents, got %v", got)
	}
	if w := e.welcome(s.AccessToken); w.Kind != "classic" {
		t.Fatalf("welcome = %+v", w)
	}
}

func TestInvitedResponsibleArrivesInTheirTeamWithTheirAgents(t *testing.T) {
	e := newTeamsEnv(t)
	owner := e.signup(auth.RegisterInput{Email: "boss@acme.com", Name: "Marta Boss", OrgName: "Acme", Audience: auth.AudienceCompany, Profession: "direction"})
	if got := e.roles(owner.AccessToken); !slices.Equal(got, []string{"analyst", "assistant", "project_manager"}) {
		t.Fatalf("direction agents = %v", got)
	}
	var team struct {
		Team struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"team"`
		Agents []string `json:"agents"`
	}
	if c := e.call("POST", "/api/v1/teams", owner.AccessToken, map[string]any{"profession": "sales"}, &team); c != 201 || team.Team.Name != "Ventas" || !slices.Equal(team.Agents, []string{"sales"}) {
		t.Fatalf("create team = %d %+v", c, team)
	}
	var inv struct {
		Token string `json:"token"`
	}
	if c := e.call("POST", "/api/v1/invitations", owner.AccessToken, map[string]any{"email": "pablo@acme.com", "role": "member",
		"department_id": team.Team.ID, "profession": "sales", "lead": true}, &inv); c != 201 {
		t.Fatalf("invite = %d", c)
	}
	sess, err := e.svc.AcceptInvitation(context.Background(), auth.AcceptInput{Token: inv.Token, Name: "Pablo", Password: goodPW}, auth.ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	w := e.welcome(sess.AccessToken)
	if w.Kind != "lead" || w.Team == nil || w.Team.Name != "Ventas" || !w.Team.HeadIsMe || w.InvitedBy != "Marta Boss" || w.CanPickTeam {
		t.Fatalf("welcome = %+v", w)
	}
	if got := agentRoles(w); !slices.Equal(got, []string{"analyst", "assistant", "sales"}) {
		t.Fatalf("welcome agents = %v", got)
	}
	var depList struct {
		Items []orgchart.Department `json:"items"`
	}
	e.call("GET", "/api/v1/org/departments", owner.AccessToken, nil, &depList)
	deps := depList.Items
	if len(deps) != 1 || deps[0].Head.ID != sess.User.ID {
		t.Fatalf("the invited responsible heads the team: %+v", deps)
	}
	if got := e.roles(owner.AccessToken); len(got) != 4 {
		t.Fatalf("no duplicated agents: %v", got)
	}
	// A member cannot create teams.
	if c := e.call("POST", "/api/v1/teams", sess.AccessToken, map[string]any{"profession": "hr"}, nil); c != 403 {
		t.Fatalf("member creates a team = %d", c)
	}
}

func TestMemberWithoutTeamPicksOneInTheirWelcome(t *testing.T) {
	e := newTeamsEnv(t)
	owner := e.signup(auth.RegisterInput{Email: "boss@acme.com", Name: "Marta", OrgName: "Acme", Audience: auth.AudienceCompany, Profession: "other"})
	var team struct {
		Team struct {
			ID string `json:"id"`
		} `json:"team"`
	}
	e.call("POST", "/api/v1/teams", owner.AccessToken, map[string]any{"name": "Personas", "profession": "hr"}, &team)
	// Somebody who came in by the verified domain (or single sign-on): a member with no team yet.
	e.signup(auth.RegisterInput{Email: "eva@acme.com", Name: "Eva", OrgName: "Eva own"})
	ctx := context.Background()
	if _, err := e.svc.AddMember(ctx, auth.Principal{UserID: owner.User.ID, OrgID: owner.OrgID, Role: auth.RoleOwner}, "eva@acme.com", auth.RoleMember); err != nil {
		t.Fatal(err)
	}
	eva, err := e.svc.Login(ctx, auth.LoginInput{Email: "eva@acme.com", Password: goodPW, OrgID: owner.OrgID}, auth.ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	w := e.welcome(eva.AccessToken)
	if w.Kind != "member" || !w.CanPickTeam || len(w.Teams) != 1 || w.Teams[0].Name != "Personas" {
		t.Fatalf("welcome = %+v", w)
	}
	if c := e.call("POST", "/api/v1/me/team", eva.AccessToken, map[string]any{"department_id": "dept_nope"}, nil); c != 400 && c != 422 {
		t.Fatalf("unknown team = %d", c)
	}
	if c := e.call("POST", "/api/v1/me/team", eva.AccessToken, map[string]any{"department_id": team.Team.ID, "profession": "hr"}, &w); c != 200 {
		t.Fatalf("pick team = %d", c)
	}
	if w.Team == nil || w.Team.Name != "Personas" || w.Profession != "hr" || w.CanPickTeam {
		t.Fatalf("after picking = %+v", w)
	}
	if c := e.call("POST", "/api/v1/me/team", eva.AccessToken, map[string]any{"department_id": team.Team.ID}, nil); c != 409 {
		t.Fatalf("a second team pick = %d, want 409", c)
	}
	if got := e.roles(owner.AccessToken); !slices.Contains(got, "hr") {
		t.Fatalf("hr agent missing: %v", got)
	}
}

func TestWorkflowTemplatesTellWhichRolesTheOfficeLacksAndStillRun(t *testing.T) {
	e := newTeamsEnv(t)
	s := e.signup(auth.RegisterInput{Email: "ana@despacho.com", Name: "Ana", OrgName: "Despacho", Audience: auth.AudienceSolo, Profession: "legal"})
	var list []struct {
		Key          string   `json:"key"`
		MissingRoles []string `json:"missing_roles"`
	}
	if c := e.call("GET", "/api/v1/workflow-templates", s.AccessToken, nil, &list); c != 200 || len(list) == 0 {
		t.Fatalf("templates = %d %d", c, len(list))
	}
	var nc []string
	for _, w := range list {
		if w.Key == "new_client" {
			nc = w.MissingRoles
		}
	}
	// new_client has sales, legal, accounting, operations and assistant steps; a lawyer's office has legal + assistant.
	if !slices.Equal(nc, []string{"sales", "accounting", "operations"}) {
		t.Fatalf("missing roles of new_client = %v", nc)
	}
	var one struct {
		MissingRoles []string `json:"missing_roles"`
	}
	if c := e.call("GET", "/api/v1/workflow-templates/new_client", s.AccessToken, nil, &one); c != 200 || len(one.MissingRoles) != 3 {
		t.Fatalf("one template = %d %+v", c, one)
	}
	var req struct {
		RequestID string `json:"request_id"`
	}
	if c := e.call("POST", "/api/v1/workflow-templates/new_client/instantiate", s.AccessToken,
		map[string]any{"params": map[string]string{"client": "Grupo Alfa", "service": "asesoría"}}, &req); c != 202 {
		t.Fatalf("instantiate = %d", c)
	}
	var tasks []struct {
		AgentID   string `json:"agent_id"`
		RequestID string `json:"request_id"`
	}
	for i := 0; i < 200; i++ {
		e.call("GET", "/api/v1/tasks", s.AccessToken, nil, &tasks)
		if len(tasks) >= 5 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(tasks) < 5 {
		t.Fatalf("tasks = %+v", tasks)
	}
	for _, tk := range tasks {
		if tk.AgentID != "legal" && tk.AgentID != "assistant" {
			t.Fatalf("a step went to %q, an agent this office does not have", tk.AgentID)
		}
	}
}
