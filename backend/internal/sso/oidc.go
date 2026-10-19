// Package sso implements enterprise single sign-on for AI Workforce OS: generic OpenID Connect (authorization code flow
// with PKCE, state and nonce), verified e-mail domains (DNS TXT challenge) and the per-organization sign-in policy.
// Design and threat model: docs/architecture/identity.md. Standard library only.
package sso

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"
)

var b64 = base64.RawURLEncoding

// Errors of token validation. They are deliberately coarse on the wire (the callback maps them to one code).
var (
	ErrToken        = errors.New("sso: invalid id_token")
	ErrTokenExpired = errors.New("sso: id_token expired")
	ErrIssuer       = errors.New("sso: unexpected issuer")
	ErrAudience     = errors.New("sso: unexpected audience")
	ErrNonce        = errors.New("sso: nonce mismatch")
	ErrAlg          = errors.New("sso: unsupported signing algorithm")
	ErrSignature    = errors.New("sso: bad signature")
	ErrNoKey        = errors.New("sso: signing key not found")
)

// Claims is the part of an ID token this package uses.
type Claims struct {
	Issuer          string
	Subject         string
	Audience        []string
	AuthorizedParty string
	Expires         time.Time
	IssuedAt        time.Time
	NotBefore       time.Time
	Nonce           string
	Email           string
	EmailVerified   *bool // nil when the provider does not send the claim (Entra ID)
	Name            string
}

// ---- JWKS ----

// Key is one public signing key.
type Key struct {
	ID  string
	Alg string // "RS256" or "ES256"
	Pub crypto.PublicKey
}

// KeySet is a parsed JWKS.
type KeySet struct{ Keys []Key }

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// ParseJWKS reads a JWKS document, keeping only signature keys of the supported types (RSA, P-256).
func ParseJWKS(raw []byte) (KeySet, error) {
	var doc struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return KeySet{}, fmt.Errorf("sso: jwks: %w", err)
	}
	var ks KeySet
	for _, k := range doc.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		switch k.Kty {
		case "RSA":
			n, e := b64Int(k.N), b64Int(k.E)
			if n == nil || e == nil || !e.IsInt64() || e.Int64() < 3 || e.Int64() > 1<<31-1 || n.BitLen() < 2048 {
				continue // a short RSA key is not trusted
			}
			ks.Keys = append(ks.Keys, Key{ID: k.Kid, Alg: "RS256", Pub: &rsa.PublicKey{N: n, E: int(e.Int64())}})
		case "EC":
			if k.Crv != "P-256" {
				continue
			}
			x, y := b64Int(k.X), b64Int(k.Y)
			if x == nil || y == nil || !elliptic.P256().IsOnCurve(x, y) {
				continue
			}
			ks.Keys = append(ks.Keys, Key{ID: k.Kid, Alg: "ES256", Pub: &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}})
		}
	}
	if len(ks.Keys) == 0 {
		return KeySet{}, errors.New("sso: jwks has no usable signing key")
	}
	return ks, nil
}

func b64Int(s string) *big.Int {
	b, err := b64.DecodeString(s)
	if err != nil || len(b) == 0 {
		return nil
	}
	return new(big.Int).SetBytes(b)
}

func (ks KeySet) find(kid, alg string) (Key, error) {
	var cand []Key
	for _, k := range ks.Keys {
		if k.Alg == alg && (kid == "" || k.ID == kid) {
			cand = append(cand, k)
		}
	}
	if len(cand) == 1 {
		return cand[0], nil
	}
	return Key{}, ErrNoKey
}

// ---- ID token validation ----

// VerifyParams is what the relying party expects.
type VerifyParams struct {
	Issuer   string
	ClientID string
	Nonce    string
	Now      time.Time
	Skew     time.Duration // default 2 minutes
}

const defaultSkew = 2 * time.Minute

