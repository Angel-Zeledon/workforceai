package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DB is the subset of *pgxpool.Pool used by this package.
type DB interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// PGStore is the Postgres implementation of Store. Tables: see
// internal/infrastructure/postgres/migrations/201_auth_tables.sql.
type PGStore struct{ db DB }

func NewPGStore(db DB) *PGStore { return &PGStore{db: db} }

var _ Store = (*PGStore)(nil)

func isUnique(err error) (string, bool) {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return pe.ConstraintName, true
	}
	return "", false
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// inTx runs fn in a transaction, committing on nil and rolling back on error.
func (s *PGStore) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

const userCols = `id, email, name, password_hash, failed_attempts, locked_until, created_at`

func scanUser(row pgx.Row) (User, error) {
	var u User
	var locked *time.Time
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.FailedAttempts, &locked, &u.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	u.LockedUntil = timeOrZero(locked)
	return u, nil
}

func (s *PGStore) CreateAccount(ctx context.Context, u User, o Org, m Membership) error {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		// organizations is protected by RLS (id = app.org_id): scope the tx to
		// the org being created so the INSERT passes WITH CHECK.
		if err := SetOrgID(ctx, tx, o.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO organizations (id, name, slug, budget_usd) VALUES ($1,$2,$3,$4)`,
			o.ID, o.Name, o.Slug, o.BudgetUSD); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO users (id, email, name, password_hash) VALUES ($1,$2,$3,$4)`,
			u.ID, u.Email, u.Name, u.PasswordHash); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO memberships (org_id, user_id, role) VALUES ($1,$2,$3)`,
			m.OrgID, m.UserID, string(m.Role))
		return err
	})
	if c, ok := isUnique(err); ok {
		if strings.Contains(c, "slug") {
			return ErrSlugTaken
		}
		return ErrEmailTaken
	}
	return err
}

func (s *PGStore) UserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(s.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE lower(email) = lower($1)`, email))
}

