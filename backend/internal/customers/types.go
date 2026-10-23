// Package customers is the customer district (B12, docs/architecture/customer-district.md): the customers
// (clients) an organization works for, the per-customer agent clones and what is scoped to each customer.
//
// A clone ("Contador · Ferretería López") is an assignment of an existing agent to one customer: the same
// role template, tools, autonomy, pause state and spend caps, under a display name, with a desk of its own
// in the drive (/Agents/<agent>/Clients/<customer>). Isolation is enforced by the backend wherever stored
// knowledge reaches an agent: a task whose request is scoped to customer A never recalls a memory of B
// (internal/memory) and never reads or writes B's folders (internal/drive, customer_scope.go).
//
// Agents never create customers, clones or links: every write here is a person's, audited.
package customers

import (
	"context"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Customer is one client of the organization.
type Customer struct {
	ID    string `json:"id"`
	OrgID string `json:"-"`
	Name  string `json:"name"`
	TaxID string `json:"tax_id"`
	// Color tints its building in the district ("#rrggbb").
	Color string `json:"color"`
	Notes string `json:"notes"`
	// FolderID is the customer's folder in the drive (/Clients/<name>, RefID = customer id); "" without a drive.
	FolderID   string     `json:"-"`
	FolderPath string     `json:"folder_path"`
	Archived   bool       `json:"archived"`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	CreatedBy  string     `json:"created_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// Assignment is a clone: an agent working for one customer.
type Assignment struct {
	ID          string    `json:"id"`
	CustomerID  string    `json:"customer_id"`
	AgentID     string    `json:"agent_id"`
	DisplayName string    `json:"display_name"`
	CreatedBy   string    `json:"created_by,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// Link kinds.
const (
	LinkRequest     = "request"
	LinkDriveFolder = "drive_folder"
)

// Link scopes one object to a customer.
type Link struct {
	Kind       string    `json:"kind"`
	RefID      string    `json:"ref_id"`
	CustomerID string    `json:"customer_id"`
	CreatedBy  string    `json:"created_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// Input is the body of create / update.
type Input struct {
	Name  *string `json:"name"`
	TaxID *string `json:"tax_id"`
	Color *string `json:"color"`
	Notes *string `json:"notes"`
}

// AssignmentInput is the body of a new clone.
type AssignmentInput struct {
	AgentID     string `json:"agent_id"`
	DisplayName string `json:"display_name"`
}

// Status of a customer's building in the district.
const (
	StatusOnTrack         = "on_track"
	StatusWaitingApproval = "waiting_approval"
	StatusBlocked         = "blocked"
	StatusIdle            = "idle"
)

// Store persists customers, clones and links. Every call is scoped to one organization (Row-Level Security in
// Postgres, migration 797).
type Store interface {
	CreateCustomer(ctx context.Context, c Customer) error
	UpdateCustomer(ctx context.Context, c Customer) error
	GetCustomer(ctx context.Context, org, id string) (Customer, error)
	// ListCustomers returns the customers by name; archived ones only when asked.
	ListCustomers(ctx context.Context, org string, includeArchived bool) ([]Customer, error)

	// AddAssignment fails with domain.ErrConflict when the agent already has a clone for the customer.
	AddAssignment(ctx context.Context, org string, a Assignment) error
	DeleteAssignment(ctx context.Context, org, customerID, id string) error
	// Assignments lists the clones of a customer ("" = every customer), oldest first.
	Assignments(ctx context.Context, org, customerID string) ([]Assignment, error)

	// AddLink fails with domain.ErrConflict when the object is already linked (to any customer).
	AddLink(ctx context.Context, org string, l Link) error
	DeleteLink(ctx context.Context, org, kind, refID string) error
	// LinkOf is the link of one object (domain.ErrNotFound when it has none).
	LinkOf(ctx context.Context, org, kind, refID string) (Link, error)
	// Links lists the links of a kind, of one customer ("" = every customer), newest first, at most limit (0 = all).
	Links(ctx context.Context, org, customerID, kind string, limit int) ([]Link, error)
}

// ErrNotFound and the other sentinels are the domain ones (HTTP mapping in internal/api).
var (
	ErrNotFound = domain.ErrNotFound
	ErrInvalid  = domain.ErrInvalid
	ErrConflict = domain.ErrConflict
)
