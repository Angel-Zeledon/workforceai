package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/docindex"
)

// DocIndexStore implements docindex.Store (migration 690_doc_index.sql). Every
// call runs in an org-scoped transaction (RLS). Searches join the live drive
// rows: only the current version of files outside the trash is ever a hit, and
// the folder scope is a WHERE clause, i.e. applied before ranking and LIMIT.
type DocIndexStore struct{ S *Store }

var _ docindex.Store = (*DocIndexStore)(nil)

// liveJoin restricts chunks to the current version of live files in live folders.
const liveJoin = ` FROM doc_chunks c
	JOIN drive_files f ON f.org_id=c.org_id AND f.id=c.file_id AND f.current_version=c.version AND f.deleted_at IS NULL
	JOIN drive_folders fo ON fo.org_id=f.org_id AND fo.id=f.folder_id AND fo.deleted_at IS NULL `

// scopeSQL is the folder scope on parameter $n (text[]) with the flag $n+1
// (scoped): unscoped = every folder (a person with files:read).
func scopeSQL(n string, flag string) string {
	return `(NOT ` + flag + `::bool OR EXISTS (SELECT 1 FROM unnest(` + n + `::text[]) u(p)
		WHERE lower(fo.path) = lower(u.p) OR left(lower(fo.path), length(u.p)+1) = lower(u.p) || '/' OR lower(fo.path || '/' || f.name) = lower(u.p)))`
}

func scopeArgs(under []string) ([]string, bool) {
	if under == nil {
		return []string{}, false
	}
	return under, true
}

func (d *DocIndexStore) Settings(ctx context.Context, org string) (docindex.Settings, error) {
	var st docindex.Settings
	err := d.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var at time.Time
		err := tx.QueryRow(ctx, `SELECT enabled, updated_by, updated_at FROM doc_index_settings WHERE org_id=$1`, org).Scan(&st.Enabled, &st.UpdatedBy, &at)
		if err == nil {
			st.UpdatedAt = &at
		}
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return docindex.Settings{Enabled: true}, nil
	}
	return st, err
}

func (d *DocIndexStore) SetSettings(ctx context.Context, org string, s docindex.Settings) error {
	return d.S.exec(ctx, org, `INSERT INTO doc_index_settings (org_id, enabled, updated_by, updated_at) VALUES ($1,$2,$3,now())
		ON CONFLICT (org_id) DO UPDATE SET enabled=EXCLUDED.enabled, updated_by=EXCLUDED.updated_by, updated_at=now()`, org, s.Enabled, s.UpdatedBy)
}

const requeue = ` ON CONFLICT (org_id, file_id) DO UPDATE SET version=EXCLUDED.version, status='pending', reason='', attempts=0, lease_until=NULL, updated_at=now() `

func (d *DocIndexStore) Enqueue(ctx context.Context, org, fileID string, version int) error {
	return d.S.exec(ctx, org, `INSERT INTO doc_index_state (org_id, file_id, version, status) VALUES ($1,$2,$3,'pending')`+requeue+
		`WHERE doc_index_state.version <> EXCLUDED.version OR doc_index_state.status = 'failed'`, org, fileID, version)
}

func (d *DocIndexStore) Reconcile(ctx context.Context, org string, limit int) (int, error) {
	n := 0
	err := d.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO doc_index_state (org_id, file_id, version, status)
			SELECT f.org_id, f.id, f.current_version, 'pending'
			FROM drive_files f
			JOIN drive_folders fo ON fo.org_id=f.org_id AND fo.id=f.folder_id AND fo.deleted_at IS NULL
			LEFT JOIN doc_index_state s ON s.org_id=f.org_id AND s.file_id=f.id
			WHERE f.org_id=$1 AND f.deleted_at IS NULL AND (s.file_id IS NULL OR s.version <> f.current_version)
			ORDER BY f.updated_at DESC LIMIT $2`+requeue, org, limit)
		if err != nil {
			return err
		}
		n = int(tag.RowsAffected())
		// files in the trash (or whose folder is): chunks and state go away
		const dead = `SELECT s.file_id FROM doc_index_state s WHERE s.org_id=$1 AND NOT EXISTS (
			SELECT 1 FROM drive_files f JOIN drive_folders fo ON fo.org_id=f.org_id AND fo.id=f.folder_id
			WHERE f.org_id=s.org_id AND f.id=s.file_id AND f.deleted_at IS NULL AND fo.deleted_at IS NULL)`
		if _, err := tx.Exec(ctx, `DELETE FROM doc_chunks WHERE org_id=$1 AND file_id IN (`+dead+`)`, org); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM doc_index_state WHERE org_id=$1 AND file_id IN (`+dead+`)`, org); err != nil {
			return err
		}
		// a worker that died 5 times on the same file: stop retrying it
		_, err = tx.Exec(ctx, `UPDATE doc_index_state SET status='failed', reason='error', lease_until=NULL, updated_at=now()
			WHERE org_id=$1 AND status='indexing' AND lease_until < now() AND attempts >= 5`, org)
		return err
	})
	return n, err
}

