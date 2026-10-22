// Package rituals holds the team rituals of the office (B4, docs/architecture/rituals.md): a daily stand-up where
// every agent says what it did, what it will do and what blocks it, and a weekly retrospective where every agent
// proposes up to two improvements to ITS OWN work.
//
// Both are built deterministically from data the platform already records (tasks, approvals, the quality review and
// the quality score): no model call is needed and the simulation stays free. A model may only write the optional
// team summary, when the organization turns it on, through the normal summarize path (budget reservation, ledger,
// local fallback). Retro proposals are suggestions: a person accepts or dismisses them; accepting an instruction note
// is an ordinary, versioned edit of the agent work profile made as that person. Nothing here grants tools,
// permissions or autonomy, and agents can neither run a ritual on their own nor accept a proposal.
package rituals

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Kind of ritual.
type Kind string

const (
	KindStandup Kind = "standup"
	KindRetro   Kind = "retro"
)

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool { return k == KindStandup || k == KindRetro }

// Origin of a ritual: the schedule or a person pressing "now".
const (
	OriginScheduled = "scheduled"
	OriginManual    = "manual"
)

// Status of a ritual.
const (
	StatusHeld    = "held"
	StatusSkipped = "skipped"
)

// Summary sources: the deterministic text, or the summarize path (runtime model or its local fallback).
const (
	SummaryDeterministic = "deterministic"
	SummaryRuntime       = "runtime"
	SummaryLocal         = "local"
)

// Item statuses inside an entry.
const (
	ItemDone            = "done"
	ItemFailed          = "failed"
	ItemRunning         = "running"
	ItemPending         = "pending"
	ItemBlocked         = "blocked"
	ItemAwaitingApprove = "awaiting_approval"
	ItemApproval        = "approval_pending" // a pending approval of the agent (not a task)
)

// Item is one task (or pending approval) mentioned in an entry. Titles are data written by people or the planner.
type Item struct {
	TaskID string `json:"task_id,omitempty"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// RetroStats are the numbers behind an agent's retrospective (window of the retro).
type RetroStats struct {
	TasksDone   int    `json:"tasks_done"`
	TasksFailed int    `json:"tasks_failed"`
	Rejected    int    `json:"rejected"`
	Reworks     int    `json:"reworks"`
	Score       *int   `json:"score"`
	Level       string `json:"level,omitempty"`
	Trend       string `json:"trend,omitempty"`
}

// Entry is what one agent says in the ritual.
type Entry struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
	Role    string `json:"role"`
	// Done is what it finished (or failed) since the previous stand-up; Next what it will work on; Blockers what
	// stops it (blocked tasks, tasks and approvals waiting on a person).
	Done     []Item `json:"done"`
	Next     []Item `json:"next"`
	Blockers []Item `json:"blockers"`
	// Lines are the 2-3 sentences of the entry in the organization language (chat and API consumers; the UI
	// renders the items with its own translations).
	Lines []string `json:"lines"`
	// Quiet means nothing to report in the window (still present, shown collapsed).
	Quiet bool        `json:"quiet"`
	Retro *RetroStats `json:"retro,omitempty"`
}

// Absent is an agent that did not take part (paused by a person).
type Absent struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
	Reason  string `json:"reason"`
}

// Ritual is one stand-up or retrospective.
type Ritual struct {
	ID         string     `json:"id"`
	Kind       Kind       `json:"kind"`
	Origin     string     `json:"origin"`
	PeriodKey  string     `json:"period_key,omitempty"` // local date (stand-up) or ISO week (retro) of a scheduled one
	Status     string     `json:"status"`
	SkipReason string     `json:"skip_reason,omitempty"`
	Locale     string     `json:"locale"`
	WindowFrom *time.Time `json:"window_from,omitempty"`
	WindowTo   *time.Time `json:"window_to,omitempty"`
	Entries    []Entry    `json:"entries"`
	Absent     []Absent   `json:"absent"`
	// Summary is the team summary; SummarySource says who wrote it (deterministic | runtime | local).
	Summary       string    `json:"summary"`
	SummarySource string    `json:"summary_source"`
	CreatedBy     string    `json:"created_by"`
	HeldAt        time.Time `json:"held_at"`
	// Proposals are filled when a single retro is read.
	Proposals []Proposal `json:"proposals,omitempty"`
}

// Proposal kinds.
const (
	ProposalInstructionNote = "instruction_note" // a sentence a person may add to the agent's instructions
	ProposalSkillSuggestion = "skill_suggestion" // a procedure worth saving as a skill (a person creates and reviews it)
)

// Proposal statuses.
const (
	ProposalPending   = "pending"
	ProposalAccepted  = "accepted"
	ProposalDismissed = "dismissed"
)

// Signals behind a proposal.
const (
	SignalRejected = "rejected_approvals"
	SignalReworks  = "reworks"
	SignalFailed   = "failed_tasks"
	SignalQuality  = "quality_score"
)

// Proposal is an improvement a retro suggests to one agent about its own work.
type Proposal struct {
	ID       string `json:"id"`
	RitualID string `json:"ritual_id"`
	AgentID  string `json:"agent_id"`
	Kind     string `json:"kind"`
	Signal   string `json:"signal"`
	Title    string `json:"title"`
	// Text is platform-written (fixed per signal, only numbers vary): never task titles or other third-party text,
	// because an accepted instruction note becomes part of the agent's instructions.
	Text string `json:"text"`
	// Evidence lists what triggered it (task / approval titles, counts): data shown to the person, never applied.
	Evidence   []string   `json:"evidence"`
	Status     string     `json:"status"`
	Applied    bool       `json:"applied"` // the note was added to the work profile when accepted
	CreatedAt  time.Time  `json:"created_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy string     `json:"resolved_by,omitempty"`
}

