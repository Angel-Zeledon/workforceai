// Package scim implements a minimal SCIM 2.0 server (RFC 7643/7644: Users and Groups) so an identity provider (Okta,
// Entra ID, Google Workspace, OneLogin...) can create and deactivate the accounts of one organization. One bearer token
// per organization; SCIM can only touch the accounts it provisioned itself and can never grant the owner role.
// Design: docs/architecture/identity.md.
package scim

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/auth"
	"github.com/jackc/pgx/v5"
)

// ErrNotFound is returned by the stores.
var ErrNotFound = errors.New("scim: not found")

// User is the provisioning record of an account SCIM created or adopted in an organization.
type User struct {
	OrgID      string
	UserID     string
	ExternalID string
	UserName   string // the e-mail address, lower-case
	Active     bool
	Role       auth.Role // admin, member or viewer
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// TokenInfo describes the organization's SCIM token without revealing it.
type TokenInfo struct {
	Exists     bool      `json:"exists"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
}

// Store is the persistence port.
type Store interface {
	PutToken(ctx context.Context, orgID, hash, createdBy string, at time.Time) error // replaces any previous token
	DeleteToken(ctx context.Context, orgID string) error
	TokenInfo(ctx context.Context, orgID string) (TokenInfo, error)
	OrgForToken(ctx context.Context, hash string) (string, error) // ErrNotFound
	TouchToken(ctx context.Context, hash string, at time.Time) error

	GetUser(ctx context.Context, orgID, userID string) (User, error) // ErrNotFound
	FindByUserName(ctx context.Context, orgID, userName string) (User, error)
	PutUser(ctx context.Context, u User) error
	ListUsers(ctx context.Context, orgID string) ([]User, error) // by user name
}

// MemStore is the in-memory Store.
type MemStore struct {
	mu     sync.Mutex
	tokens map[string]struct {
		org  string
		info TokenInfo
		by   string
	} // by hash
	users map[[2]string]User
}

// NewMemStore builds an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{tokens: map[string]struct {
		org  string
		info TokenInfo
		by   string
	}{}, users: map[[2]string]User{}}
}

func (m *MemStore) PutToken(_ context.Context, org, hash, by string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for h, t := range m.tokens {
		if t.org == org {
			delete(m.tokens, h)
		}
	}
	m.tokens[hash] = struct {
		org  string
		info TokenInfo
		by   string
	}{org, TokenInfo{Exists: true, CreatedAt: at}, by}
	return nil
}

func (m *MemStore) DeleteToken(_ context.Context, org string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for h, t := range m.tokens {
		if t.org == org {
			delete(m.tokens, h)
		}
	}
	return nil
}

func (m *MemStore) TokenInfo(_ context.Context, org string) (TokenInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tokens {
		if t.org == org {
			return t.info, nil
		}
	}
	return TokenInfo{}, nil
}

func (m *MemStore) OrgForToken(_ context.Context, hash string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[hash]
	if !ok {
		return "", ErrNotFound
	}
	return t.org, nil
}

func (m *MemStore) TouchToken(_ context.Context, hash string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tokens[hash]; ok {
		t.info.LastUsedAt = at
		m.tokens[hash] = t
	}
	return nil
}

func (m *MemStore) GetUser(_ context.Context, org, id string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[[2]string{org, id}]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (m *MemStore) FindByUserName(_ context.Context, org, name string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, u := range m.users {
		if k[0] == org && strings.EqualFold(u.UserName, name) {
			return u, nil
		}
	}
	return User{}, ErrNotFound
}

func (m *MemStore) PutUser(_ context.Context, u User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.users[[2]string{u.OrgID, u.UserID}] = u
	return nil
}

func (m *MemStore) ListUsers(_ context.Context, org string) ([]User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []User
	for k, u := range m.users {
		if k[0] == org {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserName < out[j].UserName })
	return out, nil
}

// PGStore is the Postgres Store (migration 703): scim_users is tenant data (RLS), scim_tokens is looked up by hash before
// the tenant is known.
type PGStore struct{ db auth.DB }

// NewPGStore wraps a pool.
func NewPGStore(db auth.DB) *PGStore { return &PGStore{db: db} }

var _ Store = (*PGStore)(nil)

func (s *PGStore) PutToken(ctx context.Context, org, hash, by string, at time.Time) error {
	_, err := s.db.Exec(ctx, `INSERT INTO scim_tokens (token_hash, org_id, created_by, created_at) VALUES ($1,$2,$3,$4)
		ON CONFLICT (org_id) DO UPDATE SET token_hash=EXCLUDED.token_hash, created_by=EXCLUDED.created_by, created_at=EXCLUDED.created_at, last_used_at=NULL`,
		hash, org, by, at)
	return err
}

func (s *PGStore) DeleteToken(ctx context.Context, org string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM scim_tokens WHERE org_id = $1`, org)
	return err
}

