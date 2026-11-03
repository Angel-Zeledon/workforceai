// Package offices is E2 "Oficinas" (docs/architecture/equipos-oficinas.md §3, §4 and §8;
// docs/superpowers/plans/2026-11-03-e2-oficinas.md): a department of the org chart becomes an office with members, a
// responsible (lead) and a visibility (open or private); every agent has an owner (a person or an office) and a
// visibility ladder (private, office, shared with people, company directory); offices and people keep their own
// memory (notes) that a lead may promote to company knowledge; each office may have its own 3D layout.
//
// "General" is a virtual office that always exists: open, every person of the organization is a member and the
// admins are its leads. An agent without an explicit access row belongs to the office of its department (when that
// department is an office) or to General, with visibility "office". So an organization without offices and without
// rules behaves exactly as before: everybody sees and uses every agent.
package offices

import "time"

// GeneralID is the id of the virtual office every organization has.
const GeneralID = "general"

// Office visibility.
const (
	Open    = "open"
	Private = "private"
)

// Member roles.
const (
	RoleLead   = "lead"
	RoleMember = "member"
)

// Agent owner kinds.
const (
	OwnerPerson = "person"
	OwnerOffice = "office"
)

// Agent visibility ladder (cumulative: each step includes the previous ones).
const (
	VisPrivate   = "private"
	VisOffice    = "office"
	VisPeople    = "people"
	VisDirectory = "directory"
)

// Share levels.
const (
	ShareChat = "chat"
	ShareEdit = "edit"
)

// Note scopes.
const (
	ScopeOffice  = "office"
	ScopePerson  = "person"
	ScopeCompany = "company"
)

// OfficeRow is a stored office: a department with the office turned on.
type OfficeRow struct {
	DepartmentID string    `json:"department_id"`
	Visibility   string    `json:"visibility"`
	CreatedBy    string    `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// MemberRow is a stored office membership.
type MemberRow struct {
	OfficeID string    `json:"office_id"`
	UserID   string    `json:"user_id"`
	Role     string    `json:"role"`
	AddedBy  string    `json:"added_by"`
	AddedAt  time.Time `json:"added_at"`
}

// Share gives one person the use (chat) or the edition (edit) of an agent.
type Share struct {
	UserID string `json:"user_id"`
	Level  string `json:"level"`
}

// AgentAccess is the explicit owner and visibility of an agent (absent = the defaults, see the package doc).
type AgentAccess struct {
	AgentID    string    `json:"agent_id"`
	OwnerKind  string    `json:"owner_kind"`
	OwnerID    string    `json:"owner_id"`
	HomeOffice string    `json:"home_office"` // for a personal agent: the office it goes to if its owner leaves
	Visibility string    `json:"visibility"`
	Shares     []Share   `json:"shares"`
	UpdatedBy  string    `json:"updated_by"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Note is a memory of an office, of a person or of the company (promoted).
type Note struct {
	ID           string    `json:"id"`
	Scope        string    `json:"scope"`
	OwnerRef     string    `json:"owner_ref"` // office id, user id, or "" for company
	Text         string    `json:"text"`
	PromotedFrom string    `json:"promoted_from,omitempty"`
	CreatedBy    string    `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
}

// Dept is a department of the org chart as the offices see it.
type Dept struct {
	ID       string
	Name     string
	HeadUser string // the head when it is a person ("" otherwise)
}

// Person is a member of the organization.
type Person struct {
	UserID string
	Name   string
	Admin  bool // owner or admin
}

// AgentRef is an agent of the organization.
type AgentRef struct {
	ID    string
	Name  string
	Role  string
	Title string
}

// Viewer is the person asking.
type Viewer struct {
	UserID string
	Admin  bool
}

// Office is an office as the model computes it.
type Office struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Visibility string    `json:"visibility"`
	General    bool      `json:"general"`
	Leads      []string  `json:"leads"`
	Members    []string  `json:"members"` // every member, leads included (General: everybody)
	CreatedAt  time.Time `json:"created_at"`
}

// Rights is what a viewer may do with an agent.
type Rights struct {
	See  bool `json:"see"`
	Use  bool `json:"use"`
	Edit bool `json:"edit"`
}

// EffectiveAccess is the access of an agent after the defaults and the departed-owner rule.
type EffectiveAccess struct {
	AgentID       string  `json:"agent_id"`
	OwnerKind     string  `json:"owner_kind"`
	OwnerID       string  `json:"owner_id"`
	OfficeID      string  `json:"office_id"` // the office it belongs to (the home office of a personal agent)
	Visibility    string  `json:"visibility"`
	Shares        []Share `json:"shares"`
	Explicit      bool    `json:"explicit"`
	Departed      bool    `json:"departed,omitempty"`
	PreviousOwner string  `json:"previous_owner,omitempty"`
}