// ProposalQuery filters ListProposals ("" = any).
type ProposalQuery struct {
	Status   string
	AgentID  string
	RitualID string
	Limit    int
}

// Store persists rituals, proposals and settings (memory and Postgres implement it). Every method is org-scoped.
type Store interface {
	// CreateRitual inserts a ritual. A scheduled ritual whose (kind, period key) already exists is not inserted and
	// inserted is false: that is the claim that keeps two instances from holding the same occurrence.
	CreateRitual(ctx context.Context, org string, r Ritual) (inserted bool, err error)
	// HasScheduled reports whether the scheduled occurrence (kind, period key) exists.
	HasScheduled(ctx context.Context, org string, kind Kind, periodKey string) (bool, error)
	SetRitualSummary(ctx context.Context, org, id, summary, source string) error
	GetRitual(ctx context.Context, org, id string) (Ritual, error)
	// ListRituals returns the rituals newest first; kind "" = both; since zero = any; limit <= 0 = 30.
	ListRituals(ctx context.Context, org string, kind Kind, since time.Time, limit int) ([]Ritual, error)

	CreateProposals(ctx context.Context, org string, ps []Proposal) error
	GetProposal(ctx context.Context, org, id string) (Proposal, error)
	// ListProposals returns the proposals newest first; limit <= 0 = 100.
	ListProposals(ctx context.Context, org string, q ProposalQuery) ([]Proposal, error)
	// ResolveProposal moves a proposal from one status to another in one step (domain.ErrConflict when it is not in
	// "from", domain.ErrNotFound when missing). Moving back to pending clears the resolution.
	ResolveProposal(ctx context.Context, org, id, from, to, by string, applied bool, at time.Time) (Proposal, error)

	// GetSettings returns the stored settings; ok is false when none was ever saved.
	GetSettings(ctx context.Context, org string) (s Settings, ok bool, err error)
	PutSettings(ctx context.Context, org string, s Settings) error
}

