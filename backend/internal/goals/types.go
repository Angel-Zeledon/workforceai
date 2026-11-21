// Package goals implements the goals of the company (F1, docs/architecture/goals.md): a short list of things the
// organization wants to achieve ("Subir las ventas del T4 un 15 %"). A request or a project may point to one, so
// the agents know what their work is for and the final report says which goal it contributed to.
//
// A goal may belong to a department (OfficeID). It follows the D3 seal: the goal of a private department is seen
// only by its members (administrators included only when they are members) and reaches only that department's
// agents. A goal of the whole company ("" office) or of an open department is everybody's.
package goals

import (
	"context"
	"time"
)

// Statuses of a goal. Only an active goal takes new work.
const (
	StatusActive   = "active"
	StatusAchieved = "achieved"
	StatusArchived = "archived"
)

// Limits: a short list, short texts (they travel to the agents inside a bounded memory budget).
const (
	MaxActive      = 10
	MaxTitle       = 120
	MaxDescription = 280
	MaxPeriod      = 40
)

// EventChanged is the live event of a created or changed goal.
const EventChanged = "goal.changed"

// Goal is one goal of the organization.
type Goal struct {
	ID          string `json:"id"`
	OfficeID    string `json:"office_id,omitempty"` // "" = the whole company
	OfficeName  string `json:"office_name,omitempty"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	// Period is free text ("2026-T4", "Primer semestre"); DueDate an optional deadline (a day, UTC).
	Period    string     `json:"period,omitempty"`
	DueDate   *time.Time `json:"due_date,omitempty"`
	CreatedBy string     `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// Input creates a goal. DueDate is "YYYY-MM-DD" or "".
type Input struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Period      string `json:"period"`
	DueDate     string `json:"due_date"`
	OfficeID    string `json:"office_id"`
}

// Patch changes a goal; nil fields stay. DueDate "" removes the deadline. The department of a goal does not change.
type Patch struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Period      *string `json:"period"`
	DueDate     *string `json:"due_date"`
	Status      *string `json:"status"`
}

// Actor is the person asking. Admin: the role has org:manage. Open: the open demo without accounts (everything).
type Actor struct {
	ID    string
	Admin bool
	Open  bool
}

// Store persists the goals of every organization; every call is scoped by org.
type Store interface {
	ListGoals(ctx context.Context, org string) ([]Goal, error)
	GetGoal(ctx context.Context, org, id string) (Goal, error)
	CreateGoal(ctx context.Context, org string, g Goal) error
	UpdateGoal(ctx context.Context, org string, g Goal) error
}
