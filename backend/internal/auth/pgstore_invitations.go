package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Invitations live under RLS (migrations/300_invitations.sql): organization
// scoped calls run through WithOrgTx; the accept path, which happens before the
// tenant is known, scopes its transaction to the single row whose token hash it
// knows (app.invitation_hash).

const invCols = `id, org_id, email, role, token_hash, invited_by, created_at, expires_at, accepted_at, accepted_by, revoked_at`

func scanInvitation(row pgx.Row) (Invitation, error) {
	var i Invitation
	var role string
	var acc, rev *time.Time
	var accBy *string
	if err := row.Scan(&i.ID, &i.OrgID, &i.Email, &role, &i.TokenHash, &i.InvitedBy, &i.CreatedAt, &i.ExpiresAt, &acc, &accBy, &rev); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Invitation{}, ErrNotFound
		}
		return Invitation{}, err
	}
	i.Role, i.AcceptedAt, i.RevokedAt = Role(role), timeOrZero(acc), timeOrZero(rev)
	if accBy != nil {
		i.AcceptedBy = *accBy
	}
	return i, nil
}

func byTokenHash(ctx context.Context, tx pgx.Tx, tokenHash string) error {
	_, err := tx.Exec(ctx, `SELECT set_config('app.invitation_hash', $1, true)`, tokenHash)
	return err
}

func (s *PGStore) CreateInvitation(ctx context.Context, inv Invitation, now time.Time) error {
	return WithOrgTx(ctx, s.db, inv.OrgID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE invitations SET revoked_at = $3
			WHERE org_id = $1 AND lower(email) = lower($2) AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > $3`,
			inv.OrgID, inv.Email, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO invitations (id, org_id, email, role, token_hash, invited_by, created_at, expires_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			inv.ID, inv.OrgID, inv.Email, string(inv.Role), inv.TokenHash, inv.InvitedBy, inv.CreatedAt, inv.ExpiresAt)
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23503" {
			return ErrNotFound
		}
		return err
	})
}

func (s *PGStore) ListInvitations(ctx context.Context, orgID string) ([]Invitation, error) {
	var out []Invitation
	err := WithOrgTx(ctx, s.db, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+invCols+` FROM invitations WHERE org_id = $1 ORDER BY created_at DESC LIMIT 500`, orgID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			i, err := scanInvitation(rows)
			if err != nil {
				return err
			}
			out = append(out, i)
		}
		return rows.Err()
	})
	return out, err
}

func (s *PGStore) GetInvitation(ctx context.Context, orgID, id string) (Invitation, error) {
	var inv Invitation
	err := WithOrgTx(ctx, s.db, orgID, func(tx pgx.Tx) (err error) {
		inv, err = scanInvitation(tx.QueryRow(ctx, `SELECT `+invCols+` FROM invitations WHERE org_id = $1 AND id = $2`, orgID, id))
		return err
	})
	return inv, err
}

func (s *PGStore) RevokeInvitation(ctx context.Context, orgID, id string, now time.Time) error {
	return WithOrgTx(ctx, s.db, orgID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE invitations SET revoked_at = $3
			WHERE org_id = $1 AND id = $2 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > $3`, orgID, id, now)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
}

func (s *PGStore) InvitationByTokenHash(ctx context.Context, tokenHash string) (Invitation, error) {
	var inv Invitation
	err := s.inTx(ctx, func(tx pgx.Tx) (err error) {
		if err := byTokenHash(ctx, tx, tokenHash); err != nil {
			return err
		}
		inv, err = scanInvitation(tx.QueryRow(ctx, `SELECT `+invCols+` FROM invitations WHERE token_hash = $1`, tokenHash))
		return err
	})
	return inv, err
}

func (s *PGStore) AcceptInvitation(ctx context.Context, tokenHash string, newUser *User, m Membership, now time.Time) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := byTokenHash(ctx, tx, tokenHash); err != nil {
			return err
		}
		inv, err := scanInvitation(tx.QueryRow(ctx, `SELECT `+invCols+` FROM invitations WHERE token_hash = $1 FOR UPDATE`, tokenHash))
		if errors.Is(err, ErrNotFound) {
			return ErrInvalidToken
		}
		if err != nil {
			return err
		}
		switch inv.Status(now) {
		case InvitationExpired:
			return ErrTokenExpired
		case InvitationAccepted, InvitationRevoked:
			return ErrInvalidToken
		}
		if newUser != nil {
			_, err := tx.Exec(ctx, `INSERT INTO users (id, email, name, password_hash) VALUES ($1,$2,$3,$4)`,
				newUser.ID, newUser.Email, newUser.Name, newUser.PasswordHash)
			if _, ok := isUnique(err); ok {
				return ErrEmailTaken
			}
			if err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO memberships (org_id, user_id, role) VALUES ($1,$2,$3)`, inv.OrgID, m.UserID, string(m.Role))
		if _, ok := isUnique(err); ok {
			return ErrAlreadyMember
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE invitations SET accepted_at = $2, accepted_by = $3 WHERE id = $1`, inv.ID, now, m.UserID)
		return err
	})
}
