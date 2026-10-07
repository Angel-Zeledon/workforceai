package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

type httpEnv struct {
	*env
	srv  http.Handler
	docs map[string][]string // fake tenant data: org id -> documents
}

func newHTTPEnv(t *testing.T, hc HandlerConfig, mut ...func(*Config)) *httpEnv {
	t.Helper()
	e := newEnv(t, mut...)
	h := &httpEnv{env: e, docs: map[string][]string{}}
	r := chi.NewRouter()
	r.Mount("/auth", NewHandler(e.svc, hc).Routes())
	r.Group(func(r chi.Router) {
		r.Use(e.svc.Authenticator())
		// A tenant-scoped resource, the way integrated handlers should behave:
		// the org comes ONLY from the principal in the context.
		r.With(RequirePermission(PermReportsRead)).Get("/docs", func(w http.ResponseWriter, req *http.Request) {
			org, _ := OrgIDFrom(req.Context())
			json.NewEncoder(w).Encode(h.docs[org])
		})
		r.With(RequirePermission(PermRequestsCreate)).Post("/docs", func(w http.ResponseWriter, req *http.Request) {
			org, _ := OrgIDFrom(req.Context())
			var b struct{ Text string }
			json.NewDecoder(req.Body).Decode(&b)
			h.docs[org] = append(h.docs[org], b.Text)
			w.WriteHeader(http.StatusCreated)
		})
		r.With(RequireRole(RoleAdmin)).Get("/admin", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
		r.With(RequirePermission(PermOrgDelete)).Delete("/org", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("deleted")) })
	})
	h.srv = r
	return h
}

func (h *httpEnv) do(method, path, token string, body any) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	switch b := body.(type) {
	case nil:
		rd = bytes.NewReader(nil)
	case string:
		rd = bytes.NewReader([]byte(b))
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	req.RemoteAddr = "203.0.113.9:5555"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	return rec
}

func (h *httpEnv) signup(t *testing.T, email, org string) *Session {
	t.Helper()
	rec := h.do("POST", "/auth/register", "", RegisterInput{Email: email, Password: goodPW, Name: "N", OrgName: org + " Inc"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register %s: %d %s", email, rec.Code, rec.Body)
	}
	var s Session
	json.Unmarshal(rec.Body.Bytes(), &s)
	return &s
}

func TestHTTPTenantIsolationBetweenTwoOrgs(t *testing.T) {
	h := newHTTPEnv(t, HandlerConfig{})
	a := h.signup(t, "a@example.com", "Org A")
	b := h.signup(t, "b@example.com", "Org B")

	if rec := h.do("POST", "/docs", a.AccessToken, map[string]string{"Text": "secret-of-A"}); rec.Code != 201 {
		t.Fatal(rec.Code)
	}
	if rec := h.do("POST", "/docs", b.AccessToken, map[string]string{"Text": "secret-of-B"}); rec.Code != 201 {
		t.Fatal(rec.Code)
	}
	ra := h.do("GET", "/docs", a.AccessToken, nil)
	rb := h.do("GET", "/docs", b.AccessToken, nil)
	if !strings.Contains(ra.Body.String(), "secret-of-A") || strings.Contains(ra.Body.String(), "secret-of-B") {
		t.Fatalf("org A sees: %s", ra.Body)
	}
	if !strings.Contains(rb.Body.String(), "secret-of-B") || strings.Contains(rb.Body.String(), "secret-of-A") {
		t.Fatalf("org B sees: %s", rb.Body)
	}

	// Client-supplied org hints are ignored: header, query and body cannot switch tenant.
	req := httptest.NewRequest("GET", "/docs?org_id="+b.OrgID, nil)
	req.Header.Set("Authorization", "Bearer "+a.AccessToken)
	req.Header.Set("X-Org-ID", b.OrgID)
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "secret-of-B") {
		t.Fatal("tenant switch via header/query succeeded")
	}

	// A's token with the org claim rewritten to B is rejected (bad signature).
	parts := strings.Split(a.AccessToken, ".")
	pb, _ := b64url.DecodeString(parts[1])
	var c Claims
	json.Unmarshal(pb, &c)
	c.OrgID = b.OrgID
	nb, _ := json.Marshal(c)
	forged := parts[0] + "." + b64url.EncodeToString(nb) + "." + parts[2]
	if rec := h.do("GET", "/docs", forged, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("forged org claim: %d", rec.Code)
	}

	// Member management is per org too.
	rec = h.do("GET", "/auth/members", a.AccessToken, nil)
	if !strings.Contains(rec.Body.String(), "a@example.com") || strings.Contains(rec.Body.String(), "b@example.com") {
		t.Fatalf("members leak: %s", rec.Body)
	}
	if rec := h.do("PATCH", "/auth/members/"+b.User.ID, a.AccessToken, map[string]string{"role": "viewer"}); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-org patch: %d", rec.Code)
	}
}

