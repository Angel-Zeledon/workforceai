// Package linkimports implements "import from a link" (docs/architecture/link-imports.md): a generic PULL import.
// An administrator pastes any URL that returns data (a published spreadsheet CSV link, a JSON/CSV export endpoint
// of any system, an XLSX or PDF file URL) and the platform downloads it into the drive as a versioned file, once
// or on a schedule (hourly, daily, weekly). No vendor is integrated: the link is the whole contract.
//
// Rules that never change:
//   - the download goes through the same SSRF guard as the agents' web tool and the generic API sources
//     (internal/webtool NetGuard): https by default, every redirect re-checked, every resolved address must be
//     public (CUSTOM_API_ALLOW_PRIVATE opts a self-hosted install into its LAN), a timeout, a size cap
//     (DRIVE_MAX_FILE_MB) and a type allowlist (csv, tsv, json, xlsx, txt, pdf);
//   - the optional auth header (one name + value) is sealed with the vault (CONNECTIONS_KEK) and never returned:
//     the API shows only that it is set and its last 4 characters;
//   - only humans with org:manage create, edit, run and delete imports; agents have no tool for it;
//   - a run writes a NEW version only when the content changed (SHA-256), attributed to the link import principal
//     (drive.PrincipalLinkImport). A file a person or an agent wrote is never overwritten;
//   - what is downloaded is untrusted external DATA. It is never interpreted here, and fs.read marks it as authored
//     by others (tainted), so any external action an agent takes after reading it needs a human approval;
//   - read-only mode and the kill switch skip runs (and the run history says why); every run is audited with
//     metadata only (host, size, hash, file id), never the content, the query string or the header.
package linkimports

import (
	"context"
	"errors"
	"time"
)

// Schedules.
const (
	ScheduleOnce   = "once" // only when a person presses "run now"
	ScheduleHourly = "hourly"
	ScheduleDaily  = "daily"
	ScheduleWeekly = "weekly"
)

// AllSchedules is the order shown in the UI.
var AllSchedules = []string{ScheduleOnce, ScheduleHourly, ScheduleDaily, ScheduleWeekly}

// Interval is the period of a schedule (0 for "once").
func Interval(schedule string) time.Duration {
	switch schedule {
	case ScheduleHourly:
		return time.Hour
	case ScheduleDaily:
		return 24 * time.Hour
	case ScheduleWeekly:
		return 7 * 24 * time.Hour
	}
	return 0
}

// Run outcomes.
const (
	RunOK        = "ok"        // a new file or a new version landed
	RunUnchanged = "unchanged" // the content equals what was imported last time: nothing written
	RunError     = "error"     // the download or the write failed (Code says why)
	RunSkipped   = "skipped"   // not attempted: read-only mode, kill switch (Code says which)
)

// Run triggers.
const (
	TriggerManual   = "manual"
	TriggerSchedule = "schedule"
)

// Stable error codes of a run (what the UI translates; never raw network errors).
const (
	CodeReadOnly            = "read_only_mode"
	CodeKillSwitch          = "kill_switch_active"
	CodeCustomerUnavailable = "customer_unavailable"
	CodeHeaderUnavailable   = "header_unavailable"
	CodeHTTPStatus          = "http_status"
	CodeTooLarge            = "too_large"
	CodeTypeNotAllowed      = "content_type_not_allowed"
	CodeWebPage             = "web_page_not_data"
	CodeEmpty               = "empty_response"
	CodeInsecureRedirect    = "insecure_redirect"
	CodeTooManyRedirects    = "too_many_redirects"
	CodeRedirect            = "redirect_invalid"
	CodeTimeout             = "timeout"
	CodeFetch               = "fetch_failed"
	CodeContentMismatch     = "content_mismatch"
	CodeQuota               = "quota_exceeded"
	CodeDrive               = "drive_error"
)

// Limits.
const (
	MaxName          = 80
	MaxFileStem      = 80
	MaxActive        = 50 // imports per organization
	MaxHeaderName    = 64
	MaxHeaderValue   = 4096
	DefaultRunsShown = 20
	MaxRunsShown     = 100
	// RunsKept is how many runs of one import the history keeps (older ones are pruned).
	RunsKept = 200
)

// Errors with their own HTTP mapping (internal/api/linkimports.go maps them through domain errors).
var (
	ErrReadOnly         = errors.New("linkimports: read-only mode")
	ErrKillSwitch       = errors.New("linkimports: kill switch active")
	ErrCustomerArchived = errors.New("linkimports: the import customer is archived or gone")
)

