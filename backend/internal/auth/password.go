package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// PasswordParams are the argon2id cost parameters.
type PasswordParams struct {
	Memory  uint32 // KiB
	Time    uint32
	Threads uint8
	SaltLen uint32
	KeyLen  uint32
}

// DefaultPasswordParams follow OWASP guidance for argon2id (>=19 MiB, t>=2).
var DefaultPasswordParams = PasswordParams{Memory: 64 * 1024, Time: 3, Threads: 2, SaltLen: 16, KeyLen: 32}

// Upper bounds accepted when parsing a stored hash, to stop a tampered row
// from forcing huge allocations.
const (
	maxParseMemory  = 512 * 1024
	maxParseTime    = 16
	maxParseThreads = 32
)

var errBadHash = errors.New("auth: malformed password hash")

// PasswordHasher hashes and verifies passwords using argon2id in PHC format:
// $argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>.
type PasswordHasher struct{ Params PasswordParams }

func (h PasswordHasher) params() PasswordParams {
	p := h.Params
	if p.Memory == 0 || p.Time == 0 || p.Threads == 0 || p.SaltLen < 8 || p.KeyLen < 16 {
		return DefaultPasswordParams
	}
	return p
}

// Hash returns the encoded hash of password with a fresh random salt.
func (h PasswordHasher) Hash(password string) (string, error) {
	p := h.params()
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Verify checks password against encoded in constant time. needsRehash is true
// when the stored parameters are weaker than the hasher's current ones.
func (h PasswordHasher) Verify(password, encoded string) (ok, needsRehash bool, err error) {
	p, salt, key, err := decodeHash(encoded)
	if err != nil {
		return false, false, err
	}
	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(key)))
	if subtle.ConstantTimeCompare(got, key) != 1 {
		return false, false, nil
	}
	cur := h.params()
	return true, p.Memory < cur.Memory || p.Time < cur.Time || p.Threads != cur.Threads || uint32(len(key)) < cur.KeyLen, nil
}

func decodeHash(encoded string) (PasswordParams, []byte, []byte, error) {
	var p PasswordParams
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, nil, nil, errBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return p, nil, nil, errBadHash
	}
	var m, t uint32
	var th uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &th); err != nil {
		return p, nil, nil, errBadHash
	}
	if m < 8 || m > maxParseMemory || t < 1 || t > maxParseTime || th < 1 || th > maxParseThreads {
		return p, nil, nil, errBadHash
	}
	b64 := base64.RawStdEncoding.Strict()
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return p, nil, nil, errBadHash
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > 128 {
		return p, nil, nil, errBadHash
	}
	return PasswordParams{Memory: m, Time: t, Threads: th, SaltLen: uint32(len(salt)), KeyLen: uint32(len(key))}, salt, key, nil
}
