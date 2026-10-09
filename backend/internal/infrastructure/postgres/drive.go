package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
)

// DriveStore implements drive.Store on Postgres (migration 480). Every call
// runs through WithOrgTx, so Row-Level Security applies; app_user can neither
// delete rows nor touch old versions (deletes are soft, versions append-only).
type DriveStore struct{ S *Store }

var _ drive.Store = (*DriveStore)(nil)

func isUnique(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

func mapDrive(err error) error {
	switch {
	case err == nil:
		return nil
	case isUnique(err):
		return domain.ErrConflict
	}
	return mapErr(err)
}

// ---- folders ----

const folderCols = `id, COALESCE(parent_id,''), name, path, root_kind, ref_id, created_by, created_at, updated_at, deleted_at, deleted_by, deleted_batch`

func scanFolder(r scanner) (drive.Folder, error) {
	var f drive.Folder
	var by, dby []byte
	err := r.Scan(&f.ID, &f.ParentID, &f.Name, &f.Path, &f.RootKind, &f.RefID, &by, &f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &dby, &f.DeletedBatch)
	unmarshal(by, &f.CreatedBy)
	if len(dby) > 0 && string(dby) != "null" {
		var p drive.Principal
		unmarshal(dby, &p)
		f.DeletedBy = &p
	}
	return f, err
}

func (d *DriveStore) FolderByPath(ctx context.Context, org, path string) (drive.Folder, error) {
	f, err := one(ctx, d.S, org, scanFolder, `SELECT `+folderCols+` FROM drive_folders WHERE org_id=$1 AND lower(path)=lower($2) AND deleted_at IS NULL`, org, path)
	return f, mapErr(err)
}

func (d *DriveStore) FolderByID(ctx context.Context, org, id string) (drive.Folder, error) {
	f, err := one(ctx, d.S, org, scanFolder, `SELECT `+folderCols+` FROM drive_folders WHERE org_id=$1 AND id=$2`, org, id)
	return f, mapErr(err)
}

func (d *DriveStore) CreateFolder(ctx context.Context, org string, f drive.Folder) error {
	var parent any
	if f.ParentID != "" {
		parent = f.ParentID
	}
	return mapDrive(d.S.exec(ctx, org, `INSERT INTO drive_folders (org_id, id, parent_id, name, path, root_kind, ref_id, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, org, f.ID, parent, f.Name, f.Path, f.RootKind, f.RefID, jb(f.CreatedBy), f.CreatedAt, f.UpdatedAt))
}

func (d *DriveStore) ChildFolders(ctx context.Context, org, parentID string) ([]drive.Folder, error) {
	return many(ctx, d.S, org, scanFolder, `SELECT `+folderCols+` FROM drive_folders WHERE org_id=$1 AND COALESCE(parent_id,'')=$2 AND deleted_at IS NULL ORDER BY lower(name)`, org, parentID)
}

func (d *DriveStore) MoveFolder(ctx context.Context, org, id, newParentID, newName, newPath string, at time.Time) error {
	return mapDrive(d.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var old string
		if err := tx.QueryRow(ctx, `SELECT path FROM drive_folders WHERE org_id=$1 AND id=$2 AND deleted_at IS NULL`, org, id).Scan(&old); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE drive_folders SET path = $3 || substr(path, length($2)+1), updated_at=$4
			WHERE org_id=$1 AND (path=$2 OR left(path, length($2)+1) = $2 || '/')`, org, old, newPath, at); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE drive_folders SET parent_id=NULLIF($3,''), name=$4 WHERE org_id=$1 AND id=$2`, org, id, newParentID, newName)
		return err
	}))
}

func (d *DriveStore) DeleteFolder(ctx context.Context, org, id string, by drive.Principal, at time.Time) error {
	return mapDrive(d.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var path string
		if err := tx.QueryRow(ctx, `SELECT path FROM drive_folders WHERE org_id=$1 AND id=$2 AND deleted_at IS NULL`, org, id).Scan(&path); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE drive_folders SET deleted_at=$3, deleted_by=$4, deleted_batch=$2
			WHERE org_id=$1 AND deleted_at IS NULL AND (id=$2 OR left(path, length($5)+1) = $5 || '/')`, org, id, at, jb(by), path); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE drive_files SET deleted_at=$3, deleted_by=$4, deleted_batch=$2
			WHERE org_id=$1 AND deleted_at IS NULL AND folder_id IN (SELECT id FROM drive_folders WHERE org_id=$1 AND deleted_batch=$2)`, org, id, at, jb(by))
		return err
	}))
}

func (d *DriveStore) RestoreFolder(ctx context.Context, org, id string, at time.Time) error {
	return mapDrive(d.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var parent string
		var del *time.Time
		if err := tx.QueryRow(ctx, `SELECT COALESCE(parent_id,''), deleted_at FROM drive_folders WHERE org_id=$1 AND id=$2`, org, id).Scan(&parent, &del); err != nil {
			return err
		}
		if del == nil {
			return pgx.ErrNoRows
		}
		if parent != "" {
			var pdel *time.Time
			if err := tx.QueryRow(ctx, `SELECT deleted_at FROM drive_folders WHERE org_id=$1 AND id=$2`, org, parent).Scan(&pdel); err != nil || pdel != nil {
				return domain.ErrConflict
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE drive_folders SET deleted_at=NULL, deleted_by=NULL, deleted_batch='', updated_at=$3 WHERE org_id=$1 AND deleted_batch=$2`, org, id, at); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE drive_files SET deleted_at=NULL, deleted_by=NULL, deleted_batch='' WHERE org_id=$1 AND deleted_batch=$2`, org, id)
		return err
	}))
}

// ---- files ----

const fileCols = `f.id, f.folder_id, f.name, fo.path || '/' || f.name, f.mime, f.size_bytes, f.current_version, f.sha256, f.tags, f.extraction, f.search_text,
	f.created_by, f.updated_by, f.created_at, f.updated_at, f.deleted_at, f.deleted_by, f.deleted_batch`

const fileFrom = ` FROM drive_files f JOIN drive_folders fo ON fo.org_id=f.org_id AND fo.id=f.folder_id `

func scanFile(r scanner) (drive.File, error) {
	var f drive.File
	var tags, cby, uby, dby []byte
	err := r.Scan(&f.ID, &f.FolderID, &f.Name, &f.Path, &f.Mime, &f.Size, &f.CurrentVersion, &f.SHA256, &tags, &f.Extraction, &f.SearchText,
		&cby, &uby, &f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &dby, &f.DeletedBatch)
	unmarshal(tags, &f.Tags)
	unmarshal(cby, &f.CreatedBy)
	unmarshal(uby, &f.UpdatedBy)
	if len(dby) > 0 && string(dby) != "null" {
		var p drive.Principal
		unmarshal(dby, &p)
		f.DeletedBy = &p
	}
	if f.Tags == nil {
		f.Tags = []string{}
	}
	return f, err
}

func insertVersion(ctx context.Context, tx pgx.Tx, org string, v drive.Version) error {
	_, err := tx.Exec(ctx, `INSERT INTO drive_versions (org_id, file_id, version, object_key, sha256, size_bytes, mime, author, message, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, org, v.FileID, v.Version, v.ObjectKey, v.SHA256, v.Size, v.Mime, jb(v.Author), v.Message, v.CreatedAt)
	return err
}

