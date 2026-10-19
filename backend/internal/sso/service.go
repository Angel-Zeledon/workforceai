package sso

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/auth"
)

// Sealer encrypts short secrets under the organization's data key (internal/vault.Vault implements it).
type Sealer interface {
	Enabled() bool
	Seal(ctx context.Context, org, purpose string, plain []byte) ([]byte, error)
	Unseal(ctx context.Context, org, purpose string, sealed []byte) ([]byte, error)
}

// Resolver looks up TXT records (net.Resolver implements it).
type Resolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// Options configures a Service.
type Options struct {
	Store    Store
	Sealer   Sealer
	HTTP     *http.Client // default NewSafeClient()
	Resolver Resolver     // default net.DefaultResolver
	// RedirectURL is the public URL of GET /api/v1/sso/callback, registered at the identity provider.
	RedirectURL string
	// AppURL is where the browser lands after the callback (the login page of the frontend).
	AppURL string
	// AllowHTTPIssuer permits http:// issuers (tests and local IdPs only).
	AllowHTTPIssuer bool
	// Gate (optional) can veto a sign-in after the identity is proven, e.g. an account that SCIM deprovisioned.
	Gate  func(ctx context.Context, orgID, email string) error
	Audit func(ctx context.Context, orgID, actor, action string, details map[string]any)
	Now   func() time.Time
}

// Service holds the SSO logic. Safe for concurrent use.
type Service struct {
	o     Options
	jwks  *jwksCache
	disco *discoCache
}

// New builds a Service.
func New(o Options) (*Service, error) {
	if o.Store == nil {
		return nil, errors.New("sso: Store is required")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.HTTP == nil {
		o.HTTP = NewSafeClient()
	}
	if o.Resolver == nil {
		o.Resolver = defaultResolver{}
	}
	if o.Audit == nil {
		o.Audit = func(context.Context, string, string, string, map[string]any) {}
	}
	return &Service{o: o, jwks: newJWKSCache(o.Now), disco: &discoCache{m: map[string]discoEntry{}}}, nil
}

const (
	stateTTL    = 10 * time.Minute
	exchangeTTL = 60 * time.Second
	secretPurp  = "sso:oidc:client_secret"
)

func hashOf(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }

func randToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("sso: crypto/rand unavailable")
	}
	return b64.EncodeToString(b)
}

// ---- configuration (admin) ----

// ConfigInput is what an administrator submits. An empty ClientSecret keeps the stored one.
type ConfigInput struct {
	Enabled              bool      `json:"enabled"`
	Issuer               string    `json:"issuer"`
	ClientID             string    `json:"client_id"`
	ClientSecret         string    `json:"client_secret"`
	DefaultRole          auth.Role `json:"default_role"`
	JIT                  bool      `json:"jit"`
	AllowUnverifiedEmail bool      `json:"allow_unverified_email"`
}

// View is the API representation of a Config: it never carries the secret, only whether one is stored.
type View struct {
	Configured           bool      `json:"configured"`
	Enabled              bool      `json:"enabled"`
	Issuer               string    `json:"issuer"`
	ClientID             string    `json:"client_id"`
	HasSecret            bool      `json:"has_secret"`
	DefaultRole          auth.Role `json:"default_role"`
	JIT                  bool      `json:"jit"`
	AllowUnverifiedEmail bool      `json:"allow_unverified_email"`
	RedirectURL          string    `json:"redirect_url"`
	EncryptionAvailable  bool      `json:"encryption_available"`
	UpdatedAt            time.Time `json:"updated_at,omitempty"`
}

// ValidationError lists invalid fields of a request.
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return fmt.Sprintf("sso: invalid input: %v", e.Fields) }

func (s *Service) view(c Config, found bool) View {
	v := View{RedirectURL: s.o.RedirectURL, EncryptionAvailable: s.o.Sealer != nil && s.o.Sealer.Enabled(), DefaultRole: auth.RoleMember}
	if !found {
		return v
	}
	v.Configured, v.Enabled, v.Issuer, v.ClientID = true, c.Enabled, c.Issuer, c.ClientID
	v.HasSecret, v.DefaultRole, v.JIT, v.AllowUnverifiedEmail, v.UpdatedAt = len(c.SealedSecret) > 0, c.DefaultRole, c.JIT, c.AllowUnverifiedEmail, c.UpdatedAt
	return v
}

