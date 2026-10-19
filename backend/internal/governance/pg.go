package governance

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OrgTx runs fn in a transaction bound to one organization (RLS). postgres.Store.WithOrgTx has this shape.
type OrgTx interface {
	WithOrgTx(ctx context.Context, orgID string, fn func(tx pgx.Tx) error) error
}

// AdminStore is the persistence of departments, limits and the spend overview.
type AdminStore interface {
	AgentSpend(ctx context.Context, org string, monthStart time.Time) ([]AgentSpend, error)
	DepartmentLimits(ctx context.Context, org string) (map[string]float64, error)
	SetDepartment(ctx context.Context, org, agentID, department string) error
	SetDepartmentLimit(ctx context.Context, org, department string, usd float64, actor string) error
}

// PgStore implements every store of the package on Postgres (migrations 710 and 711). All access except the
// bearer lookup goes through WithOrgTx, so Row-Level Security applies.
type PgStore struct {
	DB   OrgTx
	Pool *pgxpool.Pool // bearer lookup only
}

var (
	_ TokenStore     = (*PgStore)(nil)
	_ RetentionStore = (*PgStore)(nil)
	_ ReportStore    = (*PgStore)(nil)
	_ AdminStore     = (*PgStore)(nil)
)

// ---- tokens ----

const tokCols = `id, name, prefix, scopes, created_by, created_at, expires_at, revoked_at, last_used_at`

func scanToken(row pgx.Row, extra ...any) (APIToken, error) {
	var t APIToken
	var sc []byte
	dest := append([]any{&t.ID, &t.Name, &t.Prefix, &sc, &t.CreatedBy, &t.CreatedAt, &t.ExpiresAt, &t.RevokedAt, &t.LastUsedAt}, extra...)
	if err := row.Scan(dest...); err != nil {
		return t, err
	}
	_ = json.Unmarshal(sc, &t.Scopes)
	if t.Scopes == nil {
		t.Scopes = []string{}
	}
	return t, nil
}

func (p *PgStore) Insert(ctx context.Context, org string, t APIToken, hash string) error {
	sc, _ := json.Marshal(t.Scopes)
	return p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO api_tokens (org_id, id, name, prefix, token_hash, scopes, created_by, created_at, expires_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, org, t.ID, t.Name, t.Prefix, hash, sc, t.CreatedBy, t.CreatedAt, t.ExpiresAt)
		return err
	})
}

func (p *PgStore) List(ctx context.Context, org string) ([]APIToken, error) {
	out := []APIToken{}
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+tokCols+` FROM api_tokens WHERE org_id=$1 ORDER BY created_at DESC`, org)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			t, err := scanToken(rows)
			if err != nil {
				return err
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	return out, err
}

func (p *PgStore) Revoke(ctx context.Context, org, id string, at time.Time) error {
	return p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE api_tokens SET revoked_at = COALESCE(revoked_at, $3) WHERE org_id=$1 AND id=$2`, org, id, at)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
}

// Lookup runs in a transaction that only announces the hash: the token_lookup policy then shows that single
// row and nothing else.
func (p *PgStore) Lookup(ctx context.Context, hash string) (string, APIToken, string, error) {
	var org, stored string
	var t APIToken
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return "", t, "", err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.token_hash', $1, true)`, hash); err != nil {
		return "", t, "", err
	}
	t, err = scanToken(tx.QueryRow(ctx, `SELECT `+tokCols+`, org_id, token_hash FROM api_tokens WHERE token_hash=$1`, hash), &org, &stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", t, "", ErrNotFound
	}
	return org, t, stored, err
}

