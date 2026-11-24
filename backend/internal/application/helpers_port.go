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

// ---- runtime split (POST /v1/split) ----

// SplitRuntime is the optional runtime capability behind POST /v1/split: is a request worth dividing into
// independent parts, and how. A runtime without it (or one that fails) simply never proposes helpers: the request
// follows its normal plan.
type SplitRuntime interface {
	Split(ctx context.Context, in SplitRequest) (SplitResponse, error)
}

// SplitItem is one thing to deal out (a file of the request). Ref is how the parts point back to it.
type SplitItem struct {
	Name string `json:"name"`
	Ref  string `json:"ref"`
}

// SplitRequest: the text and the names of the items are untrusted data for the runtime.
type SplitRequest struct {
	Text     string      `json:"text"`
	Items    []SplitItem `json:"items"`
	MaxParts int         `json:"max_parts,omitempty"`
	Locale   string      `json:"locale,omitempty"`
}

// SplitPart is one proposed part: its label, its instructions and the items it takes.
type SplitPart struct {
	Label        string   `json:"label"`
	Instructions string   `json:"instructions"`
	ItemRefs     []string `json:"item_refs"`
}

type SplitResponse struct {
	WorthIt          bool        `json:"worth_it"`
	Reason           string      `json:"reason"`
	Parts            []SplitPart `json:"parts"`
	JoinInstructions string      `json:"join_instructions"`
	Source           string      `json:"source"`
	Usage            *Usage      `json:"usage,omitempty"`
}
