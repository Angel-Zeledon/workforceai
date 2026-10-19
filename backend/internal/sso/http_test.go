package sso

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"aiworkforce/backend/internal/auth"
)

func do(h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rd)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestCallbackRedirectsWithCodeAndNeverALongLivedToken(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setup(t)
	h := NewHandler(f.svc, HandlerConfig{}).PublicRoutes()

	if w := do(h, "POST", "/discover", "", map[string]string{"email": "ana@acme.io"}); w.Code != 200 || !strings.Contains(w.Body.String(), `"sso":true`) {
		t.Fatalf("discover = %d %s", w.Code, w.Body)
	}
	if w := do(h, "POST", "/discover", "", map[string]string{"email": "ana@nowhere.io"}); !strings.Contains(w.Body.String(), `"sso":false`) {
		t.Fatalf("discover(other) = %s", w.Body)
	}

	w := do(h, "POST", "/start", "", map[string]string{"email": "ana@acme.io"})
	var st struct{ URL string }
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	pu, _ := url.Parse(st.URL)
	state := pu.Query().Get("state")
	f.idp.mu.Lock()
	f.idp.challenge = pu.Query().Get("code_challenge")
	f.idp.mu.Unlock()
	pendingNonce = pu.Query().Get("nonce")

	cb := "/callback?code=good-code&state=" + url.QueryEscape(state)
	w = do(h, "GET", cb, "", nil)
	loc, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != http.StatusFound || loc.Host != "app.example.com" || loc.Path != "/login" || loc.Query().Get("sso_code") == "" {
		t.Fatalf("callback = %d %s", w.Code, w.Header().Get("Location"))
	}
	if w.Header().Get("Set-Cookie") != "" || strings.Contains(w.Header().Get("Location"), "token") {
		t.Fatal("the callback must not set cookies or put tokens in the URL")
	}
	if _, err := f.svc.Exchange(ctx, loc.Query().Get("sso_code")); err != nil {
		t.Fatal(err)
	}
	// Replaying the callback URL ends on the login page with an error code, and mints nothing.
	w = do(h, "GET", cb, "", nil)
	loc, _ = url.Parse(w.Header().Get("Location"))
	if loc.Query().Get("sso_error") != "state" || loc.Query().Get("sso_code") != "" {
		t.Fatalf("replay = %s", w.Header().Get("Location"))
	}
	// The provider reporting an error (user cancelled) also lands on the login page.
	w = do(h, "GET", "/callback?error=access_denied&state=x", "", nil)
	if loc, _ = url.Parse(w.Header().Get("Location")); loc.Query().Get("sso_error") != "provider" {
		t.Fatalf("provider error = %s", w.Header().Get("Location"))
	}
	if w := do(h, "POST", "/start", "", map[string]string{"email": "ana@nowhere.io"}); w.Code != 404 {
		t.Fatalf("start for an address without SSO = %d", w.Code)
	}
}

func TestAdminRoutesNeedAnAdmin(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	as := auth.NewMemoryStore()
	svc, err := auth.NewService(auth.Config{Store: as, Secret: []byte("0123456789abcdef0123456789abcdef-test"),
		PasswordParams: auth.PasswordParams{Memory: 8, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := svc.Register(ctx, auth.RegisterInput{Email: "boss@acme.io", Password: "Correct-Horse-9", Name: "Boss", OrgName: "Acme Inc"}, auth.ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	// A viewer of the same organization.
	if _, err := svc.SignInFederated(ctx, auth.FederatedIdentity{OrgID: owner.OrgID, Email: "viewer@acme.io", Role: auth.RoleViewer, JIT: true}, auth.ClientMeta{}); err != nil {
		t.Fatal(err)
	}
	vs, err := svc.SignInFederated(ctx, auth.FederatedIdentity{OrgID: owner.OrgID, Email: "viewer@acme.io", JIT: true}, auth.ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(f.svc, HandlerConfig{Auth: svc}).AdminRoutes()

	if w := do(h, "GET", "/sso", "", nil); w.Code != 401 {
		t.Fatalf("anonymous = %d", w.Code)
	}
	if w := do(h, "PUT", "/policy", vs.AccessToken, map[string]bool{"require_mfa": true}); w.Code != 403 {
		t.Fatalf("member = %d, want 403", w.Code)
	}
	if w := do(h, "PUT", "/policy", owner.AccessToken, map[string]bool{"require_mfa": true}); w.Code != 200 {
		t.Fatalf("owner = %d %s", w.Code, w.Body)
	}
	// The policy is stored for the caller's organization only.
	if p, _ := f.store.GetPolicy(ctx, owner.OrgID); !p.RequireMFA {
		t.Fatal("policy not stored")
	}
	if p, _ := f.store.GetPolicy(ctx, orgA); p.RequireMFA {
		t.Fatal("policy leaked to another org")
	}
	// The GET never contains the secret.
	w := do(h, "PUT", "/sso", owner.AccessToken, ConfigInput{Enabled: false, Issuer: f.idp.srv.URL, ClientID: "c", ClientSecret: "topsecret"})
	if w.Code != 200 || strings.Contains(w.Body.String(), "topsecret") {
		t.Fatalf("put sso = %d %s", w.Code, w.Body)
	}
	if w = do(h, "GET", "/sso", owner.AccessToken, nil); strings.Contains(w.Body.String(), "topsecret") || !strings.Contains(w.Body.String(), `"has_secret":true`) {
		t.Fatalf("get sso = %s", w.Body)
	}
	if w = do(h, "POST", "/domains", owner.AccessToken, map[string]string{"domain": "gmail.com"}); w.Code != 422 || !strings.Contains(w.Body.String(), "free_mail") {
		t.Fatalf("free mail = %d %s", w.Code, w.Body)
	}
}
