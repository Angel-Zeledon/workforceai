package vault

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Credential kinds.
const (
	KindOAuthRefresh = "oauth_refresh"
	KindAPIKey       = "api_key"
	KindBasic        = "basic"
)

// Credential statuses.
const (
	StatusActive    = "active"
	StatusRetired   = "retired"
	StatusDestroyed = "destroyed"
)

var (
	// ErrNotFound: no active credential for the connection.
	ErrNotFound = errors.New("vault: credential not found")
	// ErrDestroyed: the credential was crypto-shredded.
	ErrDestroyed = errors.New("vault: credential destroyed")
)

// DataKey is a per-organization data encryption key, wrapped by the KEK.
type DataKey struct {
	Org     string
	Version int
	KEKID   string
	Wrapped []byte
	Status  string // active | retired
}

// Record is one stored credential version (ciphertext only).
type Record struct {
	ID         string
	Org        string
	ConnID     string
	Version    int
	Status     string
	Kind       string
	Ciphertext []byte
	Nonce      []byte
	DEKVersion int
	Hint       string
	ExpiresAt  *time.Time
	CreatedBy  string
	CreatedAt  time.Time
}

// Meta is the non-secret description of a credential (safe for the API).
type Meta struct {
	Kind      string     `json:"kind"`
	Hint      string     `json:"hint,omitempty"`
	Version   int        `json:"version"`
	ExpiresAt *time.Time `json:"expires_at"`
	RotatedAt time.Time  `json:"rotated_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// Repo is the persistence port of the vault.
type Repo interface {
	ActiveDataKey(ctx context.Context, org string) (DataKey, bool, error)
	GetDataKey(ctx context.Context, org string, version int) (DataKey, error)
	ListDataKeys(ctx context.Context, org string) ([]DataKey, error)
	PutDataKey(ctx context.Context, k DataKey) error
	UpdateDataKey(ctx context.Context, k DataKey) error

	PutCredential(ctx context.Context, r Record) error
	ActiveCredential(ctx context.Context, org, connID string) (Record, error)
	// RetireActive marks the active credential retired (ciphertext kept).
	RetireActive(ctx context.Context, org, connID string) error
	// DestroyAll wipes ciphertext and nonce of every version (crypto-shred).
	DestroyAll(ctx context.Context, org, connID string) error
}

// Registrar learns exact secret values so redactors can scrub them later.
type Registrar interface{ Add(secret string) }

// Vault encrypts, stores and hands out credentials.
type Vault struct {
	repo Repo
	kw   KeyWrapper // nil: fail closed
	mu   sync.Mutex
	// Suspects, when set, receives every secret stored or revealed.
	Suspects Registrar
	Now      func() time.Time
}

// New builds a Vault. A nil KeyWrapper makes every operation fail with ErrNoKEK.
func New(repo Repo, kw KeyWrapper) *Vault {
	return &Vault{repo: repo, kw: kw, Now: time.Now}
}

// Enabled reports whether live credentials can be stored (a KEK is loaded).
func (v *Vault) Enabled() bool { return v != nil && v.kw != nil && v.repo != nil }

func aad(org, id string, version int, kind string) []byte {
	return []byte(fmt.Sprintf("%s|%s|%d|%s", org, id, version, kind))
}

func (v *Vault) dek(ctx context.Context, org string, version int) ([]byte, error) {
	k, err := v.repo.GetDataKey(ctx, org, version)
	if err != nil {
		return nil, err
	}
	return v.kw.Unwrap(ctx, k.KEKID, k.Wrapped)
}

// activeDEK returns (creating on first use) the org's current data key.
func (v *Vault) activeDEK(ctx context.Context, org string) ([]byte, int, error) {
	k, ok, err := v.repo.ActiveDataKey(ctx, org)
	if err != nil {
		return nil, 0, err
	}
	if !ok {
		return v.newDEK(ctx, org, 1)
	}
	d, err := v.kw.Unwrap(ctx, k.KEKID, k.Wrapped)
	return d, k.Version, err
}

func (v *Vault) newDEK(ctx context.Context, org string, version int) ([]byte, int, error) {
	d := make([]byte, 32)
	if _, err := rand.Read(d); err != nil {
		return nil, 0, err
	}
	w, id, err := v.kw.Wrap(ctx, d)
	if err != nil {
		return nil, 0, err
	}
	if err := v.repo.PutDataKey(ctx, DataKey{Org: org, Version: version, KEKID: id, Wrapped: w, Status: "active"}); err != nil {
		return nil, 0, err
	}
	return d, version, nil
}

// Put stores secret as the active credential of connID. A previous active
// version is retired (so a rotation never leaves a gap).
func (v *Vault) Put(ctx context.Context, org, connID, kind string, s Secret, createdBy string, expires *time.Time) (Meta, error) {
	if !v.Enabled() {
		return Meta{}, ErrNoKEK
	}
	if s.Len() == 0 {
		return Meta{}, errors.New("vault: empty secret")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	version := 1
	if prev, err := v.repo.ActiveCredential(ctx, org, connID); err == nil {
		version = prev.Version + 1
		if err := v.repo.RetireActive(ctx, org, connID); err != nil {
			return Meta{}, err
		}
	} else if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrDestroyed) {
		return Meta{}, err
	}
	dek, dekV, err := v.activeDEK(ctx, org)
	if err != nil {
		return Meta{}, err
	}
	defer zero(dek)
	id := uuid.NewString()
	sealed, err := seal(dek, s.Reveal(), aad(org, id, version, kind))
	if err != nil {
		return Meta{}, err
	}
	const ns = 12
	rec := Record{ID: id, Org: org, ConnID: connID, Version: version, Status: StatusActive, Kind: kind,
		Nonce: sealed[:ns], Ciphertext: sealed[ns:], DEKVersion: dekV, ExpiresAt: expires,
		CreatedBy: createdBy, CreatedAt: v.Now().UTC()}
	if s.Len() >= 20 {
		rec.Hint = string(s.Reveal()[s.Len()-4:])
	}
	if err := v.repo.PutCredential(ctx, rec); err != nil {
		return Meta{}, err
	}
	if v.Suspects != nil {
		v.Suspects.Add(string(s.Reveal()))
	}
	return metaOf(rec), nil
}

func metaOf(r Record) Meta {
	return Meta{Kind: r.Kind, Hint: r.Hint, Version: r.Version, ExpiresAt: r.ExpiresAt, RotatedAt: r.CreatedAt, CreatedAt: r.CreatedAt}
}

// Meta describes the active credential without decrypting it.
func (v *Vault) Meta(ctx context.Context, org, connID string) (Meta, error) {
	if v == nil || v.repo == nil {
		return Meta{}, ErrNotFound
	}
	r, err := v.repo.ActiveCredential(ctx, org, connID)
	if err != nil {
		return Meta{}, err
	}
	return metaOf(r), nil
}

// Use decrypts the active credential and passes it to fn. The bytes are zeroed
// when fn returns: fn must not retain them.
func (v *Vault) Use(ctx context.Context, org, connID string, fn func(Secret) error) error {
	if !v.Enabled() {
		return ErrNoKEK
	}
	r, err := v.repo.ActiveCredential(ctx, org, connID)
	if err != nil {
		return err
	}
	if r.Status == StatusDestroyed || len(r.Ciphertext) == 0 {
		return ErrDestroyed
	}
	dek, err := v.dek(ctx, org, r.DEKVersion)
	if err != nil {
		return err
	}
	defer zero(dek)
	blob := append(append([]byte{}, r.Nonce...), r.Ciphertext...)
	plain, err := open(dek, blob, aad(org, r.ID, r.Version, r.Kind))
	if err != nil {
		return fmt.Errorf("vault: integrity check failed: %w", err)
	}
	s := Secret{b: plain}
	defer s.Zero()
	if v.Suspects != nil {
		v.Suspects.Add(string(plain))
	}
	return fn(s)
}

// Destroy crypto-shreds every version of the connection's credential.
func (v *Vault) Destroy(ctx context.Context, org, connID string) error {
	if v == nil || v.repo == nil {
		return nil
	}
	return v.repo.DestroyAll(ctx, org, connID)
}

// RotateDEK creates the next data key; new credentials use it, old ones keep
// decrypting with the version recorded in their row.
func (v *Vault) RotateDEK(ctx context.Context, org string) (int, error) {
	if !v.Enabled() {
		return 0, ErrNoKEK
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	cur, ok, err := v.repo.ActiveDataKey(ctx, org)
	if err != nil {
		return 0, err
	}
	next := 1
	if ok {
		next = cur.Version + 1
		cur.Status = "retired"
		if err := v.repo.UpdateDataKey(ctx, cur); err != nil {
			return 0, err
		}
	}
	d, _, err := v.newDEK(ctx, org, next)
	zero(d)
	return next, err
}

// RewrapKeys re-wraps every data key of org with the current KEK (KEK rotation).
func (v *Vault) RewrapKeys(ctx context.Context, org string) error {
	if !v.Enabled() {
		return ErrNoKEK
	}
	keys, err := v.repo.ListDataKeys(ctx, org)
	if err != nil {
		return err
	}
	for _, k := range keys {
		d, err := v.kw.Unwrap(ctx, k.KEKID, k.Wrapped)
		if err != nil {
			return err
		}
		w, id, err := v.kw.Wrap(ctx, d)
		zero(d)
		if err != nil {
			return err
		}
		k.Wrapped, k.KEKID = w, id
		if err := v.repo.UpdateDataKey(ctx, k); err != nil {
			return err
		}
	}
	return nil
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// Seal encrypts arbitrary short-lived data (e.g. a PKCE verifier) under the
// org's active data key. purpose is bound as AAD. The result embeds the DEK
// version: dekVersion(4 bytes) || nonce || ciphertext.
func (v *Vault) Seal(ctx context.Context, org, purpose string, plain []byte) ([]byte, error) {
	if !v.Enabled() {
		return nil, ErrNoKEK
	}
	v.mu.Lock()
	dek, ver, err := v.activeDEK(ctx, org)
	v.mu.Unlock()
	if err != nil {
		return nil, err
	}
	defer zero(dek)
	blob, err := seal(dek, plain, []byte(org+"|seal|"+purpose))
	if err != nil {
		return nil, err
	}
	hdr := []byte{byte(ver >> 24), byte(ver >> 16), byte(ver >> 8), byte(ver)}
	return append(hdr, blob...), nil
}

// Unseal reverses Seal.
func (v *Vault) Unseal(ctx context.Context, org, purpose string, sealed []byte) ([]byte, error) {
	if !v.Enabled() {
		return nil, ErrNoKEK
	}
	if len(sealed) < 4 {
		return nil, errors.New("vault: sealed data too short")
	}
	ver := int(sealed[0])<<24 | int(sealed[1])<<16 | int(sealed[2])<<8 | int(sealed[3])
	dek, err := v.dek(ctx, org, ver)
	if err != nil {
		return nil, err
	}
	defer zero(dek)
	return open(dek, sealed[4:], []byte(org+"|seal|"+purpose))
}
