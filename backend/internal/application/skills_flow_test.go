package application_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/objstore"
	"aiworkforce/backend/internal/skills"
)

// The agent sees only name + description on its first turn, asks for the skill
// with skill.load through the gateway (the read round), and gets the approved
// text in the TRUSTED channel (loaded_skills), never as untrusted external
// content, and with its tools untouched.
func TestSkillLoadRoundIsTrustedAndGrantsNothing(t *testing.T) {
	var turns []application.RunTaskRequest
	rt := &fakeRuntime{plan: onePlan("sales")}
	rt.runTask = func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		turns = append(turns, in)
		res := okResult("hecho")
		if len(in.LoadedSkills) == 0 {
			res.ToolRequests = []application.ToolRequest{{Tool: "skill", Action: "load", Args: map[string]any{"name": "pitch-notes"}, Risk: "low"}}
		}
		return res, nil
	}
	w := wire(t, &spyRuntime{fakeRuntime: rt})

	drv := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: org, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	svc := skills.New(skills.Config{Store: skills.NewMemStore(), Drive: drv, OrgID: org, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		AgentRole: func(ctx context.Context, o, id string) (string, error) {
			a, err := w.store.GetAgent(ctx, o, id)
			return a.Role, err
		}})
	ctx := application.WithOrg(context.Background(), org)
	adm := skills.Actor{ID: "boss", Admin: true}
	d, err := svc.Create(ctx, adm, skills.Input{Name: "pitch-notes", Description: "How to prepare pitch notes",
		Instructions: "# Pitch\n\nOpen with the client's problem.\n", AllowedTools: []string{"email.send"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Review(ctx, adm, d.ID, true, 1, ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.Assign(ctx, adm, "sales", d.ID); err != nil {
		t.Fatal(err)
	}
	w.orch.SetSkillSource(svc)
	w.gw.AddNative(&skills.GatewayTool{Svc: svc})

	reqID, err := w.orch.Submit(context.Background(), "prepara notas para la reunión")
	if err != nil {
		t.Fatal(err)
	}
	w.waitFor("task done", func() bool { return w.tasks(reqID)["Responder correos"].Status == domain.TaskDone })

	if len(turns) != 2 {
		t.Fatalf("expected the first turn and one read round, got %d turns", len(turns))
	}
	first, second := turns[0], turns[1]
	if len(first.Skills) != 1 || first.Skills[0].Name != "pitch-notes" || first.Skills[0].Description != "How to prepare pitch notes" || len(first.LoadedSkills) != 0 {
		t.Fatalf("first turn must carry name + description only: %+v", first)
	}
	if strings.Contains(first.Context.RequestText+first.Task.Description, "Open with the client's problem") {
		t.Fatal("the body must not be in the first turn")
	}
	if len(second.LoadedSkills) != 1 || second.LoadedSkills[0].Name != "pitch-notes" || !strings.Contains(second.LoadedSkills[0].Text, "Open with the client's problem") {
		t.Fatalf("second turn must carry the skill text: %+v", second.LoadedSkills)
	}
	for _, ext := range second.ExternalContent {
		if strings.Contains(ext, "Open with the client's problem") {
			t.Fatal("approved skill text must not travel as untrusted external content")
		}
	}
	if strings.Join(second.Agent.Tools, ",") != strings.Join(first.Agent.Tools, ",") {
		t.Fatalf("a skill must not change the agent's tools: %v -> %v", first.Agent.Tools, second.Agent.Tools)
	}
}