func TestHTTPMiddlewareRejectsBadTokens(t *testing.T) {
	h := newHTTPEnv(t, HandlerConfig{}, func(c *Config) { c.AccessTTL = time.Minute })
	s := h.signup(t, "a@example.com", "A")
	parts := strings.Split(s.AccessToken, ".")

	cases := []struct {
		name, header string
		code         int
	}{
		{"no header", "", 401},
		{"wrong scheme", "Basic " + s.AccessToken, 401},
		{"empty bearer", "Bearer ", 401},
		{"garbage", "Bearer abc.def.ghi", 401},
		{"tampered sig", "Bearer " + parts[0] + "." + parts[1] + "." + flip(parts[2]), 401},
		{"alg none", "Bearer " + b64url.EncodeToString([]byte(`{"alg":"none"}`)) + "." + parts[1] + ".", 401},
		{"refresh token as access", "Bearer " + s.RefreshToken, 401},
		{"valid", "Bearer " + s.AccessToken, 200},
		{"valid, lowercase scheme", "bearer " + s.AccessToken, 200},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/docs", nil)
		if c.header != "" {
			req.Header.Set("Authorization", c.header)
		}
		rec := httptest.NewRecorder()
		h.srv.ServeHTTP(rec, req)
		if rec.Code != c.code {
			t.Errorf("%s: got %d want %d", c.name, rec.Code, c.code)
		}
		if c.code == 401 && !strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("%s: missing WWW-Authenticate", c.name)
		}
	}

	h.clock.Advance(2 * time.Minute)
	rec := h.do("GET", "/docs", s.AccessToken, nil)
	if rec.Code != 401 || !strings.Contains(rec.Header().Get("WWW-Authenticate"), "invalid_token") ||
		!strings.Contains(rec.Body.String(), "token expired") {
		t.Fatalf("expired token: %d %s %v", rec.Code, rec.Body, rec.Header())
	}
	// The refresh endpoint still works and yields a usable token.
	rec = h.do("POST", "/auth/refresh", "", map[string]string{"refresh_token": s.RefreshToken})
	if rec.Code != 200 {
		t.Fatalf("refresh: %d %s", rec.Code, rec.Body)
	}
	var n Session
	json.Unmarshal(rec.Body.Bytes(), &n)
	if rec := h.do("GET", "/docs", n.AccessToken, nil); rec.Code != 200 {
		t.Fatalf("new token: %d", rec.Code)
	}
}

func TestHTTPRBACEnforcement(t *testing.T) {
	h := newHTTPEnv(t, HandlerConfig{})
	owner := h.signup(t, "owner@example.com", "O")
	for _, r := range []struct {
		email string
		role  Role
	}{{"admin@example.com", RoleAdmin}, {"member@example.com", RoleMember}, {"viewer@example.com", RoleViewer}} {
		h.signup(t, r.email, "own "+r.email)
		if rec := h.do("POST", "/auth/members", owner.AccessToken, map[string]any{"email": r.email, "role": r.role}); rec.Code != 201 {
			t.Fatalf("add %s: %d %s", r.email, rec.Code, rec.Body)
		}
	}
	tokenOf := func(email string) string {
		rec := h.do("POST", "/auth/login", "", LoginInput{Email: email, Password: goodPW, OrgID: owner.OrgID})
		var s Session
		json.Unmarshal(rec.Body.Bytes(), &s)
		return s.AccessToken
	}
	tk := map[string]string{"owner": owner.AccessToken, "admin": tokenOf("admin@example.com"),
		"member": tokenOf("member@example.com"), "viewer": tokenOf("viewer@example.com")}

	expect := func(method, path, who string, code int, body any) {
		t.Helper()
		if rec := h.do(method, path, tk[who], body); rec.Code != code {
			t.Errorf("%s %s as %s: got %d want %d (%s)", method, path, who, rec.Code, code, rec.Body)
		}
	}
	expect("GET", "/docs", "viewer", 200, nil)
	expect("POST", "/docs", "viewer", 403, map[string]string{"Text": "x"})
	expect("POST", "/docs", "member", 201, map[string]string{"Text": "x"})
	expect("GET", "/admin", "member", 403, nil)
	expect("GET", "/admin", "viewer", 403, nil)
	expect("GET", "/admin", "admin", 200, nil)
	expect("GET", "/admin", "owner", 200, nil)
	expect("DELETE", "/org", "admin", 403, nil)
	expect("DELETE", "/org", "owner", 200, nil)
	expect("GET", "/auth/members", "viewer", 200, nil)
	expect("POST", "/auth/members", "member", 403, map[string]any{"email": "x@example.com", "role": "viewer"})

	// Escalation through the HTTP API.
	rec := h.do("GET", "/auth/members", tk["owner"], nil)
	var ml struct{ Members []Member }
	json.Unmarshal(rec.Body.Bytes(), &ml)
	ids := map[string]string{}
	for _, m := range ml.Members {
		ids[m.Email] = m.UserID
	}
	expect("PATCH", "/auth/members/"+ids["member@example.com"], "member", 403, map[string]string{"role": "owner"})
	expect("PATCH", "/auth/members/"+ids["viewer@example.com"], "viewer", 403, map[string]string{"role": "admin"})
	expect("PATCH", "/auth/members/"+ids["admin@example.com"], "admin", 403, map[string]string{"role": "owner"})
	expect("PATCH", "/auth/members/"+ids["member@example.com"], "admin", 403, map[string]string{"role": "admin"})
	expect("PATCH", "/auth/members/"+ids["owner@example.com"], "admin", 403, map[string]string{"role": "viewer"})
	expect("DELETE", "/auth/members/"+ids["owner@example.com"], "admin", 403, nil)
	expect("PATCH", "/auth/members/"+ids["viewer@example.com"], "owner", 422, map[string]string{"role": "root"})
	expect("PATCH", "/auth/members/"+ids["viewer@example.com"], "owner", 204, map[string]string{"role": "member"})
	expect("DELETE", "/auth/members/"+owner.User.ID, "owner", 409, nil) // last owner
}

