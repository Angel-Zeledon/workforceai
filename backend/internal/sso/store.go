package sso

import (
	"context"
	"errors"
	"sync"
	"time"

	"aiworkforce/backend/internal/auth"
)

var (
	ErrNotFound     = errors.New("sso: not found")
	ErrDomainTaken  = errors.New("sso: another organization already verified this domain")
	ErrNoEncryption = errors.New("sso: the encryption key (CONNECTIONS_KEK) is not set, so the client secret cannot be stored")
)

// Config is the OIDC setup of one organization. SealedSecret is ciphertext (vault envelope encryption); it never leaves
// this package: the API only ever sees View.
type Config struct {
	OrgID        string
	Enabled      bool
	Issuer       string
	ClientID     string
	SealedSecret []byte
	Scopes       string
	DefaultRole  auth.Role
	JIT          bool // create accounts / memberships on first SSO login (only ever for a verified domain)
	// AllowUnverifiedEmail accepts an ID token without an email_verified claim (Microsoft Entra ID never sends it). A token
	// that says email_verified=false is always refused.
	AllowUnverifiedEmail bool
	UpdatedBy            string
	CreatedAt, UpdatedAt time.Time
}

// Policy is the per-organization sign-in policy (auth.OrgPolicy plus its audit fields).
type Policy struct {
	OrgID      string
	RequireMFA bool
	EnforceSSO bool
	UpdatedBy  string
	UpdatedAt  time.Time
}