// GetConfig returns the organization's SSO configuration view.
func (s *Service) GetConfig(ctx context.Context, orgID string) (View, error) {
	c, err := s.o.Store.GetConfig(ctx, orgID)
	if errors.Is(err, ErrNotFound) {
		return s.view(Config{}, false), nil
	}
	return s.view(c, err == nil), err
}

// SaveConfig validates and stores the configuration. When it is enabled the provider is contacted once, so a wrong issuer
// is reported now and not to the first employee who tries to sign in.
func (s *Service) SaveConfig(ctx context.Context, orgID, actor string, in ConfigInput) (View, error) {
	ve := &ValidationError{Fields: map[string]string{}}
	in.Issuer, in.ClientID = strings.TrimSpace(in.Issuer), strings.TrimSpace(in.ClientID)
	if err := ValidIssuerURL(in.Issuer, s.o.AllowHTTPIssuer); err != nil {
		ve.Fields["issuer"] = err.Error()
	}
	if in.ClientID == "" || len(in.ClientID) > 256 {
		ve.Fields["client_id"] = "required"
	}
	if len(in.ClientSecret) > 2048 {
		ve.Fields["client_secret"] = "too long"
	}
	if in.DefaultRole == "" {
		in.DefaultRole = auth.RoleMember
	}
	if !in.DefaultRole.Valid() || in.DefaultRole == auth.RoleOwner {
		ve.Fields["default_role"] = "must be admin, member or viewer"
	}
	if len(ve.Fields) > 0 {
		return View{}, ve
	}
	old, err := s.o.Store.GetConfig(ctx, orgID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return View{}, err
	}
	c := Config{OrgID: orgID, Enabled: in.Enabled, Issuer: in.Issuer, ClientID: in.ClientID, Scopes: "openid email profile",
		DefaultRole: in.DefaultRole, JIT: in.JIT, AllowUnverifiedEmail: in.AllowUnverifiedEmail, UpdatedBy: actor,
		SealedSecret: old.SealedSecret, UpdatedAt: s.o.Now().UTC()}
	if in.ClientSecret != "" {
		if s.o.Sealer == nil || !s.o.Sealer.Enabled() {
			return View{}, ErrNoEncryption
		}
		if c.SealedSecret, err = s.o.Sealer.Seal(ctx, orgID, secretPurp, []byte(in.ClientSecret)); err != nil {
			return View{}, err
		}
	}
	if in.Enabled {
		if _, err := s.discover(ctx, c.Issuer, true); err != nil {
			ve.Fields["issuer"] = "the provider could not be reached or its metadata is invalid"
			return View{}, ve
		}
	}
	if err := s.o.Store.PutConfig(ctx, c); err != nil {
		return View{}, err
	}
	s.o.Audit(ctx, orgID, actor, "sso.config_saved", map[string]any{"enabled": c.Enabled, "issuer": c.Issuer})
	return s.GetConfig(ctx, orgID)
}

// DeleteConfig removes the configuration (and with it the enforcement that depended on it).
func (s *Service) DeleteConfig(ctx context.Context, orgID, actor string) error {
	if err := s.o.Store.DeleteConfig(ctx, orgID); err != nil {
		return err
	}
	if p, _ := s.o.Store.GetPolicy(ctx, orgID); p.EnforceSSO {
		p.EnforceSSO, p.UpdatedBy, p.UpdatedAt = false, actor, s.o.Now().UTC()
		_ = s.o.Store.PutPolicy(ctx, p)
	}
	s.o.Audit(ctx, orgID, actor, "sso.config_deleted", nil)
	return nil
}

// TestConfig checks that the stored issuer answers discovery and publishes signing keys.
func (s *Service) TestConfig(ctx context.Context, orgID string) error {
	c, err := s.o.Store.GetConfig(ctx, orgID)
	if err != nil {
		return err
	}
	d, err := s.discover(ctx, c.Issuer, true)
	if err != nil {
		return err
	}
	_, err = s.jwks.get(ctx, s.o.HTTP, d.JWKSURI, true)
	return err
}

