package application

import (
	"context"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Long-term memory (docs/architecture/memory.md). The orchestrator only knows
// this port: the implementation (internal/memory) owns the index, the
// retrievers and the permission rules.

// RecalledMemory is one relevant memory handed to the runtime as UNTRUSTED
// delimited data. Ref says where it came from (task:<id>, report:<id>,
// file:<path>, memory:<id>) so the agent can cite it or open it.
type RecalledMemory struct {
	Ref     string    `json:"ref"`
	Kind    string    `json:"kind"` // task | report | file | note
	Title   string    `json:"title"`
	Snippet string    `json:"snippet"`
	Project string    `json:"project,omitempty"`
	AgentID string    `json:"agent_id,omitempty"`
	At      time.Time `json:"at"`
}

// foreign reports whether the memory may hold text the agent did not write
// itself (drive files). Reading it taints the task like any other read.
func (r RecalledMemory) foreign() bool { return r.Kind == "file" }

// MemoryRecall is what the orchestrator asks the memory for.
type MemoryRecall struct {
	AgentID   string
	TaskID    string
	RequestID string // excluded from the results: the request's own tasks reach the agent as dependencies
	Text      string // task title + description + request text
}

// MemorySource is the long-term memory (installed by the server).
type MemorySource interface {
	// Recall returns at most a handful of memories within a character budget.
	Recall(ctx context.Context, org string, q MemoryRecall) []RecalledMemory
	// RememberTask indexes a finished task and stores the notes it declared.
	RememberTask(ctx context.Context, org string, t domain.Task, requestText string)
	// RememberReport indexes a report.
	RememberReport(ctx context.Context, org string, r domain.Report)
}

// SetMemorySource installs the long-term memory (nil removes it).
func (o *Orchestrator) SetMemorySource(s MemorySource) { o.memSrc = s }

const recallTextChars = 1500

// recallFor retrieves the memories relevant to a task (the retriever prefers
// the same project); nil when there is no memory or nothing relevant.
func (o *Orchestrator) recallFor(ctx context.Context, rs *run, t domain.Task) []RecalledMemory {
	if o.memSrc == nil {
		return nil
	}
	text := strings.TrimSpace(t.Title + "\n" + t.Description + "\n" + rs.req.Text)
	if r := []rune(text); len(r) > recallTextChars {
		text = string(r[:recallTextChars])
	}
	out := o.memSrc.Recall(ctx, o.org(ctx), MemoryRecall{AgentID: t.AgentID, TaskID: t.ID, RequestID: rs.req.ID, Text: text})
	for _, m := range out {
		if m.foreign() {
			taint := rs.taintOf(t.ID)
			rs.ext.mu.Lock()
			taint.tainted = true
			rs.ext.mu.Unlock()
			break
		}
	}
	return out
}
