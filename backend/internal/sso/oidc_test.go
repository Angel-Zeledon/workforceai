package sso

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math/big"
	"testing"
	"time"
)

var t0 = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

type signer struct {
	rsa *rsa.PrivateKey
	ec  *ecdsa.PrivateKey
}

func newSigner(t testing.TB) *signer {
	t.Helper()
	r, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	e, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &signer{rsa: r, ec: e}
}

func (s *signer) jwks() []byte {
	pad := func(b []byte) []byte { return append(make([]byte, 32-len(b)), b...) }
	doc := map[string]any{"keys": []map[string]string{
		{"kty": "RSA", "kid": "rsa1", "use": "sig", "alg": "RS256", "n": b64.EncodeToString(s.rsa.N.Bytes()), "e": b64.EncodeToString(big.NewInt(int64(s.rsa.E)).Bytes())},
		{"kty": "EC", "kid": "ec1", "use": "sig", "alg": "ES256", "crv": "P-256", "x": b64.EncodeToString(pad(s.ec.X.Bytes())), "y": b64.EncodeToString(pad(s.ec.Y.Bytes()))},
	}}
	b, _ := json.Marshal(doc)
	return b
}

// sign builds a compact JWS. alg/kid select the key; claims is the payload.
func (s *signer) sign(t testing.TB, alg, kid string, claims map[string]any) string {
	t.Helper()
	hb, _ := json.Marshal(map[string]string{"alg": alg, "kid": kid, "typ": "JWT"})
	pb, _ := json.Marshal(claims)
	in := b64.EncodeToString(hb) + "." + b64.EncodeToString(pb)
	d := sha256.Sum256([]byte(in))
	var sig []byte
	switch alg {
	case "RS256":
		sig, _ = rsa.SignPKCS1v15(rand.Reader, s.rsa, crypto.SHA256, d[:])
	case "ES256":
		r, ss, _ := ecdsa.Sign(rand.Reader, s.ec, d[:])
		sig = append(append(make([]byte, 32-len(r.Bytes())), r.Bytes()...), append(make([]byte, 32-len(ss.Bytes())), ss.Bytes()...)...)
	case "HS256": // key confusion attempt: HMAC with the public modulus as the secret
		m := hmac.New(sha256.New, s.rsa.N.Bytes())
		m.Write([]byte(in))
		sig = m.Sum(nil)
	case "none":
		return in + "."
	}
	return in + "." + b64.EncodeToString(sig)
}

func goodClaims() map[string]any {
	return map[string]any{"iss": "https://idp.example.com", "sub": "u-1", "aud": "client-1", "exp": t0.Add(time.Hour).Unix(),
		"iat": t0.Unix(), "nonce": "n-1", "email": "ana@acme.io", "email_verified": true, "name": "Ana"}
}

func params() VerifyParams {
	return VerifyParams{Issuer: "https://idp.example.com", ClientID: "client-1", Nonce: "n-1", Now: t0}
}

func keys(t testing.TB, s *signer) KeySet {
	ks, err := ParseJWKS(s.jwks())
	if err != nil {
		t.Fatal(err)
	}
	return ks
}

func mut(c map[string]any, kv ...any) map[string]any {
	out := map[string]any{}
	for k, v := range c {
		out[k] = v
	}
	for i := 0; i < len(kv); i += 2 {
		if kv[i+1] == nil {
			delete(out, kv[i].(string))
		} else {
			out[kv[i].(string)] = kv[i+1]
		}
	}
	return out
}

func TestVerifyIDTokenAccepts(t *testing.T) {
	s := newSigner(t)
	ks := keys(t, s)
	for _, tc := range []struct{ alg, kid string }{{"RS256", "rsa1"}, {"ES256", "ec1"}} {
		c, err := VerifyIDToken(s.sign(t, tc.alg, tc.kid, goodClaims()), ks, params())
		if err != nil || c.Email != "ana@acme.io" || c.Subject != "u-1" || c.EmailVerified == nil || !*c.EmailVerified {
			t.Fatalf("%s: %+v %v", tc.alg, c, err)
		}
	}
	// aud as an array that includes the client, with azp equal to the client.
	c := mut(goodClaims(), "aud", []string{"other", "client-1"}, "azp", "client-1")
	if _, err := VerifyIDToken(s.sign(t, "RS256", "rsa1", c), ks, params()); err != nil {
		t.Fatalf("multi-audience = %v", err)
	}
	// email_verified as a string (some providers) and absent (Entra ID).
	c = mut(goodClaims(), "email_verified", "true")
	if cl, err := VerifyIDToken(s.sign(t, "RS256", "rsa1", c), ks, params()); err != nil || cl.EmailVerified == nil || !*cl.EmailVerified {
		t.Fatalf("string email_verified: %+v %v", cl, err)
	}
	c = mut(goodClaims(), "email_verified", nil)
	if cl, err := VerifyIDToken(s.sign(t, "RS256", "rsa1", c), ks, params()); err != nil || cl.EmailVerified != nil {
		t.Fatalf("absent email_verified: %+v %v", cl, err)
	}
}

