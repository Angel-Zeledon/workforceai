// Package googleauth is the OAuth (authorization code + PKCE) half shared by
// the Google adapters other than Gmail (Calendar, Drive): consent URL, code
// exchange, refresh, revoke and the identity check. It mirrors gmail's flow,
// including the deliberate absence of include_granted_scopes (a read
// connection must not inherit write scopes granted to another connection).
package googleauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aiworkforce/backend/internal/connections"
)

// Endpoints are Google's OAuth endpoints (overridable for tests).
type Endpoints struct {
	AuthURL     string
	TokenURL    string
	RevokeURL   string
	UserinfoURL string
	// HTTP performs the unauthenticated token-endpoint calls.
	HTTP *http.Client
}

// Defaults fills the empty endpoints with Google's production ones.
func (e Endpoints) Defaults() Endpoints {
	if e.AuthURL == "" {
		e.AuthURL = "https://accounts.google.com/o/oauth2/v2/auth"
	}
	if e.TokenURL == "" {
		e.TokenURL = "https://oauth2.googleapis.com/token"
	}
	if e.RevokeURL == "" {
		e.RevokeURL = "https://oauth2.googleapis.com/revoke"
	}
	if e.UserinfoURL == "" {
		e.UserinfoURL = "https://openidconnect.googleapis.com/v1/userinfo"
	}
	if e.HTTP == nil {
		e.HTTP = &http.Client{Timeout: 20 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return e
}

// OAuth implements the auth methods of connections.Provider for one manifest.
type OAuth struct {
	E        Endpoints
	Manifest connections.Manifest
}

func (o *OAuth) AuthStart(in connections.AuthStartIn) (string, error) {
	am, _ := o.Manifest.AuthMethodFor("oauth2_pkce")
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
	return o.E.AuthURL + "?" + q.Encode(), nil
}

type tokenResp struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

// postForm sends an urlencoded POST and never surfaces the response body.
func (o *OAuth) postForm(ctx context.Context, endpoint string, form url.Values, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, &connections.ProviderError{Code: connections.CodeProviderError}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := o.E.HTTP.Do(req)
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

func (o *OAuth) AuthFinish(ctx context.Context, in connections.AuthFinishIn) (connections.Bundle, connections.Identity, error) {
	form := url.Values{"code": {in.Code}, "client_id": {in.App.ClientID}, "client_secret": {in.App.ClientSecret},
		"redirect_uri": {in.App.RedirectURL}, "grant_type": {"authorization_code"}, "code_verifier": {in.Verifier}}
	var tr tokenResp
	st, err := o.postForm(ctx, o.E.TokenURL, form, &tr)
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
	id, err := o.userinfo(ctx, &http.Client{Timeout: 20 * time.Second}, tr.AccessToken)
	return b, id, err
}

func (o *OAuth) userinfo(ctx context.Context, c *http.Client, token string) (connections.Identity, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, o.E.UserinfoURL, nil)
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
		Sub   string `json:"sub"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&u); err != nil || u.Sub == "" {
		return connections.Identity{}, &connections.ProviderError{Code: connections.CodeProviderError}
	}
	return connections.Identity{Email: u.Email, Ref: u.Sub}, nil
}

func (o *OAuth) Refresh(ctx context.Context, app connections.OAuthApp, refreshToken string) (connections.AccessToken, error) {
	form := url.Values{"client_id": {app.ClientID}, "client_secret": {app.ClientSecret},
		"refresh_token": {refreshToken}, "grant_type": {"refresh_token"}}
	var tr tokenResp
	st, err := o.postForm(ctx, o.E.TokenURL, form, &tr)
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

func (o *OAuth) Revoke(ctx context.Context, _ connections.OAuthApp, token string) error {
	st, err := o.postForm(ctx, o.E.RevokeURL, url.Values{"token": {token}}, nil)
	if err != nil {
		return err
	}
	if st == http.StatusOK || st == http.StatusBadRequest { // 400 = already revoked/expired
		return nil
	}
	return &connections.ProviderError{Status: st, Code: connections.CodeProviderError}
}

// Test calls the userinfo endpoint through the connection (no data changes).
func (o *OAuth) Test(ctx context.Context, h connections.Handle) connections.TestResult {
	if h.HTTP == nil {
		return connections.TestResult{Code: connections.CodeConnectionUnavailable}
	}
	start := time.Now()
	id, err := o.userinfo(ctx, h.HTTP, "")
	res := connections.TestResult{LatencyMS: int(time.Since(start).Milliseconds())}
	if err != nil {
		res.Code = connections.Classify(err).Code
		return res
	}
	res.OK, res.Account = true, id.Email
	return res
}
