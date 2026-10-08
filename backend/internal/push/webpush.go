// Package push sends Web Push notifications (RFC 8030) to the browsers of the
// people who can decide approvals. Payloads are encrypted end to end with
// aes128gcm (RFC 8291) and requests are signed with VAPID (RFC 8292); both are
// implemented here with the standard library and x/crypto/hkdf, so there is no
// third-party push dependency.
package push

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"time"

	"golang.org/x/crypto/hkdf"
)

var b64 = base64.RawURLEncoding

// decodeB64 accepts base64url with or without padding (browsers and tools differ).
func decodeB64(s string) ([]byte, error) {
	for len(s)%4 != 0 && len(s) > 0 && s[len(s)-1] == '=' {
		s = s[:len(s)-1]
	}
	if b, err := b64.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}

// VAPID is the application server key pair (P-256).
type VAPID struct {
	priv *ecdsa.PrivateKey
	// Public is the uncompressed public key, base64url (what browsers need as
	// applicationServerKey).
	Public string
}

// ParseVAPID validates a key pair given as base64url (private: 32-byte scalar,
// public: 65-byte uncompressed point). The public key must match the private one.
func ParseVAPID(public, private string) (*VAPID, error) {
	d, err := decodeB64(private)
	if err != nil || len(d) != 32 {
		return nil, errors.New("VAPID_PRIVATE_KEY must be base64url of 32 bytes")
	}
	ek, err := ecdh.P256().NewPrivateKey(d)
	if err != nil {
		return nil, fmt.Errorf("VAPID_PRIVATE_KEY: %w", err)
	}
	pub := ek.PublicKey().Bytes()
	if public != "" {
		p, err := decodeB64(public)
		if err != nil || string(p) != string(pub) {
			return nil, errors.New("VAPID_PUBLIC_KEY does not match VAPID_PRIVATE_KEY")
		}
	}
	x, y := elliptic.Unmarshal(elliptic.P256(), pub) //nolint:staticcheck // only to build the ecdsa key
	k := &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, D: new(big.Int).SetBytes(d)}
	return &VAPID{priv: k, Public: b64.EncodeToString(pub)}, nil
}

// GenerateVAPID creates a new key pair (for `server ctl vapid-keys`).
func GenerateVAPID() (public, private string, err error) {
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return b64.EncodeToString(k.PublicKey().Bytes()), b64.EncodeToString(k.Bytes()), nil
}

// authHeader builds "vapid t=<jwt>, k=<public key>" for an endpoint.
func (v *VAPID) authHeader(endpoint, subject string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	head := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, _ := json.Marshal(map[string]any{"aud": u.Scheme + "://" + u.Host, "exp": now.Add(12 * time.Hour).Unix(), "sub": subject})
	signing := head + "." + b64.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, v.priv, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return "vapid t=" + signing + "." + b64.EncodeToString(sig) + ", k=" + v.Public, nil
}

const recordSize = 4096

// encrypt seals plaintext for a user agent (RFC 8291) with a fresh ephemeral
// key and salt.
func encrypt(plaintext []byte, uaPublic, authSecret []byte) ([]byte, error) {
	as, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	return encryptWith(plaintext, uaPublic, authSecret, as, salt)
}

// encryptWith is encrypt with the ephemeral key and salt given (test vectors).
func encryptWith(plaintext, uaPublic, authSecret []byte, as *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	if len(authSecret) != 16 {
		return nil, errors.New("auth secret must be 16 bytes")
	}
	ua, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("invalid p256dh key: %w", err)
	}
	if len(plaintext)+1+16 > recordSize {
		return nil, errors.New("payload too large for a single record")
	}
	secret, err := as.ECDH(ua)
	if err != nil {
		return nil, err
	}
	asPub := as.PublicKey().Bytes()
	keyInfo := append(append([]byte("WebPush: info\x00"), uaPublic...), asPub...)
	ikm := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, secret, authSecret, keyInfo), ikm); err != nil {
		return nil, err
	}
	prk := hkdf.Extract(sha256.New, ikm, salt)
	cek := make([]byte, 16)
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(hkdf.Expand(sha256.New, prk, []byte("Content-Encoding: aes128gcm\x00")), cek); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(hkdf.Expand(sha256.New, prk, []byte("Content-Encoding: nonce\x00")), nonce); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// Header: salt | record size | key id length | key id (the ephemeral public key).
	out := make([]byte, 0, 16+4+1+len(asPub)+len(plaintext)+17)
	out = append(out, salt...)
	out = binary.BigEndian.AppendUint32(out, recordSize)
	out = append(out, byte(len(asPub)))
	out = append(out, asPub...)
	// Single (last) record: plaintext followed by the 0x02 delimiter.
	return gcm.Seal(out, nonce, append(append([]byte{}, plaintext...), 0x02), nil), nil
}
