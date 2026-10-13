package tenantdata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/objstore"
)

// Errors of the lifecycle.
var (
	// ErrProtected: the demo organization can never be deleted or purged.
	ErrProtected = errors.New("tenantdata: the demo organization cannot be deleted")
	// ErrNotDue: the organization has no deletion scheduled, or its grace period is not over.
	ErrNotDue = errors.New("tenantdata: deletion is not due")
)

// DB is a connection pool (*pgxpool.Pool fits).
type DB interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	BeginTx(ctx context.Context, o pgx.TxOptions) (pgx.Tx, error)
}

// Purge reasons, stored in the tombstone.
const (
	ReasonRequested    = "requested"
	ReasonTrialExpired = "trial_expired"
)

// PurgeOptions tune one purge.
type PurgeOptions struct {
	Reason string // ReasonRequested (default) or ReasonTrialExpired
	// RequireDue re-checks, under the row lock, that a deletion is scheduled and
	// its grace period is over (a cancel that raced with the janitor wins).
	RequireDue bool
}

// PurgeResult reports what a purge removed.
type PurgeResult struct {
	OrgHash  string           `json:"org_hash"`
	Rows     map[string]int64 `json:"rows"`     // table -> deleted rows
	Accounts int64            `json:"accounts"` // users deleted because no membership was left
	Objects  int              `json:"objects"`  // external object-store objects deleted after commit
}

// Purger deletes organizations for good. It must use the owner/migration
// connection (MIGRATE_DATABASE_URL, or DATABASE_URL without the app role): the
// application role has no DELETE on the append-only tables. Each purge also
// binds app.org_id to the organization, so it works even when the owner is
// subject to FORCE ROW LEVEL SECURITY.
type Purger struct {
	Owner DB
	// Objects (optional) is an external object store (S3) whose objects are
	// referenced by object_key columns; they are deleted after the commit.
	// Leave nil when the bytes live in Postgres (drive_objects).
	Objects objstore.Store
	Log     *slog.Logger
}

// OrgHash is the non-personal identifier kept in the tombstone.
func OrgHash(orgID string) string {
	h := sha256.Sum256([]byte("org:" + orgID))
	return hex.EncodeToString(h[:])
}

func (p *Purger) log() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.Default()
}

// beginPurge opens the owner transaction scoped to orgID (RLS) and marks it as
// the purge of orgID (append-only triggers let exactly those rows go).
func (p *Purger) beginPurge(ctx context.Context, orgID string) (pgx.Tx, error) {
	tx, err := p.Owner.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('statement_timeout', '600000', true),
		set_config('lock_timeout', '30000', true),
		set_config('app.org_id', $1, true), set_config('app.purge_org', $1, true)`, orgID); err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, err
	}
	return tx, nil
}

// PurgeOrg deletes every row of every table that carries the organization's
// data, in foreign-key order, inside one transaction; then its memberships,
// then the accounts left without any membership, then the organization row,
// and writes the tombstone. Nothing is committed unless every scoped table is
// verified empty for the organization. The demo organization is refused (here
// and by a database trigger).
func (p *Purger) PurgeOrg(ctx context.Context, orgID string, o PurgeOptions) (PurgeResult, error) {
	res := PurgeResult{OrgHash: OrgHash(orgID), Rows: map[string]int64{}}
	if orgID == domain.DemoOrgID {
		return res, ErrProtected
	}
	if strings.TrimSpace(orgID) == "" {
		return res, fmt.Errorf("%w: empty organization id", domain.ErrInvalid)
	}
	if o.Reason == "" {
		o.Reason = ReasonRequested
	}
	tx, err := p.beginPurge(ctx, orgID)
	if err != nil {
		return res, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var deletionAt *time.Time
	var now time.Time
	exists := true
	if err := tx.QueryRow(ctx, `SELECT deletion_at, now() FROM organizations WHERE id = $1 FOR UPDATE`, orgID).Scan(&deletionAt, &now); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return res, err
		}
		exists = false
	}
	if o.RequireDue && (!exists || deletionAt == nil || deletionAt.After(now)) {
		return res, ErrNotDue
	}

	sc, err := Discover(ctx, tx)
	if err != nil {
		return res, err
	}
	scopes, err := sc.PurgeScopes()
	if err != nil {
		return res, err
	}
	members, err := strings1(ctx, tx, `SELECT user_id FROM `+ident(MembershipTable)+` WHERE org_id = $1`, orgID)
	if err != nil {
		return res, err
	}
	var keys []string
	if p.Objects != nil {
		if keys, err = objectKeys(ctx, tx, sc, orgID); err != nil {
			return res, err
		}
	}

	for _, t := range scopes {
		tag, err := tx.Exec(ctx, `DELETE FROM `+ident(t.Name)+` AS t WHERE `+t.Scope, orgID)
		if err != nil {
			return res, fmt.Errorf("tenantdata: purge %s: %w", t.Name, err)
		}
		res.Rows[t.Name] = tag.RowsAffected()
	}
	if res.Accounts, err = deleteOrphanAccounts(ctx, tx, sc, members); err != nil {
		return res, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM `+ident(OrgTable)+` WHERE id = $1`, orgID)
	if err != nil {
		return res, fmt.Errorf("tenantdata: purge %s: %w", OrgTable, err)
	}
	res.Rows[OrgTable] = tag.RowsAffected()

	// Verify before committing: no scoped table keeps a row of the organization.
	for _, n := range sc.OfKind(KindOrg, KindChild) {
		pred, _ := sc.OrgScope(n, "t")
		var left int64
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+ident(n)+` AS t WHERE `+pred, orgID).Scan(&left); err != nil {
			return res, fmt.Errorf("tenantdata: verify %s: %w", n, err)
		}
		if left != 0 {
			return res, fmt.Errorf("tenantdata: purge left %d rows in %s; rolled back", left, n)
		}
	}
	if exists {
		if _, err := tx.Exec(ctx, `INSERT INTO org_tombstones (org_hash, reason) VALUES ($1, $2) ON CONFLICT (org_hash) DO NOTHING`, res.OrgHash, o.Reason); err != nil {
			return res, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return res, err
	}
	res.Objects = p.deleteObjects(ctx, keys)
	var total int64
	for _, n := range res.Rows {
		total += n
	}
	p.log().Info("tenantdata: organization purged", "org_hash", res.OrgHash, "reason", o.Reason, "rows", total, "accounts", res.Accounts, "objects", res.Objects)
	return res, nil
}