func TestVerifyIDTokenRejects(t *testing.T) {
	s := newSigner(t)
	other := newSigner(t)
	ks := keys(t, s)
	good := goodClaims()
	cases := []struct {
		name string
		tok  string
		p    VerifyParams
		want error
	}{
		{"alg none", s.sign(t, "none", "rsa1", good), params(), ErrAlg},
		{"hmac key confusion", s.sign(t, "HS256", "rsa1", good), params(), ErrAlg},
		{"signed by another key", other.sign(t, "RS256", "rsa1", good), params(), ErrSignature},
		{"unknown kid", s.sign(t, "RS256", "nope", good), params(), ErrNoKey},
		{"wrong issuer", s.sign(t, "RS256", "rsa1", mut(good, "iss", "https://evil.example.com")), params(), ErrIssuer},
		{"missing issuer", s.sign(t, "RS256", "rsa1", mut(good, "iss", nil)), params(), ErrIssuer},
		{"wrong audience", s.sign(t, "RS256", "rsa1", mut(good, "aud", "client-2")), params(), ErrAudience},
		{"azp mismatch", s.sign(t, "RS256", "rsa1", mut(good, "aud", []string{"client-1", "x"}, "azp", "x")), params(), ErrAudience},
		{"multi aud without azp", s.sign(t, "RS256", "rsa1", mut(good, "aud", []string{"client-1", "x"})), params(), ErrAudience},
		{"expired", s.sign(t, "RS256", "rsa1", mut(good, "exp", t0.Add(-10*time.Minute).Unix())), params(), ErrTokenExpired},
		{"no exp", s.sign(t, "RS256", "rsa1", mut(good, "exp", nil)), params(), ErrToken},
		{"no sub", s.sign(t, "RS256", "rsa1", mut(good, "sub", nil)), params(), ErrToken},
		{"issued in the future", s.sign(t, "RS256", "rsa1", mut(good, "iat", t0.Add(time.Hour).Unix())), params(), ErrToken},
		{"not yet valid", s.sign(t, "RS256", "rsa1", mut(good, "nbf", t0.Add(time.Hour).Unix())), params(), ErrToken},
		{"nonce mismatch", s.sign(t, "RS256", "rsa1", mut(good, "nonce", "other")), params(), ErrNonce},
		{"nonce missing in token", s.sign(t, "RS256", "rsa1", mut(good, "nonce", nil)), params(), ErrNonce},
		{"no nonce expected", s.sign(t, "RS256", "rsa1", good), VerifyParams{Issuer: params().Issuer, ClientID: "client-1", Now: t0}, ErrNonce},
		{"not a jwt", "a.b", params(), ErrToken},
		{"garbage", "!!!.???.***", params(), ErrToken},
	}
	for _, tc := range cases {
		if _, err := VerifyIDToken(tc.tok, ks, tc.p); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
	// A tampered payload must fail the signature check.
	tok := s.sign(t, "RS256", "rsa1", good)
	parts := splitTok(tok)
	forged := parts[0] + "." + b64.EncodeToString([]byte(`{"iss":"https://idp.example.com","sub":"admin","aud":"client-1","exp":9999999999,"nonce":"n-1","email":"ceo@acme.test"}`)) + "." + parts[2]
	if _, err := VerifyIDToken(forged, ks, params()); !errors.Is(err, ErrSignature) {
		t.Errorf("tampered payload = %v", err)
	}
}

func splitTok(s string) [3]string {
	var out [3]string
	i := 0
	for _, r := range s {
		if r == '.' {
			i++
			continue
		}
		out[i] += string(r)
	}
	return out
}

func TestVerifyIDTokenClockSkew(t *testing.T) {
	s := newSigner(t)
	ks := keys(t, s)
	// Expired 90 s ago: inside the 2-minute skew, accepted. Expired 3 minutes ago: refused.
	if _, err := VerifyIDToken(s.sign(t, "RS256", "rsa1", mut(goodClaims(), "exp", t0.Add(-90*time.Second).Unix())), ks, params()); err != nil {
		t.Fatalf("inside skew = %v", err)
	}
	if _, err := VerifyIDToken(s.sign(t, "RS256", "rsa1", mut(goodClaims(), "exp", t0.Add(-3*time.Minute).Unix())), ks, params()); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("outside skew = %v", err)
	}
	// iat 90 s in the future (provider clock ahead) is tolerated.
	if _, err := VerifyIDToken(s.sign(t, "RS256", "rsa1", mut(goodClaims(), "iat", t0.Add(90*time.Second).Unix())), ks, params()); err != nil {
		t.Fatalf("iat inside skew = %v", err)
	}
}

func TestParseJWKSDropsWeakAndUnsupportedKeys(t *testing.T) {
	weak, _ := rsa.GenerateKey(rand.Reader, 1024)
	doc, _ := json.Marshal(map[string]any{"keys": []map[string]string{
		{"kty": "RSA", "kid": "weak", "n": b64.EncodeToString(weak.N.Bytes()), "e": "AQAB"},
		{"kty": "oct", "kid": "sym", "k": "c2VjcmV0"},
		{"kty": "EC", "kid": "p384", "crv": "P-384", "x": "AA", "y": "AA"},
		{"kty": "EC", "kid": "off-curve", "crv": "P-256", "x": "AQ", "y": "AQ"},
		{"kty": "RSA", "kid": "enc", "use": "enc", "n": "AQ", "e": "AQAB"},
	}})
	if _, err := ParseJWKS(doc); err == nil {
		t.Fatal("a JWKS with no acceptable key must be an error")
	}
}

func TestSafeClientRefusesInternalAddresses(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1:1/", "http://[::1]:1/", "http://10.0.0.5/", "http://192.168.1.1/", "http://169.254.169.254/latest/meta-data/", "http://100.64.0.1/"} {
		if _, err := NewSafeClient().Get(u); err == nil {
			t.Errorf("%s must be refused", u)
		}
	}
}

func TestValidIssuerURL(t *testing.T) {
	for _, bad := range []string{"", "http://idp.example.com", "https://user:pw@idp.example.com", "https://idp.example.com/?x=1", "https://idp.example.com/#f", "ftp://x", "idp.example.com"} {
		if ValidIssuerURL(bad, false) == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	if ValidIssuerURL("https://login.microsoftonline.com/tenant/v2.0", false) != nil || ValidIssuerURL("http://localhost:9", true) != nil {
		t.Error("valid issuers refused")
	}
}
