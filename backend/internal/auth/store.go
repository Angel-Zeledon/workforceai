package auth

import (
	"context"
	"time"
)

// User is a global account. A user can belong to several organizations.
type User struct {
	ID             string
	Email          string // normalized (lowercase)
	Name           string
	PasswordHash   string
	FailedAttempts int
	LockedUntil    time.Time // zero when not locked
	CreatedAt      time.Time
}

// Org is the tenant created at registration.
type Org struct {
	ID        string
	Name      string
	Slug      string
	BudgetUSD float64
}

// Membership links a user to an organization with a role.
type Membership struct {
	OrgID     string
	UserID    string
	Role      Role
	CreatedAt time.Time
}

// Member is a membership joined with the user's public data.
type Member struct {
	UserID    string    `json:"user_id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// RefreshToken is the persisted (hashed) form of a refresh token. Tokens
// rotate on every use; all tokens of a login share a FamilyID so that reuse
// of an already-rotated token revokes the whole family.
type RefreshToken struct {
	ID         string
	UserID     string
	OrgID      string
	FamilyID   string
	TokenHash  string
	ExpiresAt  time.Time
	CreatedAt  time.Time
	RevokedAt  time.Time // zero when active
	ReplacedBy string
	UserAgent  string
	IP         string
}

// Store is the persistence port of the auth module. All methods must be safe
// for concurrent use. Implementations: MemoryStore (tests), PGStore (Postgres).
type Store interface {
	// CreateAccount atomically creates the organization, the user and the
	// owner membership. Returns ErrEmailTaken or ErrSlugTaken on conflicts.
	CreateAccount(ctx context.Context, u User, o Org, m Membership) error

	UserByEmail(ctx context.Context, email string) (User, error) // ErrNotFound
	UserByID(ctx context.Context, id string) (User, error)       // ErrNotFound

	MembershipsOf(ctx context.Context, userID string) ([]Membership, error) // oldest first
	GetMembership(ctx context.Context, orgID, userID string) (Membership, error)
	ListMembers(ctx context.Context, orgID string) ([]Member, error)
	AddMembership(ctx context.Context, m Membership) error // ErrAlreadyMember
	// SetMemberRole changes a role; ErrNotFound, or ErrLastOwner when it
	// would leave the organization without an owner (atomic).
	SetMemberRole(ctx context.Context, orgID, userID string, role Role) error
	// RemoveMember deletes a membership; ErrNotFound or ErrLastOwner (atomic).
	RemoveMember(ctx context.Context, orgID, userID string) error

	// RecordLoginFailure atomically increments the failure counter (resetting
	// it first if a previous lock already expired) and locks the account for
	// lockFor once max failures are reached. Returns the updated user.
	RecordLoginFailure(ctx context.Context, userID string, max int, lockFor time.Duration, now time.Time) (User, error)
	ResetLoginFailures(ctx context.Context, userID string) error
	UpdatePasswordHash(ctx context.Context, userID, hash string) error

	SaveRefreshToken(ctx context.Context, t RefreshToken) error
	// RotateRefreshToken atomically consumes the token with hash oldHash and
	// stores next, inheriting UserID, OrgID and FamilyID from the consumed
	// token (the caller only fills ID, TokenHash, ExpiresAt, CreatedAt,
	// UserAgent and IP). Returns the consumed token. Errors: ErrInvalidToken (unknown), ErrTokenExpired, and
	// ErrTokenReuse (token was already rotated/revoked; its family is revoked).
	RotateRefreshToken(ctx context.Context, oldHash string, next RefreshToken, now time.Time) (RefreshToken, error)
	// RevokeFamilyOf revokes every token in the family of the given token
	// hash. Unknown tokens are ignored (idempotent logout).
	RevokeFamilyOf(ctx context.Context, tokenHash string, now time.Time) error
	RevokeUserTokens(ctx context.Context, userID string, now time.Time) error
	RevokeOrgUserTokens(ctx context.Context, orgID, userID string, now time.Time) error
}