func TestHTTPInputValidationAndBodyLimits(t *testing.T) {
	h := newHTTPEnv(t, HandlerConfig{})
	rec := h.do("POST", "/auth/register", "", `{"email":"bad","password":"x","name":"","org_name":""}`)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), `"fields"`) {
		t.Fatalf("validation: %d %s", rec.Code, rec.Body)
	}
	for name, body := range map[string]string{
		"not json":      `nope`,
		"unknown field": `{"email":"a@example.com","password":"` + goodPW + `","name":"n","org_name":"org","is_admin":true}`,
		"trailing data": `{"email":"a@example.com"} {"x":1}`,
		"wrong type":    `{"email":5}`,
		"empty body":    ``,
	} {
		if rec := h.do("POST", "/auth/register", "", body); rec.Code != 400 {
			t.Errorf("%s: got %d want 400 (%s)", name, rec.Code, rec.Body)
		}
	}
	big := `{"email":"a@example.com","password":"` + strings.Repeat("x", 2<<20) + `"}`
	if rec := h.do("POST", "/auth/login", "", big); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize body: %d", rec.Code)
	}
	// Duplicate registration -> 409, wrong login -> 401 with a generic body.
	h.signup(t, "dup@example.com", "D")
	if rec := h.do("POST", "/auth/register", "", RegisterInput{Email: "dup@example.com", Password: goodPW, Name: "N", OrgName: "D2"}); rec.Code != 409 {
		t.Errorf("dup: %d", rec.Code)
	}
	rec = h.do("POST", "/auth/login", "", LoginInput{Email: "dup@example.com", Password: "bad-password-1"})
	rec2 := h.do("POST", "/auth/login", "", LoginInput{Email: "ghost@example.com", Password: "bad-password-1"})
	if rec.Code != 401 || rec.Body.String() != rec2.Body.String() {
		t.Errorf("login errors must be identical: %q vs %q", rec.Body, rec2.Body)
	}
	if strings.Contains(rec.Body.String(), "argon") {
		t.Error("internals leaked")
	}
}

func TestHTTPLockoutReturns429(t *testing.T) {
	h := newHTTPEnv(t, HandlerConfig{}, func(c *Config) { c.MaxFailedAttempts = 2 })
	h.signup(t, "a@example.com", "A")
	bad := LoginInput{Email: "a@example.com", Password: "bad-password-1"}
	h.do("POST", "/auth/login", "", bad)
	rec := h.do("POST", "/auth/login", "", bad)
	if rec.Code != 429 || rec.Header().Get("Retry-After") == "" || !strings.Contains(rec.Body.String(), "account_locked") {
		t.Fatalf("lockout: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
}

func TestHTTPRateLimitPerIP(t *testing.T) {
	lim := NewMemoryLimiter()
	h := newHTTPEnv(t, HandlerConfig{Limiter: lim, LoginLimit: Limit{Max: 3, Window: time.Minute}}, func(c *Config) { c.MaxFailedAttempts = 1000 })
	lim.SetClock(h.clock.Now)
	for i := 0; i < 3; i++ {
		if rec := h.do("POST", "/auth/login", "", LoginInput{Email: "x@example.com", Password: "bad-password-1"}); rec.Code != 401 {
			t.Fatalf("attempt %d: %d", i, rec.Code)
		}
	}
	rec := h.do("POST", "/auth/login", "", LoginInput{Email: "x@example.com", Password: "bad-password-1"})
	if rec.Code != 429 || rec.Header().Get("Retry-After") == "" || rec.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Fatalf("rate limit: %d %v", rec.Code, rec.Header())
	}
	// A different IP is not affected.
	req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(`{"email":"x@example.com","password":"bad-password-1"}`))
	req.RemoteAddr = "198.51.100.1:1"
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("other IP: %d", rr.Code)
	}
	h.clock.Advance(61 * time.Second)
	if rec := h.do("POST", "/auth/login", "", LoginInput{Email: "x@example.com", Password: "bad-password-1"}); rec.Code != 401 {
		t.Fatalf("after window: %d", rec.Code)
	}
}

