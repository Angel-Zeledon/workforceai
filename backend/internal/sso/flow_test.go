package sso

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/auth"
)

type fakeSealer struct{ on bool }

func (f fakeSealer) Enabled() bool { return f.on }
func (f fakeSealer) Seal(_ context.Context, org, purpose string, plain []byte) ([]byte, error) {
	return append([]byte("sealed:"+org+":"+purpose+":"), reverse(plain)...), nil
}
func (f fakeSealer) Unseal(_ context.Context, org, purpose string, sealed []byte) ([]byte, error) {
	p := []byte("sealed:" + org + ":" + purpose + ":")
	if !strings.HasPrefix(string(sealed), string(p)) {
		return nil, errors.New("bad aad")
	}
	return reverse(sealed[len(p):]), nil
}

func reverse(b []byte) []byte {
	o := make([]byte, len(b))
	for i := range b {
		o[len(b)-1-i] = b[i]
	}
	return o
}

type fakeResolver struct {
	mu  sync.Mutex
	txt map[string][]string
}

func (f *fakeResolver) LookupTXT(_ context.Context, name string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.txt[name]; ok {
		return v, nil
	}
	return nil, errors.New("no such host")
}

// idp is a minimal OpenID provider.
type idp struct {
	srv       *httptest.Server
	s         *signer
	mu        sync.Mutex
	claims    func(nonce string) map[string]any // what the next token carries
	redeemed  int
	lastForm  url.Values
	challenge string
}

func newIdP(t *testing.T) *idp {
	t.Helper()
	p := &idp{s: newSigner(t)}
	mux := http.NewServeMux()
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(Discovery{Issuer: p.srv.URL, AuthorizationEndpoint: p.srv.URL + "/authorize", TokenEndpoint: p.srv.URL + "/token", JWKSURI: p.srv.URL + "/jwks"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(p.s.jwks()) })
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		p.mu.Lock()
		defer p.mu.Unlock()
		p.lastForm = r.PostForm
		// PKCE: the verifier must hash to the challenge sent on the authorization request.
		h := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if b64.EncodeToString(h[:]) != p.challenge || r.PostForm.Get("client_secret") != "s3cret" || r.PostForm.Get("code") != "good-code" ||
			r.PostForm.Get("grant_type") != "authorization_code" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		p.redeemed++
		nonce := p.noncePending()
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": p.s.sign(t, "RS256", "rsa1", p.claims(nonce)), "access_token": "x"})
	})
	return p
}

var pendingNonce string

func (p *idp) noncePending() string { return pendingNonce }

type fixture struct {
	svc   *Service
	store *MemStore
	idp   *idp
	dns   *fakeResolver
	now   time.Time
	mu    sync.Mutex
}

func (f *fixture) clock() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.now }
func (f *fixture) advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

const orgA = "org-a"

func newFixture(t *testing.T, mutate ...func(*Options)) *fixture {
	t.Helper()
	f := &fixture{store: NewMemStore(), idp: newIdP(t), dns: &fakeResolver{txt: map[string][]string{}}, now: time.Now()}
	o := Options{Store: f.store, Sealer: fakeSealer{on: true}, Resolver: f.dns, HTTP: http.DefaultClient, AllowHTTPIssuer: true,
		RedirectURL: "https://app.example.com/api/v1/sso/callback", AppURL: "https://app.example.com", Now: f.clock}
	for _, m := range mutate {
		m(&o)
	}
	svc, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	f.svc = svc
	f.idp.claims = func(nonce string) map[string]any {
		return map[string]any{"iss": f.idp.srv.URL, "sub": "sub-1", "aud": "client-1", "exp": f.clock().Add(time.Hour).Unix(), "iat": f.clock().Unix(),
			"nonce": nonce, "email": "ana@acme.io", "email_verified": true, "name": "Ana Rojas"}
	}
	return f
}

// setup configures SSO for orgA and verifies acme.io.
func (f *fixture) setup(t *testing.T, mutate ...func(*ConfigInput)) {
	t.Helper()
	ctx := context.Background()
	in := ConfigInput{Enabled: true, Issuer: f.idp.srv.URL, ClientID: "client-1", ClientSecret: "s3cret", DefaultRole: auth.RoleMember, JIT: true}
	for _, m := range mutate {
		m(&in)
	}
	if _, err := f.svc.SaveConfig(ctx, orgA, "admin", in); err != nil {
		t.Fatal(err)
	}
	f.verifyDomain(t, orgA, "acme.io")
}