func (s *PGStore) TokenInfo(ctx context.Context, org string) (TokenInfo, error) {
	ti := TokenInfo{Exists: true}
	var used *time.Time
	err := s.db.QueryRow(ctx, `SELECT created_at, last_used_at FROM scim_tokens WHERE org_id = $1`, org).Scan(&ti.CreatedAt, &used)
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenInfo{}, nil
	}
	if used != nil {
		ti.LastUsedAt = *used
	}
	return ti, err
}

func (s *PGStore) OrgForToken(ctx context.Context, hash string) (string, error) {
	var org string
	err := s.db.QueryRow(ctx, `SELECT org_id FROM scim_tokens WHERE token_hash = $1`, hash).Scan(&org)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return org, err
}

func (s *PGStore) TouchToken(ctx context.Context, hash string, at time.Time) error {
	_, err := s.db.Exec(ctx, `UPDATE scim_tokens SET last_used_at = $2 WHERE token_hash = $1`, hash, at)
	return err
}

const userCols = `user_id, external_id, user_name, active, role, created_at, updated_at`

func scanUser(org string, r pgx.Row) (User, error) {
	u := User{OrgID: org}
	var role string
	err := r.Scan(&u.UserID, &u.ExternalID, &u.UserName, &u.Active, &role, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	u.Role = auth.Role(role)
	return u, err
}

func (s *PGStore) one(ctx context.Context, org, q string, args ...any) (User, error) {
	var u User
	err := auth.WithOrgTx(ctx, s.db, org, func(tx pgx.Tx) error {
		var e error
		u, e = scanUser(org, tx.QueryRow(ctx, q, args...))
		return e
	})
	return u, err
}

func (s *PGStore) GetUser(ctx context.Context, org, id string) (User, error) {
	return s.one(ctx, org, `SELECT `+userCols+` FROM scim_users WHERE org_id = $1 AND user_id = $2`, org, id)
}

func (s *PGStore) FindByUserName(ctx context.Context, org, name string) (User, error) {
	return s.one(ctx, org, `SELECT `+userCols+` FROM scim_users WHERE org_id = $1 AND lower(user_name) = lower($2)`, org, name)
}

func (s *PGStore) PutUser(ctx context.Context, u User) error {
	return auth.WithOrgTx(ctx, s.db, u.OrgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO scim_users (org_id, user_id, external_id, user_name, active, role, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (org_id, user_id) DO UPDATE SET external_id=EXCLUDED.external_id, user_name=EXCLUDED.user_name, active=EXCLUDED.active,
			  role=EXCLUDED.role, updated_at=EXCLUDED.updated_at`,
			u.OrgID, u.UserID, u.ExternalID, u.UserName, u.Active, string(u.Role), u.CreatedAt, u.UpdatedAt)
		return err
	})
}

func (s *PGStore) ListUsers(ctx context.Context, org string) ([]User, error) {
	var out []User
	err := auth.WithOrgTx(ctx, s.db, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+userCols+` FROM scim_users WHERE org_id = $1 ORDER BY lower(user_name)`, org)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			u, err := scanUser(org, rows)
			if err != nil {
				return err
			}
			out = append(out, u)
		}
		return rows.Err()
	})
	return out, err
}
