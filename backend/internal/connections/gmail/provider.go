// Package gmail is the Google Gmail adapter: OAuth (authorization code +
// PKCE), read/search, drafts and send, plus an in-memory fake used for
// simulated connections. Endpoints and scopes were verified against Google's
// documentation; see docs/architecture/integrations-credentials.md section 17.
package gmail

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/connections"
)

// Config holds the Google endpoints (overridable for tests).
type Config struct {
	AuthURL     string
	TokenURL    string
	RevokeURL   string
	UserinfoURL string
	APIBase     string // https://gmail.googleapis.com/gmail/v1
	// HTTP performs unauthenticated token-endpoint calls (default http.DefaultClient with timeout).
	HTTP *http.Client
}

// DefaultConfig returns the production endpoints.
func DefaultConfig() Config {
	return Config{
		AuthURL:     "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:    "https://oauth2.googleapis.com/token",
		RevokeURL:   "https://oauth2.googleapis.com/revoke",
		UserinfoURL: "https://openidconnect.googleapis.com/v1/userinfo",
		APIBase:     "https://gmail.googleapis.com/gmail/v1",
	}
}

// Provider implements connections.Provider for Gmail.
type Provider struct {
	cfg      Config
	manifest connections.Manifest

	mu    sync.Mutex
	fakes map[string]*Fake
}

// New builds the provider with the embedded manifest.
func New(cfg Config) (*Provider, error) {
	ms, err := connections.LoadManifests()
	if err != nil {
		return nil, err
	}
	d := DefaultConfig()
	if cfg.AuthURL == "" {
		cfg.AuthURL = d.AuthURL
	}
	if cfg.TokenURL == "" {
		cfg.TokenURL = d.TokenURL
	}
	if cfg.RevokeURL == "" {
		cfg.RevokeURL = d.RevokeURL
	}
	if cfg.UserinfoURL == "" {
		cfg.UserinfoURL = d.UserinfoURL
	}
	if cfg.APIBase == "" {
		cfg.APIBase = d.APIBase
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 20 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &Provider{cfg: cfg, manifest: ms["google_gmail"], fakes: map[string]*Fake{}}, nil
}

func (p *Provider) Manifest() connections.Manifest { return p.manifest }

// FakeFor returns the simulated mailbox of a connection (tests and demos).
func (p *Provider) FakeFor(connID string) *Fake {
	p.mu.Lock()
	defer p.mu.Unlock()
	f, ok := p.fakes[connID]
	if !ok {
		f = NewFake()
		p.fakes[connID] = f
	}
	return f
}

func (p *Provider) Open(h connections.Handle) connections.Executor {
	if h.Mode == connections.ModeSimulated {
		return p.FakeFor(h.ConnectionID)
	}
	return &live{base: p.cfg.APIBase, h: h}
}

func (p *Provider) AuthStart(in connections.AuthStartIn) (string, error) {
	am, _ := p.manifest.AuthMethodFor("oauth2_pkce")
	q := url.Values{}
	q.Set("client_id", in.App.ClientID)
	q.Set("redirect_uri", in.App.RedirectURL)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(in.Scopes, " "))
	q.Set("state", in.State)
	q.Set("code_challenge", in.Challenge)
	q.Set("code_challenge_method", "S256")
	// Deliberately NO include_granted_scopes: a token for the read connection
	// must not inherit the write scopes of another connection on the same
	// Google account (separate read/write connections).
	for k, v := range am.ExtraParams {
		q.Set(k, v)
	}
	if in.LoginHint != "" {
		q.Set("login_hint", in.LoginHint)
	}
	return p.cfg.AuthURL + "?" + q.Encode(), nil
}

type tokenResp struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

// postForm sends an urlencoded POST and never surfaces the response body.
func (p *Provider) postForm(ctx context.Context, endpoint string, form url.Values, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, &connections.ProviderError{Code: connections.CodeProviderError}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.cfg.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, &connections.ProviderError{Code: connections.CodeProviderError}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if out != nil {
		_ = json.Unmarshal(body, out)
	}
	return resp.StatusCode, nil
}

