// Package skills is the skill library of the organization: reusable know-how
// for agents in the open Agent Skills format (a folder with SKILL.md, YAML
// frontmatter and optional resource files). See docs/architecture/skills.md.
//
// Metadata lives in Postgres (migration 510); the files of every version live
// in the drive under /Shared/Skills/<name>/v<n>/ (no second blob store). A
// version is immutable. Only a human administrator turns a version into
// something agents can use; a skill never grants tools, connections or
// approvals, and its scripts are never executed by this package.
package skills

import (
	"errors"
	"fmt"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Status of a skill in the library.
const (
	StatusDraft    = "draft"
	StatusInReview = "in_review"
	StatusActive   = "active"
	StatusDisabled = "disabled"
)

// Source says where a skill came from.
const (
	SourceCreated  = "created"
	SourceImported = "imported"
	SourceFromWork = "from_work"
)

// Limits of a skill package (also enforced on import).
const (
	MaxPackageBytes = 2 << 20   // all files of one version
	MaxFileBytes    = 512 << 10 // one file
	MaxFiles        = 40        // including SKILL.md
	MaxZipEntries   = 300       // entries looked at before giving up
	MaxPathDepth    = 4
	MaxPathLen      = 160
	MaxNameLen      = 64
	MaxDescription  = 1024
	MaxChangelog    = 400
	MaxAllowedTools = 20
	MaxZipRatio     = 100 // compressed:uncompressed, per entry
	// SkillFile is the mandatory entry point of a package.
	SkillFile = "SKILL.md"
	// DriveRoot is where the files of every version live.
	DriveRoot = "/Shared/Skills"
)

// Skill is a skill of the library (the row; the files are in the drive).
type Skill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"` // of the latest version
	Status      string `json:"status"`
	Source      string `json:"source"`
	Builtin     bool   `json:"builtin"`
	// CurrentVersion is the latest version; ActiveVersion the one agents get (0 =
	// never approved). They differ while a newer version waits for review.
	CurrentVersion int `json:"current_version"`
	ActiveVersion  int `json:"active_version"`
	// ActiveDescription is the description of the active version (what agents see).
	ActiveDescription string     `json:"active_description,omitempty"`
	CreatedBy         string     `json:"created_by"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	ReviewedBy        string     `json:"reviewed_by,omitempty"`
	ReviewedAt        *time.Time `json:"reviewed_at,omitempty"`
	ReviewNote        string     `json:"review_note,omitempty"`
	DeletedAt         *time.Time `json:"deleted_at,omitempty"`
	// Agents is how many agents have it assigned directly (list view only).
	Agents int `json:"agents,omitempty"`
}

// FileMeta describes one file of a version (the bytes are in the drive).
type FileMeta struct {
	Path   string `json:"path"` // relative to the skill folder, "/" separated
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Script bool   `json:"script,omitempty"` // py, js, sh
}

// Warning is something a reviewer should see before approving.
type Warning struct {
	Code   string   `json:"code"` // injection | script | allowed_tools | no_fs
	Path   string   `json:"path,omitempty"`
	Detail string   `json:"detail,omitempty"`
	Hits   []string `json:"hits,omitempty"`
}

// Version is an immutable version of a skill.
type Version struct {
	SkillID      string     `json:"skill_id"`
	Version      int        `json:"version"`
	Author       string     `json:"author"`
	CreatedAt    time.Time  `json:"created_at"`
	Changelog    string     `json:"changelog"`
	Description  string     `json:"description"`
	AllowedTools []string   `json:"allowed_tools"` // informational, never a grant
	Files        []FileMeta `json:"files"`         // SKILL.md included
	Warnings     []Warning  `json:"warnings"`
	Source       string     `json:"source"`
	RestoredFrom int        `json:"restored_from,omitempty"`
	Bytes        int64      `json:"bytes"`
}

// File is a text file of a package with its content.
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Assignment links a skill to an agent.
type Assignment struct {
	AgentID    string    `json:"agent_id"`
	SkillID    string    `json:"skill_id"`
	AssignedBy string    `json:"assigned_by"`
	AssignedAt time.Time `json:"assigned_at"`
}

// Error carries a stable code; Kind decides the HTTP status.
type Error struct {
	Code string
	Kind error
	Msg  string
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg }
func (e *Error) Unwrap() error { return e.Kind }

func invalid(code, format string, a ...any) *Error {
	return &Error{Code: code, Kind: domain.ErrInvalid, Msg: fmt.Sprintf(format, a...)}
}
func conflict(code, format string, a ...any) *Error {
	return &Error{Code: code, Kind: domain.ErrConflict, Msg: fmt.Sprintf(format, a...)}
}
func notFound(format string, a ...any) *Error {
	return &Error{Code: "not_found", Kind: domain.ErrNotFound, Msg: fmt.Sprintf(format, a...)}
}
func forbidden(code, format string, a ...any) *Error {
	return &Error{Code: code, Kind: domain.ErrForbidden, Msg: fmt.Sprintf(format, a...)}
}

// ErrTooLarge is a package or upload over the limits (HTTP 413).
var ErrTooLarge = errors.New("too_large")

func tooLarge(code, format string, a ...any) *Error {
	return &Error{Code: code, Kind: ErrTooLarge, Msg: fmt.Sprintf(format, a...)}
}

// CodeOf returns the stable code of an error ("" if none).
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}