func (f *fixture) verifyDomain(t *testing.T, org, domain string) {
	t.Helper()
	ctx := context.Background()
	d, err := f.svc.AddDomain(ctx, org, "admin", domain)
	if err != nil {
		t.Fatal(err)
	}
	f.dns.mu.Lock()
	f.dns.txt[d.Challenge.Name] = []string{d.Challenge.Value}
	f.dns.mu.Unlock()
	if _, err := f.svc.VerifyDomain(ctx, org, "admin", domain); err != nil {
		t.Fatal(err)
	}
}

// start runs /sso/start and returns the state, nonce and challenge the provider would see.
func (f *fixture) start(t *testing.T, email string) (state string) {
	t.Helper()
	u, err := f.svc.Start(context.Background(), email)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	pu, _ := url.Parse(u)
	q := pu.Query()
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "client-1" ||
		q.Get("redirect_uri") != f.svc.o.RedirectURL || !strings.Contains(q.Get("scope"), "openid") || q.Get("state") == "" || q.Get("nonce") == "" {
		t.Fatalf("authorization URL = %s", u)
	}
	f.idp.mu.Lock()
	f.idp.challenge = q.Get("code_challenge")
	f.idp.mu.Unlock()
	pendingNonce = q.Get("nonce")
	return q.Get("state")
}

func TestFullLoginFlow(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setup(t)

	if !f.svc.Available(ctx, "someone@ACME.io") || f.svc.Available(ctx, "x@other.io") || f.svc.Available(ctx, "nonsense") {
		t.Fatal("Available must be true only for the verified domain")
	}
	state := f.start(t, "ana@acme.io")
	code, err := f.svc.Complete(ctx, state, "good-code")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	id, err := f.svc.Exchange(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	if id.OrgID != orgA || id.Email != "ana@acme.io" || id.Name != "Ana Rojas" || id.Provider != "oidc" || !id.JIT || id.Role != auth.RoleMember {
		t.Fatalf("identity = %+v", id)
	}
	if f.idp.lastForm.Get("client_secret") != "s3cret" {
		t.Fatal("the stored secret must be unsealed and sent to the provider")
	}
	// The exchange code is single use.
	if _, err := f.svc.Exchange(ctx, code); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("second exchange = %v, want ErrInvalidToken", err)
	}
}

func TestStateIsSingleUseAndExpires(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setup(t)

	state := f.start(t, "ana@acme.io")
	if _, err := f.svc.Complete(ctx, state, "good-code"); err != nil {
		t.Fatal(err)
	}
	// Replaying the same callback (browser back button, or an attacker who saw the URL) finds nothing.
	if _, err := f.svc.Complete(ctx, state, "good-code"); !errors.Is(err, ErrBadState) {
		t.Fatalf("replayed state = %v, want ErrBadState", err)
	}
	// A state the server never issued.
	if _, err := f.svc.Complete(ctx, "forged", "good-code"); !errors.Is(err, ErrBadState) {
		t.Fatalf("forged state = %v", err)
	}
	// An abandoned login expires.
	state = f.start(t, "ana@acme.io")
	f.advance(stateTTL + time.Second)
	if _, err := f.svc.Complete(ctx, state, "good-code"); !errors.Is(err, ErrBadState) {
		t.Fatalf("expired state = %v", err)
	}
	// And a failed attempt still burns the state: the code cannot be retried with the same state.
	state = f.start(t, "ana@acme.io")
	if _, err := f.svc.Complete(ctx, state, "bad-code"); !errors.Is(err, ErrProvider) {
		t.Fatalf("bad code = %v, want ErrProvider", err)
	}
	if _, err := f.svc.Complete(ctx, state, "good-code"); !errors.Is(err, ErrBadState) {
		t.Fatalf("retry after failure = %v, want ErrBadState", err)
	}
}

func TestExchangeCodeExpires(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setup(t)
	code, err := f.svc.Complete(ctx, f.start(t, "ana@acme.io"), "good-code")
	if err != nil {
		t.Fatal(err)
	}
	f.advance(exchangeTTL + time.Second)
	if _, err := f.svc.Exchange(ctx, code); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("expired exchange = %v", err)
	}
}

func TestNonceMismatchIsRejected(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setup(t)
	state := f.start(t, "ana@acme.io")
	inner := f.idp.claims
	f.idp.claims = func(string) map[string]any { return inner("a-nonce-from-a-different-login") }
	if _, err := f.svc.Complete(ctx, state, "good-code"); !errors.Is(err, ErrNonce) {
		t.Fatalf("nonce mismatch = %v, want ErrNonce", err)
	}
}