func (p *PgStore) Touch(ctx context.Context, org, id string, at time.Time) error {
	return p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE api_tokens SET last_used_at=$3 WHERE org_id=$1 AND id=$2`, org, id, at)
		return err
	})
}

// ---- retention ----

func (p *PgStore) GetPolicy(ctx context.Context, org string) (RetentionPolicy, error) {
	var pol RetentionPolicy
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var res []byte
		err := tx.QueryRow(ctx, `SELECT enabled, chats_days, files_days, audit_days, last_run_at, last_result FROM org_retention WHERE org_id=$1`, org).
			Scan(&pol.Enabled, &pol.ChatsDays, &pol.FilesDays, &pol.AuditDays, &pol.LastRunAt, &res)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // no row: nothing is ever deleted
		}
		pol.LastResult = res
		return err
	})
	return pol, err
}

func (p *PgStore) PutPolicy(ctx context.Context, org string, pol RetentionPolicy, actor string) error {
	return p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO org_retention (org_id, enabled, chats_days, files_days, audit_days, updated_by, updated_at, next_run_at)
			VALUES ($1,$2,$3,$4,$5,$6,now(), now() + interval '1 hour')
			ON CONFLICT (org_id) DO UPDATE SET enabled=EXCLUDED.enabled, chats_days=EXCLUDED.chats_days, files_days=EXCLUDED.files_days,
				audit_days=EXCLUDED.audit_days, updated_by=EXCLUDED.updated_by, updated_at=now(),
				next_run_at = GREATEST(org_retention.next_run_at, now() + interval '1 hour')`,
			org, pol.Enabled, pol.ChatsDays, pol.FilesDays, pol.AuditDays, actor)
		return err
	})
}

func (p *PgStore) ClaimDue(ctx context.Context, org string, now time.Time, every time.Duration) (RetentionPolicy, bool, error) {
	var pol RetentionPolicy
	ok := false
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `UPDATE org_retention SET next_run_at = $2::timestamptz + make_interval(secs => $3)
			WHERE org_id=$1 AND enabled AND next_run_at <= $2
			RETURNING enabled, chats_days, files_days, audit_days`, org, now, every.Seconds()).
			Scan(&pol.Enabled, &pol.ChatsDays, &pol.FilesDays, &pol.AuditDays)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		ok = err == nil
		return err
	})
	return pol, ok, err
}

func (p *PgStore) CountChats(ctx context.Context, org string, cutoff time.Time) (int, int, error) {
	var m, c int
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM messages WHERE org_id=$1 AND ts < $2`, org, cutoff).Scan(&m); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM conversations c WHERE c.org_id=$1 AND c.last_message_at < $2
			AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.org_id=c.org_id AND m.conversation_id=c.id AND m.ts >= $2)`, org, cutoff).Scan(&c)
	})
	return m, c, err
}

func (p *PgStore) CountFiles(ctx context.Context, org string, cutoff time.Time) (int, error) {
	var n int
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM drive_files WHERE org_id=$1 AND deleted_at IS NULL AND updated_at < $2`, org, cutoff).Scan(&n)
	})
	return n, err
}

func (p *PgStore) PurgeChats(ctx context.Context, org string, cutoff time.Time, limit int) (int, int, error) {
	var m, c int
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM messages WHERE org_id=$1 AND id IN (
			SELECT id FROM messages WHERE org_id=$1 AND ts < $2 ORDER BY ts LIMIT $3)`, org, cutoff, limit)
		if err != nil {
			return err
		}
		m = int(tag.RowsAffected())
		// Only conversations that are themselves old and now empty: a conversation with a recent message stays.
		tag, err = tx.Exec(ctx, `DELETE FROM conversations WHERE org_id=$1 AND id IN (
			SELECT c.id FROM conversations c WHERE c.org_id=$1 AND c.last_message_at < $2
			AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.org_id=c.org_id AND m.conversation_id=c.id)
			LIMIT $3)`, org, cutoff, limit)
		c = int(tag.RowsAffected())
		return err
	})
	return m, c, err
}

func (p *PgStore) TrashFiles(ctx context.Context, org string, cutoff time.Time, limit int) (int, error) {
	n := 0
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE drive_files SET deleted_at=now(), deleted_by='{"kind":"system","id":"retention"}'::jsonb
			WHERE org_id=$1 AND id IN (
				SELECT id FROM drive_files WHERE org_id=$1 AND deleted_at IS NULL AND updated_at < $2 ORDER BY updated_at LIMIT $3)`, org, cutoff, limit)
		n = int(tag.RowsAffected())
		return err
	})
	return n, err
}

func (p *PgStore) SaveResult(ctx context.Context, org string, at time.Time, r PurgeResult) error {
	b, _ := json.Marshal(r)
	return p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE org_retention SET last_run_at=$2, last_result=$3 WHERE org_id=$1`, org, at, b)
		return err
	})
}

// ---- reports ----

