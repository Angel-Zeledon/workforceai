// Package governance holds the company-level controls: public API tokens, usage and cost reports, spend limits
// by department and agent, the data retention policy with its purge job, and the audit export. HTTP handlers
// are in http.go; persistence in pg.go. See docs/architecture/governance.md.
package governance

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Errors.
var (
	ErrUnauthorized = errors.New("governance: invalid, expired or revoked token")
	ErrInvalid      = errors.New("governance: invalid")
	ErrNotFound     = errors.New("governance: not found")
	ErrTokenLimit   = errors.New("governance: too many active tokens")
)

// TokenPrefix marks API tokens so secret scanners and humans recognize them.
const TokenPrefix = "aiw_"

// MaxActiveTokens bounds the active tokens of one organization.
const MaxActiveTokens = 25

// Scopes a token can hold. None of them reaches administration: a token cannot manage members, tokens,
// webhooks, retention or approvals.
const (
	ScopeRequestsRead  = "requests:read"
	ScopeRequestsWrite = "requests:write"
	ScopeReportsRead   = "reports:read"
	ScopeAgentsRead    = "agents:read"
	ScopeUsageRead     = "usage:read"
)

// AllScopes lists the valid scopes.
var AllScopes = []string{ScopeRequestsRead, ScopeRequestsWrite, ScopeReportsRead, ScopeAgentsRead, ScopeUsageRead}

// ValidateScopes returns the scopes de-duplicated and sorted, or ErrInvalid when empty or unknown.
func ValidateScopes(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("%w: choose at least one scope", ErrInvalid)
	}
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		ok := false
		for _, a := range AllScopes {
			ok = ok || a == s
		}
		if !ok {
			return nil, fmt.Errorf("%w: unknown scope %q", ErrInvalid, s)
		}
		seen[s] = true
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}

// HasScope reports whether granted includes need. requests:write implies requests:read.
func HasScope(granted []string, need string) bool {
	for _, g := range granted {
		if g == need || (need == ScopeRequestsRead && g == ScopeRequestsWrite) {
			return true
		}
	}
	return false
}

// HashToken is what is stored: sha256 of the token. The token carries 256 bits of randomness, so a fast
// unsalted hash is the right tool (nothing to brute force) and it makes lookup by hash possible.
func HashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// GenerateToken returns a new token ("aiw_" + 256 random bits), its display prefix and its hash.
func GenerateToken() (plain, prefix, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", "", err
	}
	plain = TokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	return plain, plain[:len(TokenPrefix)+6], HashToken(plain), nil
}

// APIToken is the stored metadata of a token (never the token or its hash).
type APIToken struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// Active reports whether the token can authenticate at now.
func (t APIToken) Active(now time.Time) bool {
	return t.RevokedAt == nil && (t.ExpiresAt == nil || now.Before(*t.ExpiresAt))
}

// TokenStore persists tokens.
type TokenStore interface {
	Insert(ctx context.Context, org string, t APIToken, hash string) error
	List(ctx context.Context, org string) ([]APIToken, error)
	// Revoke is idempotent; ErrNotFound when the token does not exist in the organization.
	Revoke(ctx context.Context, org, id string, at time.Time) error
	// Lookup finds a token by hash across organizations (the bearer does not say which one it belongs to).
	Lookup(ctx context.Context, hash string) (org string, t APIToken, storedHash string, err error)
	Touch(ctx context.Context, org, id string, at time.Time) error
}

// TokenService issues and authenticates API tokens.
type TokenService struct {
	Store TokenStore
	Now   func() time.Time
}

func (s *TokenService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Create issues a token. The plaintext is returned once and never stored.
func (s *TokenService) Create(ctx context.Context, org, actor, name string, scopes []string, expiresInDays int) (APIToken, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 {
		return APIToken{}, "", fmt.Errorf("%w: name must be 1 to 80 characters", ErrInvalid)
	}
	sc, err := ValidateScopes(scopes)
	if err != nil {
		return APIToken{}, "", err
	}
	if expiresInDays < 0 || expiresInDays > 730 {
		return APIToken{}, "", fmt.Errorf("%w: expiry must be 0 (never) to 730 days", ErrInvalid)
	}
	existing, err := s.Store.List(ctx, org)
	if err != nil {
		return APIToken{}, "", err
	}
	active := 0
	for _, t := range existing {
		if t.Active(s.now()) {
			active++
		}
	}
	if active >= MaxActiveTokens {
		return APIToken{}, "", ErrTokenLimit
	}
	plain, prefix, hash, err := GenerateToken()
	if err != nil {
		return APIToken{}, "", err
	}
	t := APIToken{ID: uuid.NewString(), Name: name, Prefix: prefix, Scopes: sc, CreatedBy: actor, CreatedAt: s.now()}
	if expiresInDays > 0 {
		e := t.CreatedAt.AddDate(0, 0, expiresInDays)
		t.ExpiresAt = &e
	}
	if err := s.Store.Insert(ctx, org, t, hash); err != nil {
		return APIToken{}, "", err
	}
	return t, plain, nil
}

// Authenticate resolves a bearer token to its organization and metadata. Every failure is ErrUnauthorized:
// the caller learns nothing about why.
func (s *TokenService) Authenticate(ctx context.Context, raw string) (string, APIToken, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, TokenPrefix) || len(raw) < len(TokenPrefix)+20 || len(raw) > 200 {
		return "", APIToken{}, ErrUnauthorized
	}
	hash := HashToken(raw)
	org, t, stored, err := s.Store.Lookup(ctx, hash)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", APIToken{}, ErrUnauthorized
		}
		return "", APIToken{}, err
	}
	if subtle.ConstantTimeCompare([]byte(stored), []byte(hash)) != 1 || !t.Active(s.now()) {
		return "", APIToken{}, ErrUnauthorized
	}
	// last_used is informative: update at most once a minute per token.
	if t.LastUsedAt == nil || s.now().Sub(*t.LastUsedAt) > time.Minute {
		_ = s.Store.Touch(ctx, org, t.ID, s.now())
	}
	return org, t, nil
}
