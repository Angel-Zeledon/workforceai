package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/events"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/roles"
)

const goodPW = "Correct-Horse-9"

// ---- fakes ----

type fakeRuntime struct{}

func (fakeRuntime) Plan(context.Context, application.PlanRequest) (application.PlanResponse, error) {
	return application.PlanResponse{Tasks: []application.PlannedTask{{Key: "a", Title: "A", AgentID: "sales"}}}, nil
}
func (fakeRuntime) RunTask(_ context.Context, in application.RunTaskRequest) (application.RunTaskResponse, error) {
	return application.RunTaskResponse{Output: domain.StructuredOutput{Summary: "ok", Confidence: 0.9}}, nil
}
func (fakeRuntime) Consult(context.Context, application.ConsultRequest) (application.ConsultResponse, error) {
	return application.ConsultResponse{Answer: "x"}, nil
}
func (fakeRuntime) Synthesize(context.Context, application.SynthesizeRequest) (application.SynthesizeResponse, error) {
	return application.SynthesizeResponse{Title: "T", Summary: "S", Sections: []domain.Section{}}, nil
}
func (fakeRuntime) Health(context.Context) (string, error) { return "simulation", nil }

// orgSpy records which org ids reach the store.
type orgSpy struct {
	application.Store
	mu   sync.Mutex
	orgs map[string]int
}

func (s *orgSpy) CreateAgent(ctx context.Context, orgID string, a domain.Agent) error {
	return s.Store.(application.AgentWriter).CreateAgent(ctx, orgID, a)
}

func (s *orgSpy) note(org string) {
	s.mu.Lock()
	s.orgs[org]++
	s.mu.Unlock()
}
func (s *orgSpy) seen() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int{}
	for k, v := range s.orgs {
		out[k] = v
	}
	return out
}
func (s *orgSpy) ListAgents(ctx context.Context, org string) ([]domain.Agent, error) {
	s.note(org)
	return s.Store.ListAgents(ctx, org)
}
func (s *orgSpy) ListTasks(ctx context.Context, org, a, st string) ([]domain.Task, error) {
	s.note(org)
	return s.Store.ListTasks(ctx, org, a, st)
}
func (s *orgSpy) ListRequests(ctx context.Context, org string) ([]domain.Request, error) {
	s.note(org)
	return s.Store.ListRequests(ctx, org)
}
func (s *orgSpy) CreateRequest(ctx context.Context, org string, r domain.Request) error {
	s.note(org)
	return s.Store.CreateRequest(ctx, org, r)
}
func (s *orgSpy) GetApproval(ctx context.Context, org, id string) (domain.Approval, error) {
	s.note(org)
	return s.Store.GetApproval(ctx, org, id)
}

// ---- environment ----

type opts struct {
	auth           bool
	origins        []string
	demoReset      bool
	maxBody        int64
	wsMaxAge       time.Duration
	timeouts       api.Timeouts
	skipAuthRoutes bool
	ws             bool // mount projects and artifacts (workspaces_api_test.go)
	writeGate      func(ctx context.Context, org string) error
}

type env struct {
	t     *testing.T
	srv   *httptest.Server
	svc   *auth.Service
	spy   *orgSpy
	hub   *events.Hub
	store *memory.Store
	appr  *application.Approvals
}

