// Package objstore is the object storage port of the drive (and of the PDF
// blobs when an object store is configured). Objects are immutable blobs
// addressed by an opaque key; Postgres keeps only metadata and versions.
//
// Implementations: Memory (tests and the no-database mode), S3 (any
// S3-compatible service: MinIO in dev, AWS S3 / Cloudflare R2 in production)
// and postgres.ObjectStore (bytea fallback when no S3 is configured, so the
// docker-compose stack of old keeps working without extra services).
package objstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"regexp"
	"strings"
	"time"
)

// ErrNotFound is returned by Get, Head and (for stores that can tell) Delete.
var ErrNotFound = errors.New("objstore: object not found")

// ErrTooLarge is returned by Put when the body is larger than the declared size
// or than the store's cap.
var ErrTooLarge = errors.New("objstore: object too large")

// ErrBadKey is returned for keys that are not safe object keys.
var ErrBadKey = errors.New("objstore: invalid key")

// Info describes a stored object.
type Info struct {
	Key         string
	Size        int64
	SHA256      string // hex; "" when the store did not record it
	ContentType string
	ModifiedAt  time.Time
}

// PutOptions are optional hints of Put.
type PutOptions struct {
	ContentType string
	// MaxBytes (> 0) caps the body even when the size is unknown (-1).
	MaxBytes int64
	// SHA256 (hex, optional) is the digest the caller already computed. A store
	// that can (S3) asks the server to verify the body against it.
	SHA256 string
}

// Store is the port. Implementations must be safe for concurrent use.
//
// Put streams r to the store and returns the SHA-256 (hex) of what was written.
// size is the exact length when known and -1 otherwise (the store may then
// buffer, bounded by PutOptions.MaxBytes). A body that does not match a known
// size is an error and nothing is stored.
type Store interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, o PutOptions) (Info, error)
	// Get returns a streaming reader; the caller closes it.
	Get(ctx context.Context, key string) (io.ReadCloser, Info, error)
	Head(ctx context.Context, key string) (Info, error)
	// Delete removes an object. Deleting a missing key is not an error.
	Delete(ctx context.Context, key string) error
}

var keyRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._=@/-]{0,300}$`)

// ValidKey reports whether key is a safe object key: no leading slash, no
// "..", no empty or dot segments, a small ASCII alphabet. Keys are built by the
// application (never from user text), this is defense in depth.
func ValidKey(key string) error {
	if !keyRe.MatchString(key) || strings.Contains(key, "//") || strings.HasSuffix(key, "/") {
		return fmt.Errorf("%w: %q", ErrBadKey, key)
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "." || seg == ".." || seg == "" {
			return fmt.Errorf("%w: %q", ErrBadKey, key)
		}
	}
	return nil
}

// OrgKey builds the key "orgs/<org>/<rest...>". The org id is part of the key
// so a bucket can be inspected, backed up or lifecycled per organization.
func OrgKey(org string, rest ...string) string {
	return "orgs/" + org + "/" + strings.Join(rest, "/")
}

// OrgOf extracts <org> from an OrgKey ("" when the key has another shape).
func OrgOf(key string) string {
	p := strings.SplitN(key, "/", 3)
	if len(p) < 3 || p[0] != "orgs" {
		return ""
	}
	return p[1]
}

// HashingReader hashes everything that is read through it and counts bytes.
type HashingReader struct {
	R io.Reader
	h hash.Hash
	N int64
}

// NewHashingReader wraps r.
func NewHashingReader(r io.Reader) *HashingReader { return &HashingReader{R: r, h: sha256.New()} }

func (h *HashingReader) Read(p []byte) (int, error) {
	n, err := h.R.Read(p)
	if n > 0 {
		h.h.Write(p[:n])
		h.N += int64(n)
	}
	return n, err
}

// Sum is the hex SHA-256 of what has been read so far.
func (h *HashingReader) Sum() string { return hex.EncodeToString(h.h.Sum(nil)) }

// SumBytes is the hex SHA-256 of b.
func SumBytes(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// ReadAllBounded reads r fully, failing with ErrTooLarge past max bytes.
func ReadAllBounded(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, ErrTooLarge
	}
	return b, nil
}
