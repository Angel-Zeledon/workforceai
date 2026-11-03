package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/objstore"
)

// Moving the bytes that live in Postgres (drive_objects and the bytea column of
// artifact_blobs) to an S3-compatible store: `server ctl objects-to-s3`
// (docs/architecture/drive.md, "Moving existing files to S3").
//
//   - Idempotent and resumable: objects are walked in key order per
//     organization; an object already in S3 with the same SHA-256 is not copied
//     again, so an interrupted run is simply started again.
//   - Nothing is deleted before it is verified: the bytea is hashed and checked
//     against its recorded SHA-256, the copy is read back from S3 and hashed,
//     and only then (and only with Delete) the Postgres copy is removed.
//   - Without Delete the database is not changed at all: the application keeps
//     reading old objects from Postgres through objstore.Tiered.
//
// It runs with the owner connection (MIGRATE_URL / DATABASE_URL without SET
// ROLE): app_user may not update artifact_blobs, which are insert-only for it.
// Every statement still runs in an organization-scoped transaction (RLS).

// ObjectsMigrateOptions configures MigrateObjects.
type ObjectsMigrateOptions struct {
	// Delete removes the Postgres copy of every verified object (drive_objects
	// row deleted; artifact_blobs row switched to object_key, data NULL).
	Delete bool
	// DryRun reads and verifies the Postgres side and reports what would be copied.
	DryRun bool
	Batch  int // rows listed per query (default 100)
	// Log receives one line per object (optional).
	Log func(msg string, args ...any)
}

// ObjectsMigrateReport is what a run did.
type ObjectsMigrateReport struct {
	Orgs     int      `json:"orgs"`
	Seen     int      `json:"seen"`
	Copied   int      `json:"copied"`
	Already  int      `json:"already_in_s3"`
	Deleted  int      `json:"deleted_from_postgres"`
	Bytes    int64    `json:"bytes_copied"`
	Failed   int      `json:"failed"`
	Failures []string `json:"failures,omitempty"`
}

