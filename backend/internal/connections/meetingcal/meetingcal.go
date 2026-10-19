// Package meetingcal holds the connection providers behind the Meetings screen:
// Google Calendar (google_calendar_sync) and Microsoft 365 / Outlook
// (microsoft_calendar). They are ordinary connections (OAuth with PKCE, tokens
// in the vault, bring-your-own OAuth app, kill switch, revoke) but they expose
// NO agent tools: only the meetings service uses them, on behalf of a person
// (internal/calendar speaks the calendar APIs through the connection handle).
// The agent-facing Google Calendar tools stay in internal/connections/calendar.
package meetingcal

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/connections/googleauth"
)

// Provider ids.
const (
	GoogleID    = "google_calendar_sync"
	MicrosoftID = "microsoft_calendar"
)

// noTools is the executor of providers that have no agent tools.
type noTools struct{}

func (noTools) Execute(context.Context, connections.ExecCall) (connections.ExecResult, error) {
	return connections.ExecResult{}, connections.InvalidArgs()
}

// ---- Google ----

// Google is the google_calendar_sync provider (OAuth shared with the other Google adapters).
type Google struct{ *googleauth.OAuth }

// NewGoogle builds the provider with the embedded manifest.
func NewGoogle(e googleauth.Endpoints) (*Google, error) {
	ms, err := connections.LoadManifests()
	if err != nil {
		return nil, err
	}
	return &Google{OAuth: &googleauth.OAuth{E: e.Defaults(), Manifest: ms[GoogleID]}}, nil
}

func (g *Google) Manifest() connections.Manifest               { return g.OAuth.Manifest }
func (g *Google) Open(connections.Handle) connections.Executor { return noTools{} }

// ---- Microsoft ----

// MicrosoftConfig holds the endpoints (overridable for tests).
type MicrosoftConfig struct {
	// Tenant is "common" (default), "organizations", "consumers" or a tenant id.
	Tenant string
	// LoginBase is https://login.microsoftonline.com
	LoginBase string
	// GraphBase is https://graph.microsoft.com/v1.0
	GraphBase string
	HTTP      *http.Client
}

// Microsoft is the microsoft_calendar provider (Microsoft identity platform v2 + Graph).
type Microsoft struct {
	cfg      MicrosoftConfig
	manifest connections.Manifest
}

// NewMicrosoft builds the provider with the embedded manifest.
func NewMicrosoft(cfg MicrosoftConfig) (*Microsoft, error) {
	ms, err := connections.LoadManifests()
	if err != nil {
		return nil, err
	}
	if cfg.Tenant == "" {
		cfg.Tenant = "common"
	}
	if cfg.LoginBase == "" {
		cfg.LoginBase = "https://login.microsoftonline.com"
	}
	if cfg.GraphBase == "" {
		cfg.GraphBase = "https://graph.microsoft.com/v1.0"
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 20 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	cfg.LoginBase, cfg.GraphBase = strings.TrimRight(cfg.LoginBase, "/"), strings.TrimRight(cfg.GraphBase, "/")
	return &Microsoft{cfg: cfg, manifest: ms[MicrosoftID]}, nil
}

func (m *Microsoft) Manifest() connections.Manifest               { return m.manifest }
func (m *Microsoft) Open(connections.Handle) connections.Executor { return noTools{} }

func (m *Microsoft) authorizeURL() string {
	return m.cfg.LoginBase + "/" + url.PathEscape(m.cfg.Tenant) + "/oauth2/v2.0/authorize"
}
func (m *Microsoft) tokenURL() string {
	return m.cfg.LoginBase + "/" + url.PathEscape(m.cfg.Tenant) + "/oauth2/v2.0/token"
}

func (m *Microsoft) AuthStart(in connections.AuthStartIn) (string, error) {
	am, _ := m.manifest.AuthMethodFor("oauth2_pkce")
	q := url.Values{}
	q.Set("client_id", in.App.ClientID)
	q.Set("redirect_uri", in.App.RedirectURL)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(in.Scopes, " "))
	q.Set("state", in.State)
	q.Set("code_challenge", in.Challenge)
	q.Set("code_challenge_method", "S256")
	for k, v := range am.ExtraParams {
		q.Set(k, v)
	}
	if in.LoginHint != "" {
		q.Set("login_hint", in.LoginHint)
	}
	return m.authorizeURL() + "?" + q.Encode(), nil
}

type msToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

