package application

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// KnownProviders are the model providers the runtime can route to.
var KnownProviders = []string{"deepseek", "anthropic", "custom"}

// noneProvider is sent as allowed_providers when nothing is allowed: it matches
// no configured provider, so the runtime refuses the call (no_allowed_provider)
// instead of treating an empty list as "unrestricted".
const noneProvider = "none"

// ModelPolicy is the organization's choice of model providers and models.
// The zero value means "no organization restriction" (the server ceiling and
// the runtime's own configuration still apply).
type ModelPolicy struct {
	// AllowedProviders limits where this organization's data may be sent.
	// Empty = any provider allowed by the server (ALLOWED_PROVIDERS).
	AllowedProviders []string `json:"allowed_providers"`
	// PreferredProviders are tried first, in order; the rest stay as fallbacks.
	PreferredProviders []string `json:"preferred_providers"`
	// RoleProviders is the provider order per agent role (e.g. "legal").
	RoleProviders map[string][]string `json:"role_providers"`
	// RoleModels is the model per agent role, "provider/model".
	RoleModels map[string]string `json:"role_models"`
	UpdatedAt  *time.Time        `json:"updated_at,omitempty"`
	UpdatedBy  string            `json:"updated_by,omitempty"`
}

// RuntimePolicy is the part of the policy sent on every runtime call.
type RuntimePolicy struct {
	AllowedProviders   []string            `json:"allowed_providers,omitempty"`
	PreferredProviders []string            `json:"preferred_providers,omitempty"`
	RoleProviders      map[string][]string `json:"role_providers,omitempty"`
	RoleModels         map[string]string   `json:"role_models,omitempty"`
}

// Empty reports whether there is nothing to send.
func (p RuntimePolicy) Empty() bool {
	return len(p.AllowedProviders) == 0 && len(p.PreferredProviders) == 0 && len(p.RoleProviders) == 0 && len(p.RoleModels) == 0
}

// ModelPolicyStore persists the policy (memory and Postgres stores implement it).
type ModelPolicyStore interface {
	GetModelPolicy(ctx context.Context, orgID string) (ModelPolicy, error)
	PutModelPolicy(ctx context.Context, orgID string, p ModelPolicy) error
}

// ModelPolicyView is the admin response: the policy plus what the server allows.
type ModelPolicyView struct {
	Policy         ModelPolicy `json:"policy"`
	ServerAllowed  []string    `json:"server_allowed_providers"`
	KnownProviders []string    `json:"known_providers"`
	PricedModels   []string    `json:"priced_models"`
	Warnings       []string    `json:"warnings"`
}

// ModelPolicyService validates, stores, audits and serves the policy. Ceiling
// is the operator's ALLOWED_PROVIDERS (empty = every known provider).
type ModelPolicyService struct {
	Store   ModelPolicyStore
	Ceiling []string
	Rec     *Recorder
	Cfg     Config
	TTL     time.Duration // cache of the runtime payload per org (default 10s)

	mu    sync.Mutex
	cache map[string]cachedPolicy
}

type cachedPolicy struct {
	p  RuntimePolicy
	at time.Time
}

