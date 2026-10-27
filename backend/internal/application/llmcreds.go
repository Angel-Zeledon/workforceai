package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Bring-your-own-model (G3): an organization can use its own provider key instead of the platform's
// DeepSeek. The key is sealed with the organization's data key (the same envelope encryption as the
// connection vault: AES-256-GCM under a per-org DEK wrapped by CONNECTIONS_KEK), never stored in clear,
// never returned by any endpoint (only its last 4 characters), and travels in clear only on the internal,
// RUNTIME_TOKEN-authenticated backend -> runtime hop.

// ErrLLMEncryptionOff: no key encryption key is configured, so a key cannot be stored (fail closed).
var ErrLLMEncryptionOff = errors.New("llm: encryption is not configured on this server (CONNECTIONS_KEK); provider keys cannot be stored")

// platformProvider is the platform default, answered with the operator's key: PLATFORM_LLM_PROVIDER (any catalog
// provider except custom; it must match the key the agent runtime has, e.g. gemini with GEMINI_API_KEY), DeepSeek
// when unset or unknown. Read on every call so a test or an operator can change it without a rebuild.
func platformProvider() string {
	p := strings.ToLower(strings.TrimSpace(os.Getenv("PLATFORM_LLM_PROVIDER")))
	if _, ok := llmInfo(p); ok && p != "custom" {
		return p
	}
	return "deepseek"
}

// LLMProviderInfo is the catalog entry of a provider (what the settings screen renders).
type LLMProviderInfo struct {
	ID              string   `json:"id"`
	Label           string   `json:"label"`
	Platform        bool     `json:"platform"` // included with the product (no key needed)
	Models          []string `json:"models"`
	DefaultModel    string   `json:"default_model"`
	DefaultBaseURL  string   `json:"default_base_url,omitempty"`
	SupportsBaseURL bool     `json:"supports_base_url"`
	RequiresBaseURL bool     `json:"requires_base_url"`
	SupportsOrgID   bool     `json:"supports_org_id"` // OpenAI organization / project
	KeyURL          string   `json:"key_url,omitempty"`
}

// LLMCatalog lists the providers, in display order.
var LLMCatalog = []LLMProviderInfo{
	{ID: "deepseek", Label: "DeepSeek", Models: []string{"deepseek-chat", "deepseek-reasoner"},
		DefaultModel: "deepseek-chat", KeyURL: "https://platform.deepseek.com/api_keys"},
	{ID: "anthropic", Label: "Claude (Anthropic)", Models: []string{"claude-sonnet-5-5", "claude-opus-5-5", "claude-haiku-4-5"},
		DefaultModel: "claude-sonnet-5-5", KeyURL: "https://console.anthropic.com/settings/keys"},
	{ID: "openai", Label: "OpenAI", Models: []string{"gpt-4.1", "gpt-4.1-mini", "gpt-4o", "gpt-4o-mini", "gpt-5", "gpt-5-mini"},
		DefaultModel: "gpt-4.1", SupportsBaseURL: true, SupportsOrgID: true, KeyURL: "https://platform.openai.com/api-keys"},
	{ID: "kimi", Label: "Kimi (Moonshot)", Models: []string{"kimi-k2-0905-preview", "kimi-k2-turbo-preview", "kimi-k2-thinking", "kimi-latest"},
		DefaultModel: "kimi-k2-0905-preview", DefaultBaseURL: "https://api.moonshot.ai/v1", SupportsBaseURL: true,
		KeyURL: "https://platform.moonshot.ai/console/api-keys"},
	// Google's OpenAI-compatible endpoint (the runtime reaches it like Kimi). gemini-3.5-flash is deprecated and
	// routed by Google to 3.6 Flash. Prices: model_prices.json.
	{ID: "gemini", Label: "Gemini (Google)", Models: []string{"gemini-3.6-flash", "gemini-3.8-flash", "gemini-3.5-flash-lite", "gemini-3.1-flash-lite", "gemini-3.1-pro-preview"},
		DefaultModel: "gemini-3.6-flash", DefaultBaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", SupportsBaseURL: true,
		KeyURL: "https://aistudio.google.com/app/apikey"},
	{ID: "custom", Label: "OpenAI-compatible", Models: []string{}, SupportsBaseURL: true, RequiresBaseURL: true},
}

