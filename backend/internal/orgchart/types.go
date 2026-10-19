// Package orgchart is the corporate organization model: departments (a tree with a head and a
// cost center), agent job descriptions (role, responsibilities, allowed tools, escalation contact,
// working schedule, budget), delegation rules between people and agents, the org chart view and
// the industry templates that set all of it up (docs/architecture/organization.md).
//
// The package never widens what the engine, the organization or RBAC allow: a job description can
// only NARROW the tools of the agent's role, a schedule only DELAYS work (it is queued, never
// dropped) and a delegation rule only adds a check; with no rules everything behaves as before.
package orgchart

import (
	"time"
)

// Kind says whether a reference points to a person (a member) or an agent.
type Kind string

const (
	KindPerson Kind = "person"
	KindAgent  Kind = "agent"
)

// OwnerID is the alias of "the organization owner" for a person reference: templates cannot know a user id.
const OwnerID = "owner"

// Ref points to a person or an agent. The zero value means "nobody".
type Ref struct {
	Kind Kind   `json:"kind"`
	ID   string `json:"id"`
}

// IsZero reports whether the reference is empty.
func (r Ref) IsZero() bool { return r.Kind == "" && r.ID == "" }

// Key is the stable "kind:id" form used in graphs.
func (r Ref) Key() string { return string(r.Kind) + ":" + r.ID }

// Department is one node of the department tree.
type Department struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	ParentID   string    `json:"parent_id"`
	Head       Ref       `json:"head"`
	CostCenter string    `json:"cost_center"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Schedule is the working schedule of an agent. Days are 0 (Sunday) to 6 (Saturday), as time.Weekday.
// Start and End are "HH:MM" wall-clock times in Timezone; End may be "24:00" and may be earlier than
// Start (an overnight window that belongs to the day it starts). Disabled = always available.
type Schedule struct {
	Enabled  bool   `json:"enabled"`
	Timezone string `json:"timezone"`
	Days     []int  `json:"days"`
	Start    string `json:"start"`
	End      string `json:"end"`
}

// Job is the job description of an agent.
type Job struct {
	AgentID          string    `json:"agent_id"`
	DepartmentID     string    `json:"department_id"`
	ReportsTo        Ref       `json:"reports_to"`
	Escalation       Ref       `json:"escalation"`
	Responsibilities []string  `json:"responsibilities"`
	AllowedTools     []string  `json:"allowed_tools"`
	Schedule         Schedule  `json:"schedule"`
	BudgetUSD        *float64  `json:"budget_usd"` // monthly cap; nil = the organization default
	UpdatedBy        string    `json:"updated_by"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// Placement is where a person (a member) sits in the org chart.
type Placement struct {
	UserID       string    `json:"user_id"`
	DepartmentID string    `json:"department_id"`
	Title        string    `json:"title"`
	ReportsTo    Ref       `json:"reports_to"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Person is a member of the organization as the chart shows it.
type Person struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

// SelectorType is how a delegation rule picks the people / agents it talks about.
type SelectorType string

const (
	SelAny        SelectorType = "any"
	SelAnyPerson  SelectorType = "any_person"
	SelAnyAgent   SelectorType = "any_agent"
	SelPerson     SelectorType = "person"
	SelAgent      SelectorType = "agent"
	SelDepartment SelectorType = "department"
)

// Selector picks actors: everybody, every person, every agent, one person, one agent or a department
// (its people and agents, and those of its sub-departments).
type Selector struct {
	Type SelectorType `json:"type"`
	ID   string       `json:"id"`
}

// Rule is one delegation rule: From may (or may not) assign work To.
type Rule struct {
	ID              string    `json:"id"`
	From            Selector  `json:"from"`
	To              Selector  `json:"to"`
	Effect          string    `json:"effect"` // "allow" | "deny"
	RequireApproval bool      `json:"require_approval"`
	Approver        Ref       `json:"approver"`
	Note            string    `json:"note"`
	CreatedBy       string    `json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
}

const (
	EffectAllow = "allow"
	EffectDeny  = "deny"
)
