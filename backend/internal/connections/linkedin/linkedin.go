// Package linkedin is the LinkedIn adapter. It only offers what LinkedIn's
// self-serve API products allow an app without partner approval:
//
//   - Sign In with LinkedIn using OpenID Connect: the connected member's own
//     name, picture and email (profile.read, linkedin.whoami).
//   - Share on LinkedIn (w_member_social): text posts as that member
//     (post.create, linkedin.create_post), ALWAYS with a human approval and a
//     hold before it goes out, like an email.
//
// Reading messages, connections, other people's profiles or managing company
// pages needs LinkedIn partner programs and is never offered (manifest).
// Access tokens last about 60 days; LinkedIn only issues refresh tokens to
// approved partners, so without one the connection asks to re-authorize when
// the token expires (reauth_required) instead of failing silently.
package linkedin

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/connections/googleauth"
)

// Config holds the endpoints (overridable for tests).
type Config struct {
	AuthURL, TokenURL, RevokeURL, UserinfoURL string
	APIBase                                   string // https://api.linkedin.com
	HTTP                                      *http.Client
}

// Provider implements connections.Provider for LinkedIn.
type Provider struct {
	base     string
	manifest connections.Manifest
	oauth    *googleauth.OAuth // standard OAuth 2.0 + OIDC userinfo, with LinkedIn's endpoints

	mu    sync.Mutex
	fakes map[string]*Fake
}