type discoCache struct {
	mu sync.Mutex
	m  map[string]discoEntry
}

type discoEntry struct {
	d  Discovery
	at time.Time
}

func (s *Service) discover(ctx context.Context, issuer string, fresh bool) (Discovery, error) {
	if !fresh {
		s.disco.mu.Lock()
		e, ok := s.disco.m[issuer]
		s.disco.mu.Unlock()
		if ok && s.o.Now().Sub(e.at) < time.Hour {
			return e.d, nil
		}
	}
	d, err := Discover(ctx, s.o.HTTP, issuer, s.o.AllowHTTPIssuer)
	if err != nil {
		return Discovery{}, err
	}
	s.disco.mu.Lock()
	s.disco.m[issuer] = discoEntry{d: d, at: s.o.Now()}
	s.disco.mu.Unlock()
	return d, nil
}

// ---- policy ----

// ErrPolicyNeedsSSO: EnforceSSO needs a working SSO setup, or nobody could sign in.
var ErrPolicyNeedsSSO = errors.New("sso: enforcing SSO needs an enabled SSO configuration and a verified domain")

// OrgPolicy implements auth.PolicyProvider.
func (s *Service) OrgPolicy(ctx context.Context, orgID string) (auth.OrgPolicy, error) {
	p, err := s.o.Store.GetPolicy(ctx, orgID)
	if err != nil {
		return auth.OrgPolicy{}, err
	}
	return auth.OrgPolicy{RequireMFA: p.RequireMFA, EnforceSSO: p.EnforceSSO}, nil
}

// GetPolicy returns the stored policy.
func (s *Service) GetPolicy(ctx context.Context, orgID string) (Policy, error) {
	p, err := s.o.Store.GetPolicy(ctx, orgID)
	p.OrgID = orgID
	return p, err
}

// SetPolicy stores the policy. EnforceSSO is refused unless SSO is enabled and at least one domain is verified.
func (s *Service) SetPolicy(ctx context.Context, orgID, actor string, requireMFA, enforceSSO bool) (Policy, error) {
	if enforceSSO {
		c, err := s.o.Store.GetConfig(ctx, orgID)
		if err != nil || !c.Enabled {
			return Policy{}, ErrPolicyNeedsSSO
		}
		if n, _ := s.verifiedCount(ctx, orgID); n == 0 {
			return Policy{}, ErrPolicyNeedsSSO
		}
	}
	p := Policy{OrgID: orgID, RequireMFA: requireMFA, EnforceSSO: enforceSSO, UpdatedBy: actor, UpdatedAt: s.o.Now().UTC()}
	if err := s.o.Store.PutPolicy(ctx, p); err != nil {
		return Policy{}, err
	}
	s.o.Audit(ctx, orgID, actor, "org.identity_policy_changed", map[string]any{"require_mfa": requireMFA, "enforce_sso": enforceSSO})
	return p, nil
}

func (s *Service) verifiedCount(ctx context.Context, orgID string) (int, error) {
	ds, err := s.o.Store.ListDomains(ctx, orgID)
	n := 0
	for _, d := range ds {
		if d.Verified() {
			n++
		}
	}
	return n, err
}

// ---- the login flow ----

// Errors of the flow. The callback turns each into a stable short code for the login page.
var (
	ErrNoSSO           = errors.New("sso: single sign-on is not available for this address")
	ErrBadState        = errors.New("sso: unknown, used or expired login attempt")
	ErrProvider        = errors.New("sso: the identity provider rejected the request")
	ErrEmailUnverified = errors.New("sso: the provider did not confirm the e-mail address")
	ErrDomain          = errors.New("sso: the e-mail address is not on a domain verified for this organization")
)

func domainOf(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	i := strings.LastIndexByte(email, '@')
	if i < 1 || i == len(email)-1 {
		return ""
	}
	return email[i+1:]
}