func (d *DriveStore) CreateFile(ctx context.Context, org string, f drive.File, v drive.Version) error {
	return mapDrive(d.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO drive_files (org_id, id, folder_id, name, mime, size_bytes, current_version, sha256, tags, extraction, search_text,
			created_by, updated_by, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
			org, f.ID, f.FolderID, f.Name, f.Mime, f.Size, f.CurrentVersion, f.SHA256, jb(strs(f.Tags)), f.Extraction, f.SearchText,
			jb(f.CreatedBy), jb(f.UpdatedBy), f.CreatedAt, f.UpdatedAt); err != nil {
			return err
		}
		return insertVersion(ctx, tx, org, v)
	}))
}

func (d *DriveStore) FileByID(ctx context.Context, org, id string) (drive.File, error) {
	f, err := one(ctx, d.S, org, scanFile, `SELECT `+fileCols+fileFrom+`WHERE f.org_id=$1 AND f.id=$2`, org, id)
	return f, mapErr(err)
}

func (d *DriveStore) FileByName(ctx context.Context, org, folderID, name string) (drive.File, error) {
	f, err := one(ctx, d.S, org, scanFile, `SELECT `+fileCols+fileFrom+`WHERE f.org_id=$1 AND f.folder_id=$2 AND lower(f.name)=lower($3) AND f.deleted_at IS NULL`, org, folderID, name)
	return f, mapErr(err)
}

func (d *DriveStore) FilesIn(ctx context.Context, org, folderID string) ([]drive.File, error) {
	return many(ctx, d.S, org, scanFile, `SELECT `+fileCols+fileFrom+`WHERE f.org_id=$1 AND f.folder_id=$2 AND f.deleted_at IS NULL ORDER BY lower(f.name)`, org, folderID)
}