func (m *Microsoft) postForm(ctx context.Context, form url.Values, out *msToken) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.tokenURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return 0, &connections.ProviderError{Code: connections.CodeProviderError}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := m.cfg.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, &connections.ProviderError{Code: connections.CodeProviderError}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = json.Unmarshal(body, out)
	return resp.StatusCode, nil
}

// normScopes turns Graph's "https://graph.microsoft.com/Calendars.Read" into "Calendars.Read".
func normScopes(s string) []string {
	var out []string
	for _, f := range strings.Fields(s) {
		out = append(out, strings.TrimPrefix(f, "https://graph.microsoft.com/"))
	}
	return out
}

func (m *Microsoft) AuthFinish(ctx context.Context, in connections.AuthFinishIn) (connections.Bundle, connections.Identity, error) {
	form := url.Values{"client_id": {in.App.ClientID}, "client_secret": {in.App.ClientSecret}, "code": {in.Code},
		"redirect_uri": {in.App.RedirectURL}, "grant_type": {"authorization_code"}, "code_verifier": {in.Verifier}}
	var tr msToken
	st, err := m.postForm(ctx, form, &tr)
	if err != nil {
		return connections.Bundle{}, connections.Identity{}, err
	}
	if st != http.StatusOK || tr.AccessToken == "" {
		code := connections.CodeProviderError
		if tr.Error == "invalid_grant" {
			code = connections.CodeReauthRequired
		}
		return connections.Bundle{}, connections.Identity{}, &connections.ProviderError{Status: st, Code: code}
	}
	b := connections.Bundle{RefreshToken: tr.RefreshToken, Scopes: normScopes(tr.Scope),
		Access: connections.AccessToken{Value: tr.AccessToken, Expiry: time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)}}
	id, err := m.whoami(ctx, &http.Client{Timeout: 20 * time.Second}, tr.AccessToken)
	return b, id, err
}

func (m *Microsoft) whoami(ctx context.Context, c *http.Client, token string) (connections.Identity, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, m.cfg.GraphBase+"/me?$select=id,mail,userPrincipalName", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		return connections.Identity{}, &connections.ProviderError{Code: connections.CodeProviderError}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return connections.Identity{}, &connections.ProviderError{Status: resp.StatusCode, Code: connections.StatusCode(resp.StatusCode)}
	}
	var u struct {
		ID   string `json:"id"`
		Mail string `json:"mail"`
		UPN  string `json:"userPrincipalName"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&u); err != nil || u.ID == "" {
		return connections.Identity{}, &connections.ProviderError{Code: connections.CodeProviderError}
	}
	email := u.Mail
	if email == "" {
		email = u.UPN
	}
	return connections.Identity{Email: email, Ref: u.ID}, nil
}

func (m *Microsoft) Refresh(ctx context.Context, app connections.OAuthApp, refreshToken string) (connections.AccessToken, error) {
	form := url.Values{"client_id": {app.ClientID}, "client_secret": {app.ClientSecret}, "refresh_token": {refreshToken},
		"grant_type": {"refresh_token"}, "scope": {"https://graph.microsoft.com/.default offline_access"}}
	var tr msToken
	st, err := m.postForm(ctx, form, &tr)
	if err != nil {
		return connections.AccessToken{}, err
	}
	if st != http.StatusOK || tr.AccessToken == "" {
		code := connections.CodeProviderError
		if tr.Error == "invalid_grant" || tr.Error == "interaction_required" || st == http.StatusUnauthorized {
			code = connections.CodeReauthRequired
		}
		return connections.AccessToken{}, &connections.ProviderError{Status: st, Code: code}
	}
	return connections.AccessToken{Value: tr.AccessToken, Expiry: time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)}, nil
}

// Revoke: the Microsoft identity platform has no token revocation endpoint. The
// credential is destroyed in the vault by the service; the person can also remove
// the app at myapps.microsoft.com. Returning nil lets the revoke flow complete.
func (m *Microsoft) Revoke(context.Context, connections.OAuthApp, string) error { return nil }

func (m *Microsoft) Test(ctx context.Context, h connections.Handle) connections.TestResult {
	if h.HTTP == nil {
		return connections.TestResult{Code: connections.CodeConnectionUnavailable}
	}
	start := time.Now()
	id, err := m.whoami(ctx, h.HTTP, "")
	res := connections.TestResult{LatencyMS: int(time.Since(start).Milliseconds())}
	if err != nil {
		res.Code = connections.Classify(err).Code
		return res
	}
	res.OK, res.Account = true, id.Email
	return res
}