func TestTokenForAnotherClientOrIssuerIsRejected(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setup(t)
	inner := f.idp.claims
	for name, tweak := range map[string]struct {
		k string
		v any
		e error
	}{"audience": {"aud", "someone-elses-client", ErrAudience}, "issuer": {"iss", "https://evil.example.com", ErrIssuer}, "expired": {"exp", f.clock().Add(-time.Hour).Unix(), ErrTokenExpired}} {
		tw := tweak
		f.idp.claims = func(n string) map[string]any { return mut(inner(n), tw.k, tw.v) }
		if _, err := f.svc.Complete(ctx, f.start(t, "ana@acme.io"), "good-code"); !errors.Is(err, tw.e) {
			t.Errorf("%s: got %v, want %v", name, err, tw.e)
		}
	}
}

func TestEmailRules(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setup(t)
	inner := f.idp.claims
	try := func(kv ...any) error {
		f.idp.claims = func(n string) map[string]any { return mut(inner(n), kv...) }
		_, err := f.svc.Complete(ctx, f.start(t, "ana@acme.io"), "good-code")
		return err
	}
	if err := try("email_verified", false); !errors.Is(err, ErrEmailUnverified) {
		t.Errorf("email_verified=false = %v", err)
	}
	if err := try("email_verified", nil); !errors.Is(err, ErrEmailUnverified) { // absent claim, not trusted by default
		t.Errorf("absent email_verified = %v", err)
	}
	if err := try("email", nil); !errors.Is(err, ErrEmailUnverified) {
		t.Errorf("no email = %v", err)
	}
	// The provider vouches for an address outside the organization's verified domains: refused. This is what stops an IdP
	// misconfiguration (or a hostile one) from signing someone into an organization that does not own their address.
	if err := try("email", "ceo@rival.io"); !errors.Is(err, ErrDomain) {
		t.Errorf("foreign domain = %v, want ErrDomain", err)
	}
	// A domain verified by ANOTHER organization is also refused.
	f.verifyDomain(t, "org-b", "beta.io")
	if err := try("email", "bob@beta.io"); !errors.Is(err, ErrDomain) {
		t.Errorf("other org's domain = %v, want ErrDomain", err)
	}
	// Opt-in for Entra ID, which never sends email_verified.
	f.setup(t, func(c *ConfigInput) { c.AllowUnverifiedEmail = true })
	if err := try("email_verified", nil); err != nil {
		t.Errorf("absent email_verified with opt-in = %v", err)
	}
	if err := try("email_verified", false); !errors.Is(err, ErrEmailUnverified) { // an explicit false is never accepted
		t.Errorf("explicit false with opt-in = %v", err)
	}
}

func TestStartRequiresVerifiedDomainAndEnabledConfig(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	if _, err := f.svc.Start(ctx, "ana@acme.io"); !errors.Is(err, ErrNoSSO) {
		t.Fatalf("no config = %v", err)
	}
	f.setup(t)
	if _, err := f.svc.Start(ctx, "ana@unverified.io"); !errors.Is(err, ErrNoSSO) {
		t.Fatalf("unverified domain = %v", err)
	}
	f.setup(t, func(c *ConfigInput) { c.Enabled = false })
	if _, err := f.svc.Start(ctx, "ana@acme.io"); !errors.Is(err, ErrNoSSO) {
		t.Fatalf("disabled = %v", err)
	}
}

func TestDisablingSSOKillsPendingExchange(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setup(t)
	code, err := f.svc.Complete(ctx, f.start(t, "ana@acme.io"), "good-code")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.DeleteConfig(ctx, orgA, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Exchange(ctx, code); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("exchange after SSO removed = %v, want ErrForbidden", err)
	}
}

func TestGateCanVetoASignIn(t *testing.T) {
	ctx := context.Background()
	deny := false
	f := newFixture(t, func(o *Options) {
		o.Gate = func(context.Context, string, string) error {
			if deny {
				return errors.New("deprovisioned")
			}
			return nil
		}
	})
	f.setup(t)
	code, err := f.svc.Complete(ctx, f.start(t, "ana@acme.io"), "good-code")
	if err != nil {
		t.Fatal(err)
	}
	deny = true // deprovisioned in the meantime
	if _, err := f.svc.Exchange(ctx, code); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("exchange = %v", err)
	}
	if _, err := f.svc.Complete(ctx, f.start(t, "ana@acme.io"), "good-code"); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("complete = %v", err)
	}
}

