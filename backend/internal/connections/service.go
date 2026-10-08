package connections

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/sanitize"
	"aiworkforce/backend/internal/vault"
)

// AuditFunc records an audit log entry (metadata only).
type AuditFunc func(ctx context.Context, org, actor, action, entity, entityID string, details map[string]any)

// EmitFunc publishes a realtime event for the organization. Payloads never
// contain secrets or message content.
type EmitFunc func(ctx context.Context, org, eventType string, payload map[string]any)

// Config wires a Service.
type Config struct {
	Store     Store
	Vault     *vault.Vault
	Providers map[string]Provider
	// Apps are the OAuth clients by provider id, set once for the whole
	// deployment. A connection may instead bring its own app (oauth_client_id +
	// sealed client secret); see appFor.
	Apps map[string]OAuthApp
	// RedirectURL is the OAuth callback URL used with per-connection apps.
	RedirectURL string
	Suspects    *sanitize.Suspects
	Audit       AuditFunc
	Emit        EmitFunc
	// AdminCount returns how many humans can administer the org (owner+admin).
	AdminCount func(ctx context.Context, org string) int
	// UIBase is where the OAuth callback redirects the browser (default "/").
	UIBase string
	Now    func() time.Time
	// Transport is the base HTTP transport of provider calls (tests).
	Transport http.RoundTripper
	// OnRevoke runs when a connection stops being usable (suspend/revoke) so
	// held actions can be cancelled.
	OnRevoke func(ctx context.Context, org, connID, reason string)
}

// Service implements the connection use cases.
type Service struct {
	Config
	manifests map[string]Manifest

	tokMu  sync.Mutex
	tokens map[string]AccessToken
	locks  map[string]*sync.Mutex

	emitMu   sync.Mutex
	lastEmit map[string]time.Time
}

