package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	infmem "aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/memory"
	"aiworkforce/backend/internal/offices"
)

// memOfficesFix: a private office d-priv (member u-pri) with the agent a-priv; u-boss is an admin who is not a member.
func memOfficesFix(t *testing.T) (*offices.Service, *memory.MemStore, *memory.Service) {
	t.Helper()
	ctx := context.Background()
	svc := offices.New(offices.Config{Store: offices.NewMemStore(),
		Departments: func(context.Context, string) ([]offices.Dept, error) {
			return []offices.Dept{{ID: "d-priv", Name: "Legal"}}, nil
		},
		Placements: func(context.Context, string) (map[string]string, error) {
			return map[string]string{"u-pri": "d-priv"}, nil
		},
		AgentDepts: func(context.Context, string) (map[string]string, error) {
			return map[string]string{"a-priv": "d-priv"}, nil
		},
		Agents: func(context.Context, string) ([]offices.AgentRef, error) {
			return []offices.AgentRef{{ID: "a-priv"}, {ID: "a-gen"}}, nil
		},
		People: func(context.Context, string) ([]offices.Person, error) {
			return []offices.Person{{UserID: "u-boss", Name: "Boss", Admin: true}, {UserID: "u-pri", Name: "Pri"}}, nil
		}})
	if _, err := svc.Enable(ctx, "org1", offices.Viewer{UserID: "u-boss", Admin: true}, "d-priv", offices.Private); err != nil {
		t.Fatal(err)
	}
	docs := memory.NewMemStore()
	mem := memory.New(memory.Config{Docs: docs, Retriever: &memory.FTS{Docs: docs}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	officeRecall(mem, svc)
	return svc, docs, mem
}

func doneTask(id, agent string) domain.Task {
	fin := time.Now().UTC()
	return domain.Task{ID: id, RequestID: "rq-" + id, AgentID: agent, Status: domain.TaskDone, FinishedAt: &fin, Title: "Acuerdo de confidencialidad",
		Output: &domain.StructuredOutput{Summary: "Cláusula penal del acuerdo de confidencialidad"}}
}

func recallsRef(ctx context.Context, mem *memory.Service, agent, ref string) bool {
	for _, m := range mem.Recall(ctx, "org1", application.MemoryRecall{AgentID: agent, RequestID: "other", Text: "cláusula penal confidencialidad"}) {
		if m.Ref == ref {
			return true
		}
	}
	return false
}

// D3 (I6): officeRecall installs who is a member of each office, so a private office's agent recalls its office's
// memory for a member and for its own work, never for an admin who is not a member.
func TestOfficeRecallFollowsTheOfficeMembers(t *testing.T) {
	_, _, mem := memOfficesFix(t)
	ctx := context.Background()
	mem.RememberTask(ctx, "org1", doneTask("t1", "a-priv"), "acuerdo")
	if !recallsRef(application.WithActor(ctx, "u-pri"), mem, "a-priv", "task:t1") {
		t.Fatal("a member does not get the office's memory")
	}
	if !recallsRef(ctx, mem, "a-priv", "task:t1") {
		t.Fatal("the agent's own work does not get the office's memory")
	}
	if recallsRef(application.WithActor(ctx, "u-boss"), mem, "a-priv", "task:t1") {
		t.Fatal("an admin who is not a member gets the private office's memory")
	}
}

// D3 (I4): the backfill of an organization first moves the earlier organization memory of the private offices'
// agents to their office, beyond the caps of the re-index.
func TestBackfillOrgSealsEarlierMemory(t *testing.T) {
	_, docs, mem := memOfficesFix(t)
	ctx := context.Background()
	if err := docs.Upsert(ctx, "org1", memory.TaskDoc(doneTask("t-old", "a-priv"), "")); err != nil { // before D3
		t.Fatal(err)
	}
	backfillOrg(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), mem, infmem.New(), "org1")
	if d, _ := docs.GetByRef(ctx, "org1", "task:t-old"); d.Scope != memory.ScopeOffice || d.OfficeID != "d-priv" {
		t.Fatalf("earlier memory of the private office = %+v", d)
	}
	if recallsRef(ctx, mem, "a-gen", "task:t-old") {
		t.Fatal("a General agent still recalls it")
	}
}