func TestClientSecretIsSealedAndNeverReturned(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setup(t)
	c, err := f.store.GetConfig(ctx, orgA)
	if err != nil || len(c.SealedSecret) == 0 || strings.Contains(string(c.SealedSecret), "s3cret") {
		t.Fatalf("stored secret = %q %v; must be ciphertext", c.SealedSecret, err)
	}
	v, _ := f.svc.GetConfig(ctx, orgA)
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "s3cret") || strings.Contains(string(b), "sealed") || !v.HasSecret {
		t.Fatalf("view = %s", b)
	}
	// Saving again without a secret keeps the stored one.
	if _, err := f.svc.SaveConfig(ctx, orgA, "admin", ConfigInput{Enabled: true, Issuer: f.idp.srv.URL, ClientID: "client-1"}); err != nil {
		t.Fatal(err)
	}
	c2, _ := f.store.GetConfig(ctx, orgA)
	if string(c2.SealedSecret) != string(c.SealedSecret) {
		t.Fatal("an empty client_secret must keep the stored one")
	}
	// Without an encryption key the secret is refused, never stored in clear.
	g := newFixture(t, func(o *Options) { o.Sealer = fakeSealer{on: false} })
	if _, err := g.svc.SaveConfig(ctx, orgA, "admin", ConfigInput{Enabled: false, Issuer: g.idp.srv.URL, ClientID: "c", ClientSecret: "s3cret"}); !errors.Is(err, ErrNoEncryption) {
		t.Fatalf("no KEK = %v, want ErrNoEncryption", err)
	}
}

func TestSaveConfigValidation(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, func(o *Options) { o.AllowHTTPIssuer = false })
	var ve *ValidationError
	for name, in := range map[string]ConfigInput{
		"http issuer": {Issuer: "http://idp.example.com", ClientID: "c"},
		"no client":   {Issuer: "https://idp.example.com"},
		"owner role":  {Issuer: "https://idp.example.com", ClientID: "c", DefaultRole: auth.RoleOwner},
	} {
		if _, err := f.svc.SaveConfig(ctx, orgA, "a", in); !errors.As(err, &ve) {
			t.Errorf("%s = %v, want ValidationError", name, err)
		}
	}
}

func TestDiscoveryMustNameItsIssuer(t *testing.T) {
	// A server answering for a different issuer than the one configured is not authoritative (mix-up / spoofing).
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(Discovery{Issuer: "https://other.example.com", AuthorizationEndpoint: srv.URL + "/a", TokenEndpoint: srv.URL + "/t", JWKSURI: srv.URL + "/j"})
	})
	if _, err := Discover(context.Background(), http.DefaultClient, srv.URL, true); !errors.Is(err, ErrIssuer) {
		t.Fatalf("discover = %v, want ErrIssuer", err)
	}
}

func TestPolicyRules(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	// Enforcing SSO without a working setup would lock everybody out.
	if _, err := f.svc.SetPolicy(ctx, orgA, "a", false, true); !errors.Is(err, ErrPolicyNeedsSSO) {
		t.Fatalf("enforce without SSO = %v", err)
	}
	if p, err := f.svc.SetPolicy(ctx, orgA, "a", true, false); err != nil || !p.RequireMFA {
		t.Fatalf("require MFA = %+v %v", p, err)
	}
	f.setup(t)
	if _, err := f.svc.SetPolicy(ctx, orgA, "a", true, true); err != nil {
		t.Fatal(err)
	}
	op, _ := f.svc.OrgPolicy(ctx, orgA)
	if !op.RequireMFA || !op.EnforceSSO {
		t.Fatalf("OrgPolicy = %+v", op)
	}
	if op, _ := f.svc.OrgPolicy(ctx, "someone-else"); op.RequireMFA || op.EnforceSSO {
		t.Fatalf("policy leaked to another org: %+v", op)
	}
	// The last verified domain cannot be removed while SSO is enforced; removing the config lifts the enforcement.
	if err := f.svc.RemoveDomain(ctx, orgA, "a", "acme.io"); !errors.Is(err, ErrDomainInUse) {
		t.Fatalf("remove last domain = %v", err)
	}
	if err := f.svc.DeleteConfig(ctx, orgA, "a"); err != nil {
		t.Fatal(err)
	}
	if op, _ := f.svc.OrgPolicy(ctx, orgA); op.EnforceSSO || !op.RequireMFA {
		t.Fatalf("after removing SSO: %+v", op)
	}
}
