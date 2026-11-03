package offices

import "context"

// Store persists offices, memberships, agent access and notes. Every method is scoped to one organization
// (Row-Level Security in Postgres). The 3D layout of each office lives with the other layouts (internal/rewards).
type Store interface {
	ListOffices(ctx context.Context, org string) ([]OfficeRow, error)
	PutOffice(ctx context.Context, org string, o OfficeRow) error
	DeleteOffice(ctx context.Context, org, id string) error // also its members

	ListMembers(ctx context.Context, org string) ([]MemberRow, error)
	PutMember(ctx context.Context, org string, m MemberRow) error
	DeleteMember(ctx context.Context, org, office, user string) error

	ListAgentAccess(ctx context.Context, org string) ([]AgentAccess, error)
	PutAgentAccess(ctx context.Context, org string, a AgentAccess) error
	DeleteAgentAccess(ctx context.Context, org, agentID string) error

	ListNotes(ctx context.Context, org, scope, ref string) ([]Note, error) // scope "" = every scope; ref "" = every owner
	PutNote(ctx context.Context, org string, n Note) error
	DeleteNote(ctx context.Context, org, id, by string) error // soft: hidden from every list
}
