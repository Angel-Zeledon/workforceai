// Package positions holds the positions inside a department (docs/architecture/positions.md): a position ("Gerente de
// Contabilidad", "Asistente de gerente", "Especialista") belongs to one department (office), may report to another
// position of the same department, and carries a package: the skills of the position and its library (the folders
// its agents search and read). A position that reports to another never has a skill, a folder or a level its
// superior does not have.
package positions

import (
	"errors"
	"fmt"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Folder levels of a library entry. LevelNone is an exclusion ("!path"): the subfolder stays out of the library.
const (
	LevelNone = "none"
	LevelRead = "read"
	LevelEdit = "edit"
)

// Templates of the starter positions.
const (
	TemplateManager    = "manager"
	TemplateAssistant  = "assistant"
	TemplateSpecialist = "specialist"
)

// Limits.
const (
	MaxPositionsPerOffice = 30
	MaxSkills             = 40
	MaxFolders            = 40
	MaxName               = 80
)

// Folder is one entry of a library.
type Folder struct {
	Path  string `json:"path"`
	Level string `json:"level"`
}

// Position is a position of a department.
type Position struct {
	ID        string    `json:"id"`
	OfficeID  string    `json:"office_id"`
	Name      string    `json:"name"`
	ReportsTo string    `json:"reports_to"`
	Skills    []string  `json:"skills"`
	Library   []Folder  `json:"library"`
	Template  string    `json:"template"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedBy string    `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
	// Occupants are the agents that hold it (filled by the service, not stored).
	Occupants []string `json:"occupants"`
}

// Package is what a position gives: its skills and its library. An empty library means "no library": the agent
// keeps the defaults of its department (Bounds.Defaults).
func (p Position) Package() Package { return Package{Skills: p.Skills, Library: p.Library} }

// Package is the bundle of a position.
type Package struct {
	Skills  []string `json:"skills"`
	Library []Folder `json:"library"`
}

// Assignment says which position an agent holds (one per agent). OfficeID is the department it was given in: an
// agent moved to another department loses it.
type Assignment struct {
	AgentID    string    `json:"agent_id"`
	PositionID string    `json:"position_id"`
	OfficeID   string    `json:"office_id"`
	AssignedBy string    `json:"assigned_by"`
	AssignedAt time.Time `json:"assigned_at"`
}

// FolderLoss is a folder a subordinate loses (or keeps at a lower level) when its superior is cut.
type FolderLoss struct {
	Path string `json:"path"`
	From string `json:"from"`
	To   string `json:"to"`
}

// Change is what one subordinate position loses.
type Change struct {
	PositionID    string       `json:"position_id"`
	Name          string       `json:"name"`
	SkillsRemoved []string     `json:"skills_removed"`
	Folders       []FolderLoss `json:"folders"`
}

// Impact is what the subordinates lose after a change of their superior.
type Impact struct {
	Changed []Change `json:"changed"`
}

// Excess is what a package has beyond its superior's.
type Excess struct {
	Skills  []string `json:"skills"`
	Folders []Folder `json:"folders"`
}

// Empty reports whether nothing exceeds.
func (e Excess) Empty() bool { return len(e.Skills) == 0 && len(e.Folders) == 0 }

// Error is a positions error with a stable code (the API sends it in the body).
type Error struct {
	Code    string
	Kind    error
	Msg     string
	Details any
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg }
func (e *Error) Unwrap() error { return e.Kind }

func invalid(code, format string, a ...any) *Error {
	return &Error{Code: code, Kind: domain.ErrInvalid, Msg: fmt.Sprintf(format, a...)}
}
func notFound(format string, a ...any) *Error {
	return &Error{Code: "not_found", Kind: domain.ErrNotFound, Msg: fmt.Sprintf(format, a...)}
}
func forbidden(code, format string, a ...any) *Error {
	return &Error{Code: code, Kind: domain.ErrForbidden, Msg: fmt.Sprintf(format, a...)}
}

// CodeOf is the stable code of an error ("" when it has none).
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}