func llmInfo(id string) (LLMProviderInfo, bool) {
	for _, p := range LLMCatalog {
		if p.ID == id {
			return p, true
		}
	}
	return LLMProviderInfo{}, false
}

// LLMCredentialRecord is one stored credential. Sealed is ciphertext; it is never serialized.
type LLMCredentialRecord struct {
	Provider     string     `json:"provider"`
	Model        string     `json:"model"`
	BaseURL      string     `json:"base_url,omitempty"`
	OrgRef       string     `json:"org_id,omitempty"`
	ProjectRef   string     `json:"project_id,omitempty"`
	Sealed       []byte     `json:"-"`
	Last4        string     `json:"key_last4,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	UpdatedBy    string     `json:"updated_by,omitempty"`
	LastTestCode string     `json:"last_test_code,omitempty"`
	LastTestAt   *time.Time `json:"last_test_at,omitempty"`
}

// LLMStore persists credentials and the active provider (memory and Postgres implement it).
type LLMStore interface {
	ListLLMCredentials(ctx context.Context, orgID string) ([]LLMCredentialRecord, error)
	PutLLMCredential(ctx context.Context, orgID string, r LLMCredentialRecord) error
	DeleteLLMCredential(ctx context.Context, orgID, provider string) error
	RecordLLMTest(ctx context.Context, orgID, provider, code string, at time.Time) error
	GetLLMActive(ctx context.Context, orgID string) (string, error)
	PutLLMActive(ctx context.Context, orgID, provider, by string) error
}

// LLMSealer encrypts and decrypts under the organization's data key (internal/vault.Vault).
type LLMSealer interface {
	Enabled() bool
	Seal(ctx context.Context, org, purpose string, plain []byte) ([]byte, error)
	Unseal(ctx context.Context, org, purpose string, sealed []byte) ([]byte, error)
}

// RuntimeCredential is the credential as the runtime receives it. It carries the clear key:
// it must never be logged, so every formatting path is redacted.
type RuntimeCredential struct {
	Provider  string `json:"provider"`
	APIKey    string `json:"api_key"`
	Model     string `json:"model,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`
	OrgID     string `json:"org_id,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
}

func (c RuntimeCredential) String() string {
	return fmt.Sprintf("RuntimeCredential{provider:%s model:%s key:[REDACTED]}", c.Provider, c.Model)
}
func (c RuntimeCredential) GoString() string { return c.String() }
func (c RuntimeCredential) LogValue() slog.Value {
	return slog.StringValue(c.String())
}

// LLMTestResult is the outcome of a connection test. Code is stable:
// ok | invalid_key | quota | model_not_found | network | unknown | invalid_request.
type LLMTestResult struct {
	OK        bool   `json:"ok"`
	Code      string `json:"code"`
	Message   string `json:"message,omitempty"`
	Provider  string `json:"provider"`
	Model     string `json:"model,omitempty"`
	LatencyMS int    `json:"latency_ms"`
}

// LLMTester performs the real tiny call through the runtime (runtime.Client implements it).
type LLMTester interface {
	TestProvider(ctx context.Context, c RuntimeCredential) (LLMTestResult, error)
}

// LLMInput is what an owner/admin submits for a provider. An empty APIKey on an existing
// credential keeps the stored key (only model/base URL change).
type LLMInput struct {
	APIKey    string `json:"api_key"`
	Model     string `json:"model"`
	BaseURL   string `json:"base_url"`
	OrgID     string `json:"org_id"`
	ProjectID string `json:"project_id"`
}

// LLMProviderView is one provider card, with the state of the organization's credential.
type LLMProviderView struct {
	LLMProviderInfo
	Configured   bool       `json:"configured"` // the organization has its own key for it
	Model        string     `json:"model"`      // chosen model (the default when none)
	BaseURL      string     `json:"base_url,omitempty"`
	OrgID        string     `json:"org_id,omitempty"`
	ProjectID    string     `json:"project_id,omitempty"`
	KeyLast4     string     `json:"key_last4,omitempty"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
	LastTestCode string     `json:"last_test_code,omitempty"`
	LastTestAt   *time.Time `json:"last_test_at,omitempty"`
}

