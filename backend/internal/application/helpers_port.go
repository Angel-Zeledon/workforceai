package application

import (
	"context"

	"aiworkforce/backend/internal/domain"
)

// HelperStore persists the temporary helper groups (migration 831). It is optional, like RunStore: the Store given
// to NewOrchestrator is asserted to it, and without it helpers are unavailable (every call answers ErrInvalid).
type HelperStore interface {
	// CreateHelperGroup stores a group; domain.ErrConflict when the request already has one.
	CreateHelperGroup(ctx context.Context, orgID string, g domain.HelperGroup) error
	GetHelperGroup(ctx context.Context, orgID, id string) (domain.HelperGroup, error)
	GetHelperGroupByRequest(ctx context.Context, orgID, requestID string) (domain.HelperGroup, error)
	// UpdateHelperGroup replaces everything except spent_usd (only AddHelperSpend changes it).
	UpdateHelperGroup(ctx context.Context, orgID string, g domain.HelperGroup) error
	// AddHelperSpend adds the real cost of a call to the group (atomic).
	AddHelperSpend(ctx context.Context, orgID, id string, usd float64) error
	// ListHelperGroups returns the groups in any of the statuses (all when none is given), oldest first.
	ListHelperGroups(ctx context.Context, orgID string, statuses ...string) ([]domain.HelperGroup, error)
}
