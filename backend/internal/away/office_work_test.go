package away_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/away"
	"aiworkforce/backend/internal/domain"
)

// D3: what a General agent did for a request asked from a private office is that office's work: the person who is not
// in it reads nothing of it in the summary (its task events, its approvals, the agent's line), though the agent itself
// is one they see.
func TestSummaryLeavesOutWorkAskedFromAPrivateOffice(t *testing.T) {
	left, back := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC), time.Date(2026, 3, 1, 19, 0, 0, 0, time.UTC)
	at := left.Add(time.Hour)
	done := func(id, title, office string) domain.Event {
		task := map[string]any{"id": id, "title": title, "agent_id": "ana", "request_id": "r-" + id, "office_id": office}
		return domain.Event{Type: domain.EvTaskCompleted, AgentID: "ana", TS: at, Payload: map[string]any{"task": task, "agent_id": "ana"}}
	}
	events := []domain.Event{done("t1", "SECRETO-tarea", "priv"), done("t2", "tarea abierta", "")}
	facts := away.Facts{Notable: events, Completed: events, Counts: map[string]int{domain.EvTaskCompleted: 2},
		Pending: []domain.Approval{{ID: "ap-priv", AgentID: "ana", TaskID: "t1", Title: "SECRETO-aprobacion", Status: domain.ApprovalPending, CreatedAt: at},
			{ID: "ap-open", AgentID: "ana", TaskID: "t2", Title: "aprobacion abierta", Status: domain.ApprovalPending, CreatedAt: at}}}
	v := away.Viewer{UserID: "beto", Role: "admin", CanDecide: true, ReadApprovals: true,
		SeesAgent: func(string) bool { return true }, SeesRequest: func(id string) bool { return id != "r-t1" },
		SeesOffice:   func(id string) bool { return id != "priv" },
		SeesApproval: func(ap domain.Approval) bool { return ap.TaskID != "t1" }}
	s := away.Build(facts, v, left, back, back, 8)
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), "SECRETO") || strings.Contains(string(b), "ap-priv") {
		t.Fatalf("the summary shows the private office's work: %s", b)
	}
	if len(s.ActNow) != 1 || s.ActNow[0].RefID != "ap-open" {
		t.Fatalf("act now = %+v", s.ActNow)
	}
	if len(s.Agents) != 1 || s.Agents[0].TasksCompleted != 1 {
		t.Fatalf("the agent's line counts the work the person sees: %+v", s.Agents)
	}
}