// LLMView is the GET /settings/llm response. It never contains a key.
type LLMView struct {
	ActiveProvider     string            `json:"active_provider"`
	PlatformDefault    string            `json:"platform_default"`
	UsingPlatformModel bool              `json:"using_platform_model"` // active provider answers with the platform's own key
	EncryptionOn       bool              `json:"encryption_available"`
	Providers          []LLMProviderView `json:"providers"`
	Warnings           []string          `json:"warnings"`
}

// LLMService validates, encrypts, stores, audits and serves the organization's model credentials.
type LLMService struct {
	Store  LLMStore
	Sealer LLMSealer
	Tester LLMTester
	Policy *ModelPolicyService // optional: the active provider must be allowed by the policy
	Rec    *Recorder
	Cfg    Config
	// Suspects (optional) learns the clear keys so redactors scrub them from anything an agent reads.
	Suspects interface{ Add(string) }
	TTL      time.Duration // cache of decrypted credentials per org (default 15s)

	mu    sync.Mutex
	cache map[string]cachedLLM
	tests map[string][]time.Time // org -> recent tests (throttle)
	nowFn func() time.Time
}

type cachedLLM struct {
	active string
	creds  []RuntimeCredential
	byo    bool
	at     time.Time
}

var (
	llmRefRe   = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,80}$`)
	llmModelRe = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,120}$`)
)

func (s *LLMService) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now().UTC()
}

func (s *LLMService) org(ctx context.Context) string { return OrgFrom(ctx, s.Cfg.OrgID) }

func purposeFor(provider string) string { return "llm_key:" + provider }

func (s *LLMService) invalidate(org string) {
	s.mu.Lock()
	delete(s.cache, org)
	s.mu.Unlock()
}

// Get returns the provider cards and the active provider (no key, ever).
func (s *LLMService) Get(ctx context.Context) (LLMView, error) {
	org := s.org(ctx)
	recs, err := s.Store.ListLLMCredentials(ctx, org)
	if err != nil {
		return LLMView{}, err
	}
	active, err := s.Store.GetLLMActive(ctx, org)
	if err != nil {
		return LLMView{}, err
	}
	byID := map[string]LLMCredentialRecord{}
	for _, r := range recs {
		byID[r.Provider] = r
	}
	if active == "" {
		active = platformProvider()
	}
	v := LLMView{ActiveProvider: active, PlatformDefault: platformProvider(), EncryptionOn: s.Sealer != nil && s.Sealer.Enabled(),
		Providers: []LLMProviderView{}, Warnings: []string{}}
	for _, info := range LLMCatalog {
		info.Platform = info.ID == platformProvider()
		pv := LLMProviderView{LLMProviderInfo: info, Model: info.DefaultModel, BaseURL: info.DefaultBaseURL}
		if info.Models == nil {
			pv.Models = []string{}
		}
		if r, ok := byID[info.ID]; ok {
			t := r.UpdatedAt
			pv.Configured, pv.Model, pv.KeyLast4, pv.UpdatedAt = true, r.Model, r.Last4, &t
			pv.OrgID, pv.ProjectID = r.OrgRef, r.ProjectRef
			if r.BaseURL != "" {
				pv.BaseURL = r.BaseURL
			}
			pv.LastTestCode, pv.LastTestAt = r.LastTestCode, r.LastTestAt
			if _, priced := priceTable[strings.ToLower(r.Model)]; !priced && r.Model != "" && info.ID != "custom" {
				v.Warnings = append(v.Warnings, fmt.Sprintf("%s: model %s is not in the price table and is billed at the default rate", info.ID, r.Model))
			}
		}
		v.Providers = append(v.Providers, pv)
	}
	_, hasOwn := byID[active]
	v.UsingPlatformModel = active == platformProvider() && !hasOwn
	return v, nil
}