// Domain is an e-mail domain an organization claims. It counts only once VerifiedAt is set.
type Domain struct {
	OrgID      string    `json:"-"`
	Domain     string    `json:"domain"`
	Token      string    `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
	VerifiedAt time.Time `json:"verified_at,omitempty"`
	LastCheck  time.Time `json:"last_check_at,omitempty"`
}

// Verified reports whether the DNS challenge was completed.
func (d Domain) Verified() bool { return !d.VerifiedAt.IsZero() }

// Kinds of single-use login records.
const (
	KindState    = "state"    // between /sso/start and the provider's redirect to /sso/callback
	KindExchange = "exchange" // between /sso/callback and POST /auth/sso/exchange
)

// Pending is a single-use record. Only the SHA-256 of the secret value is stored.
type Pending struct {
	Kind      string
	Hash      string
	OrgID     string
	Nonce     string // state records
	Verifier  string // PKCE code_verifier, state records
	Identity  auth.FederatedIdentity
	ExpiresAt time.Time
}

// Store is the persistence port of the module.
type Store interface {
	GetConfig(ctx context.Context, orgID string) (Config, error) // ErrNotFound
	PutConfig(ctx context.Context, c Config) error
	DeleteConfig(ctx context.Context, orgID string) error

	GetPolicy(ctx context.Context, orgID string) (Policy, error) // zero Policy when none
	PutPolicy(ctx context.Context, p Policy) error

	ListDomains(ctx context.Context, orgID string) ([]Domain, error)
	GetDomain(ctx context.Context, orgID, domain string) (Domain, error) // ErrNotFound
	AddDomain(ctx context.Context, d Domain) error                       // idempotent per (org, domain)
	DeleteDomain(ctx context.Context, orgID, domain string) error
	// MarkVerified sets VerifiedAt and publishes the domain in the verified index. ErrDomainTaken when another
	// organization verified it first.
	MarkVerified(ctx context.Context, orgID, domain string, at time.Time) error
	TouchDomainCheck(ctx context.Context, orgID, domain string, at time.Time) error
	// OrgForVerifiedDomain is the pre-tenant lookup behind "sign in with SSO" (ErrNotFound).
	OrgForVerifiedDomain(ctx context.Context, domain string) (string, error)

	SavePending(ctx context.Context, p Pending) error
	// ConsumePending atomically returns and deletes a record: a value can be redeemed once. ErrNotFound when unknown,
	// already used or expired.
	ConsumePending(ctx context.Context, kind, hash string, now time.Time) (Pending, error)
	PurgeExpired(ctx context.Context, now time.Time) error
}

// MemStore is the in-memory Store (tests and the no-database mode).
type MemStore struct {
	mu       sync.Mutex
	configs  map[string]Config
	policies map[string]Policy
	domains  map[[2]string]Domain
	verified map[string]string
	pending  map[[2]string]Pending
}

// NewMemStore builds an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{configs: map[string]Config{}, policies: map[string]Policy{}, domains: map[[2]string]Domain{},
		verified: map[string]string{}, pending: map[[2]string]Pending{}}
}

func (m *MemStore) GetConfig(_ context.Context, org string) (Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.configs[org]
	if !ok {
		return Config{}, ErrNotFound
	}
	return c, nil
}

func (m *MemStore) PutConfig(_ context.Context, c Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.configs[c.OrgID]; ok {
		c.CreatedAt = old.CreatedAt
	}
	m.configs[c.OrgID] = c
	return nil
}

func (m *MemStore) DeleteConfig(_ context.Context, org string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.configs, org)
	return nil
}

func (m *MemStore) GetPolicy(_ context.Context, org string) (Policy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.policies[org], nil
}

func (m *MemStore) PutPolicy(_ context.Context, p Policy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.policies[p.OrgID] = p
	return nil
}

func (m *MemStore) ListDomains(_ context.Context, org string) ([]Domain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Domain
	for k, d := range m.domains {
		if k[0] == org {
			out = append(out, d)
		}
	}
	sortDomains(out)
	return out, nil
}

func (m *MemStore) GetDomain(_ context.Context, org, dom string) (Domain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.domains[[2]string{org, dom}]
	if !ok {
		return Domain{}, ErrNotFound
	}
	return d, nil
}

func (m *MemStore) AddDomain(_ context.Context, d Domain) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.domains[[2]string{d.OrgID, d.Domain}]; !ok {
		m.domains[[2]string{d.OrgID, d.Domain}] = d
	}
	return nil
}

func (m *MemStore) DeleteDomain(_ context.Context, org, dom string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.domains[[2]string{org, dom}]; !ok {
		return ErrNotFound
	}
	delete(m.domains, [2]string{org, dom})
	if m.verified[dom] == org {
		delete(m.verified, dom)
	}
	return nil
}

func (m *MemStore) MarkVerified(_ context.Context, org, dom string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.domains[[2]string{org, dom}]
	if !ok {
		return ErrNotFound
	}
	if o, taken := m.verified[dom]; taken && o != org {
		return ErrDomainTaken
	}
	d.VerifiedAt = at
	m.domains[[2]string{org, dom}] = d
	m.verified[dom] = org
	return nil
}

func (m *MemStore) TouchDomainCheck(_ context.Context, org, dom string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d, ok := m.domains[[2]string{org, dom}]; ok {
		d.LastCheck = at
		m.domains[[2]string{org, dom}] = d
	}
	return nil
}

func (m *MemStore) OrgForVerifiedDomain(_ context.Context, dom string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.verified[dom]
	if !ok {
		return "", ErrNotFound
	}
	return o, nil
}

func (m *MemStore) SavePending(_ context.Context, p Pending) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending[[2]string{p.Kind, p.Hash}] = p
	return nil
}

func (m *MemStore) ConsumePending(_ context.Context, kind, hash string, now time.Time) (Pending, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pending[[2]string{kind, hash}]
	if !ok {
		return Pending{}, ErrNotFound
	}
	delete(m.pending, [2]string{kind, hash}) // consumed even when expired
	if !now.Before(p.ExpiresAt) {
		return Pending{}, ErrNotFound
	}
	return p, nil
}

func (m *MemStore) PurgeExpired(_ context.Context, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, p := range m.pending {
		if !now.Before(p.ExpiresAt) {
			delete(m.pending, k)
		}
	}
	return nil
}
