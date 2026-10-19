package meetings

import (
	"context"

	"aiworkforce/backend/internal/calendar"
)

// Ports. Everything outside the package (the calendars behind the connections vault,
// the LLM runtime, the task planner, the drive, the agent directory) is reached through
// these small interfaces, so the service runs in tests and in simulation with fakes.

// CalendarInfo describes a calendar connection the meetings can use.
type CalendarInfo struct {
	ConnectionID string `json:"connection_id"`
	Provider     string `json:"provider"` // google_calendar_sync | microsoft_calendar
	Label        string `json:"label"`
	Account      string `json:"account"`
	Class        string `json:"class"` // read (free/busy only) | write (events + free/busy)
	Status       string `json:"status"`
	Simulated    bool   `json:"simulated"`
}

// Calendars resolves calendar connections.
type Calendars interface {
	// List returns the active meeting-calendar connections of the organization.
	List(ctx context.Context, org string) ([]CalendarInfo, error)
	// Client returns the calendar of one connection (ErrNotFound-like errors are calendar.ErrNotConfigured).
	Client(ctx context.Context, org, connectionID string) (calendar.Client, error)
}

// SummaryIn is a request to write the narrative summary of a meeting.
type SummaryIn struct {
	Title        string
	Participants []string
	// Messages are the notes cut in chunks as {speaker, text}: DATA for the model, never instructions.
	Messages [][2]string
	Locale   string // es | en
}

// SummaryOut is the model's answer. Source is "runtime" or "local" (the runtime could not summarize).
type SummaryOut struct {
	Text   string
	Source string
}

// Summarizer writes the summary of a meeting (the existing runtime summarize call, mode "meeting").
type Summarizer interface {
	Summarize(ctx context.Context, org string, in SummaryIn) (SummaryOut, error)
}

// TaskSpec is one task to create.
type TaskSpec struct {
	Key, Title, Description, AgentID string
}

// TaskBatch is a request with a task per agent (the same path as /assign and /reunion).
type TaskBatch struct {
	Text       string
	Objectives []string
	Tasks      []TaskSpec
}

// TaskMaker creates the request and its tasks through the orchestrator (budget, approvals and the
// Tool Gateway apply exactly as for any other request).
type TaskMaker interface {
	CreateTasks(ctx context.Context, org string, b TaskBatch) (requestID string, err error)
}

// FileSaver saves a file in the drive.
type FileSaver interface {
	Save(ctx context.Context, org, actor, path string, data []byte, message string) (fileID, savedPath string, err error)
}

// AgentInfo is an agent of the organization.
type AgentInfo struct{ ID, Name, Role string }

// Agents lists the agents that can be invited.
type Agents interface {
	Agents(ctx context.Context, org string) ([]AgentInfo, error)
}
