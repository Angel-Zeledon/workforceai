package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Team sign-up tables (migrations/809_team_signup.sql). Like memberships they are not under RLS; only the
// organization row itself is, so CreateOrg scopes its transaction to the organization it creates.

func isFK(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23503"
}

func (s *PGStore) CreateOrg(ctx context.Context, o Org, m Membership) error {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := SetOrgID(ctx, tx, o.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO organizations (id, name, slug, budget_usd) VALUES ($1,$2,$3,$4)`,
			o.ID, o.Name, o.Slug, o.BudgetUSD); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO memberships (org_id, user_id, role) VALUES ($1,$2,$3)`, o.ID, m.UserID, string(m.Role))
		return err
	})
	if c, ok := isUnique(err); ok {
		if strings.Contains(c, "slug") {
			return ErrSlugTaken
		}
		return ErrAlreadyMember
	}
	if isFK(err) {
		return ErrNotFound
	}
	return err
}

func (s *PGStore) PutOrgSetup(ctx context.Context, st OrgSetup) error {
	_, err := s.db.Exec(ctx, `INSERT INTO org_setup (org_id, audience, locale, created_at, setup_done_at) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (org_id) DO UPDATE SET audience = EXCLUDED.audience, locale = EXCLUDED.locale, setup_done_at = EXCLUDED.setup_done_at`,
		st.OrgID, string(st.Audience), st.Locale, st.CreatedAt, nullTime(st.SetupDoneAt))
	if isFK(err) {
		return ErrNotFound
	}
	return err
}

func (s *PGStore) GetOrgSetup(ctx context.Context, orgID string) (OrgSetup, error) {
	var st OrgSetup
	var aud string
	var done *time.Time
	err := s.db.QueryRow(ctx, `SELECT org_id, audience, locale, created_at, setup_done_at FROM org_setup WHERE org_id = $1`, orgID).
		Scan(&st.OrgID, &aud, &st.Locale, &st.CreatedAt, &done)
	if errors.Is(err, pgx.ErrNoRows) {
		return OrgSetup{}, ErrNotFound
	}
	st.Audience, st.SetupDoneAt = Audience(aud), timeOrZero(done)
	return st, err
}

func (s *PGStore) MarkOrgSetupDone(ctx context.Context, orgID string, at time.Time) error {
	tag, err := s.db.Exec(ctx, `UPDATE org_setup SET setup_done_at = $2 WHERE org_id = $1`, orgID, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const profileCols = `org_id, user_id, profession, department_id, team_name, lead, via, invited_by, provisioned_at, created_at, updated_at`

func scanProfile(row pgx.Row) (MemberProfile, error) {
	var p MemberProfile
	var prov *time.Time
	if err := row.Scan(&p.OrgID, &p.UserID, &p.Profession, &p.DepartmentID, &p.TeamName, &p.Lead, &p.Via, &p.InvitedBy, &prov, &p.CreatedAt, &p.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MemberProfile{}, ErrNotFound
		}
		return MemberProfile{}, err
	}
	p.ProvisionedAt = timeOrZero(prov)
	return p, nil
}

func (s *PGStore) PutMemberProfile(ctx context.Context, p MemberProfile) error {
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = p.CreatedAt
	}
	_, err := s.db.Exec(ctx, `INSERT INTO member_profiles (`+profileCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (org_id, user_id) DO UPDATE SET profession = EXCLUDED.profession, department_id = EXCLUDED.department_id,
			team_name = EXCLUDED.team_name, lead = EXCLUDED.lead, via = EXCLUDED.via, invited_by = EXCLUDED.invited_by,
			provisioned_at = EXCLUDED.provisioned_at, updated_at = EXCLUDED.updated_at`,
		p.OrgID, p.UserID, p.Profession, p.DepartmentID, p.TeamName, p.Lead, p.Via, p.InvitedBy, nullTime(p.ProvisionedAt), p.CreatedAt, p.UpdatedAt)
	if isFK(err) {
		return ErrNotFound
	}
	return err
}

func (s *PGStore) GetMemberProfile(ctx context.Context, orgID, userID string) (MemberProfile, error) {
	return scanProfile(s.db.QueryRow(ctx, `SELECT `+profileCols+` FROM member_profiles WHERE org_id = $1 AND user_id = $2`, orgID, userID))
}

func (s *PGStore) ListMemberProfiles(ctx context.Context, orgID string) ([]MemberProfile, error) {
	rows, err := s.db.Query(ctx, `SELECT `+profileCols+` FROM member_profiles WHERE org_id = $1 ORDER BY created_at, user_id LIMIT 2000`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemberProfile
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *PGStore) PutSignupIntent(ctx context.Context, in SignupIntent) error {
	_, err := s.db.Exec(ctx, `INSERT INTO signup_intents (user_id, audience, profession, org_name, team_name, locale, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (user_id) DO UPDATE SET audience = EXCLUDED.audience, profession = EXCLUDED.profession, org_name = EXCLUDED.org_name,
			team_name = EXCLUDED.team_name, locale = EXCLUDED.locale, created_at = EXCLUDED.created_at`,
		in.UserID, string(in.Audience), in.Profession, in.OrgName, in.TeamName, in.Locale, in.CreatedAt)
	if isFK(err) {
		return ErrNotFound
	}
	return err
}

func (s *PGStore) TakeSignupIntent(ctx context.Context, userID string) (SignupIntent, error) {
	var in SignupIntent
	var aud string
	err := s.db.QueryRow(ctx, `DELETE FROM signup_intents WHERE user_id = $1
		RETURNING user_id, audience, profession, org_name, team_name, locale, created_at`, userID).
		Scan(&in.UserID, &aud, &in.Profession, &in.OrgName, &in.TeamName, &in.Locale, &in.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SignupIntent{}, ErrNotFound
	}
	in.Audience = Audience(aud)
	return in, err
}
