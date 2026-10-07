package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// SetOrgID scopes the current transaction to orgID for Row-Level Security by
// running `SET LOCAL app.org_id = <orgID>` (via set_config(..., true), which
// accepts bind parameters and is transaction-local, so the value can never leak
// to another request when the pooled connection is reused).
//
// It must be called inside a transaction; outside one the setting would be
// discarded immediately and every RLS-protected query would see no rows.
func SetOrgID(ctx context.Context, tx pgx.Tx, orgID string) error {
	if !validOrgID(orgID) {
		return fmt.Errorf("auth: invalid org id %q", orgID)
	}
	_, err := tx.Exec(ctx, `SELECT set_config('app.org_id', $1, true)`, orgID)
	return err
}

func validOrgID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r == '-' || r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
	}
	return true
}

// WithOrgTx runs fn inside a transaction scoped to orgID (see SetOrgID).
// It commits when fn returns nil and rolls back otherwise (including panics).
func WithOrgTx(ctx context.Context, db interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}, orgID string, fn func(tx pgx.Tx) error) (err error) {
	if !validOrgID(orgID) {
		return fmt.Errorf("auth: invalid org id %q", orgID)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			// Use a fresh context so a cancelled request still rolls back.
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if err := SetOrgID(ctx, tx, orgID); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	committed = true
	return nil
}

// WithRequestTx is WithOrgTx using the organization of the authenticated
// principal found in ctx. It fails closed when there is none.
func WithRequestTx(ctx context.Context, db interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}, fn func(tx pgx.Tx) error) error {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		return errors.New("auth: no principal in context")
	}
	return WithOrgTx(ctx, db, p.OrgID, fn)
}
