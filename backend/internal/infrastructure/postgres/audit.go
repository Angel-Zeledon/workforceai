package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/audit"
	"aiworkforce/backend/internal/domain"
)

// Tamper-evident audit trail (migration 250). Every append runs in one
// org-scoped transaction that locks the organization's chain head, so
// concurrent writers produce a single, gap-free sequence.

// AddAudit appends an entry to the organization's hash chain.
func (s *Store) AddAudit(ctx context.Context, orgID string, a domain.AuditLog) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	return s.WithOrgTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO audit_chain_heads (org_id) VALUES ($1) ON CONFLICT (org_id) DO NOTHING`, orgID); err != nil {
			return err
		}
		var head domain.AuditHead
		var lastTS *time.Time
		if err := tx.QueryRow(ctx, `SELECT seq, hash, last_ts FROM audit_chain_heads WHERE org_id=$1 FOR UPDATE`, orgID).Scan(&head.Seq, &head.Hash, &lastTS); err != nil {
			return err
		}
		if lastTS != nil {
			head.TS = *lastTS
		}
		e := audit.Seal(orgID, head, a)
		if _, err := tx.Exec(ctx, `INSERT INTO audit_logs (id, org_id, actor, action, entity, entity_id, details, ts, seq, prev_hash, hash, request_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			e.ID, orgID, e.Actor, e.Action, e.Entity, e.EntityID, jb(e.Details), e.TS, e.Seq, e.PrevHash, e.Hash, e.RequestID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE audit_chain_heads SET seq=$2, hash=$3, last_ts=$4, updated_at=now() WHERE org_id=$1`, orgID, e.Seq, e.Hash, e.TS)
		return err
	})
}

const auditCols = `id, org_id, actor, action, entity, entity_id, details, ts, seq, prev_hash, hash, request_id`

func scanAudit(r scanner) (domain.AuditLog, error) {
	var a domain.AuditLog
	var details []byte
	var seq *int64
	var prev, hash *string
	if err := r.Scan(&a.ID, &a.OrgID, &a.Actor, &a.Action, &a.Entity, &a.EntityID, &details, &a.TS, &seq, &prev, &hash, &a.RequestID); err != nil {
		return a, err
	}
	a.TS = a.TS.UTC()
	unmarshal(details, &a.Details)
	if seq != nil {
		a.Seq = *seq
	}
	if prev != nil {
		a.PrevHash = *prev
	}
	if hash != nil {
		a.Hash = *hash
	}
	return a, nil
}

// QueryAudit filters and pages the audit trail (keyset on ts, id).
func (s *Store) QueryAudit(ctx context.Context, orgID string, q domain.AuditQuery) (domain.AuditPage, error) {
	q = audit.NormalizeQuery(q)
	where, args := []string{"org_id=$1"}, []any{orgID}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", fmt.Sprintf("$%d", len(args))))
	}
	if q.From != nil {
		add("ts >= ?", q.From.UTC())
	}
	if q.To != nil {
		add("ts < ?", q.To.UTC())
	}
	if q.UpToSeq != nil {
		add("(seq IS NULL OR seq <= ?)", *q.UpToSeq)
	}
	if q.Actor != "" {
		add("actor = ?", q.Actor)
	}
	if q.Entity != "" {
		add("entity = ?", q.Entity)
	}
	if q.RequestID != "" {
		add("request_id = ?", q.RequestID)
	}
	if p, ok := strings.CutSuffix(q.Action, "*"); ok {
		esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(p)
		add(`action LIKE ? ESCAPE '\'`, esc+"%")
	} else if q.Action != "" {
		add("action = ?", q.Action)
	}
	if q.Cursor != "" {
		c, err := audit.DecodeCursor(q.Cursor)
		if err != nil {
			return domain.AuditPage{}, err
		}
		args = append(args, c.TS, c.ID)
		op := ">"
		if q.Desc {
			op = "<"
		}
		where = append(where, fmt.Sprintf("(ts, id) %s ($%d, $%d)", op, len(args)-1, len(args)))
	}
	order := "ts, id"
	if q.Desc {
		order = "ts DESC, id DESC"
	}
	args = append(args, q.Limit+1)
	rows, err := many(ctx, s, orgID, scanAudit,
		`SELECT `+auditCols+` FROM audit_logs WHERE `+strings.Join(where, " AND ")+` ORDER BY `+order+fmt.Sprintf(" LIMIT $%d", len(args)), args...)
	if err != nil {
		return domain.AuditPage{}, err
	}
	page := domain.AuditPage{Items: rows}
	if len(rows) > q.Limit {
		page.Items = rows[:q.Limit]
		page.NextCursor = audit.CursorOf(page.Items[q.Limit-1]).Encode()
	}
	return page, nil
}

// AuditChain returns chained entries with seq > after, ascending.
func (s *Store) AuditChain(ctx context.Context, orgID string, after int64, limit int) ([]domain.AuditLog, error) {
	if limit <= 0 {
		limit = domain.MaxAuditPage
	}
	return many(ctx, s, orgID, scanAudit,
		`SELECT `+auditCols+` FROM audit_logs WHERE org_id=$1 AND seq IS NOT NULL AND seq > $2 ORDER BY seq LIMIT $3`, orgID, after, limit)
}

// AuditHead returns the recorded chain head.
func (s *Store) AuditHead(ctx context.Context, orgID string) (domain.AuditHead, bool, error) {
	h, err := one(ctx, s, orgID, func(r scanner) (domain.AuditHead, error) {
		var h domain.AuditHead
		err := r.Scan(&h.Seq, &h.Hash)
		return h, err
	}, `SELECT seq, hash FROM audit_chain_heads WHERE org_id=$1`, orgID)
	if err != nil {
		if mapErr(err) == domain.ErrNotFound {
			return domain.AuditHead{}, false, nil
		}
		return domain.AuditHead{}, false, err
	}
	return h, true, nil
}
