package vault

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ErrNoKEK is returned when no key-encryption key is configured: the vault
// fails closed and refuses to store or read live credentials.
var ErrNoKEK = errors.New("vault: no key encryption key configured (set CONNECTIONS_KEK)")

// KeyWrapper wraps data keys with a key that lives OUTSIDE the database
// (env in development, KMS/age in production).
type KeyWrapper interface {
	Wrap(ctx context.Context, dek []byte) (wrapped []byte, kekID string, err error)
	Unwrap(ctx context.Context, kekID string, wrapped []byte) ([]byte, error)
}

// EnvKeyWrapper wraps with an AES-256-GCM key taken from the environment.
// Several keys may be loaded at once so the KEK can be rotated: new data keys
// use the first one, any loaded key can unwrap.
type EnvKeyWrapper struct {
	keys map[string][]byte
	cur  string
}

// NewEnvKeyWrapper parses "base64(32 bytes)" values; ids are "env:" + a key fingerprint,
// (first value is the current one).
func NewEnvKeyWrapper(b64 ...string) (*EnvKeyWrapper, error) {
	w := &EnvKeyWrapper{keys: map[string][]byte{}}
	for _, v := range b64 {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		k, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("vault: KEK is not valid base64: %w", err)
		}
		if len(k) != 32 {
			return nil, fmt.Errorf("vault: KEK must be 32 bytes, got %d", len(k))
		}
		sum := sha256.Sum256(k)
		id := "env:" + hex.EncodeToString(sum[:4])
		w.keys[id] = k
		if w.cur == "" {
			w.cur = id
		}
	}
	if w.cur == "" {
		return nil, ErrNoKEK
	}
	return w, nil
}

func (w *EnvKeyWrapper) Wrap(_ context.Context, dek []byte) ([]byte, string, error) {
	out, err := seal(w.keys[w.cur], dek, []byte(w.cur))
	return out, w.cur, err
}

func (w *EnvKeyWrapper) Unwrap(_ context.Context, kekID string, wrapped []byte) ([]byte, error) {
	k, ok := w.keys[kekID]
	if !ok {
		return nil, fmt.Errorf("vault: unknown KEK %q", kekID)
	}
	return open(k, wrapped, []byte(kekID))
}

// seal returns nonce||ciphertext.
func seal(key, plain, aad []byte) ([]byte, error) {
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, err
	}
	n := make([]byte, g.NonceSize())
	if _, err := rand.Read(n); err != nil {
		return nil, err
	}
	return g.Seal(n, n, plain, aad), nil
}

func open(key, blob, aad []byte) ([]byte, error) {
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, err
	}
	if len(blob) < g.NonceSize() {
		return nil, errors.New("vault: ciphertext too short")
	}
	return g.Open(nil, blob[:g.NonceSize()], blob[g.NonceSize():], aad)
}