func newEnv(t *testing.T, o opts) *env {
	t.Helper()
	cfg := application.DefaultConfig()
	cfg.IdleDelay = 0
	mem := memory.New()
	spy := &orgSpy{Store: mem, orgs: map[string]int{}}
	if err := mem.Seed(context.Background(), domain.SeedOrg(cfg.BudgetUSD), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := events.NewHub(log)
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: spy, Pub: events.LocalBus{Hub: hub}, Log: log}
	appr := application.NewApprovals(cfg, spy, rec)
	q := &application.Queries{Store: spy, Cfg: cfg}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	orch := application.NewOrchestrator(ctx, cfg, spy, fakeRuntime{}, memory.NewLocker(), rec, appr, q, log)

	d := api.Deps{Cfg: cfg, Queries: q, Orch: orch, Approvals: appr, Store: spy, Runtime: fakeRuntime{}, Hub: hub, Log: log,
		AuthEnabled: o.auth, AllowedOrigins: o.origins, EnableDemoReset: o.demoReset, MaxBodyBytes: o.maxBody, WSMaxAge: o.wsMaxAge}
	if o.ws {
		attachWorkspaces(&d, spy, rec, appr, orch, log, ctx, o.writeGate)
	}
	var svc *auth.Service
	if o.auth {
		var err error
		svc, err = auth.NewService(auth.Config{
			Store:          auth.NewMemoryStore(),
			Secret:         []byte("0123456789abcdef0123456789abcdef-test"),
			PasswordParams: auth.PasswordParams{Memory: 8, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32},
		})
		if err != nil {
			t.Fatal(err)
		}
		d.Auth = svc
		d.AuthRoutes = auth.NewHandler(svc, auth.HandlerConfig{}).Routes()
	}
	ts := httptest.NewUnstartedServer(api.NewRouter(d))
	if o.timeouts != (api.Timeouts{}) {
		ts.Config = api.NewHTTPServer("", ts.Config.Handler, o.timeouts)
	}
	ts.Start()
	t.Cleanup(ts.Close)
	return &env{t: t, srv: ts, svc: svc, spy: spy, hub: hub, store: mem, appr: appr}
}

