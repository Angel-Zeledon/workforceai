package rituals_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/rituals"
)

// A ritual is stored once for the whole organization. What a General agent (public) did for a private office is that
// office's work: its titles and reasons are left out of the ritual when it is held; the same agent's work for an open
// office or for General stays, and so does what an agent of the private office itself did (its entry is redacted per
// person by the API).
func TestRitualsLeaveOutPrivateOfficeTasks(t *testing.T) {
	e := newEnv(t, func(c *rituals.Config) {
		c.Public = func(_ context.Context, id string) bool { return id != "sales" }
		c.PrivateOffice = func(_ context.Context, office string) bool { return office == "priv" }
	})
	ctx, done := context.Background(), e.now.Add(-time.Hour)
	mk := func(id, agent, office string, st domain.TaskStatus, review *domain.Review) {
		out := &domain.StructuredOutput{Summary: id, Review: review}
		if err := e.mem.CreateTask(ctx, e.org, domain.Task{ID: id, Title: "Titulo " + id, AgentID: agent, OfficeID: office, Status: st,
			CreatedAt: e.now.Add(-2 * time.Hour), FinishedAt: &done, Output: out}); err != nil {
			t.Fatal(err)
		}
	}
	rw := &domain.Review{Verdict: "rework", Reworks: 1, Reasons: []string{"SECRETO-razon"}}
	mk("SECRETO-hecha", "assistant", "priv", domain.TaskDone, nil)
	mk("SECRETO-fallida-1", "assistant", "priv", domain.TaskFailed, nil)
	mk("SECRETO-fallida-2", "assistant", "priv", domain.TaskFailed, rw)
	mk("SECRETO-fallida-3", "assistant", "priv", domain.TaskFailed, rw)
	mk("SECRETO-pendiente", "assistant", "priv", domain.TaskPending, nil)
	mk("ABIERTO-hecha", "assistant", "open", domain.TaskDone, nil)
	mk("GENERAL-hecha", "assistant", "", domain.TaskDone, nil)
	mk("PROPIA-hecha", "sales", "priv", domain.TaskDone, nil) // an agent of the private office: not public, left to the API's redaction
	e.approval("SECRETO-ap", "assistant", domain.ApprovalRejected, "SECRETO-nota", &done)
	e.approval("SECRETO-ap2", "assistant", domain.ApprovalRejected, "SECRETO-nota", &done)
	if err := e.mem.CreateTask(ctx, e.org, domain.Task{ID: "t-SECRETO-ap", Title: "x", AgentID: "assistant", OfficeID: "priv", Status: domain.TaskDone, CreatedAt: done}); err != nil {
		t.Fatal(err)
	}
	if err := e.mem.CreateTask(ctx, e.org, domain.Task{ID: "t-SECRETO-ap2", Title: "x", AgentID: "assistant", OfficeID: "priv", Status: domain.TaskDone, CreatedAt: done}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []rituals.Kind{rituals.KindStandup, rituals.KindRetro} {
		r, err := e.svc.RunNow(e.ctx(), kind)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(r)
		props, _ := e.svc.Proposals(e.ctx(), rituals.ProposalQuery{Limit: 200})
		praw, _ := json.Marshal(props)
		if s := string(raw) + string(praw); strings.Contains(s, "SECRETO") {
			t.Fatalf("%s holds the private office's work: %.800s", kind, s)
		}
		for _, want := range []string{"ABIERTO-hecha", "GENERAL-hecha", "PROPIA-hecha"} {
			if kind == rituals.KindStandup && !strings.Contains(string(raw), want) {
				t.Fatalf("the stand-up lost %s: %.800s", want, raw)
			}
		}
	}
}