func validateBaseURL(info LLMProviderInfo, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if info.RequiresBaseURL {
			return "", fmt.Errorf("%w: base_url is required for %s", domain.ErrInvalid, info.ID)
		}
		return "", nil
	}
	if !info.SupportsBaseURL {
		return "", fmt.Errorf("%w: %s does not take a custom base URL", domain.ErrInvalid, info.ID)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(raw) > 300 {
		return "", fmt.Errorf("%w: base_url must be a plain URL such as https://api.example.com/v1", domain.ErrInvalid)
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && info.ID == "custom") {
		return "", fmt.Errorf("%w: base_url must use https (http is only accepted for the OpenAI-compatible provider)", domain.ErrInvalid)
	}
	host := strings.ToLower(u.Hostname())
	if host == "metadata.google.internal" || host == "metadata" {
		return "", fmt.Errorf("%w: base_url points to a cloud metadata service", domain.ErrInvalid)
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast()) {
		return "", fmt.Errorf("%w: base_url points to a link-local or unspecified address", domain.ErrInvalid)
	}
	return strings.TrimRight(raw, "/"), nil
}

func validateKey(key string) error {
	if len(key) < 8 || len(key) > 512 {
		return fmt.Errorf("%w: the API key looks too short or too long", domain.ErrInvalid)
	}
	for _, r := range key {
		if r <= ' ' || r == 0x7f {
			return fmt.Errorf("%w: the API key must not contain spaces or line breaks", domain.ErrInvalid)
		}
	}
	return nil
}

// normalize validates the non-secret fields for a provider.
func (s *LLMService) normalize(provider string, in LLMInput) (LLMProviderInfo, LLMInput, error) {
	info, ok := llmInfo(provider)
	if !ok {
		return info, in, fmt.Errorf("%w: unknown provider %q", domain.ErrInvalid, provider)
	}
	in.APIKey = strings.TrimSpace(in.APIKey)
	in.Model = strings.TrimSpace(in.Model)
	in.OrgID, in.ProjectID = strings.TrimSpace(in.OrgID), strings.TrimSpace(in.ProjectID)
	if in.Model == "" {
		in.Model = info.DefaultModel
	}
	if in.Model == "" || !llmModelRe.MatchString(in.Model) {
		return info, in, fmt.Errorf("%w: model is required (letters, digits and . _ : / -)", domain.ErrInvalid)
	}
	in.Model = strings.TrimPrefix(in.Model, provider+"/")
	base, err := validateBaseURL(info, in.BaseURL)
	if err != nil {
		return info, in, err
	}
	if base == info.DefaultBaseURL {
		base = "" // the default is implied
	}
	in.BaseURL = base
	if (in.OrgID != "" || in.ProjectID != "") && !info.SupportsOrgID {
		return info, in, fmt.Errorf("%w: organization / project ids are only used by OpenAI", domain.ErrInvalid)
	}
	for _, ref := range []string{in.OrgID, in.ProjectID} {
		if ref != "" && !llmRefRe.MatchString(ref) {
			return info, in, fmt.Errorf("%w: organization / project id has invalid characters", domain.ErrInvalid)
		}
	}
	return info, in, nil
}

func last4(key string) string {
	if len(key) < 12 {
		return ""
	}
	return key[len(key)-4:]
}