// Usage groups the cost ledger. Requests are distinct request ids within each group. Entries of agents that were
// removed are kept (their id is the label); projects join through the request that launched them.
func (p *PgStore) Usage(ctx context.Context, org, by string, from, to time.Time) ([]UsageRow, error) {
	var keyExpr, labelExpr, deptExpr, joins, group string
	switch by {
	case ByAgent:
		keyExpr, labelExpr, deptExpr = `u.agent_id`, `COALESCE(MAX(a.name), u.agent_id)`, `COALESCE(MAX(a.department), '')`
		joins, group = `LEFT JOIN agents a ON a.org_id=u.org_id AND a.id=u.agent_id`, `u.agent_id`
	case ByDepartment:
		keyExpr, labelExpr, deptExpr = `COALESCE(a.department, '')`, `COALESCE(a.department, '')`, `COALESCE(a.department, '')`
		joins, group = `LEFT JOIN agents a ON a.org_id=u.org_id AND a.id=u.agent_id`, `COALESCE(a.department, '')`
	case ByProject:
		keyExpr, labelExpr, deptExpr = `COALESCE(pr.id, '')`, `COALESCE(MAX(COALESCE(pr.data->>'name', pr.data->>'title')), COALESCE(pr.id, ''))`, `''`
		joins, group = `LEFT JOIN projects pr ON pr.org_id=u.org_id AND pr.request_id=u.request_id AND pr.request_id <> ''`, `COALESCE(pr.id, '')`
	default:
		return nil, ErrInvalid
	}
	q := `SELECT ` + keyExpr + `, ` + labelExpr + `, ` + deptExpr + `,
			count(DISTINCT u.request_id), count(*), COALESCE(sum(u.input_tokens),0), COALESCE(sum(u.output_tokens),0), COALESCE(sum(u.cost_usd),0)
		FROM usage_entries u ` + joins + `
		WHERE u.org_id=$1 AND u.ts >= $2 AND u.ts < $3
		GROUP BY ` + group + ` LIMIT 5000`
	out := []UsageRow{}
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, q, org, from, to)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r UsageRow
			if err := rows.Scan(&r.Key, &r.Label, &r.Department, &r.Requests, &r.Calls, &r.InputTokens, &r.OutputTokens, &r.CostUSD); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

// ---- departments and limits ----

func (p *PgStore) AgentSpend(ctx context.Context, org string, monthStart time.Time) ([]AgentSpend, error) {
	out := []AgentSpend{}
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT a.id, a.name, a.role, COALESCE(a.department,''),
				COALESCE((SELECT sum(u.cost_usd) FROM usage_entries u WHERE u.org_id=a.org_id AND u.agent_id=a.id AND u.ts >= $2),0),
				COALESCE((SELECT c.cap_usd FROM budget_caps c WHERE c.org_id=a.org_id AND c.scope='agent' AND c.scope_id=a.id),0)
			FROM agents a WHERE a.org_id=$1 ORDER BY a.name`, org, monthStart)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a AgentSpend
			if err := rows.Scan(&a.ID, &a.Name, &a.Role, &a.Department, &a.MonthSpendUSD, &a.CapUSD); err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}

func (p *PgStore) DepartmentLimits(ctx context.Context, org string) (map[string]float64, error) {
	out := map[string]float64{}
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT department, monthly_usd FROM department_limits WHERE org_id=$1`, org)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var d string
			var v float64
			if err := rows.Scan(&d, &v); err != nil {
				return err
			}
			out[d] = v
		}
		return rows.Err()
	})
	return out, err
}

func (p *PgStore) SetDepartment(ctx context.Context, org, agentID, department string) error {
	return p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE agents SET department = NULLIF($3,'') WHERE org_id=$1 AND id=$2`, org, agentID, department)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
}

func (p *PgStore) SetDepartmentLimit(ctx context.Context, org, department string, usd float64, actor string) error {
	return p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		if usd <= 0 {
			_, err := tx.Exec(ctx, `DELETE FROM department_limits WHERE org_id=$1 AND department=$2`, org, department)
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO department_limits (org_id, department, monthly_usd, updated_by, updated_at) VALUES ($1,$2,$3,$4,now())
			ON CONFLICT (org_id, department) DO UPDATE SET monthly_usd=EXCLUDED.monthly_usd, updated_by=EXCLUDED.updated_by, updated_at=now()`,
			org, department, usd, actor)
		return err
	})
}