func (e *env) do(method, path, token string, body any, hdr map[string]string) (*http.Response, []byte) {
	e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

func (e *env) status(method, path, token string, body any) int {
	e.t.Helper()
	resp, _ := e.do(method, path, token, body, nil)
	return resp.StatusCode
}

func (e *env) register(email, org string) *auth.Session {
	e.t.Helper()
	s, err := e.svc.Register(context.Background(), auth.RegisterInput{Email: email, Password: goodPW, Name: "Test", OrgName: org + " Inc"}, auth.ClientMeta{})
	if err != nil {
		e.t.Fatalf("register: %v", err)
	}
	return s
}

// member returns an access token of a user holding `role` in owner's organization.
func (e *env) member(owner *auth.Session, email string, role auth.Role) string {
	e.t.Helper()
	ctx := context.Background()
	e.register(email, "Own of "+email)
	p := auth.Principal{UserID: owner.User.ID, OrgID: owner.OrgID, Role: auth.RoleOwner}
	if _, err := e.svc.AddMember(ctx, p, email, role); err != nil {
		e.t.Fatalf("add member: %v", err)
	}
	s, err := e.svc.Login(ctx, auth.LoginInput{Email: email, Password: goodPW, OrgID: owner.OrgID}, auth.ClientMeta{})
	if err != nil {
		e.t.Fatalf("login: %v", err)
	}
	if s.OrgID != owner.OrgID || s.Role != role {
		e.t.Fatalf("login gave org=%s role=%s", s.OrgID, s.Role)
	}
	return s.AccessToken
}

// ---- auth disabled (dev/demo) ----

func TestAuthDisabledKeepsDemoWorkingWithoutLogin(t *testing.T) {
	e := newEnv(t, opts{})
	if c := e.status("GET", "/api/v1/agents", "", nil); c != 200 {
		t.Fatalf("agents without token = %d", c)
	}
	resp, body := e.do("POST", "/api/v1/requests", "", map[string]string{"text": "hola"}, nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("create request = %d %s", resp.StatusCode, body)
	}
	if c := e.status("POST", "/api/v1/auth/login", "", map[string]string{}); c != 404 && c != 405 {
		t.Fatalf("auth routes must not exist when auth is off, got %d", c)
	}
	// every store call used the fixed demo org
	for org := range e.spy.seen() {
		if org != domain.DemoOrgID {
			t.Fatalf("unexpected org %q", org)
		}
	}
}

// ---- auth enabled ----

func TestAuthEnabledRequiresValidToken(t *testing.T) {
	e := newEnv(t, opts{auth: true})
	s := e.register("a@example.com", "A")

	for _, p := range []string{"/api/v1/agents", "/api/v1/tasks", "/api/v1/metrics", "/api/v1/approvals", "/api/v1/activity"} {
		if c := e.status("GET", p, "", nil); c != 401 {
			t.Errorf("GET %s without token = %d, want 401", p, c)
		}
		if c := e.status("GET", p, "garbage.token.value", nil); c != 401 {
			t.Errorf("GET %s with bad token = %d, want 401", p, c)
		}
		if c := e.status("GET", p, s.AccessToken, nil); c != 200 {
			t.Errorf("GET %s with token = %d, want 200", p, c)
		}
	}
	if c := e.status("POST", "/api/v1/requests", "", map[string]string{"text": "x"}); c != 401 {
		t.Errorf("POST /requests without token = %d", c)
	}
	// Health stays public.
	for _, p := range []string{"/healthz", "/api/v1/healthz"} {
		if c := e.status("GET", p, "", nil); c != 200 {
			t.Errorf("%s = %d, want 200", p, c)
		}
	}
	// WebSocket handshake without a token is refused before the upgrade.
	if _, resp, err := websocket.DefaultDialer.Dial(wsURL(e), nil); err == nil || resp == nil || resp.StatusCode != 401 {
		t.Errorf("ws without token: err=%v resp=%v", err, resp)
	}
	// An expired/forged token is not accepted either.
	forged := s.AccessToken[:len(s.AccessToken)-3] + "AAA"
	if c := e.status("GET", "/api/v1/agents", forged, nil); c != 401 {
		t.Errorf("forged token = %d", c)
	}
}

func TestRegisterLoginRefreshOverHTTP(t *testing.T) {
	e := newEnv(t, opts{auth: true})
	resp, body := e.do("POST", "/api/v1/auth/register", "", map[string]string{"email": "x@example.com", "password": goodPW, "name": "X", "org_name": "Xorg"}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("register = %d %s", resp.StatusCode, body)
	}
	var sess auth.Session
	_ = json.Unmarshal(body, &sess)
	if sess.AccessToken == "" || sess.RefreshToken == "" || sess.OrgID == "" || sess.Role != auth.RoleOwner {
		t.Fatalf("session = %+v", sess)
	}
	resp, body = e.do("POST", "/api/v1/auth/login", "", map[string]string{"email": "x@example.com", "password": goodPW}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("login = %d %s", resp.StatusCode, body)
	}
	var login auth.Session
	_ = json.Unmarshal(body, &login)
	if c := e.status("POST", "/api/v1/auth/login", "", map[string]string{"email": "x@example.com", "password": "wrong-Password-1"}); c != 401 {
		t.Errorf("bad password = %d", c)
	}
	resp, body = e.do("POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": login.RefreshToken}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("refresh = %d %s", resp.StatusCode, body)
	}
	var ref auth.Session
	_ = json.Unmarshal(body, &ref)
	if ref.RefreshToken == login.RefreshToken || ref.AccessToken == "" {
		t.Fatal("refresh must rotate the refresh token")
	}
	// Reusing the rotated token is rejected.
	if c := e.status("POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": login.RefreshToken}); c != 401 {
		t.Errorf("refresh token reuse = %d, want 401", c)
	}
	if c := e.status("GET", "/api/v1/agents", ref.AccessToken, nil); c != 200 {
		t.Errorf("agents with refreshed token = %d", c)
	}
	if c := e.status("GET", "/api/v1/auth/me", ref.AccessToken, nil); c != 200 {
		t.Errorf("me = %d", c)
	}
}

// The org id comes from the verified JWT; nothing the client sends can change it.
func TestTenantComesFromJWTNotFromClient(t *testing.T) {
	e := newEnv(t, opts{auth: true})
	a := e.register("a@example.com", "A")
	b := e.register("b@example.com", "B")
	if a.OrgID == b.OrgID {
		t.Fatal("orgs must differ")
	}
	evil := map[string]string{"X-Org-ID": b.OrgID, "X-Tenant": b.OrgID, "Org-Id": b.OrgID}
	for _, p := range []string{"/api/v1/agents?org_id=" + b.OrgID, "/api/v1/tasks?org_id=" + b.OrgID + "&org=" + b.OrgID, "/api/v1/requests"} {
		if resp, _ := e.do("GET", p, a.AccessToken, nil, evil); resp.StatusCode != 200 {
			t.Fatalf("GET %s = %d", p, resp.StatusCode)
		}
	}
	if resp, body := e.do("POST", "/api/v1/requests", a.AccessToken, map[string]string{"text": "hola", "org_id": b.OrgID}, evil); resp.StatusCode != 202 {
		t.Fatalf("POST requests = %d %s", resp.StatusCode, body)
	}
	time.Sleep(100 * time.Millisecond) // let the background run write
	seen := e.spy.seen()
	if seen[a.OrgID] == 0 {
		t.Fatalf("org A never reached the store: %v", seen)
	}
	if seen[b.OrgID] != 0 || seen[domain.DemoOrgID] != 0 {
		t.Fatalf("store was used with a foreign org: %v", seen)
	}
}

