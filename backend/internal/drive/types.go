// Package drive is the file system of the organization: folders, files and
// immutable versions. Bytes live in an object store (internal/objstore);
// Postgres keeps only metadata, versions and the extracted text used by search.
// Everything is versioned and reversible: nothing is ever overwritten or
// deleted for real by the application (a delete moves to the trash, a restore
// of an old version creates a new version).
//
// Humans act through the REST API (authorized by role); agents act through the
// Tool Gateway tools fs.* (see agent.go), where the policy of
// docs/architecture/drive.md decides: writes in the agent's own desk and in the
// folders of its projects run without approval, everything else asks a human.
package drive

import (
	"errors"
	"fmt"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Principal kinds. Agents never get approval capability.
const (
	PrincipalHuman  = "human"
	PrincipalAgent  = "agent"
	PrincipalSystem = "system"
	// PrincipalInbox is a data inbox (internal/inboxes): software outside the platform pushed the content.
	// It is never a human, so whatever it wrote is untrusted data for every agent that reads it.
	PrincipalInbox = "inbox"
)

// Principal is who performed an action.
type Principal struct {
	Kind string `json:"kind"` // human | agent | system | inbox
	ID   string `json:"id"`
}

// Human, AgentP and System build principals.
func Human(id string) Principal  { return Principal{PrincipalHuman, id} }
func AgentP(id string) Principal { return Principal{PrincipalAgent, id} }
func System() Principal          { return Principal{PrincipalSystem, "system"} }

// InboxP is the principal of a data inbox (its id).
func InboxP(id string) Principal { return Principal{PrincipalInbox, id} }

// IsAgent reports whether p is an agent.
func (p Principal) IsAgent() bool { return p.Kind == PrincipalAgent }

// Well-known top-level folders. They are created on demand.
const (
	RootProjects = "Projects" // /Projects/<project>
	RootClients  = "Clients"  // /Clients/<client>
	RootAgents   = "Agents"   // /Agents/<agent>  (the agent's desk)
	RootShared   = "Shared"   // /Shared
)

// Roots lists the allowed top-level folder names.
var Roots = []string{RootProjects, RootClients, RootAgents, RootShared}

// Folder is a node of the tree. The root "/" is virtual (no row).
type Folder struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id"` // "" for a top-level folder
	Name     string `json:"name"`
	Path     string `json:"path"`
	// RootKind is the top-level folder this one lives under (projects|clients|agents|shared).
	RootKind string `json:"root_kind"`
	// RefID links /Projects/<x>, /Clients/<x> and /Agents/<x> to the project,
	// client or agent they belong to. It is what the access policy uses.
	RefID        string     `json:"ref_id,omitempty"`
	CreatedBy    Principal  `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
	DeletedBy    *Principal `json:"deleted_by,omitempty"`
	DeletedBatch string     `json:"-"`
	// Virtual marks a well-known root that does not exist yet (it is created on
	// the first write into it).
	Virtual bool `json:"virtual,omitempty"`
}

// File is a file with its current version.
type File struct {
	ID             string     `json:"id"`
	FolderID       string     `json:"folder_id"`
	Name           string     `json:"name"`
	Path           string     `json:"path"`
	Mime           string     `json:"mime"`
	Size           int64      `json:"size"`
	CurrentVersion int        `json:"current_version"`
	SHA256         string     `json:"sha256"`
	Tags           []string   `json:"tags"`
	CreatedBy      Principal  `json:"created_by"`
	UpdatedBy      Principal  `json:"updated_by"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
	DeletedBy      *Principal `json:"deleted_by,omitempty"`
	DeletedBatch   string     `json:"-"`
	// Extraction says how the text of the file is read: text | table | docx |
	// none ("binary, not extracted").
	Extraction string `json:"extraction"`
	// SearchText is the bounded extracted text of the current version (index).
	SearchText string `json:"-"`
}

// Version is an immutable version of a file.
type Version struct {
	FileID    string    `json:"file_id"`
	Version   int       `json:"version"`
	ObjectKey string    `json:"-"`
	SHA256    string    `json:"sha256"`
	Size      int64     `json:"size"`
	Mime      string    `json:"mime"`
	Author    Principal `json:"author"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// Grant gives an agent read access to a folder subtree outside its own areas.
// Grants never allow writing without approval: writes outside the desk and the
// agent's projects always go through the policy engine.
type Grant struct {
	ID        string     `json:"id"`
	AgentID   string     `json:"agent_id"`
	Path      string     `json:"path"`
	Mode      string     `json:"mode"` // read
	CreatedBy Principal  `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// Listing is the content of a folder.
type Listing struct {
	Folder  Folder   `json:"folder"`
	Folders []Folder `json:"folders"`
	Files   []File   `json:"files"`
}

// Error carries a stable code and the sentinel that decides the HTTP status
// (errors.Is(err, domain.ErrInvalid) ...).
type Error struct {
	Code string
	Kind error
	Msg  string
	// Current is the current version of a version_conflict.
	Current int
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg }
func (e *Error) Unwrap() error { return e.Kind }

func errf(kind error, code, format string, a ...any) *Error {
	return &Error{Code: code, Kind: kind, Msg: fmt.Sprintf(format, a...)}
}

func invalid(code, format string, a ...any) *Error {
	return errf(domain.ErrInvalid, code, format, a...)
}
func conflict(code, format string, a ...any) *Error {
	return errf(domain.ErrConflict, code, format, a...)
}
func notFound(format string, a ...any) *Error {
	return errf(domain.ErrNotFound, "not_found", format, a...)
}
func forbidden(code, format string, a ...any) *Error {
	return errf(domain.ErrForbidden, code, format, a...)
}

// Sentinels with their own HTTP status (see internal/api/drive.go); use errors.Is.
var (
	ErrReadOnly        = errors.New("read_only_mode")         // 423
	ErrTooLarge        = errors.New("too_large")              // 413
	ErrQuota           = errors.New("quota_exceeded")         // 413
	ErrUnsupportedType = errors.New("unsupported_media_type") // 415
)

func readOnlyErr() error {
	return &Error{Code: "read_only_mode", Kind: ErrReadOnly, Msg: "the organization is in read-only mode"}
}