// Set stores (or rotates, or updates) the credential of one provider.
func (s *LLMService) Set(ctx context.Context, provider string, in LLMInput) (LLMView, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	_, in, err := s.normalize(provider, in)
	if err != nil {
		return LLMView{}, err
	}
	org := s.org(ctx)
	recs, err := s.Store.ListLLMCredentials(ctx, org)
	if err != nil {
		return LLMView{}, err
	}
	var prev *LLMCredentialRecord
	for i := range recs {
		if recs[i].Provider == provider {
			prev = &recs[i]
		}
	}
	now := s.now()
	rec := LLMCredentialRecord{Provider: provider, Model: in.Model, BaseURL: in.BaseURL, OrgRef: in.OrgID, ProjectRef: in.ProjectID,
		UpdatedAt: now, UpdatedBy: ActorFrom(ctx, "user"), CreatedAt: now}
	action := "settings.llm_credential_set"
	switch {
	case in.APIKey != "":
		if err := validateKey(in.APIKey); err != nil {
			return LLMView{}, err
		}
		if s.Sealer == nil || !s.Sealer.Enabled() {
			return LLMView{}, ErrLLMEncryptionOff
		}
		sealed, err := s.Sealer.Seal(ctx, org, purposeFor(provider), []byte(in.APIKey))
		if err != nil {
			return LLMView{}, fmt.Errorf("llm: sealing the key: %w", err)
		}
		rec.Sealed, rec.Last4 = sealed, last4(in.APIKey)
		if s.Suspects != nil {
			s.Suspects.Add(in.APIKey)
		}
		if prev != nil {
			action, rec.CreatedAt = "settings.llm_credential_rotated", prev.CreatedAt
		}
	case prev != nil:
		rec.Sealed, rec.Last4, rec.CreatedAt = prev.Sealed, prev.Last4, prev.CreatedAt
		rec.LastTestCode, rec.LastTestAt = prev.LastTestCode, prev.LastTestAt
		action = "settings.llm_credential_updated"
	default:
		return LLMView{}, fmt.Errorf("%w: the API key is required", domain.ErrInvalid)
	}
	if err := s.Store.PutLLMCredential(ctx, org, rec); err != nil {
		return LLMView{}, err
	}
	s.invalidate(org)
	s.audit(ctx, action, provider, map[string]any{"provider": provider, "model": rec.Model, "base_url": rec.BaseURL,
		"key_changed": in.APIKey != "", "key_last4": rec.Last4})
	return s.Get(ctx)
}

// Delete removes the credential. If it was the active provider the organization returns to the platform default.
func (s *LLMService) Delete(ctx context.Context, provider string) (LLMView, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if _, ok := llmInfo(provider); !ok {
		return LLMView{}, fmt.Errorf("%w: unknown provider %q", domain.ErrInvalid, provider)
	}
	org := s.org(ctx)
	if err := s.Store.DeleteLLMCredential(ctx, org, provider); err != nil {
		return LLMView{}, err
	}
	active, err := s.Store.GetLLMActive(ctx, org)
	if err != nil {
		return LLMView{}, err
	}
	reset := active == provider && provider != platformProvider()
	if reset {
		if err := s.Store.PutLLMActive(ctx, org, "", ActorFrom(ctx, "user")); err != nil {
			return LLMView{}, err
		}
	}
	if active == provider && provider == platformProvider() {
		// deleting the organization's own key of the platform provider returns to the platform's: same provider id
		reset = false
	}
	s.invalidate(org)
	s.audit(ctx, "settings.llm_credential_deleted", provider, map[string]any{"provider": provider, "active_reset": reset})
	return s.Get(ctx)
}

// SetActive chooses the provider the organization's agents use. "" or the platform provider without an own key is the platform default.
func (s *LLMService) SetActive(ctx context.Context, provider string) (LLMView, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		provider = platformProvider()
	}
	if _, ok := llmInfo(provider); !ok {
		return LLMView{}, fmt.Errorf("%w: unknown provider %q", domain.ErrInvalid, provider)
	}
	org := s.org(ctx)
	recs, err := s.Store.ListLLMCredentials(ctx, org)
	if err != nil {
		return LLMView{}, err
	}
	has := slices.ContainsFunc(recs, func(r LLMCredentialRecord) bool { return r.Provider == provider })
	if !has && provider != platformProvider() {
		return LLMView{}, fmt.Errorf("%w: add an API key for %s before using it", domain.ErrInvalid, provider)
	}
	if s.Policy != nil {
		if err := s.Policy.checkActive(ctx, provider); err != nil {
			return LLMView{}, err
		}
	}
	before, _ := s.Store.GetLLMActive(ctx, org)
	store := provider
	if provider == platformProvider() && !has {
		store = ""
	}
	if err := s.Store.PutLLMActive(ctx, org, store, ActorFrom(ctx, "user")); err != nil {
		return LLMView{}, err
	}
	s.invalidate(org)
	if before == "" {
		before = platformProvider()
	}
	s.audit(ctx, "settings.llm_active_changed", provider, map[string]any{"before": before, "after": provider})
	return s.Get(ctx)
}

