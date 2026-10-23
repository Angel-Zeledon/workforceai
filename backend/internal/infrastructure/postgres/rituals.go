package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/rituals"
)

// RitualStore implements rituals.Store on Postgres (migration 793). Every call runs through WithOrgTx, so Row-Level
// Security applies. A scheduled occurrence is claimed by its unique (kind, period key): ON CONFLICT DO NOTHING tells
// the caller another instance already held it.
type RitualStore struct{ S *Store }

var _ rituals.Store = (*RitualStore)(nil)

const ritualCols = `id, kind, origin, period_key, status, skip_reason, locale, window_from, window_to, entries, absent, summary, summary_source, created_by, held_at`

func scanRitual(r scanner) (rituals.Ritual, error) {
	var x rituals.Ritual
	var kind string
	var entries, absent []byte
	if err := r.Scan(&x.ID, &kind, &x.Origin, &x.PeriodKey, &x.Status, &x.SkipReason, &x.Locale, &x.WindowFrom, &x.WindowTo,
		&entries, &absent, &x.Summary, &x.SummarySource, &x.CreatedBy, &x.HeldAt); err != nil {
		return x, err
	}
	x.Kind = rituals.Kind(kind)
	unmarshal(entries, &x.Entries)
	unmarshal(absent, &x.Absent)
	if x.Entries == nil {
		x.Entries = []rituals.Entry{}
	}
	if x.Absent == nil {
		x.Absent = []rituals.Absent{}
	}
	return x, nil
}

func (q *RitualStore) CreateRitual(ctx context.Context, org string, r rituals.Ritual) (bool, error) {
	if r.Entries == nil {
		r.Entries = []rituals.Entry{}
	}
	if r.Absent == nil {
		r.Absent = []rituals.Absent{}
	}
	tag, err := q.S.execTag(ctx, org, `INSERT INTO rituals (org_id, `+ritualCols+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) ON CONFLICT DO NOTHING`,
		org, r.ID, string(r.Kind), r.Origin, r.PeriodKey, r.Status, r.SkipReason, r.Locale, r.WindowFrom, r.WindowTo,
		jb(r.Entries), jb(r.Absent), r.Summary, r.SummarySource, r.CreatedBy, r.HeldAt)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (q *RitualStore) HasScheduled(ctx context.Context, org string, kind rituals.Kind, key string) (bool, error) {
	return one(ctx, q.S, org, func(r scanner) (bool, error) {
		var b bool
		err := r.Scan(&b)
		return b, err
	}, `SELECT EXISTS (SELECT 1 FROM rituals WHERE org_id=$1 AND kind=$2 AND period_key=$3 AND origin='scheduled')`, org, string(kind), key)
}

func (q *RitualStore) SetRitualSummary(ctx context.Context, org, id, summary, source string) error {
	tag, err := q.S.execTag(ctx, org, `UPDATE rituals SET summary=$3, summary_source=$4 WHERE org_id=$1 AND id=$2`, org, id, summary, source)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

func (q *RitualStore) GetRitual(ctx context.Context, org, id string) (rituals.Ritual, error) {
	r, err := one(ctx, q.S, org, scanRitual, `SELECT `+ritualCols+` FROM rituals WHERE org_id=$1 AND id=$2`, org, id)
	return r, mapErr(err)
}

func (q *RitualStore) ListRituals(ctx context.Context, org string, kind rituals.Kind, since time.Time, limit int) ([]rituals.Ritual, error) {
	if limit <= 0 {
		limit = 30
	}
	return many(ctx, q.S, org, scanRitual, `SELECT `+ritualCols+` FROM rituals
		WHERE org_id=$1 AND ($2 = '' OR kind=$2) AND held_at >= $3 ORDER BY held_at DESC, id DESC LIMIT $4`,
		org, string(kind), since, limit)
}

const proposalCols = `id, ritual_id, agent_id, kind, signal, title, text, evidence, status, applied, created_at, resolved_at, resolved_by`

func scanProposal(r scanner) (rituals.Proposal, error) {
	var p rituals.Proposal
	var ev []byte
	if err := r.Scan(&p.ID, &p.RitualID, &p.AgentID, &p.Kind, &p.Signal, &p.Title, &p.Text, &ev, &p.Status, &p.Applied,
		&p.CreatedAt, &p.ResolvedAt, &p.ResolvedBy); err != nil {
		return p, err
	}
	unmarshal(ev, &p.Evidence)
	if p.Evidence == nil {
		p.Evidence = []string{}
	}
	return p, nil
}

func (q *RitualStore) CreateProposals(ctx context.Context, org string, ps []rituals.Proposal) error {
	if len(ps) == 0 {
		return nil
	}
	return q.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		for _, p := range ps {
			if _, err := tx.Exec(ctx, `INSERT INTO ritual_proposals (org_id, `+proposalCols+`)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
				org, p.ID, p.RitualID, p.AgentID, p.Kind, p.Signal, p.Title, p.Text, jb(strs(p.Evidence)), p.Status, p.Applied,
				p.CreatedAt, p.ResolvedAt, p.ResolvedBy); err != nil {
				return err
			}
		}
		return nil
	})
}