// NewService builds a Service; manifests come from the embedded providers.
func NewService(c Config) (*Service, error) {
	if c.Store == nil {
		return nil, errors.New("connections: store required")
	}
	ms, err := LoadManifests()
	if err != nil {
		return nil, err
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Audit == nil {
		c.Audit = func(context.Context, string, string, string, string, string, map[string]any) {}
	}
	if c.Emit == nil {
		c.Emit = func(context.Context, string, string, map[string]any) {}
	}
	if c.AdminCount == nil {
		c.AdminCount = func(context.Context, string) int { return 1 }
	}
	if c.UIBase == "" {
		c.UIBase = "/"
	}
	if c.Providers == nil {
		c.Providers = map[string]Provider{}
	}
	return &Service{Config: c, manifests: ms, tokens: map[string]AccessToken{}, locks: map[string]*sync.Mutex{}, lastEmit: map[string]time.Time{}}, nil
}

// AdminCount returns how many humans can administer the organization.
func (s *Service) AdminCount(ctx context.Context, org string) int {
	return s.Config.AdminCount(ctx, org)
}

// Manifests returns the loaded provider manifests.
func (s *Service) Manifests() map[string]Manifest { return s.manifests }

// Manifest returns one manifest.
func (s *Service) Manifest(id string) (Manifest, bool) { m, ok := s.manifests[id]; return m, ok }

func newID() string { return "cn_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20] }

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// ---- catalog ----

// ProviderInfo is the public description of a provider.
type ProviderInfo struct {
	ID       string `json:"id"`
	LabelKey string `json:"label_key"`
	Phase    string `json:"phase"`
	// Available is false for providers announced but not implemented yet.
	Available bool `json:"available"`
	// Auth lists the ways to connect: "oauth2_byo_app" (bring your own OAuth
	// app, self-hosted) and/or "api_key".
	Auth            []string             `json:"auth"`
	Capabilities    []CapabilityInfo     `json:"capabilities"`
	ResourceFilters []ResourceFilterInfo `json:"resource_filters"`
	NeverOffered    []string             `json:"never_offered"`
	Limits          Limits               `json:"default_limits"`
	// LiveAvailable is false when live credentials cannot be stored (no KEK) or
	// the OAuth app is not configured: the UI then offers simulated mode only.
	LiveAvailable bool `json:"live_available"`
	// SplitReadWrite: read and write capabilities live in different connections.
	SplitReadWrite bool `json:"split_read_write"`
}

// ResourceFilterInfo describes a resource filter the UI can offer.
type ResourceFilterInfo struct {
	Key     string `json:"key"`
	Type    string `json:"type"` // list | number
	Default any    `json:"default,omitempty"`
}

// CapabilityInfo is the public part of CapabilitySpec.
type CapabilityInfo struct {
	ID             string   `json:"id"`
	Profile        string   `json:"profile"` // read | write
	Risk           string   `json:"risk"`
	SideEffects    bool     `json:"side_effects"`
	Reversibility  string   `json:"reversibility,omitempty"`
	Default        bool     `json:"default"`
	AlwaysApproval bool     `json:"always_approval"`
	HoldSeconds    int      `json:"hold_seconds,omitempty"`
	LabelKey       string   `json:"label_key"`
	Tools          []string `json:"tools"`
}

// Catalog lists the provider catalog.
func (s *Service) Catalog() []ProviderInfo {
	var out []ProviderInfo
	for _, id := range s.sortedProviderIDs() {
		m := s.manifests[id]
		pi := ProviderInfo{ID: m.ID, LabelKey: m.LabelKey, Phase: m.Phase, Available: m.Phase == "available", Capabilities: []CapabilityInfo{},
			ResourceFilters: []ResourceFilterInfo{}, Auth: []string{}, NeverOffered: m.NeverOffered, Limits: m.DefaultLimits, SplitReadWrite: true}
		for _, f := range m.ResourceFilters {
			rf := ResourceFilterInfo{Key: f, Type: "list"}
			if f == "max_age_days" {
				rf.Type = "number"
			}
			pi.ResourceFilters = append(pi.ResourceFilters, rf)
		}
		if pi.NeverOffered == nil {
			pi.NeverOffered = []string{}
		}
		for _, a := range m.Auth {
			switch a.Method {
			case "oauth2_pkce":
				pi.Auth = append(pi.Auth, "oauth2_byo_app")
				// live OAuth needs a KEK and an OAuth app (env-wide or brought per connection)
				pi.LiveAvailable = pi.LiveAvailable || (s.Vault.Enabled() && s.Providers[id] != nil)
			case "api_key":
				pi.Auth = append(pi.Auth, "api_key")
				pi.LiveAvailable = pi.LiveAvailable || s.Vault.Enabled()
			}
		}
		for _, name := range m.CapabilityNames() {
			c := m.Capabilities[name]
			ci := CapabilityInfo{ID: name, Profile: c.Class, Risk: c.Risk, SideEffects: c.SideEffects, Reversibility: c.Reversibility,
				Default: c.Default, AlwaysApproval: c.AlwaysApproval, HoldSeconds: c.HoldSecondsDefault, LabelKey: c.LabelKey, Tools: c.Tools}
			if ci.Tools == nil {
				ci.Tools = []string{}
			}
			pi.Capabilities = append(pi.Capabilities, ci)
		}
		out = append(out, pi)
	}
	return out
}

func (s *Service) sortedProviderIDs() []string {
	ids := make([]string, 0, len(s.manifests))
	for id := range s.manifests {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// ---- create / read ----

// CreateInput creates a connection.
type CreateInput struct {
	Provider      string
	Kind          string // oauth2 | api_key
	Label         string
	Capabilities  []string
	ResourceScope ResourceScope
	Mode          string // live (default) | simulated
	Secret        *vault.Secret
	ExpiresAt     *time.Time
	Actor         string
	// OAuthClientID / OAuthClientSecret: the owner's own OAuth app ("bring your
	// own app", self-hosted). Both or none. The secret is write-only.
	OAuthClientID     string
	OAuthClientSecret *vault.Secret
}

var secretlessKinds = map[string]bool{"oauth2": true, "api_key": true}

// Create registers a connection. Nothing is granted to any agent (D1).
func (s *Service) Create(ctx context.Context, org string, in CreateInput) (Connection, error) {
	m, ok := s.manifests[in.Provider]
	if !ok {
		return Connection{}, invalid("unknown provider %q", in.Provider)
	}
	label := strings.TrimSpace(in.Label)
	if label == "" || len(label) > 80 {
		return Connection{}, invalid("label is required (max 80 chars)")
	}
	if len(in.Capabilities) == 0 {
		in.Capabilities = defaultCaps(m)
	}
	in.Capabilities = dedupe(in.Capabilities)
	for _, c := range in.Capabilities {
		if _, ok := m.Capability(c); !ok || slices.Contains(m.NeverOffered, c) {
			return Connection{}, invalid("capability %q is not offered by %s", c, in.Provider)
		}
	}
	// Decision 1: the read connection is SEPARATE from the write connection.
	if m.ClassOf(in.Capabilities) == "mixed" {
		return Connection{}, invalid("read_write_must_be_separate: create one connection for reading and another for writing")
	}
	if in.Mode == "" {
		in.Mode = ModeLive
	}
	if in.Mode != ModeLive && in.Mode != ModeSimulated {
		return Connection{}, invalid("mode must be live or simulated")
	}
	if in.Kind == "" {
		in.Kind = "oauth2"
		if _, ok := m.AuthMethodFor("oauth2_pkce"); !ok {
			in.Kind = "api_key"
		}
	}
	if !secretlessKinds[in.Kind] {
		return Connection{}, invalid("kind must be oauth2 or api_key")
	}
	now := s.Now().UTC()
	c := Connection{ID: newID(), OrgID: org, Provider: in.Provider, Kind: in.Kind, Label: label, Mode: in.Mode,
		Status: StatusPending, Class: m.ClassOf(in.Capabilities), RequestedCapabilities: in.Capabilities,
		GrantedCapabilities: []string{}, ProviderScopes: []string{}, ResourceScope: in.ResourceScope, Limits: m.DefaultLimits,
		CreatedBy: in.Actor, CreatedAt: now, UpdatedAt: now, ExpiresAt: in.ExpiresAt}
	if c.Limits.OnExceed == "" {
		c.Limits.OnExceed = "deny"
	}

	switch {
	case in.Mode == ModeSimulated:
		// D12: simulated connections store no secret and use the fake adapter.
		c.Status, c.AccountLabel, c.GrantedCapabilities = StatusActive, "simulated@example.test", in.Capabilities
		c.ProviderScopes = []string{}
		if in.Secret != nil {
			return Connection{}, invalid("a simulated connection stores no secret")
		}
	case in.Kind == "api_key":
		am, ok := m.AuthMethodFor("api_key")
		if !ok {
			return Connection{}, invalid("%s does not support API keys", in.Provider)
		}
		if in.Secret == nil || in.Secret.Len() == 0 {
			return Connection{}, invalid("secret is required for api_key connections")
		}
		if !s.Vault.Enabled() {
			return Connection{}, ErrNoKEK
		}
		if am.SecretRegex != "" && !regexp.MustCompile(am.SecretRegex).Match(in.Secret.Reveal()) {
			return Connection{}, invalid("secret has an unexpected format")
		}
		if err := s.Store.CreateConnection(ctx, c); err != nil {
			return Connection{}, err
		}
		if _, err := s.Vault.Put(ctx, org, c.ID, vault.KindAPIKey, *in.Secret, in.Actor, in.ExpiresAt); err != nil {
			c.Status, c.StatusReason = StatusError, "vault_error"
			_ = s.Store.UpdateConnection(ctx, c)
			return Connection{}, err
		}
		c.Status, c.GrantedCapabilities, c.AccountLabel = StatusActive, in.Capabilities, "api key"
	default: // live oauth2
		if in.Secret != nil {
			return Connection{}, invalid("OAuth connections take no secret")
		}
		if !s.Vault.Enabled() {
			return Connection{}, ErrNoKEK
		}
		if (in.OAuthClientID != "") != (in.OAuthClientSecret != nil && in.OAuthClientSecret.Len() > 0) {
			return Connection{}, invalid("oauth_client_id and oauth_client_secret must be provided together")
		}
		if in.OAuthClientID != "" {
			if len(in.OAuthClientID) > 300 {
				return Connection{}, invalid("oauth_client_id is too long")
			}
			enc, err := s.Vault.Seal(ctx, org, "oauthapp:"+c.ID, in.OAuthClientSecret.Reveal())
			if err != nil {
				return Connection{}, err
			}
			c.OAuthClientID, c.OAuthClientSecretEnc = in.OAuthClientID, enc
			s.Suspects.Add(string(in.OAuthClientSecret.Reveal()))
		}
	}
	if in.Kind != "api_key" || in.Mode == ModeSimulated {
		if err := s.Store.CreateConnection(ctx, c); err != nil {
			return Connection{}, err
		}
	} else if err := s.Store.UpdateConnection(ctx, c); err != nil {
		return Connection{}, err
	}
	s.Audit(ctx, org, in.Actor, "connection.created", "connection", c.ID,
		map[string]any{"provider": c.Provider, "mode": c.Mode, "kind": c.Kind, "capabilities": c.RequestedCapabilities})
	s.Emit(ctx, org, "connection.created", map[string]any{"connection": s.brief(c)})
	return s.view(ctx, c), nil
}

func defaultCaps(m Manifest) []string {
	var out []string
	for _, n := range m.CapabilityNames() {
		if m.Capabilities[n].Default {
			out = append(out, n)
		}
	}
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func (s *Service) brief(c Connection) map[string]any {
	return map[string]any{"id": c.ID, "provider": c.Provider, "label": c.Label, "status": c.Status,
		"granted_capabilities": c.GrantedCapabilities}
}

// view decorates a connection for API responses (credential metadata, grants).
func (s *Service) view(ctx context.Context, c Connection) Connection {
	if c.GrantedCapabilities == nil {
		c.GrantedCapabilities = []string{}
	}
	if c.RequestedCapabilities == nil {
		c.RequestedCapabilities = []string{}
	}
	if c.ProviderScopes == nil {
		c.ProviderScopes = []string{}
	}
	if meta, err := s.Vault.Meta(ctx, c.OrgID, c.ID); err == nil {
		if meta.Kind == vault.KindOAuthRefresh {
			meta.Hint = "" // never reveal even a fragment of an OAuth token
		}
		c.Credential = &meta
	}
	if gs, err := s.Store.ListGrantsByConnection(ctx, c.OrgID, c.ID); err == nil {
		for _, g := range gs {
			if g.Status == GrantActive || g.Status == GrantPendingApproval {
				c.GrantsCount++
			}
		}
	}
	return c
}

// Get returns one connection.
func (s *Service) Get(ctx context.Context, org, id string) (Connection, error) {
	c, err := s.Store.GetConnection(ctx, org, id)
	if err != nil {
		return Connection{}, err
	}
	return s.view(ctx, c), nil
}

// List returns connections (metadata only).
func (s *Service) List(ctx context.Context, org, provider, status string) ([]Connection, error) {
	cs, err := s.Store.ListConnections(ctx, org, provider, status)
	if err != nil {
		return nil, err
	}
	for i := range cs {
		cs[i] = s.view(ctx, cs[i])
	}
	if cs == nil {
		cs = []Connection{}
	}
	return cs, nil
}

// PatchInput changes mutable fields (nil = unchanged).
type PatchInput struct {
	Label         *string
	Limits        *Limits
	ResourceScope *ResourceScope
	ReadOnly      *bool
	Actor         string
}

// Patch updates label, limits, resource scope and the read-only switch.
func (s *Service) Patch(ctx context.Context, org, id string, in PatchInput) (Connection, error) {
	c, err := s.Store.GetConnection(ctx, org, id)
	if err != nil {
		return Connection{}, err
	}
	if c.Status == StatusRevoked {
		return Connection{}, fmt.Errorf("%w: connection is revoked", ErrConflict)
	}
	if in.Label != nil {
		l := strings.TrimSpace(*in.Label)
		if l == "" || len(l) > 80 {
			return Connection{}, invalid("label is required (max 80 chars)")
		}
		c.Label = l
	}
	if in.Limits != nil {
		if err := validateLimits(*in.Limits); err != nil {
			return Connection{}, err
		}
		c.Limits = *in.Limits
	}
	if in.ResourceScope != nil {
		c.ResourceScope = *in.ResourceScope
	}
	if in.ReadOnly != nil {
		c.ReadOnly = *in.ReadOnly
	}
	c.UpdatedAt = s.Now().UTC()
	if err := s.Store.UpdateConnection(ctx, c); err != nil {
		return Connection{}, err
	}
	s.Audit(ctx, org, in.Actor, "connection.updated", "connection", id, map[string]any{"read_only": c.ReadOnly})
	s.Emit(ctx, org, "connection.updated", map[string]any{"connection": s.brief(c)})
	return s.view(ctx, c), nil
}

func validateLimits(l Limits) error {
	if l.PerMinute < 0 || l.PerHour < 0 || l.PerDay < 0 || l.WritePerDay < 0 || l.MaxItemsPerCall < 0 || l.MaxBytesPerDay < 0 || l.MonthlyBudgetUSD < 0 {
		return invalid("limits cannot be negative")
	}
	if l.OnExceed != "" && l.OnExceed != "deny" {
		return invalid("on_exceed must be deny")
	}
	return nil
}

// PutLimits replaces the limits of a connection.
func (s *Service) PutLimits(ctx context.Context, org, id string, l Limits, actor string) (Connection, error) {
	return s.Patch(ctx, org, id, PatchInput{Limits: &l, Actor: actor})
}

// ---- OAuth ----

const stateTTL = 10 * time.Minute

func randB64(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashState(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// appFor returns the OAuth app of a connection: its own (bring your own app)
// or the deployment-wide one. The client secret never leaves this package.
func (s *Service) appFor(ctx context.Context, c Connection) OAuthApp {
	if c.OAuthClientID != "" && len(c.OAuthClientSecretEnc) > 0 {
		plain, err := s.Vault.Unseal(ctx, c.OrgID, "oauthapp:"+c.ID, c.OAuthClientSecretEnc)
		if err != nil {
			return OAuthApp{} // unreadable: treated as "not configured" (fails closed)
		}
		s.Suspects.Add(string(plain))
		redirect := s.RedirectURL
		if redirect == "" {
			redirect = s.Apps[c.Provider].RedirectURL
		}
		return OAuthApp{ClientID: c.OAuthClientID, ClientSecret: string(plain), RedirectURL: redirect}
	}
	return s.Apps[c.Provider]
}

// OAuthStart begins authorization: single-use state + PKCE; the verifier is
// sealed with the vault. Returns the provider consent URL.
func (s *Service) OAuthStart(ctx context.Context, org, id, userID string) (string, time.Time, error) {
	c, err := s.Store.GetConnection(ctx, org, id)
	if err != nil {
		return "", time.Time{}, err
	}
	if c.Mode != ModeLive || c.Kind != "oauth2" {
		return "", time.Time{}, invalid("connection does not use OAuth")
	}
	if c.Status != StatusPending && c.Status != StatusNeedsReauth && c.Status != StatusActive {
		return "", time.Time{}, fmt.Errorf("%w: connection is %s", ErrConflict, c.Status)
	}
	p, ok := s.Providers[c.Provider]
	app := s.appFor(ctx, c)
	if !ok || !app.Configured() {
		return "", time.Time{}, ErrOAuthUnavailable
	}
	m := p.Manifest()
	am, _ := m.AuthMethodFor("oauth2_pkce")
	state, verifier := randB64(32), randB64(48)
	sum := sha256.Sum256([]byte(verifier))
	enc, err := s.Vault.Seal(ctx, org, "pkce:"+hashState(state), []byte(verifier))
	if err != nil {
		return "", time.Time{}, err
	}
	exp := s.Now().UTC().Add(stateTTL)
	if err := s.Store.PutOAuthState(ctx, OAuthState{StateHash: hashState(state), OrgID: org, ConnectionID: id, UserID: userID,
		VerifierEnc: enc, RedirectURI: app.RedirectURL, Capabilities: c.RequestedCapabilities, ExpiresAt: exp}); err != nil {
		return "", time.Time{}, err
	}
	url, err := p.AuthStart(AuthStartIn{App: app, State: state, Challenge: base64.RawURLEncoding.EncodeToString(sum[:]),
		Scopes: m.ScopesFor(c.RequestedCapabilities, am)})
	return url, exp, err
}

// OAuthCallback completes the flow. It returns the UI path to redirect to.
// Provider error text is never forwarded; only stable codes.
func (s *Service) OAuthCallback(ctx context.Context, code, state, providerErr string) (string, error) {
	st, err := s.Store.ConsumeOAuthState(ctx, hashState(state), s.Now().UTC())
	if err != nil {
		return "", invalid("invalid_state")
	}
	c, err := s.Store.GetConnection(ctx, st.OrgID, st.ConnectionID)
	if err != nil {
		return "", err
	}
	fail := func(reason string) (string, error) {
		c.Status, c.StatusReason, c.LastErrorCode, c.UpdatedAt = StatusError, reason, reason, s.Now().UTC()
		if prevActive := c.GrantedCapabilities != nil && len(c.GrantedCapabilities) > 0 && c.AccountRef != ""; prevActive {
			c.Status = StatusNeedsReauth
		}
		_ = s.Store.UpdateConnection(ctx, c)
		s.Audit(ctx, c.OrgID, st.UserID, "connection.error", "connection", c.ID, map[string]any{"reason": reason})
		s.Emit(ctx, c.OrgID, "connection.status_changed", map[string]any{"connection_id": c.ID, "to": c.Status, "reason": reason})
		return s.uiPath(c.ID, "error="+reason), nil
	}
	if providerErr != "" || code == "" {
		return fail("consent_denied")
	}
	p, ok := s.Providers[c.Provider]
	app := s.appFor(ctx, c)
	if !ok || !app.Configured() {
		return fail("oauth_unavailable")
	}
	verifier, err := s.Vault.Unseal(ctx, st.OrgID, "pkce:"+st.StateHash, st.VerifierEnc)
	if err != nil {
		return fail("state_corrupt")
	}
	bundle, ident, err := p.AuthFinish(ctx, AuthFinishIn{App: app, Code: code, Verifier: string(verifier)})
	if err != nil {
		return fail(Classify(err).Code)
	}
	m := p.Manifest()
	am, _ := m.AuthMethodFor("oauth2_pkce")
	allowed := m.ScopesFor(c.RequestedCapabilities, am)
	for _, sc := range bundle.Scopes {
		if !slices.Contains(allowed, sc) {
			// The token is broader than requested (would break read/write
			// separation): refuse it and close it at the provider.
			_ = p.Revoke(ctx, app, firstNonEmpty(bundle.RefreshToken, bundle.Access.Value))
			return fail("scope_overreach")
		}
	}
	ref := hashState("acct:" + ident.Ref)
	if c.AccountRef != "" && c.AccountRef != ref {
		_ = p.Revoke(ctx, app, firstNonEmpty(bundle.RefreshToken, bundle.Access.Value))
		return fail("account_mismatch")
	}
	if bundle.RefreshToken == "" {
		return fail("no_refresh_token")
	}
	if _, err := s.Vault.Put(ctx, c.OrgID, c.ID, vault.KindOAuthRefresh, vault.SecretFromString(bundle.RefreshToken), st.UserID, nil); err != nil {
		return fail("vault_error")
	}
	if bundle.Access.Value != "" {
		s.setToken(c.ID, bundle.Access)
	}
	c.AccountLabel, c.AccountRef = ident.Email, ref
	c.ProviderScopes = bundle.Scopes
	c.GrantedCapabilities = m.CapabilitiesGrantedBy(c.RequestedCapabilities, bundle.Scopes)
	c.Status, c.StatusReason, c.LastErrorCode, c.UpdatedAt = StatusActive, "", "", s.Now().UTC()
	if err := s.Store.UpdateConnection(ctx, c); err != nil {
		return "", err
	}
	s.Audit(ctx, c.OrgID, st.UserID, "connection.connected", "connection", c.ID,
		map[string]any{"granted_capabilities": c.GrantedCapabilities, "account_label": c.AccountLabel})
	s.Emit(ctx, c.OrgID, "connection.status_changed", map[string]any{"connection_id": c.ID, "from": "pending", "to": StatusActive, "reason": ""})
	_, _ = s.Test(ctx, c.OrgID, c.ID, st.UserID)
	return s.uiPath(c.ID, "connected=1"), nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (s *Service) uiPath(id, q string) string {
	return strings.TrimRight(s.UIBase, "/") + "/connections/" + id + "?" + q
}

// AddCapabilities requests more capabilities (incremental consent). The
// connection class cannot change: a read connection cannot gain write.
func (s *Service) AddCapabilities(ctx context.Context, org, id string, add []string, actor string) (Connection, error) {
	c, err := s.Store.GetConnection(ctx, org, id)
	if err != nil {
		return Connection{}, err
	}
	m := s.manifests[c.Provider]
	next := dedupe(append(append([]string{}, c.RequestedCapabilities...), add...))
	for _, a := range add {
		if _, ok := m.Capability(a); !ok || slices.Contains(m.NeverOffered, a) {
			return Connection{}, invalid("capability %q is not offered", a)
		}
	}
	if m.ClassOf(next) == "mixed" {
		return Connection{}, invalid("read_write_must_be_separate: use a separate connection")
	}
	c.RequestedCapabilities, c.UpdatedAt = next, s.Now().UTC()
	if c.Mode == ModeSimulated {
		c.GrantedCapabilities = next
	}
	if err := s.Store.UpdateConnection(ctx, c); err != nil {
		return Connection{}, err
	}
	s.Audit(ctx, org, actor, "connection.scope_upgraded", "connection", id, map[string]any{"added": add})
	return s.view(ctx, c), nil
}

// ---- tokens / handle ----

func (s *Service) setToken(connID string, t AccessToken) {
	s.tokMu.Lock()
	s.tokens[connID] = t
	s.tokMu.Unlock()
	s.Suspects.Add(t.Value)
}

func (s *Service) purgeToken(connID string) {
	s.tokMu.Lock()
	delete(s.tokens, connID)
	s.tokMu.Unlock()
}

func (s *Service) lockFor(connID string) *sync.Mutex {
	s.tokMu.Lock()
	defer s.tokMu.Unlock()
	l, ok := s.locks[connID]
	if !ok {
		l = &sync.Mutex{}
		s.locks[connID] = l
	}
	return l
}

// accessToken returns a valid access token, refreshing it (one refresh at a
// time per connection) through the vault.
func (s *Service) accessToken(ctx context.Context, c Connection) (string, error) {
	l := s.lockFor(c.ID)
	l.Lock()
	defer l.Unlock()
	s.tokMu.Lock()
	t, ok := s.tokens[c.ID]
	s.tokMu.Unlock()
	if ok && t.Expiry.After(s.Now().Add(60*time.Second)) {
		return t.Value, nil
	}
	p, ok := s.Providers[c.Provider]
	if !ok {
		return "", &ProviderError{Code: CodeProviderError}
	}
	var out AccessToken
	err := s.Vault.Use(ctx, c.OrgID, c.ID, func(sec vault.Secret) error {
		var rerr error
		out, rerr = p.Refresh(ctx, s.appFor(ctx, c), string(sec.Reveal()))
		return rerr
	})
	if err != nil {
		if errors.Is(err, vault.ErrNoKEK) || errors.Is(err, vault.ErrDestroyed) || errors.Is(err, vault.ErrNotFound) {
			return "", &ProviderError{Code: CodeConnectionUnavailable}
		}
		return "", err
	}
	s.setToken(c.ID, out)
	return out.Value, nil
}

// authTransport injects the Authorization header inside the gateway and
// scrubs everything else: callers (provider adapters) never see the token and
// transport errors never carry URLs or headers.
type authTransport struct {
	base  http.RoundTripper
	token func(ctx context.Context) (string, error)
}

func (t authTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	tok, err := t.token(r.Context())
	if err != nil {
		return nil, err
	}
	r2 := r.Clone(r.Context())
	r2.Header.Set("Authorization", "Bearer "+tok)
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(r2)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, &ProviderError{Code: CodeProviderError}
	}
	return resp, nil
}

// Handle builds the provider handle of a connection.
func (s *Service) Handle(c Connection) Handle {
	h := Handle{Org: c.OrgID, ConnectionID: c.ID, Mode: c.Mode, ResourceScope: c.ResourceScope}
	if c.Mode == ModeLive {
		h.HTTP = &http.Client{Timeout: 20 * time.Second, Transport: authTransport{base: s.Transport,
			token: func(ctx context.Context) (string, error) { return s.accessToken(ctx, c) }},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return h
}

// Executor returns the tool executor of a connection.
func (s *Service) Executor(c Connection) (Executor, error) {
	p, ok := s.Providers[c.Provider]
	if !ok {
		return nil, ErrUnknownProvider
	}
	return p.Open(s.Handle(c)), nil
}

// ---- test / lifecycle ----

// Test runs the provider's no-side-effect check and records the result.
func (s *Service) Test(ctx context.Context, org, id, actor string) (TestResult, error) {
	c, err := s.Store.GetConnection(ctx, org, id)
	if err != nil {
		return TestResult{}, err
	}
	res := TestResult{OK: true, Account: c.AccountLabel}
	now := s.Now().UTC()
	_, hasAdapter := s.Providers[c.Provider]
	if c.Mode == ModeLive && (c.Kind == "oauth2" || (c.Kind == "api_key" && hasAdapter)) {
		p, ok := s.Providers[c.Provider]
		if !ok {
			return TestResult{}, ErrUnknownProvider
		}
		start := time.Now()
		res = p.Test(ctx, s.Handle(c))
		res.LatencyMS = int(time.Since(start).Milliseconds())
	}
	c.LastTestedAt = &now
	c.LastTestStatus = "ok"
	if !res.OK {
		c.LastTestStatus, c.LastErrorCode = "failed", res.Code
		if res.Code == CodeReauthRequired && c.Status == StatusActive {
			c.Status, c.StatusReason = StatusNeedsReauth, CodeReauthRequired
			s.purgeToken(c.ID)
			s.Emit(ctx, org, "connection.status_changed", map[string]any{"connection_id": id, "from": StatusActive, "to": StatusNeedsReauth, "reason": CodeReauthRequired})
		}
	}
	c.UpdatedAt = now
	if err := s.Store.UpdateConnection(ctx, c); err != nil {
		return res, err
	}
	s.Audit(ctx, org, actor, "connection.tested", "connection", id, map[string]any{"ok": res.OK, "code": res.Code})
	s.Emit(ctx, org, "connection.tested", map[string]any{"connection_id": id, "status": c.LastTestStatus, "latency_ms": res.LatencyMS})
	return res, nil
}

// MarkNeedsReauth is called by the gateway when the provider rejects the token.
func (s *Service) MarkNeedsReauth(ctx context.Context, org, id string) {
	c, err := s.Store.GetConnection(ctx, org, id)
	if err != nil || c.Status != StatusActive {
		return
	}
	c.Status, c.StatusReason, c.LastErrorCode, c.UpdatedAt = StatusNeedsReauth, CodeReauthRequired, CodeReauthRequired, s.Now().UTC()
	_ = s.Store.UpdateConnection(ctx, c)
	s.purgeToken(id)
	s.Emit(ctx, org, "connection.status_changed", map[string]any{"connection_id": id, "from": StatusActive, "to": StatusNeedsReauth, "reason": CodeReauthRequired})
}

// Suspend blocks new uses at once (reversible; the secret is kept).
func (s *Service) Suspend(ctx context.Context, org, id, actor, reason string) (Connection, error) {
	c, err := s.Store.GetConnection(ctx, org, id)
	if err != nil {
		return Connection{}, err
	}
	if c.Status == StatusRevoked {
		return Connection{}, fmt.Errorf("%w: connection is revoked", ErrConflict)
	}
	if c.Status != StatusSuspended {
		from := c.Status
		c.Status, c.StatusReason, c.UpdatedAt = StatusSuspended, firstNonEmpty(reason, "manual"), s.Now().UTC()
		if err := s.Store.UpdateConnection(ctx, c); err != nil {
			return Connection{}, err
		}
		s.purgeToken(id)
		if s.OnRevoke != nil {
			s.OnRevoke(ctx, org, id, "suspended")
		}
		s.Audit(ctx, org, actor, "connection.suspended", "connection", id, map[string]any{"reason": c.StatusReason})
		s.Emit(ctx, org, "connection.status_changed", map[string]any{"connection_id": id, "from": from, "to": StatusSuspended, "reason": c.StatusReason})
	}
	return s.view(ctx, c), nil
}

// Resume re-enables a suspended connection. The caller enforces who may
// resume after a kill switch (owner).
func (s *Service) Resume(ctx context.Context, org, id, actor string) (Connection, error) {
	c, err := s.Store.GetConnection(ctx, org, id)
	if err != nil {
		return Connection{}, err
	}
	if c.Status != StatusSuspended {
		return Connection{}, fmt.Errorf("%w: connection is %s, not suspended", ErrConflict, c.Status)
	}
	c.Status, c.StatusReason, c.UpdatedAt = StatusActive, "", s.Now().UTC()
	if err := s.Store.UpdateConnection(ctx, c); err != nil {
		return Connection{}, err
	}
	s.Audit(ctx, org, actor, "connection.resumed", "connection", id, nil)
	s.Emit(ctx, org, "connection.status_changed", map[string]any{"connection_id": id, "from": StatusSuspended, "to": StatusActive, "reason": ""})
	return s.view(ctx, c), nil
}

// Revoke is the red button (integrations-credentials.md 6.4): suspend, cancel
// held sends, revoke at the provider, crypto-shred, revoke grants. If the
// provider call fails the ciphertext is kept so SweepRevocations can retry.
func (s *Service) Revoke(ctx context.Context, org, id, confirmName, actor string) (Connection, error) {
	c, err := s.Store.GetConnection(ctx, org, id)
	if err != nil {
		return Connection{}, err
	}
	if strings.TrimSpace(confirmName) != c.Label {
		return Connection{}, invalid("confirm_name must equal the connection label")
	}
	if c.Status == StatusRevoked {
		return s.view(ctx, c), nil // idempotent
	}
	from := c.Status
	c.Status, c.StatusReason = StatusSuspended, "revoking"
	_ = s.Store.UpdateConnection(ctx, c)
	s.purgeToken(id)
	if s.OnRevoke != nil {
		s.OnRevoke(ctx, org, id, "revoked")
	}
	pending := !s.revokeAtProvider(ctx, c)
	now := s.Now().UTC()
	if !pending {
		_ = s.Vault.Destroy(ctx, org, id)
	}
	n, _ := s.Store.RevokeGrants(ctx, org, id, actor, now)
	c.Status, c.StatusReason, c.RevokedAt, c.RevokedBy, c.UpdatedAt = StatusRevoked, "user_revoked", &now, actor, now
	c.PendingProviderRevocation = pending
	if err := s.Store.UpdateConnection(ctx, c); err != nil {
		return Connection{}, err
	}
	s.Audit(ctx, org, actor, "connection.revoked", "connection", id, map[string]any{"grants_revoked": n, "pending_provider_revocation": pending})
	s.Emit(ctx, org, "connection.status_changed", map[string]any{"connection_id": id, "from": from, "to": StatusRevoked, "reason": "user_revoked"})
	s.Emit(ctx, org, "connection.revoked", map[string]any{"connection_id": id, "pending_provider_revocation": pending})
	return s.view(ctx, c), nil
}

// revokeAtProvider returns true when nothing is left to revoke remotely.
func (s *Service) revokeAtProvider(ctx context.Context, c Connection) bool {
	if c.Mode != ModeLive || c.Kind != "oauth2" {
		return true
	}
	p, ok := s.Providers[c.Provider]
	if !ok {
		return true
	}
	ok = false
	err := s.Vault.Use(ctx, c.OrgID, c.ID, func(sec vault.Secret) error {
		rerr := p.Revoke(ctx, s.appFor(ctx, c), string(sec.Reveal()))
		ok = rerr == nil
		return nil
	})
	if errors.Is(err, vault.ErrDestroyed) || errors.Is(err, vault.ErrNotFound) {
		return true
	}
	return err == nil && ok
}

// SweepRevocations retries provider-side revocations that failed earlier.
func (s *Service) SweepRevocations(ctx context.Context, org string) {
	cs, err := s.Store.ListConnections(ctx, org, "", StatusRevoked)
	if err != nil {
		return
	}
	for _, c := range cs {
		if !c.PendingProviderRevocation {
			continue
		}
		if s.revokeAtProvider(ctx, c) {
			_ = s.Vault.Destroy(ctx, org, c.ID)
			c.PendingProviderRevocation, c.UpdatedAt = false, s.Now().UTC()
			_ = s.Store.UpdateConnection(ctx, c)
			s.Audit(ctx, org, "system", "connection.provider_revocation_done", "connection", c.ID, nil)
		}
	}
}

// RotateCredential replaces an API key (write-only). OAuth connections are
// re-authorized instead.
func (s *Service) RotateCredential(ctx context.Context, org, id string, secret vault.Secret, actor string, destroyPrevious bool) (Connection, error) {
	c, err := s.Store.GetConnection(ctx, org, id)
	if err != nil {
		return Connection{}, err
	}
	if c.Kind != "api_key" || c.Mode != ModeLive {
		return Connection{}, invalid("only live api_key connections rotate a pasted secret; re-authorize OAuth connections")
	}
	if c.Status == StatusRevoked {
		return Connection{}, fmt.Errorf("%w: connection is revoked", ErrConflict)
	}
	am, _ := s.manifests[c.Provider].AuthMethodFor("api_key")
	if am.SecretRegex != "" && !regexp.MustCompile(am.SecretRegex).Match(secret.Reveal()) {
		return Connection{}, invalid("secret has an unexpected format")
	}
	meta, err := s.Vault.Put(ctx, org, id, vault.KindAPIKey, secret, actor, c.ExpiresAt)
	if err != nil {
		return Connection{}, err
	}
	if c.Status == StatusExpired || c.Status == StatusError {
		c.Status, c.StatusReason = StatusActive, ""
	}
	c.UpdatedAt = s.Now().UTC()
	_ = s.Store.UpdateConnection(ctx, c)
	s.Audit(ctx, org, actor, "connection.rotated", "connection", id, map[string]any{"version": meta.Version, "destroy_previous": destroyPrevious})
	s.Emit(ctx, org, "connection.rotated", map[string]any{"connection_id": id, "version": meta.Version})
	return s.view(ctx, c), nil
}

// SuspendAll suspends every active connection of an organization (lockdown).
func (s *Service) SuspendAll(ctx context.Context, org, actor, reason string) int {
	cs, err := s.Store.ListConnections(ctx, org, "", "")
	if err != nil {
		return 0
	}
	n := 0
	for _, c := range cs {
		if c.Status == StatusActive || c.Status == StatusPending || c.Status == StatusNeedsReauth {
			if _, err := s.Suspend(ctx, org, c.ID, actor, reason); err == nil {
				n++
			}
		}
	}
	return n
}

// ResumeAll resumes the connections that a kill switch suspended.
func (s *Service) ResumeAll(ctx context.Context, org, actor string) int {
	cs, err := s.Store.ListConnections(ctx, org, "", StatusSuspended)
	if err != nil {
		return 0
	}
	n := 0
	for _, c := range cs {
		if c.StatusReason == "kill_switch" {
			if _, err := s.Resume(ctx, org, c.ID, actor); err == nil {
				n++
			}
		}
	}
	return n
}