// VerifyIDToken validates a compact JWS ID token against OIDC Core 3.1.3.7: the algorithm is taken from an allow-list
// (never "none", never HMAC: the client secret must not be usable as a verification key), the signature must verify with a
// key from the issuer's JWKS, and iss, aud, azp, exp, iat, nbf and nonce must all match what this login expects.
func VerifyIDToken(raw string, ks KeySet, p VerifyParams) (Claims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 || len(raw) > 16<<10 {
		return Claims{}, ErrToken
	}
	hb, e1 := b64.DecodeString(parts[0])
	pb, e2 := b64.DecodeString(parts[1])
	sb, e3 := b64.DecodeString(parts[2])
	if e1 != nil || e2 != nil || e3 != nil {
		return Claims{}, ErrToken
	}
	var hdr struct {
		Alg  string   `json:"alg"`
		Kid  string   `json:"kid"`
		Typ  string   `json:"typ"`
		Crit []string `json:"crit"`
	}
	if json.Unmarshal(hb, &hdr) != nil || len(hdr.Crit) > 0 {
		return Claims{}, ErrToken
	}
	if hdr.Alg != "RS256" && hdr.Alg != "ES256" {
		return Claims{}, ErrAlg
	}
	key, err := ks.find(hdr.Kid, hdr.Alg)
	if err != nil {
		return Claims{}, err
	}
	signed := []byte(parts[0] + "." + parts[1])
	digest := sha256.Sum256(signed)
	switch pub := key.Pub.(type) {
	case *rsa.PublicKey:
		if rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sb) != nil {
			return Claims{}, ErrSignature
		}
	case *ecdsa.PublicKey:
		if len(sb) != 64 || !ecdsa.Verify(pub, digest[:], new(big.Int).SetBytes(sb[:32]), new(big.Int).SetBytes(sb[32:])) {
			return Claims{}, ErrSignature
		}
	default:
		return Claims{}, ErrAlg
	}

	var c struct {
		Iss      string          `json:"iss"`
		Sub      string          `json:"sub"`
		Aud      json.RawMessage `json:"aud"`
		Azp      string          `json:"azp"`
		Exp      *float64        `json:"exp"`
		Iat      *float64        `json:"iat"`
		Nbf      *float64        `json:"nbf"`
		Nonce    string          `json:"nonce"`
		Email    string          `json:"email"`
		Verified json.RawMessage `json:"email_verified"`
		Name     string          `json:"name"`
	}
	if json.Unmarshal(pb, &c) != nil {
		return Claims{}, ErrToken
	}
	skew := p.Skew
	if skew <= 0 {
		skew = defaultSkew
	}
	if c.Iss == "" || c.Iss != p.Issuer {
		return Claims{}, ErrIssuer
	}
	aud := audList(c.Aud)
	if !contains(aud, p.ClientID) {
		return Claims{}, ErrAudience
	}
	if (len(aud) > 1 || c.Azp != "") && c.Azp != p.ClientID {
		return Claims{}, ErrAudience
	}
	if c.Exp == nil || c.Sub == "" {
		return Claims{}, ErrToken
	}
	exp := time.Unix(int64(*c.Exp), 0)
	if !p.Now.Before(exp.Add(skew)) {
		return Claims{}, ErrTokenExpired
	}
	out := Claims{Issuer: c.Iss, Subject: c.Sub, Audience: aud, AuthorizedParty: c.Azp, Expires: exp,
		Nonce: c.Nonce, Email: strings.TrimSpace(c.Email), Name: strings.TrimSpace(c.Name)}
	if c.Iat != nil {
		out.IssuedAt = time.Unix(int64(*c.Iat), 0)
		if out.IssuedAt.After(p.Now.Add(skew)) {
			return Claims{}, ErrToken // issued in the future
		}
	}
	if c.Nbf != nil {
		out.NotBefore = time.Unix(int64(*c.Nbf), 0)
		if out.NotBefore.After(p.Now.Add(skew)) {
			return Claims{}, ErrToken
		}
	}
	if p.Nonce == "" || c.Nonce == "" || !constEq(c.Nonce, p.Nonce) {
		return Claims{}, ErrNonce
	}
	out.EmailVerified = boolClaim(c.Verified)
	return out, nil
}

func audList(raw json.RawMessage) []string {
	var one string
	if json.Unmarshal(raw, &one) == nil && one != "" {
		return []string{one}
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return many
	}
	return nil
}

// boolClaim reads email_verified, which some providers send as the string "true".
func boolClaim(raw json.RawMessage) *bool {
	if len(raw) == 0 {
		return nil
	}
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return &b
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		v := strings.EqualFold(s, "true")
		return &v
	}
	f := false
	return &f
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

func constEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var d byte
	for i := 0; i < len(a); i++ {
		d |= a[i] ^ b[i]
	}
	return d == 0
}

// ---- discovery and the provider HTTP client ----

// Discovery is the subset of the provider metadata document that the flow needs.
type Discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// ValidIssuerURL checks an issuer typed by an administrator: https, a host, no credentials, query or fragment.
func ValidIssuerURL(s string, allowHTTP bool) error {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("sso: the issuer must be a plain URL")
	}
	if u.Scheme != "https" && !(allowHTTP && u.Scheme == "http") {
		return errors.New("sso: the issuer must use https")
	}
	return nil
}

// NewSafeClient returns an HTTP client for talking to identity providers on behalf of a tenant: it never connects to
// loopback, private, link-local or otherwise internal addresses (an admin-typed issuer URL must not become an SSRF
// primitive), follows no redirects, and limits time. The check runs on the resolved address at dial time, so a DNS
// answer that changes between validation and connection cannot bypass it.
func NewSafeClient() *http.Client {
	d := &net.Dialer{Timeout: 5 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil || blockedIP(ip) {
			return errors.New("sso: address not allowed")
		}
		return nil
	}}
	return &http.Client{
		Timeout:       10 * time.Second,
		Transport:     &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 5 * time.Second, DisableKeepAlives: true, Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() ||
		(ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1]&0xc0 == 64) // 100.64.0.0/10 carrier-grade NAT
}

const maxProviderBody = 1 << 20

func getJSON(ctx context.Context, hc *http.Client, rawURL string, dst any) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxProviderBody))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sso: %s answered %d", req.URL.Host, res.StatusCode)
	}
	if dst != nil {
		if err := json.Unmarshal(body, dst); err != nil {
			return nil, err
		}
	}
	return body, nil
}

// Discover reads {issuer}/.well-known/openid-configuration and checks that the document names the issuer it was fetched
// for (OIDC Discovery 4.3: a mismatch means the document is not authoritative) and that every endpoint is https.
func Discover(ctx context.Context, hc *http.Client, issuer string, allowHTTP bool) (Discovery, error) {
	if err := ValidIssuerURL(issuer, allowHTTP); err != nil {
		return Discovery{}, err
	}
	var d Discovery
	if _, err := getJSON(ctx, hc, strings.TrimRight(issuer, "/")+"/.well-known/openid-configuration", &d); err != nil {
		return Discovery{}, err
	}
	if d.Issuer != issuer {
		return Discovery{}, ErrIssuer
	}
	for _, e := range []string{d.AuthorizationEndpoint, d.TokenEndpoint, d.JWKSURI} {
		if ValidIssuerURL(strings.SplitN(e, "?", 2)[0], allowHTTP) != nil {
			return Discovery{}, errors.New("sso: provider metadata has an invalid endpoint")
		}
	}
	return d, nil
}

// jwksCache keeps provider keys for an hour and re-fetches at most once a minute when a token names an unknown key
// (key rotation) so a forged kid cannot make the server hammer the provider.
type jwksCache struct {
	mu  sync.Mutex
	m   map[string]jwksEntry
	now func() time.Time
}

type jwksEntry struct {
	ks      KeySet
	fetched time.Time
}

func newJWKSCache(now func() time.Time) *jwksCache {
	return &jwksCache{m: map[string]jwksEntry{}, now: now}
}

func (c *jwksCache) get(ctx context.Context, hc *http.Client, uri string, force bool) (KeySet, error) {
	c.mu.Lock()
	e, ok := c.m[uri]
	c.mu.Unlock()
	age := c.now().Sub(e.fetched)
	if ok && age < time.Hour && (!force || age < time.Minute) {
		return e.ks, nil
	}
	raw, err := getJSON(ctx, hc, uri, nil)
	if err != nil {
		if ok {
			return e.ks, nil // keep serving the last good set while the provider is down
		}
		return KeySet{}, err
	}
	ks, err := ParseJWKS(raw)
	if err != nil {
		return KeySet{}, err
	}
	c.mu.Lock()
	c.m[uri] = jwksEntry{ks: ks, fetched: c.now()}
	c.mu.Unlock()
	return ks, nil
}