func (d *DocIndexStore) Claim(ctx context.Context, org string, n int, lease time.Duration) ([]docindex.Job, error) {
	return many(ctx, d.S, org, func(r scanner) (docindex.Job, error) {
		var j docindex.Job
		err := r.Scan(&j.FileID, &j.Version, &j.Attempts)
		return j, err
	}, `UPDATE doc_index_state s SET status='indexing', lease_until=now() + make_interval(secs => $3), attempts=s.attempts+1, updated_at=now()
		WHERE (s.org_id, s.file_id) IN (
			SELECT org_id, file_id FROM doc_index_state
			WHERE org_id=$1 AND (status='pending' OR status='indexing' AND lease_until < now() AND attempts < 5)
			ORDER BY updated_at LIMIT $2 FOR UPDATE SKIP LOCKED)
		RETURNING s.file_id, s.version, s.attempts`, org, n, lease.Seconds())
}

func nullInt(v int) *int {
	if v <= 0 {
		return nil
	}
	return &v
}

func (d *DocIndexStore) Finish(ctx context.Context, org string, j docindex.Job, chunks []docindex.StoredChunk, out docindex.Outcome) error {
	return d.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var ver int
		var status string
		err := tx.QueryRow(ctx, `SELECT version, status FROM doc_index_state WHERE org_id=$1 AND file_id=$2 FOR UPDATE`, org, j.FileID).Scan(&ver, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // dropped meanwhile (trash)
		}
		if err != nil {
			return err
		}
		if ver != j.Version || status != docindex.StatusIndexing {
			return nil // a newer version was queued: this result is stale
		}
		if _, err := tx.Exec(ctx, `DELETE FROM doc_chunks WHERE org_id=$1 AND file_id=$2`, org, j.FileID); err != nil {
			return err
		}
		if out.Status == docindex.StatusIndexed && len(chunks) > 0 {
			b := &pgx.Batch{}
			for _, c := range chunks {
				var vec []float32
				if len(c.Vec) > 0 {
					vec = c.Vec
				}
				b.Queue(`INSERT INTO doc_chunks (org_id, file_id, version, chunk_idx, page, line_no, start_off, end_off, heading, text, lang, tsv, content_hash, embedding, embed_model)
					VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::text,$10::text,$11::text,
						setweight(to_tsvector('simple'::regconfig, $9), 'A') || to_tsvector($11::text::regconfig, $10::text) || to_tsvector('simple'::regconfig, $10),
						$12,$13,$14)`,
					org, j.FileID, j.Version, c.Idx, nullInt(c.Page), nullInt(c.Line), c.Start, c.End, c.Heading, c.Text, docindex.ValidLang(c.Lang), c.Hash, vec, c.Model)
			}
			if err := tx.SendBatch(ctx, b).Close(); err != nil {
				return err
			}
		}
		reason := out.Reason
		if len(reason) > 60 {
			reason = reason[:60]
		}
		_, err = tx.Exec(ctx, `UPDATE doc_index_state SET status=$3, reason=$4, chunks=$5, embedded=$6, embed_model=$7, lease_until=NULL, updated_at=now()
			WHERE org_id=$1 AND file_id=$2`, org, j.FileID, out.Status, reason, out.Chunks, out.Embedded, out.Model)
		return err
	})
}

func (d *DocIndexStore) Drop(ctx context.Context, org, fileID string) error {
	return d.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM doc_chunks WHERE org_id=$1 AND file_id=$2`, org, fileID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM doc_index_state WHERE org_id=$1 AND file_id=$2`, org, fileID)
		return err
	})
}

func (d *DocIndexStore) DropAll(ctx context.Context, org string) error {
	return d.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM doc_chunks WHERE org_id=$1`, org); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM doc_index_state WHERE org_id=$1`, org)
		return err
	})
}

func (d *DocIndexStore) PreviousVectors(ctx context.Context, org, fileID, model string) (map[string][]float32, error) {
	type hv struct {
		h string
		v []float32
	}
	rows, err := many(ctx, d.S, org, func(r scanner) (hv, error) {
		var x hv
		err := r.Scan(&x.h, &x.v)
		return x, err
	}, `SELECT content_hash, embedding FROM doc_chunks WHERE org_id=$1 AND file_id=$2 AND embed_model=$3 AND embedding IS NOT NULL`, org, fileID, model)
	out := make(map[string][]float32, len(rows))
	for _, x := range rows {
		out[x.h] = x.v
	}
	return out, err
}

