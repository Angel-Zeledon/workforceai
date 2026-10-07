// Package connections models third-party connections (Gmail first): their
// state, capabilities, per-agent grants, limits and usage. Secrets live ONLY
// in the vault; nothing in this package serializes a credential.
package connections

import (
	"context"
	"errors"
	"time"

	"aiworkforce/backend/internal/vault"
)

// Connection statuses (see the state diagram in integrations-credentials.md 3.4).
const (
	StatusPending     = "pending"
	StatusActive      = "active"
	StatusNeedsReauth = "needs_reauth"
	StatusError       = "error"
	StatusSuspended   = "suspended"
	StatusRevoked     = "revoked"
	StatusExpired     = "expired"
)

// Connection modes.
const (
	ModeLive      = "live"
	ModeSimulated = "simulated"
)

// Grant statuses. "pending_second_approval" is a write grant waiting for a second human.
const (
	GrantActive          = "active"
	GrantPendingApproval = "pending_second_approval"
	GrantSuspended       = "suspended"
	GrantRevoked         = "revoked"
	GrantExpired         = "expired"
)

// Stable error / deny codes (the UI translates them; never raw provider text).
const (
	CodeScopeNotGranted       = "scope_not_granted"
	CodeConnectionUnavailable = "connection_unavailable"
	CodeReauthRequired        = "reauth_required"
	CodeReadOnlyMode          = "read_only_mode"
	CodeKillSwitch            = "kill_switch_active"
	CodeAgentPaused           = "agent_paused"
	CodeRateLimit             = "rate_limit"
	CodeBudget                = "budget_exceeded"
	CodeRiskExceedsGrant      = "risk_exceeds_grant"
	CodeControlsUnavailable   = "controls_unavailable"
	CodeOutOfScope            = "resource_out_of_scope"
	CodeSecretDetected        = "secret_detected"
	CodeProviderError         = "provider_error"
	CodeInvalidArgs           = "invalid_args"
)

var (
	ErrNotFound         = errors.New("connections: not found")
	ErrInvalid          = errors.New("connections: invalid input")
	ErrConflict         = errors.New("connections: conflict")
	ErrNoKEK            = vault.ErrNoKEK
	ErrOAuthUnavailable = errors.New("connections: OAuth app not configured for this provider")
	// ErrNeedsSecondApprover: a write grant needs a different human to approve it.
	ErrNeedsSecondApprover = errors.New("connections: a second human must approve this write grant")
)

// Limits caps usage of a connection or a grant (a grant can only be <= its connection).
type Limits struct {
	PerMinute        int     `json:"per_minute,omitempty"`
	PerHour          int     `json:"per_hour,omitempty"`
	PerDay           int     `json:"per_day,omitempty"`
	WritePerDay      int     `json:"write_per_day,omitempty"`
	MaxItemsPerCall  int     `json:"max_items_per_call,omitempty"`
	MaxBytesPerDay   int64   `json:"max_bytes_per_day,omitempty"`
	MonthlyBudgetUSD float64 `json:"monthly_budget_usd,omitempty"`
	OnExceed         string  `json:"on_exceed,omitempty"` // deny (default)
}

// ResourceScope narrows what a connection/grant may touch. Grants can only
// narrow the connection's scope.
type ResourceScope struct {
	Labels        []string `json:"labels,omitempty"`
	ExcludeLabels []string `json:"exclude_labels,omitempty"`
	MaxAgeDays    int      `json:"max_age_days,omitempty"`
}

// Constraints are extra rules of a grant.
type Constraints struct {
	AllowedRecipientDomains []string `json:"allowed_recipient_domains,omitempty"`
	CustomerScoped          bool     `json:"customer_scoped,omitempty"`
}

