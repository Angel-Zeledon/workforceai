package memory_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/memory"
)

// E2: the notes of the agent's office, of the person it works for and the company's knowledge reach the task first,
// within the same budget as the indexed memories.
func TestExtraRecallWithinBudget(t *testing.T) {
	store := memory.NewMemStore()
	var asked []string
	svc := memory.New(memory.Config{Docs: store, Retriever: &memory.FTS{Docs: store},
		ExtraRecall: func(_ context.Context, org, agentID, requestID, text string) []application.RecalledMemory {
			asked = append(asked, org+"|"+agentID+"|"+requestID)
			return []application.RecalledMemory{
				{Ref: "team:n1", Kind: memory.KindNote, Title: "Memoria de Legal", Snippet: "Los contratos usan la plantilla de la socia"},
				{Ref: "team:n2", Kind: memory.KindNote, Title: "Big", Snippet: strings.Repeat("x", memory.MaxChars)},
			}
		}})
	ctx := context.Background()
	fin := time.Now()
	svc.RememberTask(ctx, "o", domain.Task{ID: "t1", RequestID: "r0", AgentID: "a", Status: domain.TaskDone, FinishedAt: &fin, Title: "Contrato de alquiler",
		Output: &domain.StructuredOutput{Summary: "Revisión del contrato de alquiler"}}, "contrato alquiler")
	got := svc.Recall(ctx, "o", application.MemoryRecall{AgentID: "a", RequestID: "r1", Text: "revisar contrato de alquiler"})
	if len(asked) != 1 || asked[0] != "o|a|r1" {
		t.Fatalf("asked = %v", asked)
	}
	if len(got) < 2 || got[0].Ref != "team:n1" || got[1].Ref != "task:t1" {
		t.Fatalf("recall = %+v", got)
	}
	for _, m := range got {
		if m.Ref == "team:n2" {
			t.Fatal("a note over the budget is left out")
		}
	}
}

// SetExtraRecall is safe while tasks recall (like SetOfficeScope); `go test -race` checks it where cgo exists.
func TestSetExtraRecallIsSafeWhileRecalling(t *testing.T) {
	store := memory.NewMemStore()
	svc := memory.New(memory.Config{Docs: store, Retriever: &memory.FTS{Docs: store}})
	note := func(context.Context, string, string, string, string) []application.RecalledMemory {
		return []application.RecalledMemory{{Ref: "team:n1", Kind: memory.KindNote, Title: "N", Snippet: "x"}}
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				svc.SetExtraRecall(note)
				svc.SetExtraRecall(nil)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				svc.Recall(context.Background(), "o", application.MemoryRecall{AgentID: "a", Text: "x"})
			}
		}()
	}
	wg.Wait()
}