func (d *DriveStore) AddVersion(ctx context.Context, org string, expected int, f drive.File, v drive.Version) error {
	return mapDrive(d.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE drive_files SET size_bytes=$4, sha256=$5, mime=$6, current_version=$7, tags=$8, extraction=$9, search_text=$10, updated_by=$11, updated_at=$12
			WHERE org_id=$1 AND id=$2 AND current_version=$3 AND deleted_at IS NULL`,
			org, f.ID, expected, f.Size, f.SHA256, f.Mime, f.CurrentVersion, jb(strs(f.Tags)), f.Extraction, f.SearchText, jb(f.UpdatedBy), f.UpdatedAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrConflict // somebody else wrote first (or the file is gone)
		}
		return insertVersion(ctx, tx, org, v)
	}))
}

func (d *DriveStore) UpdateFile(ctx context.Context, org string, f drive.File) error {
	tag, err := d.S.execTag(ctx, org, `UPDATE drive_files SET folder_id=$3, name=$4, tags=$5, updated_by=$6, updated_at=$7 WHERE org_id=$1 AND id=$2 AND deleted_at IS NULL`,
		org, f.ID, f.FolderID, f.Name, jb(strs(f.Tags)), jb(f.UpdatedBy), f.UpdatedAt)
	if err != nil {
		return mapDrive(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (d *DriveStore) DeleteFile(ctx context.Context, org, id string, by drive.Principal, at time.Time) error {
	tag, err := d.S.execTag(ctx, org, `UPDATE drive_files SET deleted_at=$3, deleted_by=$4, deleted_batch=$2 WHERE org_id=$1 AND id=$2 AND deleted_at IS NULL`, org, id, at, jb(by))
	if err != nil {
		return mapDrive(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (d *DriveStore) RestoreFile(ctx context.Context, org, id string, at time.Time) error {
	return mapDrive(d.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var folder string
		var del *time.Time
		if err := tx.QueryRow(ctx, `SELECT folder_id, deleted_at FROM drive_files WHERE org_id=$1 AND id=$2`, org, id).Scan(&folder, &del); err != nil {
			return err
		}
		if del == nil {
			return pgx.ErrNoRows
		}
		var fdel *time.Time
		if err := tx.QueryRow(ctx, `SELECT deleted_at FROM drive_folders WHERE org_id=$1 AND id=$2`, org, folder).Scan(&fdel); err != nil || fdel != nil {
			return domain.ErrConflict
		}
		_, err := tx.Exec(ctx, `UPDATE drive_files SET deleted_at=NULL, deleted_by=NULL, deleted_batch='', updated_at=$3 WHERE org_id=$1 AND id=$2`, org, id, at)
		return err // a name clash raises the partial unique index -> conflict
	}))
}

func scanVersion(r scanner) (drive.Version, error) {
	var v drive.Version
	var by []byte
	err := r.Scan(&v.FileID, &v.Version, &v.ObjectKey, &v.SHA256, &v.Size, &v.Mime, &by, &v.Message, &v.CreatedAt)
	unmarshal(by, &v.Author)
	return v, err
}

const versionCols = `file_id, version, object_key, sha256, size_bytes, mime, author, message, created_at`

func (d *DriveStore) Versions(ctx context.Context, org, fileID string) ([]drive.Version, error) {
	vs, err := many(ctx, d.S, org, scanVersion, `SELECT `+versionCols+` FROM drive_versions WHERE org_id=$1 AND file_id=$2 ORDER BY version DESC`, org, fileID)
	if err == nil && len(vs) == 0 {
		return nil, domain.ErrNotFound
	}
	return vs, err
}

func (d *DriveStore) VersionOf(ctx context.Context, org, fileID string, n int) (drive.Version, error) {
	v, err := one(ctx, d.S, org, scanVersion, `SELECT `+versionCols+` FROM drive_versions WHERE org_id=$1 AND file_id=$2 AND version=$3`, org, fileID, n)
	return v, mapErr(err)
}

func (d *DriveStore) Trash(ctx context.Context, org string) (drive.Trash, error) {
	fo, err := many(ctx, d.S, org, scanFolder, `SELECT `+folderCols+` FROM drive_folders WHERE org_id=$1 AND deleted_at IS NOT NULL AND deleted_batch=id ORDER BY deleted_at DESC LIMIT 500`, org)
	if err != nil {
		return drive.Trash{}, err
	}
	fi, err := many(ctx, d.S, org, scanFile, `SELECT `+fileCols+fileFrom+`WHERE f.org_id=$1 AND f.deleted_at IS NOT NULL AND f.deleted_batch=f.id ORDER BY f.deleted_at DESC LIMIT 500`, org)
	return drive.Trash{Folders: fo, Files: fi}, err
}

func (d *DriveStore) UsedBytes(ctx context.Context, org string) (int64, error) {
	n, err := one(ctx, d.S, org, func(r scanner) (int64, error) { var n int64; return n, r.Scan(&n) },
		`SELECT COALESCE(sum(size_bytes),0)::bigint FROM drive_versions WHERE org_id=$1`, org)
	return n, err
}

func (d *DriveStore) Search(ctx context.Context, org string, q drive.SearchQuery) ([]drive.Hit, error) {
	vec, cfg := "f.search_es", "spanish"
	if q.Config == "simple" {
		vec, cfg = "f.search_simple", "simple"
	}
	under := q.Under
	if under == nil {
		under = []string{}
	}
	limit := q.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	sql := `WITH qq AS (SELECT websearch_to_tsquery('` + cfg + `'::regconfig, $2) AS q)
		SELECT f.id, fo.path || '/' || f.name, f.name, f.mime, f.current_version, f.size_bytes, f.updated_by,
		       ts_rank(` + vec + `, qq.q)::float8 + CASE WHEN lower(f.name) LIKE '%' || lower($5) || '%' THEN 0.5 ELSE 0 END AS score,
		       ts_headline('` + cfg + `'::regconfig, left(f.search_text, 20000), qq.q, 'MaxFragments=1,MaxWords=30,MinWords=10,StartSel=«,StopSel=»')
		FROM drive_files f JOIN drive_folders fo ON fo.org_id=f.org_id AND fo.id=f.folder_id, qq
		WHERE f.org_id=$1 AND f.deleted_at IS NULL AND fo.deleted_at IS NULL
		  AND (` + vec + ` @@ qq.q OR lower(f.name) LIKE '%' || lower($5) || '%')
		  AND (cardinality($3::text[]) = 0 OR EXISTS (SELECT 1 FROM unnest($3::text[]) u(p)
		        WHERE lower(fo.path) = lower(u.p) OR left(lower(fo.path), length(u.p)+1) = lower(u.p) || '/'))
		ORDER BY score DESC, fo.path, f.name LIMIT $4`
	likeEsc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q.Query)
	// An explicit but empty scope means "nothing readable": return no rows.
	if q.Under != nil && len(q.Under) == 0 {
		return []drive.Hit{}, nil
	}
	return many(ctx, d.S, org, func(r scanner) (drive.Hit, error) {
		var h drive.Hit
		var by []byte
		err := r.Scan(&h.FileID, &h.Path, &h.Name, &h.Mime, &h.Version, &h.Size, &by, &h.Score, &h.Snippet)
		unmarshal(by, &h.UpdatedBy)
		h.Snippet = strings.Join(strings.Fields(h.Snippet), " ")
		return h, err
	}, sql, org, q.Query, under, limit, likeEsc)
}

// ---- grants ----

func (d *DriveStore) AddGrant(ctx context.Context, org string, g drive.Grant) error {
	return d.S.exec(ctx, org, `INSERT INTO drive_grants (org_id, id, agent_id, path, mode, created_by, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		org, g.ID, g.AgentID, g.Path, g.Mode, jb(g.CreatedBy), g.CreatedAt)
}

func (d *DriveStore) Grants(ctx context.Context, org, agentID string) ([]drive.Grant, error) {
	return many(ctx, d.S, org, func(r scanner) (drive.Grant, error) {
		var g drive.Grant
		var by []byte
		err := r.Scan(&g.ID, &g.AgentID, &g.Path, &g.Mode, &by, &g.CreatedAt, &g.RevokedAt)
		unmarshal(by, &g.CreatedBy)
		return g, err
	}, `SELECT id, agent_id, path, mode, created_by, created_at, revoked_at FROM drive_grants WHERE org_id=$1 AND revoked_at IS NULL AND ($2='' OR agent_id=$2) ORDER BY created_at`, org, agentID)
}

func (d *DriveStore) RevokeGrant(ctx context.Context, org, id string, at time.Time) error {
	tag, err := d.S.execTag(ctx, org, `UPDATE drive_grants SET revoked_at=$3 WHERE org_id=$1 AND id=$2 AND revoked_at IS NULL`, org, id, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