// Connection is the public (secret-free) view of a connection.
type Connection struct {
	ID                    string        `json:"id"`
	OrgID                 string        `json:"-"`
	Provider              string        `json:"provider"`
	Kind                  string        `json:"kind"`
	Label                 string        `json:"label"`
	AccountLabel          string        `json:"account_label"`
	AccountRef            string        `json:"-"` // stable provider id (hashed), never shown
	Mode                  string        `json:"mode"`
	Status                string        `json:"status"`
	StatusReason          string        `json:"status_reason"`
	Class                 string        `json:"profile"` // read | write
	RequestedCapabilities []string      `json:"requested_capabilities"`
	GrantedCapabilities   []string      `json:"granted_capabilities"`
	ProviderScopes        []string      `json:"provider_scopes"`
	ResourceScope         ResourceScope `json:"resource_scope"`
	Limits                Limits        `json:"limits"`
	ReadOnly              bool          `json:"read_only"`
	LastTestedAt          *time.Time    `json:"last_tested_at"`
	LastTestStatus        string        `json:"last_test_status"`
	LastUsedAt            *time.Time    `json:"last_used_at"`
	LastErrorCode         string        `json:"last_error_code"`
	ExpiresAt             *time.Time    `json:"expires_at"`
	CreatedBy             string        `json:"created_by"`
	CreatedAt             time.Time     `json:"created_at"`
	UpdatedAt             time.Time     `json:"updated_at"`
	RevokedAt             *time.Time    `json:"revoked_at"`
	RevokedBy             string        `json:"revoked_by,omitempty"`
	// PendingProviderRevocation is true while the provider-side revoke failed.
	PendingProviderRevocation bool `json:"pending_provider_revocation"`

	// OAuthClientID is the public client id of the owner's own OAuth app
	// ("bring your own app"); the client secret is stored sealed and never returned.
	OAuthClientID        string `json:"oauth_client_id,omitempty"`
	OAuthClientSecretEnc []byte `json:"-"`

	// Filled by the service for responses.
	Credential  *vault.Meta `json:"credential"`
	GrantsCount int         `json:"grants_count"`
}