func (s *PGStore) UserByID(ctx context.Context, id string) (User, error) {
	return scanUser(s.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

func (s *PGStore) MembershipsOf(ctx context.Context, userID string) ([]Membership, error) {
	rows, err := s.db.Query(ctx,
		`SELECT org_id, user_id, role, created_at FROM memberships WHERE user_id = $1 ORDER BY created_at, org_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Membership
	for rows.Next() {
		var m Membership
		var role string
		if err := rows.Scan(&m.OrgID, &m.UserID, &role, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Role = Role(role)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *PGStore) GetMembership(ctx context.Context, orgID, userID string) (Membership, error) {
	var m Membership
	var role string
	err := s.db.QueryRow(ctx,
		`SELECT org_id, user_id, role, created_at FROM memberships WHERE org_id = $1 AND user_id = $2`, orgID, userID).
		Scan(&m.OrgID, &m.UserID, &role, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, ErrNotFound
	}
	m.Role = Role(role)
	return m, err
}

func (s *PGStore) ListMembers(ctx context.Context, orgID string) ([]Member, error) {
	rows, err := s.db.Query(ctx, `SELECT u.id, u.email, u.name, m.role, m.created_at
		FROM memberships m JOIN users u ON u.id = m.user_id WHERE m.org_id = $1 ORDER BY u.email`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		var role string
		if err := rows.Scan(&m.UserID, &m.Email, &m.Name, &role, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Role = Role(role)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *PGStore) AddMembership(ctx context.Context, m Membership) error {
	_, err := s.db.Exec(ctx, `INSERT INTO memberships (org_id, user_id, role) VALUES ($1,$2,$3)`,
		m.OrgID, m.UserID, string(m.Role))
	if _, ok := isUnique(err); ok {
		return ErrAlreadyMember
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23503" { // foreign key violation
		return ErrNotFound
	}
	return err
}

// lockOwners locks all owner rows of the org (serialising concurrent
// demotions/removals) and returns their user ids.
func lockOwners(ctx context.Context, tx pgx.Tx, orgID string) (map[string]bool, error) {
	rows, err := tx.Query(ctx, `SELECT user_id FROM memberships WHERE org_id = $1 AND role = 'owner' FOR UPDATE`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	owners := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		owners[id] = true
	}
	return owners, rows.Err()
}

func (s *PGStore) SetMemberRole(ctx context.Context, orgID, userID string, role Role) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		owners, err := lockOwners(ctx, tx, orgID)
		if err != nil {
			return err
		}
		if owners[userID] && role != RoleOwner && len(owners) <= 1 {
			return ErrLastOwner
		}
		tag, err := tx.Exec(ctx, `UPDATE memberships SET role = $3 WHERE org_id = $1 AND user_id = $2`, orgID, userID, string(role))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func (s *PGStore) RemoveMember(ctx context.Context, orgID, userID string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		owners, err := lockOwners(ctx, tx, orgID)
		if err != nil {
			return err
		}
		if owners[userID] && len(owners) <= 1 {
			return ErrLastOwner
		}
		tag, err := tx.Exec(ctx, `DELETE FROM memberships WHERE org_id = $1 AND user_id = $2`, orgID, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func (s *PGStore) RecordLoginFailure(ctx context.Context, userID string, max int, lockFor time.Duration, now time.Time) (User, error) {
	const expired = `(locked_until IS NOT NULL AND locked_until <= $4)`
	q := fmt.Sprintf(`UPDATE users SET
		failed_attempts = CASE WHEN %[1]s THEN 1 ELSE failed_attempts + 1 END,
		locked_until = CASE
			WHEN (CASE WHEN %[1]s THEN 1 ELSE failed_attempts + 1 END) >= $2 THEN $4::timestamptz + make_interval(secs => $3)
			WHEN %[1]s THEN NULL
			ELSE locked_until END,
		updated_at = now()
		WHERE id = $1 RETURNING %[2]s`, expired, userCols)
	return scanUser(s.db.QueryRow(ctx, q, userID, max, lockFor.Seconds(), now))
}

func (s *PGStore) ResetLoginFailures(ctx context.Context, userID string) error {
	_, err := s.db.Exec(ctx, `UPDATE users SET failed_attempts = 0, locked_until = NULL, updated_at = now() WHERE id = $1`, userID)
	return err
}

func (s *PGStore) UpdatePasswordHash(ctx context.Context, userID, hash string) error {
	_, err := s.db.Exec(ctx, `UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`, userID, hash)
	return err
}

func (s *PGStore) SaveRefreshToken(ctx context.Context, t RefreshToken) error {
	_, err := s.db.Exec(ctx, `INSERT INTO refresh_tokens
		(id, user_id, org_id, family_id, token_hash, expires_at, created_at, user_agent, ip)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		t.ID, t.UserID, t.OrgID, t.FamilyID, t.TokenHash, t.ExpiresAt, t.CreatedAt, t.UserAgent, t.IP)
	return err
}

func (s *PGStore) RotateRefreshToken(ctx context.Context, oldHash string, next RefreshToken, now time.Time) (RefreshToken, error) {
	var old RefreshToken
	var retErr error
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var revoked *time.Time
		var replaced *string
		err := tx.QueryRow(ctx, `SELECT id, user_id, org_id, family_id, token_hash, expires_at, created_at, revoked_at, replaced_by
			FROM refresh_tokens WHERE token_hash = $1 FOR UPDATE`, oldHash).
			Scan(&old.ID, &old.UserID, &old.OrgID, &old.FamilyID, &old.TokenHash, &old.ExpiresAt, &old.CreatedAt, &revoked, &replaced)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidToken
		}
		if err != nil {
			return err
		}
		if revoked != nil {
			// Reuse: revoke the family and COMMIT that, then report the error.
			if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = $2 WHERE family_id = $1 AND revoked_at IS NULL`,
				old.FamilyID, now); err != nil {
				return err
			}
			retErr = ErrTokenReuse
			return nil
		}
		if !old.ExpiresAt.After(now) {
			return ErrTokenExpired
		}
		if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = $2, replaced_by = $3 WHERE id = $1`,
			old.ID, now, next.ID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO refresh_tokens
			(id, user_id, org_id, family_id, token_hash, expires_at, created_at, user_agent, ip)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			next.ID, old.UserID, old.OrgID, old.FamilyID, next.TokenHash, next.ExpiresAt, next.CreatedAt, next.UserAgent, next.IP)
		return err
	})
	if err != nil {
		return RefreshToken{}, err
	}
	if retErr != nil {
		return RefreshToken{}, retErr
	}
	return old, nil
}

func (s *PGStore) RevokeFamilyOf(ctx context.Context, tokenHash string, now time.Time) error {
	_, err := s.db.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = $2
		WHERE revoked_at IS NULL AND family_id = (SELECT family_id FROM refresh_tokens WHERE token_hash = $1)`, tokenHash, now)
	return err
}

func (s *PGStore) RevokeUserTokens(ctx context.Context, userID string, now time.Time) error {
	_, err := s.db.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`, userID, now)
	return err
}

func (s *PGStore) RevokeOrgUserTokens(ctx context.Context, orgID, userID string, now time.Time) error {
	_, err := s.db.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = $3 WHERE org_id = $1 AND user_id = $2 AND revoked_at IS NULL`,
		orgID, userID, now)
	return err
}