func (s *Service) orgFor(ctx context.Context, email string) (Config, error) {
	dom := domainOf(email)
	if dom == "" {
		return Config{}, ErrNoSSO
	}
	org, err := s.o.Store.OrgForVerifiedDomain(ctx, dom)
	if err != nil {
		return Config{}, ErrNoSSO
	}
	c, err := s.o.Store.GetConfig(ctx, org)
	if err != nil || !c.Enabled {
		return Config{}, ErrNoSSO
	}
	return c, nil
}

// Available reports whether the address belongs to a domain with SSO switched on. It answers only yes or no: it never
// says which organization, so the endpoint cannot be used to map customers.
func (s *Service) Available(ctx context.Context, email string) bool {
	_, err := s.orgFor(ctx, email)
	return err == nil
}

// Start begins a login: it records the single-use state (with nonce and PKCE verifier) and returns the provider URL.
func (s *Service) Start(ctx context.Context, email string) (string, error) {
	c, err := s.orgFor(ctx, email)
	if err != nil {
		return "", err
	}
	d, err := s.discover(ctx, c.Issuer, false)
	if err != nil {
		return "", ErrProvider
	}
	state, nonce, verifier := randToken(32), randToken(24), randToken(48)
	if err := s.o.Store.SavePending(ctx, Pending{Kind: KindState, Hash: hashOf(state), OrgID: c.OrgID, Nonce: nonce, Verifier: verifier,
		Identity: auth.FederatedIdentity{Email: strings.ToLower(strings.TrimSpace(email))}, ExpiresAt: s.o.Now().Add(stateTTL)}); err != nil {
		return "", err
	}
	ch := sha256.Sum256([]byte(verifier))
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", c.ClientID)
	q.Set("redirect_uri", s.o.RedirectURL)
	q.Set("scope", c.Scopes)
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("code_challenge", b64.EncodeToString(ch[:]))
	q.Set("code_challenge_method", "S256")
	q.Set("login_hint", strings.ToLower(strings.TrimSpace(email)))
	sep := "?"
	if strings.Contains(d.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return d.AuthorizationEndpoint + sep + q.Encode(), nil
}

// Complete finishes a login from the provider's redirect. On success it returns the single-use exchange code that the
// browser redeems at POST /auth/sso/exchange; no session or token is ever put in a URL.
func (s *Service) Complete(ctx context.Context, state, code string) (string, error) {
	if state == "" || code == "" || len(state) > 256 || len(code) > 4096 {
		return "", ErrBadState
	}
	p, err := s.o.Store.ConsumePending(ctx, KindState, hashOf(state), s.o.Now()) // replay: the second use finds nothing
	if err != nil {
		return "", ErrBadState
	}
	c, err := s.o.Store.GetConfig(ctx, p.OrgID)
	if err != nil || !c.Enabled {
		return "", ErrNoSSO
	}
	d, err := s.discover(ctx, c.Issuer, false)
	if err != nil {
		return "", ErrProvider
	}
	idToken, err := s.redeemCode(ctx, c, d, code, p.Verifier)
	if err != nil {
		return "", err
	}
	ks, err := s.jwks.get(ctx, s.o.HTTP, d.JWKSURI, false)
	if err != nil {
		return "", ErrProvider
	}
	vp := VerifyParams{Issuer: c.Issuer, ClientID: c.ClientID, Nonce: p.Nonce, Now: s.o.Now()}
	claims, err := VerifyIDToken(idToken, ks, vp)
	if errors.Is(err, ErrNoKey) { // the provider rotated its keys: refresh once
		if ks, err = s.jwks.get(ctx, s.o.HTTP, d.JWKSURI, true); err == nil {
			claims, err = VerifyIDToken(idToken, ks, vp)
		}
	}
	if err != nil {
		return "", err
	}
	id, err := s.identityOf(ctx, c, claims)
	if err != nil {
		return "", err
	}
	ex := randToken(32)
	if err := s.o.Store.SavePending(ctx, Pending{Kind: KindExchange, Hash: hashOf(ex), OrgID: c.OrgID, Identity: id, ExpiresAt: s.o.Now().Add(exchangeTTL)}); err != nil {
		return "", err
	}
	return ex, nil
}

// identityOf applies the organization's rules to verified claims: a usable e-mail, confirmed by the provider, on a domain
// this organization has verified. The last check is what stops an IdP (or a typo) from signing someone into an
// organization that does not own their address.
func (s *Service) identityOf(ctx context.Context, c Config, cl Claims) (auth.FederatedIdentity, error) {
	email := strings.ToLower(cl.Email)
	if domainOf(email) == "" {
		return auth.FederatedIdentity{}, ErrEmailUnverified
	}
	if cl.EmailVerified != nil && !*cl.EmailVerified {
		return auth.FederatedIdentity{}, ErrEmailUnverified
	}
	if cl.EmailVerified == nil && !c.AllowUnverifiedEmail {
		return auth.FederatedIdentity{}, ErrEmailUnverified
	}
	org, err := s.o.Store.OrgForVerifiedDomain(ctx, domainOf(email))
	if err != nil || org != c.OrgID {
		return auth.FederatedIdentity{}, ErrDomain
	}
	if s.o.Gate != nil {
		if err := s.o.Gate(ctx, c.OrgID, email); err != nil {
			return auth.FederatedIdentity{}, auth.ErrForbidden
		}
	}
	return auth.FederatedIdentity{OrgID: c.OrgID, Email: email, Name: cl.Name, Provider: "oidc", Role: c.DefaultRole, JIT: c.JIT}, nil
}

func (s *Service) redeemCode(ctx context.Context, c Config, d Discovery, code, verifier string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", s.o.RedirectURL)
	form.Set("client_id", c.ClientID)
	form.Set("code_verifier", verifier)
	if len(c.SealedSecret) > 0 {
		if s.o.Sealer == nil {
			return "", ErrProvider
		}
		plain, err := s.o.Sealer.Unseal(ctx, c.OrgID, secretPurp, c.SealedSecret)
		if err != nil {
			return "", ErrProvider
		}
		form.Set("client_secret", string(plain))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", ErrProvider
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := s.o.HTTP.Do(req)
	if err != nil {
		return "", ErrProvider
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxProviderBody))
	if err != nil || res.StatusCode != http.StatusOK {
		return "", ErrProvider
	}
	var tr struct {
		IDToken string `json:"id_token"`
	}
	if json.Unmarshal(body, &tr) != nil || tr.IDToken == "" {
		return "", ErrProvider
	}
	return tr.IDToken, nil
}

// Exchange implements auth.SSOExchanger: it redeems the one-time code from the callback exactly once.
func (s *Service) Exchange(ctx context.Context, code string) (auth.FederatedIdentity, error) {
	if code == "" || len(code) > 256 {
		return auth.FederatedIdentity{}, auth.ErrInvalidToken
	}
	p, err := s.o.Store.ConsumePending(ctx, KindExchange, hashOf(code), s.o.Now())
	if err != nil {
		return auth.FederatedIdentity{}, auth.ErrInvalidToken
	}
	// Re-check the live rules: SSO may have been switched off in the 60 seconds since the callback.
	c, err := s.o.Store.GetConfig(ctx, p.OrgID)
	if err != nil || !c.Enabled {
		return auth.FederatedIdentity{}, auth.ErrForbidden
	}
	id := p.Identity
	if s.o.Gate != nil {
		if err := s.o.Gate(ctx, p.OrgID, id.Email); err != nil {
			return auth.FederatedIdentity{}, auth.ErrForbidden
		}
	}
	id.JIT, id.Role = c.JIT, c.DefaultRole
	return id, nil
}

// ErrorCode maps a flow error to the short code the login page shows (no provider detail reaches the URL).
func ErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrBadState):
		return "state"
	case errors.Is(err, ErrNoSSO):
		return "unavailable"
	case errors.Is(err, ErrEmailUnverified):
		return "email_unverified"
	case errors.Is(err, ErrDomain):
		return "domain"
	case errors.Is(err, ErrTokenExpired), errors.Is(err, ErrToken), errors.Is(err, ErrIssuer), errors.Is(err, ErrAudience),
		errors.Is(err, ErrNonce), errors.Is(err, ErrAlg), errors.Is(err, ErrSignature), errors.Is(err, ErrNoKey):
		return "token"
	default:
		return "provider"
	}
}
