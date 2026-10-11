package drive

import (
	"context"
	"time"
)

// SearchQuery is a full-text search over file names and extracted text.
type SearchQuery struct {
	Query string
	// Under limits the search to these path prefixes (nil = everything). The
	// caller computes it from what the principal may read.
	Under []string
	Limit int
	// Config is the Postgres text search configuration: "spanish" (default) or "simple".
	Config string
}

// Hit is a search result.
type Hit struct {
	FileID  string  `json:"file_id"`
	Path    string  `json:"path"`
	Name    string  `json:"name"`
	Mime    string  `json:"mime"`
	Version int     `json:"version"`
	Score   float64 `json:"score"`
	Snippet string  `json:"snippet"`
	Size    int64   `json:"size"`
	// UpdatedBy is the last author (the content of an agent-written file is
	// treated as data like any other when it is read back).
	UpdatedBy Principal `json:"updated_by"`
}

// Trash lists the roots of deleted batches.
type Trash struct {
	Folders []Folder `json:"folders"`
	Files   []File   `json:"files"`
}

// Store persists metadata. Implementations scope every call by organization
// (RLS in Postgres). Content bytes are NOT here: see internal/objstore.
type Store interface {
	FolderByPath(ctx context.Context, org, path string) (Folder, error)
	FolderByID(ctx context.Context, org, id string) (Folder, error)
	CreateFolder(ctx context.Context, org string, f Folder) error
	ChildFolders(ctx context.Context, org, parentID string) ([]Folder, error)
	// MoveFolder re-parents/renames a folder and rewrites the paths of its subtree.
	MoveFolder(ctx context.Context, org, id, newParentID, newName, newPath string, at time.Time) error
	DeleteFolder(ctx context.Context, org, id string, by Principal, at time.Time) error
	RestoreFolder(ctx context.Context, org, id string, at time.Time) error

	CreateFile(ctx context.Context, org string, f File, v Version) error
	FileByID(ctx context.Context, org, id string) (File, error)
	FileByName(ctx context.Context, org, folderID, name string) (File, error)
	FilesIn(ctx context.Context, org, folderID string) ([]File, error)
	// AddVersion appends v and makes it current, only if the file's current
	// version is still expected (compare-and-swap): domain.ErrConflict otherwise.
	AddVersion(ctx context.Context, org string, expected int, f File, v Version) error
	// UpdateFile changes name, folder and tags (versions are not touched).
	UpdateFile(ctx context.Context, org string, f File) error
	DeleteFile(ctx context.Context, org, id string, by Principal, at time.Time) error
	RestoreFile(ctx context.Context, org, id string, at time.Time) error
	Versions(ctx context.Context, org, fileID string) ([]Version, error)
	VersionOf(ctx context.Context, org, fileID string, n int) (Version, error)
	Trash(ctx context.Context, org string) (Trash, error)
	// UsedBytes is the total size of every stored version (quota).
	UsedBytes(ctx context.Context, org string) (int64, error)
	Search(ctx context.Context, org string, q SearchQuery) ([]Hit, error)

	AddGrant(ctx context.Context, org string, g Grant) error
	Grants(ctx context.Context, org, agentID string) ([]Grant, error) // active; agentID "" = all
	RevokeGrant(ctx context.Context, org, id string, at time.Time) error
}