func (q *RitualStore) GetProposal(ctx context.Context, org, id string) (rituals.Proposal, error) {
	p, err := one(ctx, q.S, org, scanProposal, `SELECT `+proposalCols+` FROM ritual_proposals WHERE org_id=$1 AND id=$2`, org, id)
	return p, mapErr(err)
}

func (q *RitualStore) ListProposals(ctx context.Context, org string, f rituals.ProposalQuery) ([]rituals.Proposal, error) {
	if f.Limit <= 0 {
		f.Limit = 100
	}
	return many(ctx, q.S, org, scanProposal, `SELECT `+proposalCols+` FROM ritual_proposals
		WHERE org_id=$1 AND ($2 = '' OR status=$2) AND ($3 = '' OR agent_id=$3) AND ($4 = '' OR ritual_id=$4)
		ORDER BY created_at DESC, id DESC LIMIT $5`, org, f.Status, f.AgentID, f.RitualID, f.Limit)
}

// ResolveProposal changes the status only when the row is still in "from" (the WHERE clause is the compare-and-set).
func (q *RitualStore) ResolveProposal(ctx context.Context, org, id, from, to, by string, applied bool, at time.Time) (rituals.Proposal, error) {
	var out rituals.Proposal
	err := q.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var sql string
		var args []any
		if to == rituals.ProposalPending {
			sql = `UPDATE ritual_proposals SET status=$4, resolved_at=NULL, resolved_by='', applied=false
				WHERE org_id=$1 AND id=$2 AND status=$3 RETURNING ` + proposalCols
			args = []any{org, id, from, to}
		} else {
			sql = `UPDATE ritual_proposals SET status=$4, resolved_at=$5, resolved_by=$6, applied=$7
				WHERE org_id=$1 AND id=$2 AND status=$3 RETURNING ` + proposalCols
			args = []any{org, id, from, to, at, by, applied}
		}
		p, err := scanProposal(tx.QueryRow(ctx, sql, args...))
		out = p
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		if _, gerr := q.GetProposal(ctx, org, id); gerr != nil {
			return rituals.Proposal{}, domain.ErrNotFound
		}
		return rituals.Proposal{}, domain.ErrConflict
	}
	return out, err
}

func (q *RitualStore) GetSettings(ctx context.Context, org string) (rituals.Settings, bool, error) {
	raw, err := one(ctx, q.S, org, func(r scanner) ([]byte, error) {
		var b []byte
		err := r.Scan(&b)
		return b, err
	}, `SELECT settings FROM ritual_settings WHERE org_id=$1`, org)
	if err = mapErr(err); errors.Is(err, domain.ErrNotFound) {
		return rituals.Settings{}, false, nil
	} else if err != nil {
		return rituals.Settings{}, false, err
	}
	var s rituals.Settings
	unmarshal(raw, &s)
	return s, true, nil
}

func (q *RitualStore) PutSettings(ctx context.Context, org string, s rituals.Settings) error {
	return q.S.exec(ctx, org, `INSERT INTO ritual_settings (org_id, settings, updated_at) VALUES ($1,$2,now())
		ON CONFLICT (org_id) DO UPDATE SET settings=EXCLUDED.settings, updated_at=now()`, org, jb(s))
}
