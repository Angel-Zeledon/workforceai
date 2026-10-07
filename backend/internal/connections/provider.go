package connections

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Provider is the adapter of one third-party service.
type Provider interface {
	Manifest() Manifest
	// AuthStart builds the consent URL for the scopes of the requested capabilities.
	AuthStart(in AuthStartIn) (string, error)
	// AuthFinish exchanges the code (with the PKCE verifier) for tokens and
	// resolves the account identity.
	AuthFinish(ctx context.Context, in AuthFinishIn) (Bundle, Identity, error)
	// Refresh obtains a short-lived access token from a refresh token.
	Refresh(ctx context.Context, cfg OAuthApp, refreshToken string) (AccessToken, error)
	// Test performs the cheapest authenticated call (no data changes).
	Test(ctx context.Context, h Handle) TestResult
	// Revoke asks the provider to invalidate the token.
	Revoke(ctx context.Context, cfg OAuthApp, token string) error
	// Open returns the tool executor bound to a connection handle.
	Open(h Handle) Executor
}

// OAuthApp is the platform's (self-hosted: the owner's) OAuth client.
type OAuthApp struct {
	ClientID     string
	ClientSecret string // never logged; kept out of every response
	RedirectURL  string
}

// Configured reports whether the app can start a flow.
func (a OAuthApp) Configured() bool {
	return a.ClientID != "" && a.ClientSecret != "" && a.RedirectURL != ""
}

// AuthStartIn is the input of AuthStart.
type AuthStartIn struct {
	App       OAuthApp
	State     string
	Challenge string // PKCE S256 challenge
	Scopes    []string
	LoginHint string
}

// AuthFinishIn is the input of AuthFinish.
type AuthFinishIn struct {
	App      OAuthApp
	Code     string
	Verifier string
}

// Bundle is what a token exchange yields. RefreshToken goes to the vault.
type Bundle struct {
	RefreshToken string
	Access       AccessToken
	Scopes       []string
}

// AccessToken is a short-lived bearer token (memory only).
type AccessToken struct {
	Value  string
	Expiry time.Time
}

// Identity identifies the connected account.
type Identity struct {
	Email string
	Ref   string // stable id (e.g. Google "sub"), compared on re-authorization
}

// TestResult is the outcome of Provider.Test.
type TestResult struct {
	OK        bool
	LatencyMS int
	Account   string
	Code      string // stable error code when !OK
}

// Handle gives a provider access to ONE connection without exposing the
// token: HTTP returns a client whose transport injects the Authorization
// header and scrubs credentials from errors.
type Handle struct {
	Org           string
	ConnectionID  string
	Mode          string
	ResourceScope ResourceScope
	HTTP          *http.Client
}

// Item is one normalized result item. Meta is trusted structure (ids, dates,
// counts); Texts are third-party text and ALWAYS go through the sanitizer.
type Item struct {
	ID    string
	Meta  map[string]any
	Texts []Text
}

// Text is a named piece of untrusted third-party text.
type Text struct {
	Name  string // "subject", "from", "body", ...
	Value string
	HTML  bool
}

// ExecCall is what an Executor receives (after policy/limits passed).
type ExecCall struct {
	Tool, Action string
	Args         map[string]any
	MaxItems     int
}

// ExecResult is the raw outcome of a provider call.
type ExecResult struct {
	Summary     string
	Items       []Item
	ResourceRef string // opaque, content free: "q:'from:x' n=3"
	BytesIn     int64
	BytesOut    int64
	Status      int            // provider HTTP status
	Data        map[string]any // trusted, structured result (ids), never message text
}

// Executor runs the tools of a provider.
type Executor interface {
	Execute(ctx context.Context, c ExecCall) (ExecResult, error)
}

// ProviderError is a classified provider failure. Message is safe (no body).
type ProviderError struct {
	Status     int
	Code       string // reauth_required | insufficient_scope | rate_limited | provider_error | not_found | resource_out_of_scope | invalid_args
	RetryAfter time.Duration
}

func (e *ProviderError) Error() string {
	return "provider error: " + e.Code
}

// Classify extracts a ProviderError from err.
func Classify(err error) *ProviderError {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe
	}
	return &ProviderError{Code: CodeProviderError}
}

// ErrUnknownProvider is returned for a provider id without manifest/adapter.
var ErrUnknownProvider = errors.New("connections: unknown provider")
