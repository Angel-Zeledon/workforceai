package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/linkimports"
)

// LinkImportStore implements linkimports.Store on Postgres (migration 804). Every call runs through WithOrgTx, so
// Row-Level Security applies.
type LinkImportStore struct{ S *Store }

var _ linkimports.Store = (*LinkImportStore)(nil)

const linkImportCols = `id, name, slug, url, folder, customer_id, file_stem, schedule, enabled, header_name, header_sealed, header_hint,
	created_by, created_at, updated_at, next_run_at, last_run_at, last_status, last_code, last_hash, last_file_id, last_path, last_version,
	last_data_at, run_count`

func scanLinkImport(r scanner) (linkimports.LinkImport, error) {
	var li linkimports.LinkImport
	err := r.Scan(&li.ID, &li.Name, &li.Slug, &li.URL, &li.Folder, &li.CustomerID, &li.FileStem, &li.Schedule, &li.Enabled, &li.HeaderName,
		&li.HeaderSealed, &li.HeaderHint, &li.CreatedBy, &li.CreatedAt, &li.UpdatedAt, &li.NextRunAt, &li.LastRunAt, &li.LastStatus, &li.LastCode,
		&li.LastHash, &li.LastFileID, &li.LastPath, &li.LastVersion, &li.LastDataAt, &li.RunCount)
	if len(li.HeaderSealed) == 0 {
		li.HeaderSealed = nil
	}
	return li, err
}

func sealedOrNil(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func (p *LinkImportStore) Create(ctx context.Context, org string, li linkimports.LinkImport) error {
	err := p.S.exec(ctx, org, `INSERT INTO link_imports (org_id, `+linkImportCols+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26)`,
		org, li.ID, li.Name, li.Slug, li.URL, li.Folder, li.CustomerID, li.FileStem, li.Schedule, li.Enabled, li.HeaderName, sealedOrNil(li.HeaderSealed),
		li.HeaderHint, li.CreatedBy, li.CreatedAt, li.UpdatedAt, li.NextRunAt, li.LastRunAt, li.LastStatus, li.LastCode, li.LastHash, li.LastFileID,
		li.LastPath, li.LastVersion, li.LastDataAt, li.RunCount)
	if isUniqueViolation(err) {
		return domain.ErrConflict
	}
	return err
}

func (p *LinkImportStore) Update(ctx context.Context, org string, li linkimports.LinkImport) error {
	tag, err := p.S.execTag(ctx, org, `UPDATE link_imports SET name=$3, url=$4, file_stem=$5, schedule=$6, enabled=$7, header_name=$8,
		header_sealed=$9, header_hint=$10, next_run_at=$11, updated_at=$12 WHERE org_id=$1 AND id=$2`,
		org, li.ID, li.Name, li.URL, li.FileStem, li.Schedule, li.Enabled, li.HeaderName, sealedOrNil(li.HeaderSealed), li.HeaderHint, li.NextRunAt, li.UpdatedAt)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

func (p *LinkImportStore) Get(ctx context.Context, org, id string) (linkimports.LinkImport, error) {
	li, err := one(ctx, p.S, org, scanLinkImport, `SELECT `+linkImportCols+` FROM link_imports WHERE org_id=$1 AND id=$2`, org, id)
	return li, mapErr(err)
}

func (p *LinkImportStore) List(ctx context.Context, org string) ([]linkimports.LinkImport, error) {
	return many(ctx, p.S, org, scanLinkImport, `SELECT `+linkImportCols+` FROM link_imports WHERE org_id=$1 ORDER BY created_at DESC, id DESC`, org)
}

func (p *LinkImportStore) Delete(ctx context.Context, org, id string) error {
	tag, err := p.S.execTag(ctx, org, `DELETE FROM link_imports WHERE org_id=$1 AND id=$2`, org, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

func (p *LinkImportStore) Due(ctx context.Context, org string, now time.Time) ([]linkimports.LinkImport, error) {
	return many(ctx, p.S, org, scanLinkImport, `SELECT `+linkImportCols+` FROM link_imports
		WHERE org_id=$1 AND enabled AND schedule <> 'once' AND next_run_at IS NOT NULL AND next_run_at <= $2
		ORDER BY next_run_at, id LIMIT 200`, org, now)
}

func (p *LinkImportStore) Claim(ctx context.Context, org, id string, expected, next time.Time) (bool, error) {
	tag, err := p.S.execTag(ctx, org, `UPDATE link_imports SET next_run_at=$4 WHERE org_id=$1 AND id=$2 AND next_run_at=$3`, org, id, expected, next)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

const linkRunCols = `id, import_id, trigger, actor, status, code, http_status, started_at, duration_ms, size_bytes, mime, sha256, file_id, path, version`

func scanLinkRun(r scanner) (linkimports.Run, error) {
	var x linkimports.Run
	err := r.Scan(&x.ID, &x.ImportID, &x.Trigger, &x.Actor, &x.Status, &x.Code, &x.HTTPStatus, &x.StartedAt, &x.DurationMS, &x.Size, &x.Mime,
		&x.Hash, &x.FileID, &x.Path, &x.Version)
	return x, err
}

func (p *LinkImportStore) RecordRun(ctx context.Context, org string, r linkimports.Run) error {
	return p.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE link_imports SET last_run_at=$3, last_status=$4, last_code=$5, run_count=run_count+1,
			last_hash    = CASE WHEN $4 IN ('ok','unchanged') THEN $6 ELSE last_hash END,
			last_file_id = CASE WHEN $4 IN ('ok','unchanged') THEN $7 ELSE last_file_id END,
			last_path    = CASE WHEN $4 IN ('ok','unchanged') THEN $8 ELSE last_path END,
			last_version = CASE WHEN $4 IN ('ok','unchanged') THEN $9 ELSE last_version END,
			last_data_at = CASE WHEN $4 = 'ok' THEN $3 ELSE last_data_at END
			WHERE org_id=$1 AND id=$2`, org, r.ImportID, r.StartedAt, r.Status, r.Code, r.Hash, r.FileID, r.Path, r.Version)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		if _, err := tx.Exec(ctx, `INSERT INTO link_import_runs (org_id, `+linkRunCols+`)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
			org, r.ID, r.ImportID, r.Trigger, r.Actor, r.Status, r.Code, r.HTTPStatus, r.StartedAt, r.DurationMS, r.Size, r.Mime, r.Hash,
			r.FileID, r.Path, r.Version); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM link_import_runs WHERE org_id=$1 AND import_id=$2 AND id NOT IN (
			SELECT id FROM link_import_runs WHERE org_id=$1 AND import_id=$2 ORDER BY started_at DESC, id DESC LIMIT $3)`,
			org, r.ImportID, linkimports.RunsKept)
		return err
	})
}

func (p *LinkImportStore) Runs(ctx context.Context, org, importID string, limit int) ([]linkimports.Run, error) {
	if limit <= 0 {
		limit = linkimports.MaxRunsShown
	}
	return many(ctx, p.S, org, scanLinkRun, `SELECT `+linkRunCols+` FROM link_import_runs WHERE org_id=$1 AND import_id=$2
		ORDER BY started_at DESC, id DESC LIMIT $3`, org, importID, limit)
}
