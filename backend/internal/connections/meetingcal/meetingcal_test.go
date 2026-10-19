package meetingcal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/connections/googleauth"
)

func TestManifestsAreSeparateReadAndWriteAndHaveNoAgentTools(t *testing.T) {
	ms, err := connections.LoadManifests()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{GoogleID, MicrosoftID} {
		m, ok := ms[id]
		if !ok {
			t.Fatalf("manifest %s missing", id)
		}
		if m.ClassOf([]string{"calendar.freebusy", "calendar.sync"}) != "mixed" {
			t.Errorf("%s: freebusy (read) and sync (write) must be separate connections", id)
		}
		for name, c := range m.Capabilities {
			if len(c.Tools) != 0 {
				t.Errorf("%s/%s: agents get no tools from the meetings calendars", id, name)
			}
		}
		if _, ok := m.AuthMethodFor("oauth2_pkce"); !ok {
			t.Errorf("%s: OAuth with PKCE expected", id)
		}
	}
}

func TestMicrosoftOAuthFlowWithBYOApp(t *testing.T) {
	var tokenForms []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token"):
			_ = r.ParseForm()
			tokenForms = append(tokenForms, r.PostForm)
			if r.PostForm.Get("grant_type") == "refresh_token" && r.PostForm.Get("refresh_token") == "dead" {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "AADSTS secret details"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "refresh_token": "rt", "expires_in": 3600,
				"scope": "https://graph.microsoft.com/Calendars.ReadWrite openid email User.Read"})
		case r.URL.Path == "/me":
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "oid-1", "userPrincipalName": "ana@contoso.com"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	p, err := NewMicrosoft(MicrosoftConfig{Tenant: "tenant-1", LoginBase: srv.URL, GraphBase: srv.URL, HTTP: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	app := connections.OAuthApp{ClientID: "cid", ClientSecret: "csecret", RedirectURL: "https://app/cb"}
	am, _ := p.Manifest().AuthMethodFor("oauth2_pkce")
	u, err := p.AuthStart(connections.AuthStartIn{App: app, State: "st", Challenge: "ch", Scopes: p.Manifest().ScopesFor([]string{"calendar.sync"}, am)})
	if err != nil {
		t.Fatal(err)
	}
	pu, _ := url.Parse(u)
	if !strings.HasPrefix(u, srv.URL+"/tenant-1/oauth2/v2.0/authorize?") {
		t.Fatalf("tenant must be in the path: %s", u)
	}
	q := pu.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("state") != "st" || q.Get("client_id") != "cid" {
		t.Fatalf("query: %v", q)
	}
	for _, s := range []string{"Calendars.ReadWrite", "offline_access", "openid"} {
		if !strings.Contains(q.Get("scope"), s) {
			t.Errorf("scope %s missing in %q", s, q.Get("scope"))
		}
	}
	b, id, err := p.AuthFinish(context.Background(), connections.AuthFinishIn{App: app, Code: "code", Verifier: "ver"})
	if err != nil {
		t.Fatal(err)
	}
	if b.RefreshToken != "rt" || id.Email != "ana@contoso.com" || id.Ref != "oid-1" {
		t.Fatalf("bundle/identity: %+v %+v", b, id)
	}
	granted := p.Manifest().CapabilitiesGrantedBy([]string{"calendar.sync"}, b.Scopes)
	if len(granted) != 1 {
		t.Fatalf("Graph's URL-style scopes must be normalized so the capability counts as granted: %v", b.Scopes)
	}
	if tokenForms[0].Get("code_verifier") != "ver" || tokenForms[0].Get("client_secret") != "csecret" {
		t.Fatalf("exchange form: %v", tokenForms[0])
	}
	if _, err := p.Refresh(context.Background(), app, "rt"); err != nil {
		t.Fatal(err)
	}
	_, err = p.Refresh(context.Background(), app, "dead")
	if pe := connections.Classify(err); pe.Code != connections.CodeReauthRequired || strings.Contains(err.Error(), "AADSTS") {
		t.Fatalf("invalid_grant must become reauth_required without provider text: %v", err)
	}
}

func TestGoogleSyncProviderUsesSharedOAuth(t *testing.T) {
	p, err := NewGoogle(googleauth.Endpoints{})
	if err != nil {
		t.Fatal(err)
	}
	am, _ := p.Manifest().AuthMethodFor("oauth2_pkce")
	u, _ := p.AuthStart(connections.AuthStartIn{App: connections.OAuthApp{ClientID: "c", RedirectURL: "https://app/cb"}, State: "s", Challenge: "c",
		Scopes: p.Manifest().ScopesFor([]string{"calendar.freebusy"}, am)})
	if !strings.Contains(u, "calendar.freebusy") || strings.Contains(u, "calendar.events") {
		t.Fatalf("free/busy connection must not ask for event scopes: %s", u)
	}
	if _, err := p.Open(connections.Handle{}).Execute(context.Background(), connections.ExecCall{Tool: "calendar", Action: "create_event"}); err == nil {
		t.Fatal("agents have no tools on this provider")
	}
}