// Grant lets one agent use some capabilities of one connection.
type Grant struct {
	ID               string        `json:"id"`
	OrgID            string        `json:"-"`
	ConnectionID     string        `json:"connection_id"`
	AgentID          string        `json:"agent_id"`
	Capabilities     []string      `json:"capabilities"`
	ResourceScope    ResourceScope `json:"resource_scope"`
	Constraints      Constraints   `json:"constraints"`
	MaxRisk          string        `json:"max_risk"`
	AutonomyOverride string        `json:"autonomy_override,omitempty"`
	Limits           Limits        `json:"limits"`
	RedactionProfile string        `json:"redaction_profile"`
	IsDefault        bool          `json:"is_default"`
	Alias            string        `json:"alias,omitempty"`
	Status           string        `json:"status"`
	ValidFrom        time.Time     `json:"valid_from"`
	ValidUntil       *time.Time    `json:"valid_until"`
	GrantedBy        string        `json:"granted_by"`
	// RequestedBy is the maker of a write grant (same human as GrantedBy);
	// ApprovedBy is the checker (a different human).
	RequestedBy string     `json:"requested_by"`
	ApprovedBy  string     `json:"approved_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	RevokedAt   *time.Time `json:"revoked_at"`
	RevokedBy   string     `json:"revoked_by,omitempty"`
}

// Usage is one row of the append-only usage projection. It stores metadata,
// never content.
type Usage struct {
	ID            string    `json:"id"`
	OrgID         string    `json:"-"`
	ConnectionID  string    `json:"connection_id"`
	GrantID       string    `json:"grant_id,omitempty"`
	AgentID       string    `json:"agent_id"`
	TaskID        string    `json:"task_id,omitempty"`
	ApprovalID    string    `json:"approval_id,omitempty"`
	OnBehalfOf    string    `json:"on_behalf_of,omitempty"`
	Tool          string    `json:"tool"`
	Action        string    `json:"action"`
	Capability    string    `json:"capability"`
	Decision      string    `json:"decision"` // allowed | needs_approval | denied
	DenyReason    string    `json:"deny_reason,omitempty"`
	Status        string    `json:"status"` // succeeded | failed | skipped | held | scheduled
	ResourceRef   string    `json:"resource_ref,omitempty"`
	ItemsCount    int       `json:"items_count"`
	BytesIn       int64     `json:"bytes_in"`
	BytesOut      int64     `json:"bytes_out"`
	LatencyMS     int       `json:"latency_ms"`
	ProviderState int       `json:"provider_status,omitempty"`
	ErrorCode     string    `json:"error_code,omitempty"`
	CostUSD       float64   `json:"cost_usd"`
	Tainted       bool      `json:"tainted"`
	CreatedAt     time.Time `json:"created_at"`
}

// Hold is a deferred external-effect action (email send) waiting out its
// cancellation window.
type Hold struct {
	ID           string         `json:"id"`
	OrgID        string         `json:"-"`
	ConnectionID string         `json:"connection_id"`
	GrantID      string         `json:"grant_id"`
	AgentID      string         `json:"agent_id"`
	TaskID       string         `json:"task_id,omitempty"`
	ApprovalID   string         `json:"approval_id,omitempty"`
	Tool         string         `json:"tool"`
	Action       string         `json:"action"`
	Capability   string         `json:"capability"`
	Payload      map[string]any `json:"-"` // message content: never returned by the API
	Summary      string         `json:"summary"`
	Recipients   []string       `json:"recipients"`
	Status       string         `json:"status"` // held | sent | cancelled | failed
	Reason       string         `json:"reason,omitempty"`
	HoldUntil    time.Time      `json:"hold_until"`
	CreatedAt    time.Time      `json:"created_at"`
	DecidedBy    string         `json:"decided_by,omitempty"`
	Tainted      bool           `json:"tainted"`
}

// Hold statuses.
const (
	HoldHeld      = "held"
	HoldSent      = "sent"
	HoldCancelled = "cancelled"
	HoldFailed    = "failed"
)

// UsageFilter selects usage rows.
type UsageFilter struct {
	ConnectionID string
	AgentID      string
	Result       string // allowed | needs_approval | denied
	From, To     *time.Time
	Limit        int
	Before       string // cursor: id of the last row seen
}

// UsageSummary aggregates usage.
type UsageSummary struct {
	Reads    int     `json:"reads"`
	Writes   int     `json:"writes"`
	Denied   int     `json:"denied"`
	Approval int     `json:"needs_approval"`
	CostUSD  float64 `json:"cost_usd"`
	Period   string  `json:"period"`
}

// Store is the persistence port of connections, grants, usage and holds.
type Store interface {
	CreateConnection(ctx context.Context, c Connection) error
	GetConnection(ctx context.Context, org, id string) (Connection, error)
	UpdateConnection(ctx context.Context, c Connection) error
	ListConnections(ctx context.Context, org, provider, status string) ([]Connection, error)

	PutOAuthState(ctx context.Context, s OAuthState) error
	// ConsumeOAuthState atomically marks the state used and returns it.
	// ErrNotFound if unknown, used or expired.
	ConsumeOAuthState(ctx context.Context, stateHash string, now time.Time) (OAuthState, error)

	UpsertGrant(ctx context.Context, g Grant) error
	GetGrant(ctx context.Context, org, connID, agentID string) (Grant, error) // active or pending
	ListGrantsByConnection(ctx context.Context, org, connID string) ([]Grant, error)
	ListGrantsByAgent(ctx context.Context, org, agentID string) ([]Grant, error)
	RevokeGrants(ctx context.Context, org, connID, by string, at time.Time) (int, error)

	AddUsage(ctx context.Context, u Usage) error
	ListUsage(ctx context.Context, org string, f UsageFilter) ([]Usage, error)
	// CountUsage counts succeeded/scheduled calls (and bytes) since `since`.
	CountUsage(ctx context.Context, org, connID, agentID string, since time.Time, writesOnly bool) (n int, bytes int64, err error)
	SumCost(ctx context.Context, org, connID string, since time.Time) (float64, error)

	PutHold(ctx context.Context, h Hold) error
	GetHold(ctx context.Context, org, id string) (Hold, error)
	UpdateHold(ctx context.Context, h Hold) error
	ListHolds(ctx context.Context, org, status string) ([]Hold, error)
	// ListDueHolds returns held holds of every org whose window ended.
	ListDueHolds(ctx context.Context, now time.Time) ([]Hold, error)
}

// OAuthState is a pending OAuth authorization (single use, short lived).
type OAuthState struct {
	StateHash    string
	OrgID        string
	ConnectionID string
	UserID       string
	// Verifier is the PKCE verifier. It is stored encrypted by the store's
	// owner (the service seals it with the vault before handing it here).
	VerifierEnc  []byte
	RedirectURI  string
	Capabilities []string
	ExpiresAt    time.Time
}
