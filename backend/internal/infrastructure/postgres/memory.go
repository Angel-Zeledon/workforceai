package postgres

import (
	"context"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/memory"
)

// MemoryStore implements memory.DocStore on Postgres (migration 550_memory.sql).
// Every call runs through WithOrgTx, so Row-Level Security isolates the
// organizations; the org_id filters are a second barrier.
type MemoryStore struct{ S *Store }

var _ memory.DocStore = (*MemoryStore)(nil)

const memCols = `id, kind, ref, title, body, agent_id, scope, project_id, project_name, request_id, source_ref, suppressed, created_at, updated_at`

func scanMemDoc(r scanner) (memory.Doc, error) {
	var d memory.Doc
	err := r.Scan(&d.ID, &d.Kind, &d.Ref, &d.Title, &d.Body, &d.AgentID, &d.Scope, &d.ProjectID, &d.ProjectName, &d.RequestID, &d.SourceRef, &d.Suppressed, &d.CreatedAt, &d.UpdatedAt)
	return d, err
}

// Upsert implements memory.DocStore. A suppressed document stays suppressed
// (the person's deletion survives a re-index or a backfill).
func (m *MemoryStore) Upsert(ctx context.Context, org string, d memory.Doc) error {
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	if d.UpdatedAt.IsZero() {
		d.UpdatedAt = d.CreatedAt
	}
	return m.S.exec(ctx, org, `INSERT INTO memory_docs (org_id, id, kind, ref, title, body, agent_id, scope, project_id, project_name, request_id, source_ref, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (org_id, ref) DO UPDATE SET title=EXCLUDED.title, body=EXCLUDED.body, agent_id=EXCLUDED.agent_id, scope=EXCLUDED.scope,
			project_id=EXCLUDED.project_id, project_name=EXCLUDED.project_name, request_id=EXCLUDED.request_id, source_ref=EXCLUDED.source_ref,
			updated_at=EXCLUDED.updated_at`,
		org, d.ID, d.Kind, d.Ref, d.Title, d.Body, d.AgentID, d.Scope, d.ProjectID, d.ProjectName, d.RequestID, d.SourceRef, d.CreatedAt, d.UpdatedAt)
}

// Search implements memory.DocStore. The terms are letters and digits only
// (textfold.Terms) joined with OR; the score is ts_rank_cd normalized into [0,1).
func (m *MemoryStore) Search(ctx context.Context, org string, p memory.SearchParams) ([]memory.DocHit, error) {
	if len(p.Terms) == 0 {
		return []memory.DocHit{}, nil
	}
	limit := p.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return many(ctx, m.S, org, func(r scanner) (memory.DocHit, error) {
		var h memory.DocHit
		err := r.Scan(&h.Doc.ID, &h.Doc.Kind, &h.Doc.Ref, &h.Doc.Title, &h.Doc.Body, &h.Doc.AgentID, &h.Doc.Scope, &h.Doc.ProjectID, &h.Doc.ProjectName,
			&h.Doc.RequestID, &h.Doc.SourceRef, &h.Doc.Suppressed, &h.Doc.CreatedAt, &h.Doc.UpdatedAt, &h.Score)
		return h, err
	}, `WITH qq AS (SELECT to_tsquery('spanish'::regconfig, $2) AS q)
		SELECT `+memCols+`, ts_rank_cd(tsv, qq.q, 32)::float8 AS score
		FROM memory_docs, qq
		WHERE org_id=$1 AND NOT suppressed AND tsv @@ qq.q
		  AND (scope='org' OR agent_id=$3)
		  AND (NOT $5 OR agent_id=$3)
		  AND ($4='' OR request_id<>$4)
		ORDER BY score DESC, ref LIMIT $6`,
		org, strings.Join(p.Terms, " | "), p.AgentID, p.ExcludeRequest, p.OnlyAgent, limit)
}

// List implements memory.DocStore: notes first, then the rest, newest first.
func (m *MemoryStore) List(ctx context.Context, org, agentID string, limit int) ([]memory.Doc, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	return many(ctx, m.S, org, scanMemDoc,
		`SELECT `+memCols+` FROM memory_docs WHERE org_id=$1 AND agent_id=$2 AND NOT suppressed
		 ORDER BY (kind='note') DESC, created_at DESC, id LIMIT $3`, org, agentID, limit)
}

// GetByRef implements memory.DocStore.
func (m *MemoryStore) GetByRef(ctx context.Context, org, ref string) (memory.Doc, error) {
	d, err := one(ctx, m.S, org, scanMemDoc, `SELECT `+memCols+` FROM memory_docs WHERE org_id=$1 AND ref=$2`, org, ref)
	return d, mapErr(err)
}

// Get implements memory.DocStore.
func (m *MemoryStore) Get(ctx context.Context, org, id string) (memory.Doc, error) {
	d, err := one(ctx, m.S, org, scanMemDoc, `SELECT `+memCols+` FROM memory_docs WHERE org_id=$1 AND id=$2`, org, id)
	return d, mapErr(err)
}

// Suppress implements memory.DocStore.
func (m *MemoryStore) Suppress(ctx context.Context, org, id, by string, at time.Time) error {
	tag, err := m.S.execTag(ctx, org, `UPDATE memory_docs SET suppressed=true, suppressed_by=$3, suppressed_at=$4 WHERE org_id=$1 AND id=$2`, org, id, by, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Clear implements memory.DocStore (demo reset).
func (m *MemoryStore) Clear(ctx context.Context, org string) error {
	return m.S.exec(ctx, org, `DELETE FROM memory_docs WHERE org_id=$1`, org)
}
