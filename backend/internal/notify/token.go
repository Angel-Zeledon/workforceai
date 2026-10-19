// Package notify tells people that an approval is waiting (e-mail, Slack,
// Microsoft Teams) and lets them decide from outside the app with a signed,
// single-use, expiring link. It also holds the plan state (free plan with a
// hard spend cap). docs/architecture/approvals-notify.md has the full design.
//
// Rules that never change:
//   - a link is a bearer token for ONE approval and ONE decision; it expires
//     (default 24 h) and works once (a replay ledger, shared by replicas);
//   - opening a link (GET) changes nothing: mail scanners and chat previews
//     prefetch links. Only the confirmation (POST) decides;
//   - high-risk approvals, double approvals and role-gated approvals never
//     accept a link: they need the signed-in session;
//   - the token carries no secret and no content of the work.
package notify

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

// Decisions a link can carry.
const (
	DecisionApprove = "approve"
	DecisionReject  = "reject"
)

// Errors of Verify / Consume. They are distinct so the landing page can say
// "expired" or "already used" instead of a generic failure.
var (
	ErrMalformed    = errors.New("notify: malformed token")
	ErrBadSignature = errors.New("notify: bad signature")
	ErrExpired      = errors.New("notify: token expired")
	ErrUsed         = errors.New("notify: token already used")
)

// Claims is the signed payload.
type Claims struct {
	Org      string `json:"o"`
	Approval string `json:"a"`
	Decision string `json:"d"`
	// To names who the link was sent to (an e-mail or "slack"/"teams"); it is
	// recorded as the actor of the decision ("link:<to>").
	To  string `json:"t,omitempty"`
	Exp int64  `json:"e"` // unix seconds
	JTI string `json:"j"` // random id: the single-use key
}

// Signer signs and verifies tokens with HMAC-SHA256.
type Signer struct {
	key []byte
	now func() time.Time
}

// MinKeyLen is the shortest accepted key.
const MinKeyLen = 32

// NewSigner returns a Signer. A key shorter than MinKeyLen is refused so that a
// forgotten default can never sign links.
func NewSigner(key []byte) (*Signer, error) {
	if len(key) < MinKeyLen {
		return nil, errors.New("notify: signing key must be at least 32 bytes")
	}
	return &Signer{key: append([]byte(nil), key...), now: time.Now}, nil
}

// WithClock returns a copy that reads time from now (tests).
func (s *Signer) WithClock(now func() time.Time) *Signer {
	c := *s
	c.now = now
	return &c
}

func (s *Signer) mac(payload string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(payload))
	return m.Sum(nil)
}

// NewJTI returns a random token id.
func NewJTI() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Sign returns "<payload>.<signature>" (both base64url). Exp and JTI are filled
// in when empty: ttl is the validity from now.
func (s *Signer) Sign(c Claims, ttl time.Duration) (string, error) {
	if c.Org == "" || c.Approval == "" || (c.Decision != DecisionApprove && c.Decision != DecisionReject) {
		return "", ErrMalformed
	}
	if c.Exp == 0 {
		if ttl <= 0 {
			return "", ErrMalformed
		}
		c.Exp = s.now().Add(ttl).Unix()
	}
	if c.JTI == "" {
		c.JTI = NewJTI()
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	p := base64.RawURLEncoding.EncodeToString(raw)
	return p + "." + base64.RawURLEncoding.EncodeToString(s.mac(p)), nil
}

// Verify checks the signature first (constant time) and then the expiry.
func (s *Signer) Verify(tok string) (Claims, error) {
	var c Claims
	p, sig, ok := strings.Cut(strings.TrimSpace(tok), ".")
	if !ok || p == "" || sig == "" || len(tok) > 2048 {
		return c, ErrMalformed
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return c, ErrMalformed
	}
	if !hmac.Equal(got, s.mac(p)) {
		return c, ErrBadSignature
	}
	raw, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil || json.Unmarshal(raw, &c) != nil {
		return Claims{}, ErrMalformed
	}
	if c.Org == "" || c.Approval == "" || c.JTI == "" || (c.Decision != DecisionApprove && c.Decision != DecisionReject) {
		return Claims{}, ErrMalformed
	}
	if !s.now().Before(time.Unix(c.Exp, 0)) {
		return c, ErrExpired
	}
	return c, nil
}

// ReplayStore remembers which token ids were used. Consume is atomic: exactly
// one caller gets nil for a given (org, jti); every later one gets ErrUsed.
type ReplayStore interface {
	Consume(ctx context.Context, org, jti string, expires time.Time) error
}

// MemReplay is the in-memory ReplayStore (no database, tests).
type MemReplay struct {
	mu   sync.Mutex
	seen map[string]time.Time
	now  func() time.Time
}

// NewMemReplay returns an empty store.
func NewMemReplay() *MemReplay { return &MemReplay{seen: map[string]time.Time{}, now: time.Now} }

// Consume implements ReplayStore.
func (m *MemReplay) Consume(_ context.Context, org, jti string, expires time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for k, e := range m.seen { // expired ids can never verify again: forget them
		if now.After(e) {
			delete(m.seen, k)
		}
	}
	k := org + "|" + jti
	if _, used := m.seen[k]; used {
		return ErrUsed
	}
	m.seen[k] = expires
	return nil
}
