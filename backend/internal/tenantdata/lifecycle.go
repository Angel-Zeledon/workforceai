package tenantdata

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/objstore"
)

// DefaultGrace is the time between the deletion request and the purge.
const DefaultGrace = 7 * 24 * time.Hour

// Errors returned to the HTTP layer.
var (
	ErrNameMismatch   = errors.New("tenantdata: the confirmation does not match the organization name")
	ErrBadPassword    = errors.New("tenantdata: wrong password")
	ErrNoAccounts     = errors.New("tenantdata: deleting an organization requires authentication")
	ErrRateLimited    = errors.New("tenantdata: too many attempts")
	ErrExportTooOften = errors.New("tenantdata: one export per hour")
)

// RateLimitedError carries the wait before the next allowed attempt.
type RateLimitedError struct {
	Err        error
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string { return e.Err.Error() }
func (e *RateLimitedError) Unwrap() error { return e.Err }

// Accounts reads accounts (auth.Store fits).
type Accounts interface {
	UserByID(ctx context.Context, id string) (auth.User, error)
}

// Service is the HTTP-facing side of the lifecycle: export, deletion schedule,
// cancel and status. It uses the RLS-restricted application pool, so a request
// can only ever see and change its own organization.
type Service struct {
	App      DB       // application pool (runs as app_user, RLS applies)
	Accounts Accounts // password re-check; nil disables deletion requests
	Hasher   auth.PasswordHasher
	Limiter  auth.Limiter // export and password-attempt limits (nil = no limit)
	// Objects (optional) is the external object store (S3) whose objects the
	// export includes; nil when the bytes live in Postgres (drive_objects).
	Objects objstore.Store
	Grace   time.Duration // default 7 days
	// ExportLimit default: 1 per hour per organization. PasswordLimit default: 5 per 15 minutes per user.
	ExportLimit   auth.Limit
	PasswordLimit auth.Limit
}

func (s *Service) grace() time.Duration {
	if s.Grace > 0 {
		return s.Grace
	}
	return DefaultGrace
}

// DeletionStatus is GET /org/deletion.
type DeletionStatus struct {
	Pending bool `json:"pending"`
	// OrgName is what the owner must type to confirm a deletion.
	OrgName     string     `json:"org_name"`
	RequestedAt *time.Time `json:"requested_at,omitempty"`
	DeletionAt  *time.Time `json:"deletion_at,omitempty"`
	RequestedBy string     `json:"requested_by,omitempty"`
	GraceDays   int        `json:"grace_days"`
	// Protected is true for the demo organization, which can never be deleted.
	Protected bool `json:"protected"`
	// Now is the server time, so the client can show a countdown.
	Now time.Time `json:"now"`
}

func (s *Service) status(ctx context.Context, tx pgx.Tx, orgID string) (DeletionStatus, string, error) {
	st := DeletionStatus{GraceDays: int(s.grace() / (24 * time.Hour)), Protected: orgID == domain.DemoOrgID}
	var name string
	var by *string
	err := tx.QueryRow(ctx, `SELECT name, pending_deletion_at, deletion_at, deletion_requested_by, now() FROM organizations WHERE id = $1`, orgID).
		Scan(&name, &st.RequestedAt, &st.DeletionAt, &by, &st.Now)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, "", domain.ErrNotFound
	}
	if err != nil {
		return st, "", err
	}
	if by != nil {
		st.RequestedBy = *by
	}
	st.Pending = st.DeletionAt != nil
	st.OrgName = name
	return st, name, nil
}

// Status returns the deletion status of the organization.
func (s *Service) Status(ctx context.Context, orgID string) (DeletionStatus, error) {
	var st DeletionStatus
	err := auth.WithOrgTx(ctx, s.App, orgID, func(tx pgx.Tx) (e error) {
		st, _, e = s.status(ctx, tx, orgID)
		return e
	})
	return st, err
}

// ScheduleDeletion schedules the purge of the organization after the grace
// period. confirmName must be exactly the organization name and password the
// requesting owner's current password (role checks are the HTTP layer's job).
// Idempotent: a pending deletion is returned unchanged.
func (s *Service) ScheduleDeletion(ctx context.Context, orgID, userID, confirmName, password string) (DeletionStatus, error) {
	if orgID == domain.DemoOrgID {
		return DeletionStatus{}, ErrProtected
	}
	if s.Accounts == nil || userID == "" {
		return DeletionStatus{}, ErrNoAccounts
	}
	if err := s.allow(ctx, "orgdelete:pw:"+userID, s.PasswordLimit, auth.Limit{Max: 5, Window: 15 * time.Minute}, ErrRateLimited); err != nil {
		return DeletionStatus{}, err
	}
	u, err := s.Accounts.UserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, auth.ErrNotFound) {
			return DeletionStatus{}, ErrBadPassword
		}
		return DeletionStatus{}, err
	}
	if ok, _, err := s.Hasher.Verify(password, u.PasswordHash); err != nil || !ok {
		return DeletionStatus{}, ErrBadPassword
	}
	var st DeletionStatus
	err = auth.WithOrgTx(ctx, s.App, orgID, func(tx pgx.Tx) error {
		cur, name, err := s.status(ctx, tx, orgID)
		if err != nil {
			return err
		}
		if strings.TrimSpace(confirmName) != name {
			return ErrNameMismatch
		}
		if cur.Pending {
			st = cur
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE organizations SET pending_deletion_at = now(), deletion_at = now() + make_interval(secs => $2),
			deletion_requested_by = $3 WHERE id = $1 AND deletion_at IS NULL`, orgID, s.grace().Seconds(), userID); err != nil {
			return err
		}
		st, _, err = s.status(ctx, tx, orgID)
		return err
	})
	return st, err
}

// CancelDeletion cancels a pending deletion (idempotent).
func (s *Service) CancelDeletion(ctx context.Context, orgID string) (DeletionStatus, error) {
	var st DeletionStatus
	err := auth.WithOrgTx(ctx, s.App, orgID, func(tx pgx.Tx) (e error) {
		if _, e = tx.Exec(ctx, `UPDATE organizations SET pending_deletion_at = NULL, deletion_at = NULL, deletion_requested_by = NULL WHERE id = $1`, orgID); e != nil {
			return e
		}
		st, _, e = s.status(ctx, tx, orgID)
		return e
	})
	return st, err
}

// AllowExport consumes the export allowance of the organization (1 per hour by default).
func (s *Service) AllowExport(ctx context.Context, orgID string) error {
	return s.allow(ctx, "orgexport:"+orgID, s.ExportLimit, auth.Limit{Max: 1, Window: time.Hour}, ErrExportTooOften)
}

func (s *Service) allow(ctx context.Context, key string, l, def auth.Limit, deny error) error {
	if s.Limiter == nil {
		return nil
	}
	if l.Max <= 0 || l.Window <= 0 {
		l = def
	}
	res, err := s.Limiter.Allow(ctx, key, l)
	if err != nil {
		if deny == ErrExportTooOften {
			return nil // fail open for the export (the limiter is a cost guard), closed for passwords
		}
		return fmt.Errorf("tenantdata: rate limiter: %w", err)
	}
	if !res.Allowed {
		return &RateLimitedError{Err: deny, RetryAfter: res.RetryAfter}
	}
	return nil
}
