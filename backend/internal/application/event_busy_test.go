package application_test

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
)

// agent.state_changed of an agent that works for a private office: the members receive the real state (what it does,
// its task), the rest of the organization receives "busy" (the state, no activity, no task) first, so nobody outside the
// office learns that the agent works for it, nor the title of its task; the event log keeps the busy one only.
func TestAgentStateOfPrivateWorkIsBusyForTheRest(t *testing.T) {
	ctx := application.WithOrg(context.Background(), "org1")
	st, pub := memory.New(), &capturePub{}
	rec := &application.Recorder{OrgID: "org1", Store: st, Pub: pub, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	rec.SetAudienceFor(func(_ context.Context, _ string, ev domain.Event) (application.Audience, bool) {
		p, _ := ev.Payload.(map[string]any)
		if id, _ := p["task_id"].(*string); id != nil && *id == "task-priv" {
			return application.Audience{Users: []string{"u-pri"}}, true
		}
		return application.Audience{}, false
	})
	task := "task-priv"
	rec.Emit(ctx, application.Action{Type: domain.EvAgentState, AgentID: "assistant", SkipAudit: true,
		Payload: map[string]any{"agent_id": "assistant", "state": domain.StateWorking, "activity": "SECRETO-redacta el informe", "task_id": &task, "progress": 40}})
	rec.Emit(ctx, application.Action{Type: domain.EvAgentState, AgentID: "sales", SkipAudit: true,
		Payload: map[string]any{"agent_id": "sales", "state": domain.StateWorking, "activity": "ABIERTO-llama al cliente", "progress": 10}})

	if len(pub.evs) != 3 {
		t.Fatalf("published %d events, want the busy one, the real one and General's: %+v", len(pub.evs), pub.evs)
	}
	busy, real, general := pub.evs[0], pub.evs[1], pub.evs[2]
	if busy.Audience != nil || !slices.Equal(real.Audience, []string{"u-pri"}) || general.Audience != nil {
		t.Fatalf("audiences: busy %v, real %v, general %v", busy.Audience, real.Audience, general.Audience)
	}
	p, _ := busy.Payload.(map[string]any)
	if p["agent_id"] != "assistant" || p["state"] != domain.StateWorking || p["activity"] != "" || p["task_id"] != nil || p["progress"] != 0 {
		t.Fatalf("busy payload = %v", p)
	}
	if rp, _ := real.Payload.(map[string]any); !strings.Contains(rp["activity"].(string), "SECRETO") {
		t.Fatalf("the members must get the real state: %v", rp)
	}
	log, _ := st.ListEvents(ctx, "org1", domain.EventQuery{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour), Types: []string{domain.EvAgentState}, Limit: 10})
	for _, ev := range log {
		if b, _ := ev.Payload.(map[string]any); strings.Contains(b["activity"].(string), "SECRETO") {
			t.Fatalf("the event log keeps the private state: %v", b)
		}
	}
	if len(log) != 2 {
		t.Fatalf("the log holds the busy state and General's: %d", len(log))
	}
}