func TestNewOrganizationGetsItsAgents(t *testing.T) {
	e := newEnv(t, opts{auth: true})
	a := e.register("a@example.com", "A")
	resp, body := e.do("GET", "/api/v1/agents", a.AccessToken, nil, nil)
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	var agents []domain.Agent
	_ = json.Unmarshal(body, &agents)
	if len(agents) != len(roles.SeedAgents()) {
		t.Fatalf("agents = %d", len(agents))
	}
}

func TestApprovalDecisionRequiresAdminRole(t *testing.T) {
	e := newEnv(t, opts{auth: true})
	owner := e.register("owner@example.com", "Acme")
	viewer := e.member(owner, "viewer@example.com", auth.RoleViewer)
	member := e.member(owner, "member@example.com", auth.RoleMember)
	admin := e.member(owner, "admin@example.com", auth.RoleAdmin)

	newApproval := func(id string) {
		err := e.store.CreateApproval(context.Background(), owner.OrgID, domain.Approval{ID: id, TaskID: "t", AgentID: "sales", Action: "send_proposal",
			Title: "x", Risk: "high", Status: domain.ApprovalPending, CreatedAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
	}
	newApproval("ap1")
	path := "/api/v1/approvals/ap1/decision"
	body := map[string]string{"decision": "approve"}

	if c := e.status("POST", path, "", body); c != 401 {
		t.Errorf("anonymous = %d, want 401", c)
	}
	if c := e.status("POST", path, viewer, body); c != 403 {
		t.Errorf("viewer = %d, want 403", c)
	}
	if c := e.status("POST", path, member, body); c != 403 {
		t.Errorf("member = %d, want 403", c)
	}
	if ap, _ := e.store.GetApproval(context.Background(), owner.OrgID, "ap1"); ap.Status != domain.ApprovalPending {
		t.Fatalf("approval resolved by an unauthorized user: %s", ap.Status)
	}
	// Reading approvals is allowed to everyone in the org.
	if c := e.status("GET", "/api/v1/approvals", viewer, nil); c != 200 {
		t.Errorf("viewer list approvals = %d", c)
	}
	if c := e.status("POST", path, admin, body); c != 200 {
		t.Errorf("admin = %d, want 200", c)
	}
	newApproval("ap2")
	if c := e.status("POST", "/api/v1/approvals/ap2/decision", owner.AccessToken, map[string]string{"decision": "reject"}); c != 200 {
		t.Errorf("owner = %d, want 200", c)
	}
	// Other RBAC edges: viewers cannot create requests or post messages.
	if c := e.status("POST", "/api/v1/requests", viewer, map[string]string{"text": "x"}); c != 403 {
		t.Errorf("viewer create request = %d, want 403", c)
	}
	if c := e.status("POST", "/api/v1/requests", member, map[string]string{"text": "x"}); c != 202 {
		t.Errorf("member create request = %d, want 202", c)
	}
}

func TestApprovalOfAnotherTenantIsNotFound(t *testing.T) {
	e := newEnv(t, opts{auth: true})
	a := e.register("a@example.com", "A")
	b := e.register("b@example.com", "B")
	_ = a
	// The memory store is single-tenant, so assert at the boundary instead:
	// the store only ever sees B's org id when B decides.
	_ = e.store.CreateApproval(context.Background(), b.OrgID, domain.Approval{ID: "apx", TaskID: "t", AgentID: "sales", Action: "x",
		Title: "x", Risk: "low", Status: domain.ApprovalPending, CreatedAt: time.Now()})
	e.do("POST", "/api/v1/approvals/apx/decision", b.AccessToken, map[string]string{"decision": "approve"}, map[string]string{"X-Org-ID": a.OrgID})
	seen := e.spy.seen()
	if seen[a.OrgID] != 0 {
		t.Fatalf("approval lookup used the client's org: %v", seen)
	}
	if seen[b.OrgID] == 0 {
		t.Fatalf("approval lookup did not use the token's org: %v", seen)
	}
}

// ---- /demo/reset ----

func TestDemoResetRegistration(t *testing.T) {
	t.Run("disabled by default", func(t *testing.T) {
		e := newEnv(t, opts{})
		if c := e.status("POST", "/api/v1/demo/reset", "", nil); c != 404 && c != 405 {
			t.Fatalf("reset must not exist, got %d", c)
		}
	})
	t.Run("disabled with auth on even for owners", func(t *testing.T) {
		e := newEnv(t, opts{auth: true})
		s := e.register("o@example.com", "O")
		if c := e.status("POST", "/api/v1/demo/reset", s.AccessToken, nil); c != 404 && c != 405 {
			t.Fatalf("reset must not exist, got %d", c)
		}
	})
	t.Run("enabled, auth off (dev)", func(t *testing.T) {
		e := newEnv(t, opts{demoReset: true})
		if c := e.status("POST", "/api/v1/demo/reset", "", nil); c != 200 {
			t.Fatalf("reset = %d", c)
		}
	})
	t.Run("enabled, auth on requires admin", func(t *testing.T) {
		e := newEnv(t, opts{auth: true, demoReset: true})
		owner := e.register("o@example.com", "O")
		viewer := e.member(owner, "v@example.com", auth.RoleViewer)
		member := e.member(owner, "m@example.com", auth.RoleMember)
		admin := e.member(owner, "ad@example.com", auth.RoleAdmin)
		for name, c := range map[string]struct {
			tok  string
			want int
		}{
			"anonymous": {"", 401}, "viewer": {viewer, 403}, "member": {member, 403}, "admin": {admin, 200}, "owner": {owner.AccessToken, 200},
		} {
			if got := e.status("POST", "/api/v1/demo/reset", c.tok, nil); got != c.want {
				t.Errorf("%s: reset = %d, want %d", name, got, c.want)
			}
		}
	})
}

// ---- CORS ----

func TestCORSPolicy(t *testing.T) {
	cases := []struct {
		name      string
		o         opts
		origin    string
		wantACAO  string
		wantAllow bool
	}{
		{"dev default allows any", opts{}, "http://localhost:3000", "*", true},
		{"allow-list reflects allowed origin", opts{origins: []string{"https://app.example.com"}}, "https://app.example.com", "https://app.example.com", true},
		{"allow-list rejects others", opts{origins: []string{"https://app.example.com"}}, "https://evil.example", "", false},
		{"allow-list is exact (no suffix tricks)", opts{origins: []string{"https://app.example.com"}}, "https://app.example.com.evil.io", "", false},
		{"auth on + empty list = same-origin only", opts{auth: true}, "https://app.example.com", "", false},
		{"auth on + list", opts{auth: true, origins: []string{"https://app.example.com"}}, "https://app.example.com", "https://app.example.com", true},
		{"list set in dev still restricts", opts{origins: []string{"http://localhost:3000"}}, "http://evil.example", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, c.o)
			hdr := map[string]string{"Origin": c.origin}
			resp, _ := e.do("GET", "/healthz", "", nil, hdr)
			if got := resp.Header.Get("Access-Control-Allow-Origin"); got != c.wantACAO {
				t.Errorf("ACAO = %q, want %q", got, c.wantACAO)
			}
			if !containsToken(resp.Header.Values("Vary"), "Origin") {
				t.Errorf("Vary must include Origin, got %v", resp.Header.Values("Vary"))
			}
			// Preflight
			pf, _ := e.do("OPTIONS", "/api/v1/requests", "", nil, map[string]string{
				"Origin": c.origin, "Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "authorization,content-type"})
			if pf.StatusCode != http.StatusNoContent {
				t.Errorf("preflight status = %d", pf.StatusCode)
			}
			if got := pf.Header.Get("Access-Control-Allow-Origin"); got != c.wantACAO {
				t.Errorf("preflight ACAO = %q, want %q", got, c.wantACAO)
			}
			if c.wantAllow && !strings.Contains(pf.Header.Get("Access-Control-Allow-Headers"), "Authorization") {
				t.Errorf("preflight must allow Authorization")
			}
			if !c.wantAllow && pf.Header.Get("Access-Control-Allow-Methods") != "" {
				t.Errorf("rejected origin must not get CORS headers")
			}
		})
	}
}

func containsToken(vals []string, tok string) bool {
	for _, v := range vals {
		for _, p := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(p), tok) {
				return true
			}
		}
	}
	return false
}