// New builds the provider with the embedded manifest.
func New(cfg Config) (*Provider, error) {
	ms, err := connections.LoadManifests()
	if err != nil {
		return nil, err
	}
	m := ms["linkedin"]
	am, _ := m.AuthMethodFor("oauth2_pkce")
	e := googleauth.Endpoints{AuthURL: or(cfg.AuthURL, am.AuthorizeURL), TokenURL: or(cfg.TokenURL, am.TokenURL),
		RevokeURL: or(cfg.RevokeURL, am.RevokeURL), UserinfoURL: or(cfg.UserinfoURL, "https://api.linkedin.com/v2/userinfo"), HTTP: cfg.HTTP}
	return &Provider{base: strings.TrimRight(or(cfg.APIBase, "https://api.linkedin.com"), "/"), manifest: m,
		oauth: &googleauth.OAuth{E: e.Defaults(), Manifest: m}, fakes: map[string]*Fake{}}, nil
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (p *Provider) Manifest() connections.Manifest { return p.manifest }

func (p *Provider) AuthStart(in connections.AuthStartIn) (string, error) {
	return p.oauth.AuthStart(in)
}

// AuthFinish exchanges the code. Without a refresh token (the normal case for
// non-partner apps) the access token itself is kept in the vault and reused
// until it expires (about 60 days): then the member re-authorizes.
func (p *Provider) AuthFinish(ctx context.Context, in connections.AuthFinishIn) (connections.Bundle, connections.Identity, error) {
	b, id, err := p.oauth.AuthFinish(ctx, in)
	if err != nil {
		return b, id, err
	}
	if b.RefreshToken == "" {
		b.RefreshToken = accessPrefix + b.Access.Value
	}
	return b, id, nil
}

// accessPrefix marks a stored access token (no refresh token was issued).
const accessPrefix = "access:"

// Refresh returns the stored access token when no refresh token exists, or
// uses LinkedIn's refresh grant (partners only) otherwise.
func (p *Provider) Refresh(ctx context.Context, app connections.OAuthApp, secret string) (connections.AccessToken, error) {
	if tok, ok := strings.CutPrefix(secret, accessPrefix); ok {
		return connections.StaticToken(tok)
	}
	return p.oauth.Refresh(ctx, app, secret)
}

// Revoke asks LinkedIn to invalidate the token (its revoke endpoint needs the
// app's credentials, unlike Google's).
func (p *Provider) Revoke(ctx context.Context, app connections.OAuthApp, token string) error {
	form := url.Values{"client_id": {app.ClientID}, "client_secret": {app.ClientSecret}, "token": {strings.TrimPrefix(token, accessPrefix)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.oauth.E.RevokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return &connections.ProviderError{Code: connections.CodeProviderError}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.oauth.E.HTTP.Do(req)
	if err != nil {
		return &connections.ProviderError{Code: connections.CodeProviderError}
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusBadRequest { // 400 = already revoked/expired
		return nil
	}
	return &connections.ProviderError{Status: resp.StatusCode, Code: connections.CodeProviderError}
}

// Test reads the member's own OIDC userinfo (no data changes).
func (p *Provider) Test(ctx context.Context, h connections.Handle) connections.TestResult {
	start := time.Now()
	me, _, err := (&live{base: p.base, h: h}).whoami(ctx)
	res := connections.TestResult{LatencyMS: int(time.Since(start).Milliseconds())}
	if err != nil {
		res.Code = connections.Classify(err).Code
		return res
	}
	res.OK, res.Account = true, me.Email
	return res
}

// FakeFor returns the simulated LinkedIn member of a connection.
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
		return &scoped{next: p.FakeFor(h.ConnectionID)}
	}
	return &scoped{next: &live{base: p.base, h: h}}
}

// Member is the connected member (OIDC userinfo).
type Member struct {
	Sub, Name, Email string
}

type backend interface {
	whoami(ctx context.Context) (Member, int, error)
	post(ctx context.Context, author, text string) (string, int, error)
}

// scoped validates the arguments; the gateway already enforced grants,
// approvals, holds, limits and the kill switch.
type scoped struct{ next backend }

// MaxPostChars is LinkedIn's limit for the commentary of a post.
const MaxPostChars = 3000

func (s *scoped) Execute(ctx context.Context, c connections.ExecCall) (connections.ExecResult, error) {
	switch c.Action {
	case "whoami":
		me, st, err := s.next.whoami(ctx)
		if err != nil {
			return connections.ExecResult{Status: st}, err
		}
		return connections.ExecResult{Status: st, Summary: "connected member", ResourceRef: "me",
			Items: []connections.Item{{ID: me.Sub, Meta: map[string]any{"member": me.Sub},
				Texts: []connections.Text{{Name: "name", Value: me.Name}, {Name: "email", Value: me.Email}}}}}, nil
	case "create_post":
		text := strings.TrimSpace(connections.ArgStr(c.Args, "text", "body"))
		if text == "" || len([]rune(text)) > MaxPostChars {
			return connections.ExecResult{}, connections.InvalidArgs()
		}
		me, st, err := s.next.whoami(ctx)
		if err != nil {
			return connections.ExecResult{Status: st}, err
		}
		id, st, err := s.next.post(ctx, "urn:li:person:"+me.Sub, text)
		if err != nil {
			return connections.ExecResult{Status: st}, err
		}
		return connections.ExecResult{Status: st, Summary: "post published", ResourceRef: "member post", Data: map[string]any{"post_id": id}}, nil
	}
	return connections.ExecResult{}, connections.InvalidArgs()
}

// ---- live client ----

type live struct {
	base string
	h    connections.Handle
}

func (l *live) whoami(ctx context.Context) (Member, int, error) {
	var out struct {
		Sub   string `json:"sub"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	st, _, err := connections.DoJSON(ctx, l.h, http.MethodGet, l.base+"/v2/userinfo", nil, nil, &out)
	if err == nil && out.Sub == "" {
		err = &connections.ProviderError{Status: st, Code: connections.CodeProviderError}
	}
	return Member{Sub: out.Sub, Name: out.Name, Email: out.Email}, st, err
}

// post publishes a text share with the Share on LinkedIn API (ugcPosts).
func (l *live) post(ctx context.Context, author, text string) (string, int, error) {
	body := map[string]any{
		"author":         author,
		"lifecycleState": "PUBLISHED",
		"specificContent": map[string]any{"com.linkedin.ugc.ShareContent": map[string]any{
			"shareCommentary": map[string]any{"text": text}, "shareMediaCategory": "NONE"}},
		"visibility": map[string]any{"com.linkedin.ugc.MemberNetworkVisibility": "PUBLIC"},
	}
	var out struct {
		ID string `json:"id"`
	}
	st, _, err := connections.DoJSON(ctx, l.h, http.MethodPost, l.base+"/v2/ugcPosts", nil, body, &out)
	return out.ID, st, err
}

// ---- simulated member ----

// Fake is an in-memory LinkedIn member for simulated connections.
type Fake struct {
	mu    sync.Mutex
	Posts []string
}

func NewFake() *Fake { return &Fake{} }

func (f *Fake) whoami(context.Context) (Member, int, error) {
	return Member{Sub: "sim-member", Name: "Simulated Member", Email: "member@example.com"}, 200, nil
}

func (f *Fake) post(_ context.Context, _ string, text string) (string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Posts = append(f.Posts, text)
	return fmt.Sprintf("urn:li:share:sim-%d", len(f.Posts)), 201, nil
}
