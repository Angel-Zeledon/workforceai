package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	tokenTypeAccess = "access"
	maxTokenLen     = 4096
	minSecretLen    = 32
)

// Claims are the JWT claims of an access token. The role is informational:
// authorization uses the role stored in the database unless the service is
// configured with TrustTokenClaims.
type Claims struct {
	Issuer    string `json:"iss"`
	Subject   string `json:"sub"`
	Audience  string `json:"aud"`
	ExpiresAt int64  `json:"exp"`
	NotBefore int64  `json:"nbf"`
	IssuedAt  int64  `json:"iat"`
	ID        string `json:"jti"`
	OrgID     string `json:"org"`
	Role      Role   `json:"role"`
	Type      string `json:"typ"`
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// TokenSigner issues and verifies HS256 JWTs. Only HS256 is accepted on
// verification; "none" and any other algorithm are rejected.
type TokenSigner struct {
	secret   []byte
	issuer   string
	audience string
}

// NewTokenSigner builds a signer. The secret must be at least 32 bytes.
func NewTokenSigner(secret []byte, issuer, audience string) (*TokenSigner, error) {
	if len(secret) < minSecretLen {
		return nil, fmt.Errorf("auth: JWT secret must be at least %d bytes", minSecretLen)
	}
	return &TokenSigner{secret: append([]byte(nil), secret...), issuer: issuer, audience: audience}, nil
}

var b64url = base64.RawURLEncoding

func (s *TokenSigner) mac(signingInput string) []byte {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(signingInput))
	return m.Sum(nil)
}

// Sign serialises c (filling iss/aud/typ) into a compact JWT.
func (s *TokenSigner) Sign(c Claims) (string, error) {
	c.Issuer, c.Audience, c.Type = s.issuer, s.audience, tokenTypeAccess
	h, _ := json.Marshal(jwtHeader{Alg: "HS256", Typ: "JWT"})
	p, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	in := b64url.EncodeToString(h) + "." + b64url.EncodeToString(p)
	return in + "." + b64url.EncodeToString(s.mac(in)), nil
}

// Verify validates signature, algorithm, issuer, audience, type and the
// exp/nbf window against now.
func (s *TokenSigner) Verify(token string, now time.Time) (*Claims, error) {
	if token == "" || len(token) > maxTokenLen {
		return nil, ErrInvalidToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}
	strict := b64url.Strict()
	hb, err := strict.DecodeString(parts[0])
	if err != nil {
		return nil, ErrInvalidToken
	}
	var h jwtHeader
	if err := json.Unmarshal(hb, &h); err != nil || h.Alg != "HS256" {
		return nil, ErrInvalidToken
	}
	sig, err := strict.DecodeString(parts[2])
	if err != nil || !hmac.Equal(sig, s.mac(parts[0]+"."+parts[1])) {
		return nil, ErrInvalidToken
	}
	pb, err := strict.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalidToken
	}
	var c Claims
	if err := json.Unmarshal(pb, &c); err != nil {
		return nil, ErrInvalidToken
	}
	if c.Type != tokenTypeAccess || c.Issuer != s.issuer || c.Audience != s.audience ||
		c.Subject == "" || c.OrgID == "" || c.ExpiresAt == 0 {
		return nil, ErrInvalidToken
	}
	n := now.Unix()
	if n >= c.ExpiresAt {
		return nil, ErrTokenExpired
	}
	if c.NotBefore > n || c.IssuedAt > n+60 {
		return nil, ErrInvalidToken
	}
	return &c, nil
}

// newOpaqueToken returns a random 256-bit URL-safe token (used for refresh
// tokens) and its storage hash. Only the hash is persisted.
func newOpaqueToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	token = b64url.EncodeToString(b)
	return token, hashToken(token), nil
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("auth: crypto/rand unavailable")
	}
	return hex.EncodeToString(b)
}