// LinkImport is one configured import.
type LinkImport struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	// URL is the link as the administrator pasted it. It may carry a token in its query string (published links
	// often do): it is shown only to administrators and never written to the audit (only its host is).
	URL string `json:"url"`
	// Folder is where the file lands. Without a customer it is an absolute drive path (/Shared/... or
	// /Projects/...). With a customer it is RELATIVE to that customer's folder (/Clients/<name>), resolved at every
	// run, so renaming the customer's folder never breaks the import nor moves data outside it.
	Folder     string `json:"folder"`
	CustomerID string `json:"customer_id"`
	// FileStem is the file name without extension ("" = the slug). The extension follows the downloaded type.
	FileStem string `json:"file_stem"`
	Schedule string `json:"schedule"`
	Enabled  bool   `json:"enabled"`
	// HeaderName is the optional auth header ("Authorization", "X-API-Key"...). Its value is sealed.
	HeaderName   string     `json:"header_name"`
	HeaderSealed []byte     `json:"-"`
	HeaderHint   string     `json:"header_hint"` // last 4 characters of the value; never enough to use it
	CreatedBy    string     `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	NextRunAt    *time.Time `json:"next_run_at"`
	LastRunAt    *time.Time `json:"last_run_at"`
	LastStatus   string     `json:"last_status"`
	LastCode     string     `json:"last_code"`
	// LastHash is the SHA-256 (hex) of the content imported last time ("" before the first successful run).
	LastHash    string     `json:"last_hash"`
	LastFileID  string     `json:"last_file_id"`
	LastPath    string     `json:"last_path"`
	LastVersion int        `json:"last_version"`
	LastDataAt  *time.Time `json:"last_data_at"` // when new data last landed
	RunCount    int64      `json:"run_count"`
}

// View is an import as the admin UI sees it (never the header value).
type View struct {
	LinkImport
	HeaderSet bool   `json:"header_set"`
	Host      string `json:"host"`
	// FolderPath is the absolute destination (the customer's folder resolved); "" when it cannot be resolved.
	FolderPath string `json:"folder_path"`
}

// HeaderInput sets, keeps or clears the auth header. On update, an empty Value with the same Name keeps the
// sealed value (the UI never has it); Clear removes it.
type HeaderInput struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Clear bool   `json:"clear"`
}

// Input creates or edits an import. Folder and CustomerID are fixed after creation (a new destination is a new
// import, so the history of where data went stays readable).
type Input struct {
	Name       string       `json:"name"`
	URL        string       `json:"url"`
	Folder     string       `json:"folder"`
	CustomerID string       `json:"customer_id"`
	FileStem   string       `json:"file_stem"`
	Schedule   string       `json:"schedule"`
	Enabled    *bool        `json:"enabled"`
	Header     *HeaderInput `json:"header"`
}

// Run is one attempt (metadata only, never content).
type Run struct {
	ID         string    `json:"id"`
	ImportID   string    `json:"import_id"`
	Trigger    string    `json:"trigger"`
	Actor      string    `json:"actor"`
	Status     string    `json:"status"`
	Code       string    `json:"code"`
	HTTPStatus int       `json:"http_status,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	DurationMS int64     `json:"duration_ms"`
	Size       int64     `json:"size"`
	Mime       string    `json:"mime"`
	Hash       string    `json:"hash"`
	FileID     string    `json:"file_id"`
	Path       string    `json:"path"`
	Version    int       `json:"version"`
}

// Store persists imports and their runs. Every method is scoped to org (Row-Level Security in Postgres,
// migration 804).
type Store interface {
	Create(ctx context.Context, org string, li LinkImport) error
	// Update writes name, url, file_stem, schedule, enabled, header_name, header_sealed, header_hint, next_run_at
	// and updated_at.
	Update(ctx context.Context, org string, li LinkImport) error
	Get(ctx context.Context, org, id string) (LinkImport, error)
	// List returns every import of org, newest first.
	List(ctx context.Context, org string) ([]LinkImport, error)
	// Delete removes an import and its run history (the files it wrote stay in the drive).
	Delete(ctx context.Context, org, id string) error
	// Due lists the enabled scheduled imports of org whose next_run_at is at or before now.
	Due(ctx context.Context, org string, now time.Time) ([]LinkImport, error)
	// Claim moves next_run_at from expected to next (compare-and-set) and reports whether this caller won: two
	// schedulers never run the same period twice.
	Claim(ctx context.Context, org, id string, expected, next time.Time) (bool, error)
	// RecordRun stores a run, copies its outcome to the import (last_run_at, last_status, last_code, run_count;
	// the hash and file when the run is ok or unchanged; last_data_at only when it is ok) and prunes the history
	// beyond RunsKept.
	RecordRun(ctx context.Context, org string, r Run) error
	// Runs lists the runs of an import, newest first.
	Runs(ctx context.Context, org, importID string, limit int) ([]Run, error)
}