type failingLimiter struct{}

func (failingLimiter) Allow(_ context.Context, _ string, _ Limit) (LimitResult, error) {
	return LimitResult{}, errBadHash
}

func TestRateLimitFailOpenVsClosed(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	key := func(*http.Request) string { return "k" }
	l := Limit{Max: 1, Window: time.Second}
	for failOpen, want := range map[bool]int{true: 200, false: 503} {
		rec := httptest.NewRecorder()
		RateLimit(failingLimiter{}, l, key, failOpen)(ok).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		if rec.Code != want {
			t.Errorf("failOpen=%v: %d want %d", failOpen, rec.Code, want)
		}
	}
}

func TestHTTPUserRateLimit(t *testing.T) {
	lim := NewMemoryLimiter()
	h := newHTTPEnv(t, HandlerConfig{Limiter: lim, UserLimit: Limit{Max: 2, Window: time.Minute}})
	lim.SetClock(h.clock.Now)
	a := h.signup(t, "a@example.com", "A")
	b := h.signup(t, "b@example.com", "B")
	h.do("GET", "/auth/me", a.AccessToken, nil)
	h.do("GET", "/auth/me", a.AccessToken, nil)
	if rec := h.do("GET", "/auth/me", a.AccessToken, nil); rec.Code != 429 {
		t.Fatalf("user A third call: %d", rec.Code)
	}
	if rec := h.do("GET", "/auth/me", b.AccessToken, nil); rec.Code != 200 {
		t.Fatalf("user B must be independent: %d", rec.Code)
	}
}

func TestHTTPMeLogoutFlow(t *testing.T) {
	h := newHTTPEnv(t, HandlerConfig{})
	s := h.signup(t, "a@example.com", "A")
	rec := h.do("GET", "/auth/me", s.AccessToken, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"role":"owner"`) || !strings.Contains(rec.Body.String(), "org:delete") {
		t.Fatalf("me: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("POST", "/auth/logout", "", map[string]string{"refresh_token": s.RefreshToken}); rec.Code != 204 {
		t.Fatalf("logout: %d", rec.Code)
	}
	if rec := h.do("POST", "/auth/refresh", "", map[string]string{"refresh_token": s.RefreshToken}); rec.Code != 401 {
		t.Fatalf("refresh after logout: %d", rec.Code)
	}
	if rec := h.do("POST", "/auth/logout-all", s.AccessToken, nil); rec.Code != 204 {
		t.Fatalf("logout-all: %d", rec.Code)
	}
	if rec := h.do("GET", "/auth/me", "", nil); rec.Code != 401 {
		t.Fatalf("me without token: %d", rec.Code)
	}
}

func TestRequirePermissionFailsClosedWithoutPrincipal(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	for _, mw := range []func(http.Handler) http.Handler{RequirePermission(PermAgentsRead), RequireRole(RoleViewer)} {
		rec := httptest.NewRecorder()
		mw(ok).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		if rec.Code != 401 {
			t.Errorf("no principal: %d want 401", rec.Code)
		}
	}
	// Unknown role never satisfies RequireRole, even for the lowest requirement.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(WithPrincipal(req.Context(), Principal{UserID: "u", OrgID: "o", Role: "hacker"}))
	RequireRole(RoleViewer)(ok).ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("unknown role: %d", rec.Code)
	}
}

func TestPrincipalContextHelpers(t *testing.T) {
	ctx := bg
	if _, ok := PrincipalFrom(ctx); ok {
		t.Fatal("empty ctx has no principal")
	}
	if _, ok := OrgIDFrom(WithPrincipal(ctx, Principal{UserID: "u"})); ok {
		t.Fatal("principal without org must not count")
	}
	ctx = WithPrincipal(ctx, Principal{UserID: "u", OrgID: "o", Role: RoleAdmin})
	if o, _ := OrgIDFrom(ctx); o != "o" {
		t.Fatal(o)
	}
	if u, _ := UserIDFrom(ctx); u != "u" {
		t.Fatal(u)
	}
}
