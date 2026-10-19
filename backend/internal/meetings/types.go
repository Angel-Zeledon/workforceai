// Package meetings is the meeting-room side of the office: meetings with
// people and agents as attendees, an agenda, a video link from a PROVIDER
// (Jitsi by default, Google Meet, Zoom or Microsoft Teams; we run no media
// stack), the calendar sync (Google Calendar / Microsoft 365 through the
// connections vault), an ICS export and subscription feed, and the minutes an
// agent writes after the meeting (summary, decisions, action items) saved to
// the drive, with an explicit step that turns the action items into tasks.
// See docs/architecture/meetings.md.
package meetings

import (
	"errors"
	"time"
)

// Status is the state of a meeting (see state.go for the machine).
type Status string

const (
	StatusScheduled Status = "scheduled"
	StatusLive      Status = "live"
	StatusEnded     Status = "ended"
	StatusCancelled Status = "cancelled"
)

// Video providers. Jitsi links are generated here; the others are attached
// (pasted by the person, or created by the calendar when the meeting is synced:
// Meet by Google Calendar, Teams by Microsoft 365).
const (
	ProviderJitsi  = "jitsi"
	ProviderMeet   = "meet"
	ProviderZoom   = "zoom"
	ProviderTeams  = "teams"
	ProviderCustom = "custom"
	ProviderNone   = "none"
)

// Attendee kinds.
const (
	KindPerson   = "person"   // a member of the organization
	KindAgent    = "agent"    // an AI employee
	KindExternal = "external" // a guest (identified by e-mail)
)

// Attendee is someone invited to a meeting.
type Attendee struct {
	Kind  string `json:"kind"`
	ID    string `json:"id,omitempty"` // person id or agent id
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

// AgendaItem is one point of the agenda. Owner (an attendee id) is who presents or prepares it.
type AgendaItem struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Minutes int    `json:"minutes,omitempty"`
	Owner   string `json:"owner,omitempty"`
	Notes   string `json:"notes,omitempty"`
}

// External links a meeting to its calendar event and agent briefing.
type External struct {
	// CalendarProvider is google_calendar_sync | microsoft_calendar when the meeting was written to a calendar.
	CalendarProvider string `json:"calendar_provider,omitempty"`
	ConnectionID     string `json:"connection_id,omitempty"`
	EventID          string `json:"event_id,omitempty"`
	// SyncError is the stable code of the last failed calendar write (the meeting itself is fine).
	SyncError string `json:"sync_error,omitempty"`
	// BriefRequestID is the request that carries the preparation tasks given to the invited agents.
	BriefRequestID string `json:"brief_request_id,omitempty"`
}

// Meeting is a scheduled, live or finished meeting.
type Meeting struct {
	ID          string       `json:"id"`
	OrgID       string       `json:"-"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Room        string       `json:"room"`
	Status      Status       `json:"status"`
	Start       time.Time    `json:"start"`
	End         time.Time    `json:"end"`
	Organizer   string       `json:"organizer"`
	Provider    string       `json:"provider"`
	JoinURL     string       `json:"join_url"`
	Attendees   []Attendee   `json:"attendees"`
	Agenda      []AgendaItem `json:"agenda"`
	External    External     `json:"external"`
	StartedAt   *time.Time   `json:"started_at"`
	EndedAt     *time.Time   `json:"ended_at"`
	CancelledAt *time.Time   `json:"cancelled_at"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`

	// Filled by the service for responses.
	HasMinutes bool `json:"has_minutes"`
	// Embeddable says the link can be shown inside the app (Jitsi, whose base URL the organization controls).
	Embeddable bool `json:"embeddable"`
}

// ActionItem is a follow-up from the minutes.
type ActionItem struct {
	Text string `json:"text"`
	// Owner is who must do it: an attendee name or id, as written in the notes.
	Owner string `json:"owner,omitempty"`
	// OwnerID is the attendee it matched (agent id, person id), when it matched.
	OwnerID   string `json:"owner_id,omitempty"`
	OwnerKind string `json:"owner_kind,omitempty"`
	Due       string `json:"due,omitempty"` // YYYY-MM-DD
	// TaskKey is set once a task was created for it (an item never becomes two tasks).
	TaskKey string `json:"task_key,omitempty"`
}

// Minutes are the minutes of a meeting.
type Minutes struct {
	MeetingID   string       `json:"meeting_id"`
	OrgID       string       `json:"-"`
	Summary     string       `json:"summary"`
	Decisions   []string     `json:"decisions"`
	ActionItems []ActionItem `json:"action_items"`
	Lang        string       `json:"lang"`
	// Source is notes | transcript | upload (how the text came in).
	Source string `json:"source"`
	// SummarySource is runtime | local (the model wrote it, or the deterministic fallback did).
	SummarySource string `json:"summary_source"`
	Truncated     bool   `json:"truncated,omitempty"` // the notes were longer than the limit
	FileID        string `json:"file_id,omitempty"`
	FilePath      string `json:"file_path,omitempty"`
	// RequestID is the request that carries the tasks created from the action items.
	RequestID string    `json:"request_id,omitempty"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Room is a meeting room of the office.
type Room struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"` // empty: the UI shows the translated default name
	Capacity int    `json:"capacity"`
}

// Settings are the per-organization meeting settings. The zero value means defaults.
type Settings struct {
	DefaultProvider string `json:"default_provider"`
	// JitsiBaseURL is the Jitsi server links are generated for (https, no path secrets). Empty: the platform default.
	JitsiBaseURL string `json:"jitsi_base_url"`
	// CalendarConnectionID is the connection meetings are written to ("sync to calendar"). Empty: sync is off.
	CalendarConnectionID string `json:"calendar_connection_id"`
	// FreeBusyConnectionID answers "when is everyone free". Empty: the calendar connection above.
	FreeBusyConnectionID string `json:"free_busy_connection_id"`
	// MinutesFolder is where minutes are saved in the drive.
	MinutesFolder string `json:"minutes_folder"`
	Rooms         []Room `json:"rooms"`
}

// Feed is an ICS subscription of one person. The token is shown once; only its hash is stored.
type Feed struct {
	ID        string     `json:"id"`
	OrgID     string     `json:"-"`
	UserID    string     `json:"user_id"`
	Label     string     `json:"label"`
	TokenHash string     `json:"-"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at"`
}

// Errors. The API maps them to stable codes.
var (
	ErrNotFound    = errors.New("meetings: not found")
	ErrInvalid     = errors.New("meetings: invalid input")
	ErrState       = errors.New("meetings: not allowed in the current state")
	ErrRoomBusy    = errors.New("meetings: the room is taken at that time")
	ErrUnavailable = errors.New("meetings: this feature is not configured")
	ErrNothingToDo = errors.New("meetings: nothing to do")
	// ErrBudget: the model call was refused because the budget is exhausted (the adapter wraps it).
	ErrBudget = errors.New("meetings: budget exceeded")
)

// Defaults.
const (
	DefaultMinutesFolder = "/Shared/Meetings"
	MaxAttendees         = 50
	MaxAgenda            = 30
	MaxDuration          = 12 * time.Hour
	MaxNotesChars        = 60000
)

// DefaultRooms are the rooms of an office that configured none.
func DefaultRooms() []Room {
	return []Room{{ID: "boardroom", Capacity: 12}, {ID: "huddle", Capacity: 4}, {ID: "focus", Capacity: 2}}
}
