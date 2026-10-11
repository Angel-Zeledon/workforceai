package linkedin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"aiworkforce/backend/internal/connections"
)

// fakeLinkedIn serves the OAuth token, OIDC userinfo, ugcPosts and revoke endpoints.
type fakeLinkedIn struct {
	mu      sync.Mutex
	posts   []map[string]any
	revoked url.Values
}

func (f *fakeLinkedIn) server(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/v2/accessToken":
			_ = r.ParseForm()
			if r.Form.Get("code") != "good-code" || r.Form.Get("client_secret") != "s3cret" {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"AQV-member-token","expires_in":5184000,"scope":"openid,profile,email,w_member_social"}`))
		case "/v2/userinfo":
			if r.Header.Get("Authorization") != "Bearer AQV-member-token" {
				w.WriteHeader(401)
				return
			}
			_, _ = w.Write([]byte(`{"sub":"abc123","name":"Valeria Ríos","email":"valeria@example.com"}`))
		case "/v2/ugcPosts":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			f.mu.Lock()
			f.posts = append(f.posts, b)
			f.mu.Unlock()
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"id":"urn:li:share:777"}`))
		case "/oauth/v2/revoke":
			_ = r.ParseForm()
			f.mu.Lock()
			f.revoked = r.Form
			f.mu.Unlock()
		default:
			w.WriteHeader(404)
		}
	}))
}

// bearer injects the token like the connections service's handle does.
type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func TestLinkedInOAuthPostAndRevoke(t *testing.T) {
	fl := &fakeLinkedIn{}
	srv := fl.server(t)
	defer srv.Close()
	p, err := New(Config{AuthURL: srv.URL + "/oauth/v2/authorization", TokenURL: srv.URL + "/oauth/v2/accessToken",
		RevokeURL: srv.URL + "/oauth/v2/revoke", UserinfoURL: srv.URL + "/v2/userinfo", APIBase: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	app := connections.OAuthApp{ClientID: "cid", ClientSecret: "s3cret", RedirectURL: "http://localhost/cb"}

	u, err := p.AuthStart(connections.AuthStartIn{App: app, State: "st", Challenge: "ch", Scopes: []string{"openid", "profile", "email", "w_member_social"}})
	if err != nil || !strings.HasPrefix(u, srv.URL+"/oauth/v2/authorization?") || !strings.Contains(u, "w_member_social") || !strings.Contains(u, "state=st") {
		t.Fatalf("consent url: %v %s", err, u)
	}
	if _, _, err := p.AuthFinish(context.Background(), connections.AuthFinishIn{App: app, Code: "bad"}); err == nil {
		t.Fatal("a bad code must fail")
	}
	b, id, err := p.AuthFinish(context.Background(), connections.AuthFinishIn{App: app, Code: "good-code", Verifier: "v"})
	if err != nil || id.Ref != "abc123" || id.Email != "valeria@example.com" {
		t.Fatalf("finish: %v %+v", err, id)
	}
	// no refresh token from LinkedIn: the access token is what the vault keeps, and Refresh returns it
	if b.RefreshToken != accessPrefix+"AQV-member-token" {
		t.Fatalf("stored secret = %q", b.RefreshToken)
	}
	tok, err := p.Refresh(context.Background(), app, b.RefreshToken)
	if err != nil || tok.Value != "AQV-member-token" {
		t.Fatalf("refresh: %v %+v", err, tok)
	}

	h := connections.Handle{Org: "o", ConnectionID: "c", Mode: "live", HTTP: &http.Client{Transport: bearer{"AQV-member-token"}}}
	if tr := p.Test(context.Background(), h); !tr.OK || tr.Account != "valeria@example.com" {
		t.Fatalf("test: %+v", tr)
	}
	ex := p.Open(h)
	res, err := ex.Execute(context.Background(), connections.ExecCall{Tool: "linkedin", Action: "create_post", Args: map[string]any{"text": "Lanzamos en México 🚀"}})
	if err != nil || res.Data["post_id"] != "urn:li:share:777" {
		t.Fatalf("post: %v %+v", err, res)
	}
	fl.mu.Lock()
	post := fl.posts[0]
	fl.mu.Unlock()
	if post["author"] != "urn:li:person:abc123" || !strings.Contains(mustJSON(post), "Lanzamos en México") {
		t.Fatalf("posted body: %v", post)
	}
	for _, bad := range []string{"", strings.Repeat("x", MaxPostChars+1)} {
		if _, err := ex.Execute(context.Background(), connections.ExecCall{Tool: "linkedin", Action: "create_post", Args: map[string]any{"text": bad}}); connections.Classify(err).Code != connections.CodeInvalidArgs {
			t.Fatalf("text of %d chars must be invalid: %v", len(bad), err)
		}
	}
	if _, err := ex.Execute(context.Background(), connections.ExecCall{Tool: "linkedin", Action: "send_message"}); err == nil {
		t.Fatal("unknown actions are refused")
	}

	if err := p.Revoke(context.Background(), app, b.RefreshToken); err != nil {
		t.Fatal(err)
	}
	fl.mu.Lock()
	rv := fl.revoked
	fl.mu.Unlock()
	if rv.Get("token") != "AQV-member-token" || rv.Get("client_id") != "cid" || rv.Get("client_secret") != "s3cret" {
		t.Fatalf("revoke form: %v", rv)
	}
}

func TestLinkedInManifestOnlyOffersWhatTheAPIAllows(t *testing.T) {
	p, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	m := p.Manifest()
	post, ok := m.Capabilities["post.create"]
	if !ok || !post.AlwaysApproval || post.HoldSecondsDefault <= 0 || post.Default || post.Risk != "high" {
		t.Fatalf("posting must always need approval and a hold: %+v", post)
	}
	for _, never := range []string{"messages.read", "messages.send", "connections.read", "company.admin"} {
		found := false
		for _, n := range m.NeverOffered {
			found = found || n == never
		}
		if !found {
			t.Fatalf("%s must be listed as never offered", never)
		}
	}
	// simulated connections work without any LinkedIn account
	ex := p.Open(connections.Handle{ConnectionID: "sim1", Mode: connections.ModeSimulated})
	if _, err := ex.Execute(context.Background(), connections.ExecCall{Action: "create_post", Args: map[string]any{"text": "Hola"}}); err != nil {
		t.Fatal(err)
	}
	if got := p.FakeFor("sim1").Posts; len(got) != 1 || got[0] != "Hola" {
		t.Fatalf("simulated posts: %v", got)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