func (d *DocIndexStore) RequeueUnembedded(ctx context.Context, org, model string, limit int) (int, error) {
	tag, err := d.S.execTag(ctx, org, `UPDATE doc_index_state SET status='pending', attempts=0, updated_at=now()
		WHERE (org_id, file_id) IN (SELECT org_id, file_id FROM doc_index_state
			WHERE org_id=$1 AND status='indexed' AND chunks > 0 AND (embedded < chunks OR embed_model <> $2)
			  AND (reason NOT IN ('embeddings_failed','embeddings_daily_cap','embeddings_budget') OR updated_at < now() - interval '1 hour')
			ORDER BY updated_at LIMIT $3)`, org, model, limit)
	return int(tag.RowsAffected()), err
}

func (d *DocIndexStore) ChunkCount(ctx context.Context, org string) (int, error) {
	return one(ctx, d.S, org, func(r scanner) (int, error) {
		var n int
		err := r.Scan(&n)
		return n, err
	}, `SELECT COALESCE(sum(chunks),0)::int FROM doc_index_state WHERE org_id=$1 AND status='indexed'`, org)
}

const passageCols = `c.file_id, c.version, c.chunk_idx, fo.path || '/' || f.name, f.name, COALESCE(c.page,0), COALESCE(c.line_no,0),
	c.start_off, c.end_off, c.heading, c.text, f.updated_by`

func scanPassage(r scanner, rank bool) (docindex.Passage, error) {
	var p docindex.Passage
	var by []byte
	dest := []any{&p.Key.FileID, &p.Key.Version, &p.Key.Idx, &p.Path, &p.Name, &p.Page, &p.Line, &p.Start, &p.End, &p.Heading, &p.Text, &by}
	if rank {
		dest = append(dest, &p.Rank)
	}
	err := r.Scan(dest...)
	unmarshal(by, &p.UpdatedBy)
	return p, err
}

func (d *DocIndexStore) Keyword(ctx context.Context, org string, q docindex.KeywordQuery) ([]docindex.Passage, error) {
	if q.Under != nil && len(q.Under) == 0 {
		return []docindex.Passage{}, nil
	}
	under, scoped := scopeArgs(q.Under)
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// The exact words ('simple') OR the stems of the query language; when the
	// language is unknown, the stems of every supported language. The configs
	// are constants from ValidLang (never user text).
	langs := []string{docindex.ValidLang(q.Lang)}
	if langs[0] == "simple" {
		langs = []string{"spanish", "english", "portuguese"}
	}
	tsq := `websearch_to_tsquery('simple'::regconfig, $2)`
	for _, l := range langs {
		tsq += ` || websearch_to_tsquery('` + l + `'::regconfig, $2)`
	}
	return many(ctx, d.S, org, func(r scanner) (docindex.Passage, error) { return scanPassage(r, true) },
		`WITH qq AS (SELECT `+tsq+` AS q)
		SELECT `+passageCols+`, ts_rank_cd(c.tsv, qq.q, 32)::float8 AS rank`+liveJoin+`, qq
		WHERE c.org_id=$1 AND c.tsv @@ qq.q AND `+scopeSQL("$3", "$4")+`
		ORDER BY rank DESC, c.file_id, c.chunk_idx LIMIT $5`,
		org, q.Query, under, scoped, limit)
}

// orQuery builds "a:* | b:*" from folded terms (letters and digits only).
func orQuery(terms []string) string {
	var parts []string
	for _, t := range terms {
		ok := t != ""
		for _, r := range t {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127) {
				ok = false
			}
		}
		if ok && len(parts) < 12 {
			parts = append(parts, t+":*")
		}
	}
	return strings.Join(parts, " | ")
}