// Test runs a tiny real call with the given credential. With an empty api_key the stored key is used,
// so "Probar conexion" works both before saving (typed key) and after (stored key). The key is never stored by a test.
func (s *LLMService) Test(ctx context.Context, provider string, in LLMInput) (LLMTestResult, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if s.Tester == nil {
		return LLMTestResult{}, fmt.Errorf("%w: the model runtime is not available", domain.ErrInvalid)
	}
	org := s.org(ctx)
	if !s.allowTest(org) {
		return LLMTestResult{OK: false, Code: "rate_limited", Provider: provider,
			Message: "Too many connection tests in a short time. Wait a moment and try again."}, nil
	}
	recs, err := s.Store.ListLLMCredentials(ctx, org)
	if err != nil {
		return LLMTestResult{}, err
	}
	var prev *LLMCredentialRecord
	for i := range recs {
		if recs[i].Provider == provider {
			prev = &recs[i]
		}
	}
	// Fields the caller left empty fall back to the stored credential.
	if prev != nil {
		if strings.TrimSpace(in.Model) == "" {
			in.Model = prev.Model
		}
		if strings.TrimSpace(in.BaseURL) == "" {
			in.BaseURL = prev.BaseURL
		}
		if in.OrgID == "" {
			in.OrgID = prev.OrgRef
		}
		if in.ProjectID == "" {
			in.ProjectID = prev.ProjectRef
		}
	}
	info, in, err := s.normalize(provider, in)
	if err != nil {
		return LLMTestResult{}, err
	}
	key := in.APIKey
	stored := false
	if key == "" {
		if prev == nil {
			if provider == platformProvider() {
				return LLMTestResult{}, fmt.Errorf("%w: the platform's included model needs no test; enter your own key to test it", domain.ErrInvalid)
			}
			return LLMTestResult{}, fmt.Errorf("%w: enter an API key to test", domain.ErrInvalid)
		}
		plain, err := s.Sealer.Unseal(ctx, org, purposeFor(provider), prev.Sealed)
		if err != nil {
			return LLMTestResult{}, fmt.Errorf("llm: opening the stored key: %w", err)
		}
		key, stored = string(plain), true
	} else if err := validateKey(key); err != nil {
		return LLMTestResult{}, err
	}
	base := in.BaseURL
	if base == "" {
		base = info.DefaultBaseURL
	}
	res, terr := s.Tester.TestProvider(ctx, RuntimeCredential{Provider: provider, APIKey: key, Model: in.Model, BaseURL: base,
		OrgID: in.OrgID, ProjectID: in.ProjectID})
	if terr != nil {
		// The runtime itself did not answer: report it as a network problem, never with the raw error (it may echo the request).
		slog.Warn("llm test: runtime unreachable", "provider", provider)
		res = LLMTestResult{OK: false, Code: "network", Provider: provider, Model: in.Model,
			Message: "The AI runtime could not be reached. Try again in a moment."}
	}
	res.Message = strings.ReplaceAll(res.Message, key, "[REDACTED]")
	if stored {
		_ = s.Store.RecordLLMTest(ctx, org, provider, res.Code, s.now())
	}
	s.audit(ctx, "settings.llm_credential_tested", provider, map[string]any{"provider": provider, "model": in.Model, "ok": res.OK,
		"code": res.Code, "stored_key": stored})
	return res, nil
}