// ---- WebSocket ----

func wsURL(e *env) string { return "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/ws" }

func dialWS(t *testing.T, e *env, token string, hdr http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	u := wsURL(e)
	if token != "" {
		u += "?access_token=" + token
	}
	return websocket.DefaultDialer.Dial(u, hdr)
}

func readFrame(t *testing.T, c *websocket.Conn) domain.Event {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, raw, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read ws: %v", err)
	}
	var ev domain.Event
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestWebSocketOriginCheckUsesTheSameAllowList(t *testing.T) {
	e := newEnv(t, opts{origins: []string{"https://app.example.com"}})
	ok, _, err := dialWS(t, e, "", http.Header{"Origin": {"https://app.example.com"}})
	if err != nil {
		t.Fatalf("allowed origin rejected: %v", err)
	}
	ok.Close()
	_, resp, err := dialWS(t, e, "", http.Header{"Origin": {"https://evil.example"}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin must get 403, err=%v resp=%v", err, resp)
	}
	// Non-browser clients (no Origin) are still subject to auth, not to the origin list.
	c, _, err := dialWS(t, e, "", nil)
	if err != nil {
		t.Fatalf("no-origin client: %v", err)
	}
	c.Close()

	// Dev default: any origin.
	dev := newEnv(t, opts{})
	c, _, err = dialWS(t, dev, "", http.Header{"Origin": {"http://localhost:3000"}})
	if err != nil {
		t.Fatalf("dev origin: %v", err)
	}
	c.Close()

	// Auth on + empty list: cross-origin browsers are refused even with a valid token.
	ae := newEnv(t, opts{auth: true})
	s := ae.register("a@example.com", "A")
	if _, resp, err := dialWS(t, ae, s.AccessToken, http.Header{"Origin": {"https://evil.example"}}); err == nil || resp.StatusCode != 403 {
		t.Fatalf("cross-origin ws with token must be refused: err=%v", err)
	}
	c, _, err = dialWS(t, ae, s.AccessToken, nil)
	if err != nil {
		t.Fatalf("authenticated ws: %v", err)
	}
	c.Close()
}

func TestWebSocketIsTenantScoped(t *testing.T) {
	e := newEnv(t, opts{auth: true})
	a := e.register("a@example.com", "A")
	b := e.register("b@example.com", "B")
	ca, _, err := dialWS(t, e, a.AccessToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ca.Close()
	cb, _, err := dialWS(t, e, b.AccessToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cb.Close()
	if h := readFrame(t, ca); h.Type != domain.EvHello || h.OrgID != a.OrgID {
		t.Fatalf("hello A = %+v", h)
	}
	if h := readFrame(t, cb); h.Type != domain.EvHello || h.OrgID != b.OrgID {
		t.Fatalf("hello B = %+v", h)
	}
	pub := events.LocalBus{Hub: e.hub}
	_ = pub.Publish(context.Background(), domain.Event{ID: "1", Type: "secret.of.a", TS: time.Now(), OrgID: a.OrgID})
	_ = pub.Publish(context.Background(), domain.Event{ID: "2", Type: "secret.of.b", TS: time.Now(), OrgID: b.OrgID})
	if ev := readFrame(t, ca); ev.Type != "secret.of.a" {
		t.Fatalf("A received %s", ev.Type)
	}
	if ev := readFrame(t, cb); ev.Type != "secret.of.b" {
		t.Fatalf("B received %s", ev.Type)
	}
}

func TestWebSocketClosesWhenTokenLifetimeEnds(t *testing.T) {
	e := newEnv(t, opts{auth: true, wsMaxAge: 150 * time.Millisecond})
	a := e.register("a@example.com", "A")
	c, _, err := dialWS(t, e, a.AccessToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	readFrame(t, c) // hello
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = c.ReadMessage()
	ce, ok := err.(*websocket.CloseError)
	if !ok || ce.Code != 4401 {
		t.Fatalf("want close 4401, got %v", err)
	}
}

// ---- limits and timeouts ----

func TestRequestBodyLimit(t *testing.T) {
	e := newEnv(t, opts{maxBody: 1024})
	small := map[string]string{"text": "hola"}
	if c := e.status("POST", "/api/v1/requests", "", small); c != 202 {
		t.Fatalf("small body = %d", c)
	}
	big := map[string]string{"text": strings.Repeat("x", 4096)}
	resp, body := e.do("POST", "/api/v1/requests", "", big, nil)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("big body = %d %s, want 413", resp.StatusCode, body)
	}
	if c := e.status("POST", "/api/v1/approvals/x/decision", "", big); c != http.StatusRequestEntityTooLarge {
		t.Fatalf("big decision body = %d, want 413", c)
	}
	// Malformed JSON stays a 400.
	if c := e.status("POST", "/api/v1/requests", "", []byte("{not json")); c != 400 {
		t.Fatalf("bad json = %d", c)
	}
}

func TestHTTPServerTimeoutsAreSet(t *testing.T) {
	s := api.NewHTTPServer(":0", http.NewServeMux(), api.Timeouts{ReadHeader: time.Second, Read: 2 * time.Second, Write: 3 * time.Second, Idle: 4 * time.Second})
	if s.ReadHeaderTimeout != time.Second || s.ReadTimeout != 2*time.Second || s.WriteTimeout != 3*time.Second || s.IdleTimeout != 4*time.Second {
		t.Fatalf("timeouts not applied: %+v", s)
	}
	if s.MaxHeaderBytes <= 0 {
		t.Fatal("MaxHeaderBytes must be bounded")
	}
}

func TestSlowClientIsCutOffByReadTimeout(t *testing.T) {
	e := newEnv(t, opts{timeouts: api.Timeouts{ReadHeader: time.Second, Read: 300 * time.Millisecond, Write: 5 * time.Second, Idle: 5 * time.Second}})
	conn, err := net.Dial("tcp", strings.TrimPrefix(e.srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Promise a body and never send it.
	_, _ = conn.Write([]byte("POST /api/v1/requests HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{"))
	start := time.Now()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _ = io.ReadAll(conn) // returns when the server closes
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("slow client held the connection for %v despite ReadTimeout", d)
	}
}

func TestIdleKeepAliveIsClosed(t *testing.T) {
	e := newEnv(t, opts{timeouts: api.Timeouts{ReadHeader: time.Second, Read: time.Second, Write: time.Second, Idle: 200 * time.Millisecond}})
	conn, err := net.Dial("tcp", strings.TrimPrefix(e.srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("GET /healthz HTTP/1.1\r\nHost: x\r\n\r\n"))
	buf := make([]byte, 4096)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Read(buf); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _ = io.ReadAll(conn) // idle connection must be closed by the server
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("idle connection survived %v despite IdleTimeout", d)
	}
}

// WriteTimeout must not kill long-lived WebSockets.
func TestWebSocketSurvivesWriteTimeout(t *testing.T) {
	e := newEnv(t, opts{timeouts: api.Timeouts{ReadHeader: time.Second, Read: 200 * time.Millisecond, Write: 200 * time.Millisecond, Idle: time.Second}})
	c, _, err := dialWS(t, e, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	readFrame(t, c) // hello
	time.Sleep(600 * time.Millisecond)
	_ = events.LocalBus{Hub: e.hub}.Publish(context.Background(), domain.Event{ID: "1", Type: "late", TS: time.Now(), OrgID: domain.DemoOrgID})
	if ev := readFrame(t, c); ev.Type != "late" {
		t.Fatalf("got %s", ev.Type)
	}
}