var (
	roleKeyRe   = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)
	modelNameRe = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,120}$`)
)

// ParseProviderList parses a comma-separated provider list (ALLOWED_PROVIDERS).
func ParseProviderList(raw string) []string { return normList(strings.Split(raw, ",")) }

func (s *ModelPolicyService) org(ctx context.Context) string { return OrgFrom(ctx, s.Cfg.OrgID) }

func (s *ModelPolicyService) serverAllowed() []string {
	if len(s.Ceiling) == 0 {
		return append([]string{}, KnownProviders...)
	}
	out := []string{}
	for _, p := range s.Ceiling {
		if slices.Contains(KnownProviders, p) && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// Get returns the organization's policy and the server limits.
func (s *ModelPolicyService) Get(ctx context.Context) (ModelPolicyView, error) {
	p, err := s.Store.GetModelPolicy(ctx, s.org(ctx))
	if err != nil {
		return ModelPolicyView{}, err
	}
	return s.view(normalizePolicy(p)), nil
}

func (s *ModelPolicyService) view(p ModelPolicy) ModelPolicyView {
	priced := make([]string, 0, len(priceTable))
	for k := range priceTable {
		priced = append(priced, k)
	}
	sort.Strings(priced)
	v := ModelPolicyView{Policy: p, ServerAllowed: s.serverAllowed(), KnownProviders: KnownProviders, PricedModels: priced, Warnings: []string{}}
	roles := make([]string, 0, len(p.RoleModels))
	for r := range p.RoleModels {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	for _, r := range roles {
		m := p.RoleModels[r]
		if _, ok := priceTable[strings.ToLower(m[strings.Index(m, "/")+1:])]; !ok {
			v.Warnings = append(v.Warnings, fmt.Sprintf("role %s: model %s is not in the price table and is billed at the default rate", r, m))
		}
	}
	return v
}

// Put validates and stores a new policy, audits the change and refreshes the cache.
func (s *ModelPolicyService) Put(ctx context.Context, in ModelPolicy) (ModelPolicyView, error) {
	p := normalizePolicy(in)
	if err := s.validate(p); err != nil {
		return ModelPolicyView{}, err
	}
	org := s.org(ctx)
	before, err := s.Store.GetModelPolicy(ctx, org)
	if err != nil {
		return ModelPolicyView{}, err
	}
	now := time.Now().UTC()
	p.UpdatedAt, p.UpdatedBy = &now, ActorFrom(ctx, "user")
	if err := s.Store.PutModelPolicy(ctx, org, p); err != nil {
		return ModelPolicyView{}, err
	}
	s.mu.Lock()
	delete(s.cache, org)
	s.mu.Unlock()
	if s.Rec != nil {
		s.Rec.Audit(ctx, domain.AuditLog{Actor: p.UpdatedBy, Action: "settings.model_policy_updated", Entity: "org", EntityID: org,
			Details: map[string]any{"before": policyDetails(normalizePolicy(before)), "after": policyDetails(p)}})
	}
	return s.view(p), nil
}

func policyDetails(p ModelPolicy) map[string]any {
	return map[string]any{"allowed_providers": p.AllowedProviders, "preferred_providers": p.PreferredProviders,
		"role_providers": p.RoleProviders, "role_models": p.RoleModels}
}

// normalizePolicy lowercases and dedupes provider lists and role keys.
func normalizePolicy(p ModelPolicy) ModelPolicy {
	out := ModelPolicy{AllowedProviders: normList(p.AllowedProviders), PreferredProviders: normList(p.PreferredProviders),
		RoleProviders: map[string][]string{}, RoleModels: map[string]string{}, UpdatedAt: p.UpdatedAt, UpdatedBy: p.UpdatedBy}
	for k, v := range p.RoleProviders {
		if l := normList(v); len(l) > 0 {
			out.RoleProviders[strings.ToLower(strings.TrimSpace(k))] = l
		}
	}
	for k, v := range p.RoleModels {
		if v = strings.TrimSpace(v); v != "" {
			out.RoleModels[strings.ToLower(strings.TrimSpace(k))] = v
		}
	}
	return out
}

func normList(in []string) []string {
	out := []string{}
	for _, v := range in {
		v = strings.ToLower(strings.TrimSpace(v))
		if v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func (s *ModelPolicyService) validate(p ModelPolicy) error {
	server := s.serverAllowed()
	for _, v := range p.AllowedProviders {
		if !slices.Contains(KnownProviders, v) {
			return fmt.Errorf("%w: unknown provider %q (known: %s)", domain.ErrInvalid, v, strings.Join(KnownProviders, ", "))
		}
		if !slices.Contains(server, v) {
			return fmt.Errorf("%w: provider %q is not allowed by this server (ALLOWED_PROVIDERS: %s)", domain.ErrInvalid, v, strings.Join(server, ", "))
		}
	}
	effective := server
	if len(p.AllowedProviders) > 0 {
		effective = p.AllowedProviders
	}
	inEffective := func(field string, l []string) error {
		for _, v := range l {
			if !slices.Contains(effective, v) {
				return fmt.Errorf("%w: %s: provider %q is not among the allowed providers (%s)", domain.ErrInvalid, field, v, strings.Join(effective, ", "))
			}
		}
		return nil
	}
	if err := inEffective("preferred_providers", p.PreferredProviders); err != nil {
		return err
	}
	if len(p.RoleProviders) > 50 || len(p.RoleModels) > 50 {
		return fmt.Errorf("%w: at most 50 roles", domain.ErrInvalid)
	}
	for role, l := range p.RoleProviders {
		if !roleKeyRe.MatchString(role) {
			return fmt.Errorf("%w: invalid role %q (lowercase letters, digits and _)", domain.ErrInvalid, role)
		}
		if err := inEffective("role_providers."+role, l); err != nil {
			return err
		}
	}
	for role, m := range p.RoleModels {
		if !roleKeyRe.MatchString(role) {
			return fmt.Errorf("%w: invalid role %q (lowercase letters, digits and _)", domain.ErrInvalid, role)
		}
		prov, name, ok := strings.Cut(m, "/")
		prov = strings.ToLower(prov)
		if !ok || name == "" || !modelNameRe.MatchString(name) {
			return fmt.Errorf("%w: role_models.%s must be \"provider/model\"", domain.ErrInvalid, role)
		}
		if err := inEffective("role_models."+role, []string{prov}); err != nil {
			return err
		}
		if known := providerOfModel(name); known != "" && known != prov {
			return fmt.Errorf("%w: role_models.%s: %s is a %s model, not %s", domain.ErrInvalid, role, name, known, prov)
		}
	}
	return nil
}

// Runtime returns the payload merged into every runtime call of the tenant in
// ctx. Errors fall back to the server ceiling only (never wider), with a warning.
func (s *ModelPolicyService) Runtime(ctx context.Context) RuntimePolicy {
	org := s.org(ctx)
	ttl := s.TTL
	if ttl <= 0 {
		ttl = 10 * time.Second
	}
	s.mu.Lock()
	if c, ok := s.cache[org]; ok && time.Since(c.at) < ttl {
		s.mu.Unlock()
		return c.p
	}
	s.mu.Unlock()
	p, err := s.Store.GetModelPolicy(ctx, org)
	if err != nil {
		slog.Warn("model policy unavailable: applying the server ceiling only", "org", org, "err", err)
		return s.runtimeOf(ModelPolicy{})
	}
	rp := s.runtimeOf(normalizePolicy(p))
	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]cachedPolicy{}
	}
	s.cache[org] = cachedPolicy{p: rp, at: time.Now()}
	s.mu.Unlock()
	return rp
}

func (s *ModelPolicyService) runtimeOf(p ModelPolicy) RuntimePolicy {
	rp := RuntimePolicy{PreferredProviders: p.PreferredProviders, RoleProviders: p.RoleProviders, RoleModels: p.RoleModels}
	// Allowed = organization list within the server ceiling. It is only sent
	// when something restricts it; an empty intersection becomes "none".
	switch {
	case len(p.AllowedProviders) > 0 && len(s.Ceiling) > 0:
		server := s.serverAllowed()
		for _, v := range p.AllowedProviders {
			if slices.Contains(server, v) {
				rp.AllowedProviders = append(rp.AllowedProviders, v)
			}
		}
		if len(rp.AllowedProviders) == 0 {
			rp.AllowedProviders = []string{noneProvider}
		}
	case len(p.AllowedProviders) > 0:
		rp.AllowedProviders = p.AllowedProviders
	case len(s.Ceiling) > 0:
		rp.AllowedProviders = s.serverAllowed()
		if len(rp.AllowedProviders) == 0 {
			rp.AllowedProviders = []string{noneProvider}
		}
	}
	if len(rp.RoleProviders) == 0 {
		rp.RoleProviders = nil
	}
	if len(rp.RoleModels) == 0 {
		rp.RoleModels = nil
	}
	return rp
}