// PurgeAccounts deletes the given accounts that have no membership left (and
// everything that hangs from them: refresh tokens, e-mail verifications...).
// Accounts that still belong to an organization are kept. Same code path as
// the end of PurgeOrg.
func (p *Purger) PurgeAccounts(ctx context.Context, userIDs []string) (int64, error) {
	if len(userIDs) == 0 {
		return 0, nil
	}
	tx, err := p.Owner.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	sc, err := Discover(ctx, tx)
	if err != nil {
		return 0, err
	}
	n, err := deleteOrphanAccounts(ctx, tx, sc, userIDs)
	if err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}

// deleteOrphanAccounts removes, among candidates, the accounts without any
// membership: first every row that references them (generic, from the
// foreign keys), then the users.
func deleteOrphanAccounts(ctx context.Context, tx pgx.Tx, sc *Schema, candidates []string) (int64, error) {
	if len(candidates) == 0 {
		return 0, nil
	}
	victims, err := strings1(ctx, tx, `SELECT u.id FROM `+ident(AccountTable)+` u WHERE u.id = ANY($1::text[])
		AND NOT EXISTS (SELECT 1 FROM `+ident(MembershipTable)+` m WHERE m.user_id = u.id)`, candidates)
	if err != nil || len(victims) == 0 {
		return 0, err
	}
	deps, err := sc.UserDependents()
	if err != nil {
		return 0, err
	}
	for _, d := range deps {
		if _, err := tx.Exec(ctx, `DELETE FROM `+ident(d.Name)+` AS t WHERE `+d.Scope, victims); err != nil {
			return 0, fmt.Errorf("tenantdata: purge %s of accounts: %w", d.Name, err)
		}
	}
	tag, err := tx.Exec(ctx, `DELETE FROM `+ident(AccountTable)+` WHERE id = ANY($1::text[])`, victims)
	if err != nil {
		return 0, fmt.Errorf("tenantdata: purge accounts: %w", err)
	}
	return tag.RowsAffected(), nil
}

// objectKeys lists the object-store keys referenced by the organization's rows.
func objectKeys(ctx context.Context, q pgx.Tx, sc *Schema, orgID string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, tc := range sc.ObjectKeyColumns() {
		pred, ok := sc.OrgScope(tc[0], "t")
		if !ok {
			continue
		}
		ks, err := strings1(ctx, q, `SELECT DISTINCT t.`+ident(tc[1])+` FROM `+ident(tc[0])+` AS t WHERE `+pred+` AND t.`+ident(tc[1])+` IS NOT NULL AND t.`+ident(tc[1])+` <> ''`, orgID)
		if err != nil {
			return nil, err
		}
		for _, k := range ks {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	return out, nil
}

func (p *Purger) deleteObjects(ctx context.Context, keys []string) int {
	n := 0
	for _, k := range keys {
		if err := p.Objects.Delete(context.WithoutCancel(ctx), k); err != nil {
			p.log().Warn("tenantdata: delete object after purge", "err", err)
			continue
		}
		n++
	}
	return n
}

type queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// strings1 runs a query returning one text column.
func strings1(ctx context.Context, q queryer, sql string, args ...any) ([]string, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