func (d *DocIndexStore) Candidates(ctx context.Context, org string, q docindex.CandidateQuery) ([]docindex.Candidate, error) {
	if q.Under != nil && len(q.Under) == 0 {
		return []docindex.Candidate{}, nil
	}
	under, scoped := scopeArgs(q.Under)
	limit := q.Limit
	if limit <= 0 || limit > 20000 {
		limit = 3000
	}
	return many(ctx, d.S, org, func(r scanner) (docindex.Candidate, error) {
		var c docindex.Candidate
		err := r.Scan(&c.Key.FileID, &c.Key.Version, &c.Key.Idx, &c.Vec)
		return c, err
	}, `SELECT c.file_id, c.version, c.chunk_idx, c.embedding`+liveJoin+`
		WHERE c.org_id=$1 AND c.embedding IS NOT NULL AND c.embed_model=$2 AND `+scopeSQL("$4", "$5")+`
		ORDER BY (CASE WHEN $3 <> '' AND c.tsv @@ to_tsquery('simple'::regconfig, $3) THEN 0 ELSE 1 END), f.updated_at DESC, c.file_id, c.chunk_idx
		LIMIT $6`, org, q.Model, orQuery(q.OrTerms), under, scoped, limit)
}

func (d *DocIndexStore) Passages(ctx context.Context, org string, keys []docindex.Key, under []string) ([]docindex.Passage, error) {
	if len(keys) == 0 || under != nil && len(under) == 0 {
		return []docindex.Passage{}, nil
	}
	ids, vers, idxs := make([]string, len(keys)), make([]int32, len(keys)), make([]int32, len(keys))
	for i, k := range keys {
		ids[i], vers[i], idxs[i] = k.FileID, int32(k.Version), int32(k.Idx)
	}
	u, scoped := scopeArgs(under)
	return many(ctx, d.S, org, func(r scanner) (docindex.Passage, error) { return scanPassage(r, false) },
		`SELECT `+passageCols+liveJoin+`
		WHERE c.org_id=$1 AND (c.file_id, c.version, c.chunk_idx) IN (SELECT * FROM unnest($2::text[], $3::int[], $4::int[]))
		  AND `+scopeSQL("$5", "$6"), org, ids, vers, idxs, u, scoped)
}

func (d *DocIndexStore) Status(ctx context.Context, org string, under []string) (docindex.StatusCounts, error) {
	u, scoped := scopeArgs(under)
	return one(ctx, d.S, org, func(r scanner) (docindex.StatusCounts, error) {
		var s docindex.StatusCounts
		err := r.Scan(&s.Unindexed, &s.Pending, &s.Indexing, &s.Indexed, &s.Skipped, &s.Failed, &s.Chunks, &s.Embedded, &s.TooLarge)
		return s, err
	}, `SELECT
			count(*) FILTER (WHERE s.file_id IS NULL OR s.version <> f.current_version AND s.status = 'indexed'),
			count(*) FILTER (WHERE s.status = 'pending' AND NOT (s.version <> f.current_version AND s.status = 'indexed')),
			count(*) FILTER (WHERE s.status = 'indexing'),
			count(*) FILTER (WHERE s.status = 'indexed' AND s.version = f.current_version),
			count(*) FILTER (WHERE s.status = 'skipped'),
			count(*) FILTER (WHERE s.status = 'failed'),
			COALESCE(sum(s.chunks) FILTER (WHERE s.status = 'indexed' AND s.version = f.current_version), 0)::int,
			COALESCE(sum(s.embedded) FILTER (WHERE s.status = 'indexed' AND s.version = f.current_version), 0)::int,
			count(*) FILTER (WHERE s.status = 'skipped' AND s.reason = 'too_large')
		FROM drive_files f
		JOIN drive_folders fo ON fo.org_id=f.org_id AND fo.id=f.folder_id AND fo.deleted_at IS NULL
		LEFT JOIN doc_index_state s ON s.org_id=f.org_id AND s.file_id=f.id
		WHERE f.org_id=$1 AND f.deleted_at IS NULL AND `+scopeSQL("$2", "$3"), org, u, scoped)
}

func (d *DocIndexStore) EmbedUsage(ctx context.Context, org string, day time.Time) (int, error) {
	n, err := one(ctx, d.S, org, func(r scanner) (int, error) {
		var n int64
		err := r.Scan(&n)
		return int(n), err
	}, `SELECT tokens FROM doc_embed_usage WHERE org_id=$1 AND day=$2::date`, org, day.UTC().Format(time.DateOnly))
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

func (d *DocIndexStore) AddEmbedUsage(ctx context.Context, org string, day time.Time, tokens int, usd float64) error {
	return d.S.exec(ctx, org, `INSERT INTO doc_embed_usage (org_id, day, tokens, calls, cost_usd) VALUES ($1,$2::date,$3,1,$4)
		ON CONFLICT (org_id, day) DO UPDATE SET tokens=doc_embed_usage.tokens+EXCLUDED.tokens, calls=doc_embed_usage.calls+1,
			cost_usd=doc_embed_usage.cost_usd+EXCLUDED.cost_usd`, org, day.UTC().Format(time.DateOnly), tokens, usd)
}