// Slot is when a ritual runs: local time on the given weekdays (0 = Sunday ... 6 = Saturday).
type Slot struct {
	Enabled  bool   `json:"enabled"`
	Time     string `json:"time"`
	Weekdays []int  `json:"weekdays"`
}

// Settings are the ritual settings of an organization.
type Settings struct {
	// Timezone is the IANA zone the slots are read in.
	Timezone string `json:"timezone"`
	Standup  Slot   `json:"standup"`
	// Retro runs once a week: exactly one weekday.
	Retro Slot `json:"retro"`
	// UseModel lets the summarize path (a model, within the spend caps) write the team summary. Off: zero model calls.
	UseModel bool `json:"use_model"`
}

// DefaultSettings: both rituals OFF until a person turns them on (an upgrade must not start posting in the office
// chat every morning), stand-up at 09:30 Monday to Friday, retro on Friday at 16:00, no model.
func DefaultSettings() Settings {
	return Settings{Timezone: "UTC",
		Standup: Slot{Enabled: false, Time: "09:30", Weekdays: []int{1, 2, 3, 4, 5}},
		Retro:   Slot{Enabled: false, Time: "16:00", Weekdays: []int{5}}}
}

// Normalize fills empty fields with the defaults and validates the settings.
func (s Settings) Normalize() (Settings, error) {
	d := DefaultSettings()
	s.Timezone = strings.TrimSpace(s.Timezone)
	if s.Timezone == "" {
		s.Timezone = d.Timezone
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return Settings{}, fmt.Errorf("%w: unknown time zone %q", domain.ErrInvalid, s.Timezone)
	}
	var err error
	if s.Standup, err = normSlot(s.Standup, d.Standup, "standup"); err != nil {
		return Settings{}, err
	}
	if s.Retro, err = normSlot(s.Retro, d.Retro, "retro"); err != nil {
		return Settings{}, err
	}
	if len(s.Retro.Weekdays) != 1 {
		return Settings{}, fmt.Errorf("%w: the retro runs once a week (exactly one weekday)", domain.ErrInvalid)
	}
	return s, nil
}

func normSlot(s, def Slot, name string) (Slot, error) {
	s.Time = strings.TrimSpace(s.Time)
	if s.Time == "" {
		s.Time = def.Time
	}
	if _, _, err := parseClock(s.Time); err != nil {
		return Slot{}, fmt.Errorf("%w: %s.time %v", domain.ErrInvalid, name, err)
	}
	if len(s.Weekdays) == 0 {
		s.Weekdays = append([]int{}, def.Weekdays...)
	}
	seen := map[int]bool{}
	out := make([]int, 0, len(s.Weekdays))
	for _, d := range s.Weekdays {
		if d < 0 || d > 6 || seen[d] {
			return Slot{}, fmt.Errorf("%w: %s.weekdays must be unique numbers from 0 (Sunday) to 6 (Saturday)", domain.ErrInvalid, name)
		}
		seen[d] = true
		out = append(out, d)
	}
	for i := 1; i < len(out); i++ { // insertion sort: at most 7 values
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	s.Weekdays = out
	return s, nil
}

// parseClock reads "HH:MM" (00:00-23:59).
func parseClock(v string) (int, int, error) {
	h, m, ok := strings.Cut(v, ":")
	if !ok || len(h) != 2 || len(m) != 2 {
		return 0, 0, fmt.Errorf("must be HH:MM")
	}
	hh, e1 := strconv.Atoi(h)
	mm, e2 := strconv.Atoi(m)
	if e1 != nil || e2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, fmt.Errorf("must be HH:MM between 00:00 and 23:59")
	}
	return hh, mm, nil
}

// Error is a ritual that could not be held now. Code: blocked (kill switch / controls unavailable) | limit (too many
// manual runs today).
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string { return "ritual " + e.Code + ": " + e.Msg }

const (
	CodeBlocked = "blocked"
	CodeLimit   = "limit"
)