// allowTest throttles to 10 tests per minute per organization (each one is a billed call).
func (s *LLMService) allowTest(org string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tests == nil {
		s.tests = map[string][]time.Time{}
	}
	cut := s.now().Add(-time.Minute)
	recent := s.tests[org][:0]
	for _, t := range s.tests[org] {
		if t.After(cut) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 10 {
		s.tests[org] = recent
		return false
	}
	s.tests[org] = append(recent, s.now())
	return true
}

func (s *LLMService) audit(ctx context.Context, action, provider string, details map[string]any) {
	if s.Rec == nil {
		return
	}
	org := s.org(ctx)
	s.Rec.Audit(ctx, domain.AuditLog{Actor: ActorFrom(ctx, "user"), Action: action, Entity: "llm_provider", EntityID: org + "/" + provider, Details: details})
}

// Apply merges the organization's choice into the runtime policy sent on every call: the active provider
// first and, when it is the organization's own key, that credential, with byo_only so nothing else answers.
// Without a choice the policy is returned untouched (platform default).
func (s *LLMService) Apply(ctx context.Context, p RuntimePolicy) RuntimePolicy {
	c := s.load(ctx)
	if c.active == "" || c.active == platformProvider() && !c.byo {
		return p
	}
	out := p
	out.PreferredProviders = append([]string{c.active}, slices.DeleteFunc(slices.Clone(p.PreferredProviders), func(x string) bool { return x == c.active })...)
	if c.byo {
		out.ProviderCredentials = c.creds
		out.ByoOnly = true
	}
	return out
}

func (s *LLMService) load(ctx context.Context) cachedLLM {
	org := s.org(ctx)
	ttl := s.TTL
	if ttl <= 0 {
		ttl = 15 * time.Second
	}
	s.mu.Lock()
	if c, ok := s.cache[org]; ok && time.Since(c.at) < ttl {
		s.mu.Unlock()
		return c
	}
	s.mu.Unlock()
	c := s.read(ctx, org)
	c.at = time.Now()
	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]cachedLLM{}
	}
	s.cache[org] = c
	s.mu.Unlock()
	return c
}

// read resolves the active provider and (if it has one) its decrypted credential. Any problem degrades to
// "no choice" for the platform default only when the organization never chose; if it chose its own key and
// the key cannot be opened, the call must fail closed rather than silently switch model, so a poisoned
// credential is sent (empty key) and the runtime rejects it.
func (s *LLMService) read(ctx context.Context, org string) cachedLLM {
	active, err := s.Store.GetLLMActive(ctx, org)
	if err != nil || active == "" {
		return cachedLLM{}
	}
	recs, err := s.Store.ListLLMCredentials(ctx, org)
	if err != nil {
		slog.Warn("llm credentials unavailable", "org", org, "err", err)
		return cachedLLM{active: active, byo: active != platformProvider(), creds: []RuntimeCredential{{Provider: active}}}
	}
	for _, r := range recs {
		if r.Provider != active {
			continue
		}
		if s.Sealer == nil {
			return cachedLLM{active: active, byo: true, creds: []RuntimeCredential{{Provider: active}}}
		}
		plain, err := s.Sealer.Unseal(ctx, org, purposeFor(r.Provider), r.Sealed)
		if err != nil {
			slog.Error("llm key cannot be opened: failing closed", "org", org, "provider", r.Provider)
			return cachedLLM{active: active, byo: true, creds: []RuntimeCredential{{Provider: active}}}
		}
		if s.Suspects != nil {
			s.Suspects.Add(string(plain))
		}
		info, _ := llmInfo(r.Provider)
		base := r.BaseURL
		if base == "" {
			base = info.DefaultBaseURL
		}
		return cachedLLM{active: active, byo: true, creds: []RuntimeCredential{{Provider: r.Provider, APIKey: string(plain), Model: r.Model,
			BaseURL: base, OrgID: r.OrgRef, ProjectID: r.ProjectRef}}}
	}
	return cachedLLM{active: active}
}

// checkActive refuses an active provider that the organization policy or the server forbids.
func (s *ModelPolicyService) checkActive(ctx context.Context, provider string) error {
	if !slices.Contains(s.serverAllowed(), provider) {
		return fmt.Errorf("%w: provider %q is not allowed by this server (ALLOWED_PROVIDERS)", domain.ErrInvalid, provider)
	}
	p, err := s.Store.GetModelPolicy(ctx, s.org(ctx))
	if err != nil {
		return err
	}
	p = normalizePolicy(p)
	if len(p.AllowedProviders) > 0 && !slices.Contains(p.AllowedProviders, provider) {
		return fmt.Errorf("%w: provider %q is not among the organization's allowed providers; change the data policy first", domain.ErrInvalid, provider)
	}
	return nil
}
