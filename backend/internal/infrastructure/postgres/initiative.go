package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

var _ application.InitiativeStore = (*Store)(nil)

const initiativeCols = `id, agent_id, signal, signal_key, title, body, prompt, status, created_at, resolved_at, resolved_by, request_id`

func scanInitiative(r scanner) (application.Initiative, error) {
	var it application.Initiative
	var st string
	err := r.Scan(&it.ID, &it.AgentID, &it.Signal, &it.SignalKey, &it.Title, &it.Body, &it.Prompt, &st, &it.CreatedAt,
		&it.ResolvedAt, &it.ResolvedBy, &it.RequestID)
	it.Status = application.InitiativeStatus(st)
	return it, err
}

func (s *Store) CreateInitiative(ctx context.Context, orgID string, it application.Initiative) error {
	return s.exec(ctx, orgID, `INSERT INTO initiatives (org_id, id, agent_id, signal, signal_key, title, body, prompt, status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		orgID, it.ID, it.AgentID, it.Signal, it.SignalKey, it.Title, it.Body, it.Prompt, string(it.Status), it.CreatedAt)
}

func (s *Store) ListInitiatives(ctx context.Context, orgID string, status application.InitiativeStatus, limit int) ([]application.Initiative, error) {
	if limit <= 0 {
		limit = 100
	}
	return many(ctx, s, orgID, scanInitiative, `SELECT `+initiativeCols+` FROM initiatives
		WHERE org_id=$1 AND ($2 = '' OR status=$2) ORDER BY created_at DESC, id DESC LIMIT $3`, orgID, string(status), limit)
}

func (s *Store) GetInitiative(ctx context.Context, orgID, id string) (application.Initiative, error) {
	it, err := one(ctx, s, orgID, scanInitiative, `SELECT `+initiativeCols+` FROM initiatives WHERE org_id=$1 AND id=$2`, orgID, id)
	return it, mapErr(err)
}

// ResolveInitiative changes the status only when the row is still in "from" (the WHERE clause is the compare-and-set).
func (s *Store) ResolveInitiative(ctx context.Context, orgID, id string, from, to application.InitiativeStatus, by, requestID string, at time.Time) (application.Initiative, error) {
	var out application.Initiative
	err := s.WithOrgTx(ctx, orgID, func(tx pgx.Tx) error {
		var q string
		var args []any
		if to == application.InitiativePending {
			q = `UPDATE initiatives SET status=$4, resolved_at=NULL, resolved_by='', request_id='' WHERE org_id=$1 AND id=$2 AND status=$3 RETURNING ` + initiativeCols
			args = []any{orgID, id, string(from), string(to)}
		} else {
			q = `UPDATE initiatives SET status=$4, resolved_at=$5, resolved_by=$6, request_id=CASE WHEN $7::text <> '' THEN $7::text ELSE request_id END
				WHERE org_id=$1 AND id=$2 AND status=$3 RETURNING ` + initiativeCols
			args = []any{orgID, id, string(from), string(to), at, by, requestID}
		}
		it, err := scanInitiative(tx.QueryRow(ctx, q, args...))
		out = it
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Missing, or no longer in the expected status: tell them apart.
		if _, gerr := s.GetInitiative(ctx, orgID, id); gerr != nil {
			return application.Initiative{}, domain.ErrNotFound
		}
		return application.Initiative{}, domain.ErrConflict
	}
	return out, err
}

func (s *Store) CountInitiatives(ctx context.Context, orgID, agentID string, since time.Time) (int, error) {
	return one(ctx, s, orgID, func(r scanner) (int, error) {
		var n int
		err := r.Scan(&n)
		return n, err
	}, `SELECT count(*) FROM initiatives WHERE org_id=$1 AND created_at >= $2 AND ($3 = '' OR agent_id=$3)`, orgID, since, agentID)
}

func (s *Store) HasInitiativeSignal(ctx context.Context, orgID, signal, key string, since time.Time) (bool, error) {
	return one(ctx, s, orgID, func(r scanner) (bool, error) {
		var b bool
		err := r.Scan(&b)
		return b, err
	}, `SELECT EXISTS (SELECT 1 FROM initiatives WHERE org_id=$1 AND signal=$2 AND signal_key=$3 AND created_at >= $4)`, orgID, signal, key, since)
}

func (s *Store) GetInitiativeSettings(ctx context.Context, orgID string) (application.InitiativeSettings, bool, error) {
	var raw []byte
	v, err := one(ctx, s, orgID, func(r scanner) (application.InitiativeSettings, error) {
		var v application.InitiativeSettings
		err := r.Scan(&v.Enabled, &v.DailyLimit, &v.MonthlyLimit, &raw)
		return v, err
	}, `SELECT enabled, daily_limit, monthly_limit, agent_daily FROM initiative_settings WHERE org_id=$1`, orgID)
	if err = mapErr(err); errors.Is(err, domain.ErrNotFound) {
		return application.InitiativeSettings{}, false, nil
	} else if err != nil {
		return application.InitiativeSettings{}, false, err
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &v.AgentDaily)
	}
	return v, true, nil
}

func (s *Store) PutInitiativeSettings(ctx context.Context, orgID string, v application.InitiativeSettings) error {
	if v.AgentDaily == nil {
		v.AgentDaily = map[string]int{}
	}
	return s.exec(ctx, orgID, `INSERT INTO initiative_settings (org_id, enabled, daily_limit, monthly_limit, agent_daily, updated_at)
		VALUES ($1,$2,$3,$4,$5,now())
		ON CONFLICT (org_id) DO UPDATE SET enabled=EXCLUDED.enabled, daily_limit=EXCLUDED.daily_limit,
			monthly_limit=EXCLUDED.monthly_limit, agent_daily=EXCLUDED.agent_daily, updated_at=now()`,
		orgID, v.Enabled, v.DailyLimit, v.MonthlyLimit, jb(v.AgentDaily))
}
