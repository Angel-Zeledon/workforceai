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

const memCols = `id, kind, ref, title, body, agent_id, scope, project_id, project_name, request_id, source_ref, suppressed, created_at, updated_at, embed_model, embed_hash, customer_id`

// memCustomerFilter is the customer isolation of memory.VisibleToCustomer in SQL (B12, migration 797):
// with a customer ($N non-empty) only that customer's documents and the notes a person wrote without one.
func memCustomerFilter(n string) string {
	return `(` + n + `='' OR customer_id=` + n + ` OR (customer_id='' AND kind='note' AND starts_with(id, '` + memory.UserNotePrefix + `')))`
}

func scanMemDoc(r scanner) (memory.Doc, error) {
	var d memory.Doc
	err := r.Scan(&d.ID, &d.Kind, &d.Ref, &d.Title, &d.Body, &d.AgentID, &d.Scope, &d.ProjectID, &d.ProjectName, &d.RequestID, &d.SourceRef, &d.Suppressed, &d.CreatedAt, &d.UpdatedAt, &d.EmbedModel, &d.EmbedHash, &d.CustomerID)
	return d, err
}

// Upsert implements memory.DocStore. A suppressed document stays suppressed
// (the person's deletion survives a re-index or a backfill). A vector in d.Vec
// replaces the stored one; without one, the stored vector is kept while the
// embedded text is unchanged and dropped when it changed (never stale).
func (m *MemoryStore) Upsert(ctx context.Context, org string, d memory.Doc) error {
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	if d.UpdatedAt.IsZero() {
		d.UpdatedAt = d.CreatedAt
	}
	var vec []float32 // NULL unless the writer embedded the document
	if len(d.Vec) > 0 {
		vec = d.Vec
	}
	return m.S.exec(ctx, org, `INSERT INTO memory_docs (org_id, id, kind, ref, title, body, agent_id, scope, project_id, project_name, request_id, source_ref, created_at, updated_at, embedding, embed_model, embed_hash, customer_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		ON CONFLICT (org_id, ref) DO UPDATE SET title=EXCLUDED.title, body=EXCLUDED.body, agent_id=EXCLUDED.agent_id, scope=EXCLUDED.scope,
			project_id=EXCLUDED.project_id, project_name=EXCLUDED.project_name, request_id=EXCLUDED.request_id, source_ref=EXCLUDED.source_ref,
			customer_id=EXCLUDED.customer_id, updated_at=EXCLUDED.updated_at,
			embedding=CASE WHEN EXCLUDED.embedding IS NOT NULL THEN EXCLUDED.embedding
			               WHEN memory_docs.embed_hash=EXCLUDED.embed_hash THEN memory_docs.embedding END,
			embed_model=CASE WHEN EXCLUDED.embedding IS NOT NULL THEN EXCLUDED.embed_model
			                 WHEN memory_docs.embed_hash=EXCLUDED.embed_hash THEN memory_docs.embed_model ELSE '' END,
			embed_hash=EXCLUDED.embed_hash`,
		org, d.ID, d.Kind, d.Ref, d.Title, d.Body, d.AgentID, d.Scope, d.ProjectID, d.ProjectName, d.RequestID, d.SourceRef, d.CreatedAt, d.UpdatedAt,
		vec, d.EmbedModel, memory.ContentHash(d), d.CustomerID)
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
			&h.Doc.RequestID, &h.Doc.SourceRef, &h.Doc.Suppressed, &h.Doc.CreatedAt, &h.Doc.UpdatedAt, &h.Doc.EmbedModel, &h.Doc.EmbedHash, &h.Doc.CustomerID, &h.Score)
		return h, err
	}, `WITH qq AS (SELECT to_tsquery('spanish'::regconfig, $2) AS q)
		SELECT `+memCols+`, ts_rank_cd(tsv, qq.q, 32)::float8 AS score
		FROM memory_docs, qq
		WHERE org_id=$1 AND NOT suppressed AND tsv @@ qq.q
		  AND (scope='org' OR agent_id=$3)
		  AND (NOT $5 OR agent_id=$3)
		  AND ($4='' OR request_id<>$4)
		  AND `+memCustomerFilter("$7")+`
		ORDER BY score DESC, ref LIMIT $6`,
		org, strings.Join(p.Terms, " | "), p.AgentID, p.ExcludeRequest, p.OnlyAgent, limit, p.CustomerID)
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

var _ memory.CustomerRetagger = (*MemoryStore)(nil)

// RetagCustomer implements memory.CustomerRetagger (B12): moves the documents of one request (or one document) from
// one customer tag to another; only the documents tagged `from` change.
func (m *MemoryStore) RetagCustomer(ctx context.Context, org string, sel memory.RetagSelector, from, to string) (int, error) {
	col, ref := "request_id", sel.RequestID
	if sel.DocID != "" {
		col, ref = "id", sel.DocID
	}
	if ref == "" {
		return 0, nil
	}
	tag, err := m.S.execTag(ctx, org, `UPDATE memory_docs SET customer_id=$4, updated_at=now() WHERE org_id=$1 AND `+col+`=$2 AND customer_id=$3`, org, ref, from, to)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// Clear implements memory.DocStore (demo reset).
func (m *MemoryStore) Clear(ctx context.Context, org string) error {
	return m.S.exec(ctx, org, `DELETE FROM memory_docs WHERE org_id=$1`, org)
}

var _ memory.VecStore = (*MemoryStore)(nil)

// Candidates implements memory.VecStore: the vectors the agent may see, newest
// first, capped; the cosine runs in the backend (no pgvector).
func (m *MemoryStore) Candidates(ctx context.Context, org string, q memory.VecQuery) ([]memory.VecCandidate, error) {
	limit := q.Limit
	if limit <= 0 || limit > 20000 {
		limit = memory.DefaultCandidate
	}
	return many(ctx, m.S, org, func(r scanner) (memory.VecCandidate, error) {
		var c memory.VecCandidate
		err := r.Scan(&c.ID, &c.Vec)
		return c, err
	}, `SELECT id, embedding FROM memory_docs
		WHERE org_id=$1 AND NOT suppressed AND embedding IS NOT NULL AND embed_model=$2
		  AND (scope='org' OR agent_id=$3)
		  AND (NOT $5 OR agent_id=$3)
		  AND ($4='' OR request_id<>$4)
		  AND `+memCustomerFilter("$7")+`
		ORDER BY updated_at DESC, id LIMIT $6`,
		org, q.Model, q.AgentID, q.ExcludeRequest, q.OnlyAgent, limit, q.CustomerID)
}

// Unembedded implements memory.VecStore.
func (m *MemoryStore) Unembedded(ctx context.Context, org, model string, limit int) ([]memory.Doc, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	return many(ctx, m.S, org, scanMemDoc,
		`SELECT `+memCols+` FROM memory_docs WHERE org_id=$1 AND NOT suppressed AND (embedding IS NULL OR embed_model<>$2)
		 ORDER BY updated_at DESC, id LIMIT $3`, org, model, limit)
}

// SetEmbedding implements memory.VecStore. A row rewritten since the vector
// was computed (its embed_hash is set by every upsert) is left alone.
func (m *MemoryStore) SetEmbedding(ctx context.Context, org, id, hash, model string, vec []float32) error {
	return m.S.exec(ctx, org, `UPDATE memory_docs SET embedding=$4, embed_model=$5, embed_hash=$3
		WHERE org_id=$1 AND id=$2 AND (embed_hash='' OR embed_hash=$3)`, org, id, hash, vec, model)
}