func (p *Provider) AuthFinish(ctx context.Context, in connections.AuthFinishIn) (connections.Bundle, connections.Identity, error) {
	form := url.Values{"code": {in.Code}, "client_id": {in.App.ClientID}, "client_secret": {in.App.ClientSecret},
		"redirect_uri": {in.App.RedirectURL}, "grant_type": {"authorization_code"}, "code_verifier": {in.Verifier}}
	var tr tokenResp
	st, err := p.postForm(ctx, p.cfg.TokenURL, form, &tr)
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
	b := connections.Bundle{RefreshToken: tr.RefreshToken, Scopes: strings.Fields(tr.Scope),
		Access: connections.AccessToken{Value: tr.AccessToken, Expiry: time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)}}
	id, err := p.userinfo(ctx, tr.AccessToken)
	return b, id, err
}

func (p *Provider) userinfo(ctx context.Context, token string) (connections.Identity, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.UserinfoURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := p.cfg.HTTP.Do(req)
	if err != nil {
		return connections.Identity{}, &connections.ProviderError{Code: connections.CodeProviderError}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return connections.Identity{}, &connections.ProviderError{Status: resp.StatusCode, Code: statusCode(resp.StatusCode)}
	}
	var u struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&u); err != nil || u.Sub == "" {
		return connections.Identity{}, &connections.ProviderError{Code: connections.CodeProviderError}
	}
	return connections.Identity{Email: u.Email, Ref: u.Sub}, nil
}

func (p *Provider) Refresh(ctx context.Context, app connections.OAuthApp, refreshToken string) (connections.AccessToken, error) {
	form := url.Values{"client_id": {app.ClientID}, "client_secret": {app.ClientSecret},
		"refresh_token": {refreshToken}, "grant_type": {"refresh_token"}}
	var tr tokenResp
	st, err := p.postForm(ctx, p.cfg.TokenURL, form, &tr)
	if err != nil {
		return connections.AccessToken{}, err
	}
	if st != http.StatusOK || tr.AccessToken == "" {
		code := connections.CodeProviderError
		if tr.Error == "invalid_grant" || st == http.StatusUnauthorized {
			code = connections.CodeReauthRequired
		}
		return connections.AccessToken{}, &connections.ProviderError{Status: st, Code: code}
	}
	return connections.AccessToken{Value: tr.AccessToken, Expiry: time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)}, nil
}

func (p *Provider) Revoke(ctx context.Context, _ connections.OAuthApp, token string) error {
	st, err := p.postForm(ctx, p.cfg.RevokeURL, url.Values{"token": {token}}, nil)
	if err != nil {
		return err
	}
	// 200 = revoked; 400 invalid_token = already revoked/expired (nothing left to do).
	if st == http.StatusOK || st == http.StatusBadRequest {
		return nil
	}
	return &connections.ProviderError{Status: st, Code: connections.CodeProviderError}
}

func (p *Provider) Test(ctx context.Context, h connections.Handle) connections.TestResult {
	if h.HTTP == nil {
		return connections.TestResult{OK: false, Code: connections.CodeConnectionUnavailable}
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.UserinfoURL, nil)
	start := time.Now()
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return connections.TestResult{OK: false, Code: connections.Classify(err).Code}
	}
	defer resp.Body.Close()
	res := connections.TestResult{LatencyMS: int(time.Since(start).Milliseconds())}
	if resp.StatusCode != http.StatusOK {
		res.Code = statusCode(resp.StatusCode)
		return res
	}
	var u struct {
		Email string `json:"email"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&u)
	res.OK, res.Account = true, u.Email
	return res
}

// statusCode maps an HTTP status to a stable code.
func statusCode(st int) string {
	switch {
	case st == http.StatusUnauthorized:
		return connections.CodeReauthRequired
	case st == http.StatusForbidden:
		return "insufficient_scope"
	case st == http.StatusNotFound:
		return "not_found"
	case st == http.StatusTooManyRequests:
		return "rate_limited"
	}
	return connections.CodeProviderError
}

func retryAfter(h http.Header) time.Duration {
	if n, err := strconv.Atoi(h.Get("Retry-After")); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return 0
}
