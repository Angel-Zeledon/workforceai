package auth

import "time"

// E1 "Alta y agentes por rol" (docs/architecture/equipos-oficinas.md §6): who an organization is for, and the
// profession and team (a department) of each person. These records only describe; they grant nothing. The API layer
// reads them to give a new organization the agents of its people's work instead of the 7 fixed ones, to place each
// person in their team and to choose the welcome each one sees.

// Audience answers "who is it for?" at sign-up.
type Audience string

const (
	AudienceSolo    Audience = "solo"    // just me: a personal office, no team steps
	AudienceTeam    Audience = "team"    // my team: one named team, colleagues invited now or later
	AudienceCompany Audience = "company" // my company: several teams, each with its responsible
)

// Valid reports whether a is one of the three answers.
func (a Audience) Valid() bool {
	return a == AudienceSolo || a == AudienceTeam || a == AudienceCompany
}

// How a person came into an organization (MemberProfile.Via).
const (
	ViaSignup     = "signup"     // created the organization
	ViaInvitation = "invitation" // accepted an invitation
	ViaDomain     = "domain"     // registered with an address on the organization's verified domain
	ViaSSO        = "sso"        // single sign-on (just-in-time)
	ViaSelf       = "self"       // filled in their profile in their own welcome
)

// OrgSetup exists only for organizations created by the new sign-up. SetupDoneAt is when the responsible finished (or
// skipped) setting up the teams.
type OrgSetup struct {
	OrgID       string
	Audience    Audience
	Locale      string
	CreatedAt   time.Time
	SetupDoneAt time.Time
}

// MemberProfile is the profession and team of a person in an organization. TeamName is a team still to be created
// (the owner of a "team" sign-up); DepartmentID the team they are in. ProvisionedAt is set once their suggested agents
// and their place in the team were set up.
type MemberProfile struct {
	OrgID         string
	UserID        string
	Profession    string
	DepartmentID  string
	TeamName      string
	Lead          bool // responsible of their team
	Via           string
	InvitedBy     string
	ProvisionedAt time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// SignupIntent keeps what a person who registers to join their company by its verified domain chose, until the
// verification link proves the address. If the company no longer lets them in, their own organization is created
// from it.
type SignupIntent struct {
	UserID     string
	Audience   Audience
	Profession string
	OrgName    string
	TeamName   string
	Locale     string
	CreatedAt  time.Time
}