// OrgsWithObjects lists organizations that may hold objects: members'
// organizations, plus every org_id of drive_objects/artifact_blobs the
// connection can see (all of them for a superuser; none under RLS otherwise).
func OrgsWithObjects(ctx context.Context, pool *pgxpool.Pool, extra ...string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range extra {
		add(id)
	}
	for _, q := range []string{`SELECT DISTINCT org_id FROM memberships`, `SELECT DISTINCT org_id FROM drive_objects`,
		`SELECT DISTINCT org_id FROM artifact_blobs WHERE data IS NOT NULL`} {
		rows, err := pool.Query(ctx, q)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			add(id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	slices.Sort(out)
	return out, nil
}

type pgObject struct {
	key, sha, mime string
	size           int64
	blobID         string // artifact_blobs row (empty for drive_objects)
}

// MigrateObjects copies the Postgres objects of orgs to dst (see the package
// comment above). It stops at the first infrastructure error (database or S3
// unreachable); per-object problems are counted in Failed and never deleted.
func MigrateObjects(ctx context.Context, pool *pgxpool.Pool, dst objstore.Store, orgs []string, o ObjectsMigrateOptions) (ObjectsMigrateReport, error) {
	if o.Batch <= 0 {
		o.Batch = 100
	}
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	var rep ObjectsMigrateReport
	for _, org := range orgs {
		rep.Orgs++
		if err := migrateOrg(ctx, pool, dst, org, o, &rep); err != nil {
			return rep, fmt.Errorf("organization %s: %w", org, err)
		}
	}
	return rep, nil
}

func migrateOrg(ctx context.Context, pool *pgxpool.Pool, dst objstore.Store, org string, o ObjectsMigrateOptions, rep *ObjectsMigrateReport) error {
	lists := []struct {
		name string
		q    string
		blob bool
	}{
		{"drive_objects", `SELECT key, sha256, content_type, size_bytes FROM drive_objects WHERE org_id=$1 AND key > $2 ORDER BY key LIMIT $3`, false},
		{"artifact_blobs", `SELECT id, sha256, mime, size_bytes FROM artifact_blobs WHERE org_id=$1 AND data IS NOT NULL AND id > $2 ORDER BY id LIMIT $3`, true},
	}
	for _, l := range lists {
		cursor := ""
		for {
			var page []pgObject
			err := auth.WithOrgTx(ctx, pool, org, func(tx pgx.Tx) error {
				rows, err := tx.Query(ctx, l.q, org, cursor, o.Batch)
				if err != nil {
					return err
				}
				defer rows.Close()
				for rows.Next() {
					var ob pgObject
					var id string
					if err := rows.Scan(&id, &ob.sha, &ob.mime, &ob.size); err != nil {
						return err
					}
					ob.key = id
					if l.blob {
						ob.blobID, ob.key = id, objstore.OrgKey(org, "pdf", id) // the key PutBlob uses (artifact_blobs.go)
					}
					page = append(page, ob)
					cursor = id
				}
				return rows.Err()
			})
			if err != nil {
				return fmt.Errorf("list %s: %w", l.name, err)
			}
			for _, ob := range page {
				rep.Seen++
				if err := migrateOne(ctx, pool, dst, org, ob, o, rep); err != nil {
					return err
				}
			}
			if len(page) < o.Batch {
				break
			}
		}
	}
	return nil
}

// errObject is a problem with one object: reported, never fatal, nothing deleted.
type errObject struct{ msg string }

func (e errObject) Error() string { return e.msg }

func migrateOne(ctx context.Context, pool *pgxpool.Pool, dst objstore.Store, org string, ob pgObject, o ObjectsMigrateOptions, rep *ObjectsMigrateReport) error {
	fail := func(msg string) error {
		rep.Failed++
		rep.Failures = append(rep.Failures, ob.key+": "+msg)
		o.Log("object failed", "key", ob.key, "reason", msg)
		return nil
	}
	data, err := loadObject(ctx, pool, org, ob)
	if err != nil {
		var eo errObject
		if errors.As(err, &eo) {
			return fail(eo.msg)
		}
		return err
	}
	sum := hexSum(data)
	if int64(len(data)) != ob.size || (ob.sha != "" && sum != ob.sha) {
		return fail("the Postgres copy does not match its recorded size/sha256: left untouched")
	}
	already := false
	if info, err := dst.Head(ctx, ob.key); err == nil && info.Size == ob.size {
		got, err := remoteSum(ctx, dst, ob.key)
		if err != nil {
			return err
		}
		already = got == sum
	} else if err != nil && !errors.Is(err, objstore.ErrNotFound) {
		return fmt.Errorf("head %s: %w", ob.key, err)
	}
	switch {
	case already:
		rep.Already++
	case o.DryRun:
		o.Log("would copy", "key", ob.key, "size", ob.size)
		return nil
	default:
		if _, err := dst.Put(ctx, ob.key, bytes.NewReader(data), ob.size, objstore.PutOptions{ContentType: ob.mime, SHA256: sum}); err != nil {
			return fmt.Errorf("put %s: %w", ob.key, err)
		}
		got, err := remoteSum(ctx, dst, ob.key)
		if err != nil {
			return err
		}
		if got != sum {
			return fail("the copy in S3 does not match after upload: Postgres copy kept")
		}
		rep.Copied++
		rep.Bytes += ob.size
		o.Log("copied", "key", ob.key, "size", ob.size)
	}
	if !o.Delete || o.DryRun {
		return nil
	}
	var n int64
	err = auth.WithOrgTx(ctx, pool, org, func(tx pgx.Tx) error {
		var tag interface{ RowsAffected() int64 }
		var err error
		if ob.blobID == "" {
			tag, err = tx.Exec(ctx, `DELETE FROM drive_objects WHERE org_id=$1 AND key=$2 AND sha256=$3`, org, ob.key, sum)
		} else {
			tag, err = tx.Exec(ctx, `UPDATE artifact_blobs SET object_key=$3, data=NULL WHERE org_id=$1 AND id=$2 AND data IS NOT NULL AND sha256=$4`,
				org, ob.blobID, ob.key, sum)
		}
		if err == nil {
			n = tag.RowsAffected()
		}
		return err
	})
	if err != nil {
		return fmt.Errorf("remove the Postgres copy of %s: %w", ob.key, err)
	}
	rep.Deleted += int(n)
	return nil
}

func loadObject(ctx context.Context, pool *pgxpool.Pool, org string, ob pgObject) ([]byte, error) {
	var data []byte
	err := auth.WithOrgTx(ctx, pool, org, func(tx pgx.Tx) error {
		if ob.blobID == "" {
			return tx.QueryRow(ctx, `SELECT data FROM drive_objects WHERE org_id=$1 AND key=$2`, org, ob.key).Scan(&data)
		}
		return tx.QueryRow(ctx, `SELECT data FROM artifact_blobs WHERE org_id=$1 AND id=$2`, org, ob.blobID).Scan(&data)
	})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && data == nil) {
		return nil, errObject{"gone meanwhile"}
	}
	return data, err
}

// remoteSum reads the object back from the store and hashes it.
func remoteSum(ctx context.Context, dst objstore.Store, key string) (string, error) {
	rc, _, err := dst.Get(ctx, key)
	if err != nil {
		return "", fmt.Errorf("read back %s: %w", key, err)
	}
	defer rc.Close()
	h := sha256.New()
	if _, err := io.Copy(h, rc); err != nil {
		return "", fmt.Errorf("read back %s: %w", key, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hexSum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
