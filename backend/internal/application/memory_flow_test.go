package application_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/memory"
)

// A finished task is remembered (with the notes its output declared); the next
// request gets the relevant memory as data in its task, never the memory of its
// own request, and the agent can search for more with memory.search in the read round.
func TestLongTermMemoryIsRememberedAndRecalled(t *testing.T) {
	var mu sync.Mutex
	var turns []application.RunTaskRequest
	rt := &fakeRuntime{plan: onePlan("sales")}
	rt.runTask = func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		mu.Lock()
		turns = append(turns, in)
		mu.Unlock()
		res := okResult("Proyección de ventas por $50,000 aprobada")
		res.Output.MemoryNotes = []string{"El cliente Acme prefiere cotizaciones en pesos"}
		if strings.Contains(in.Context.RequestText, "segunda") && len(in.ExternalContent) == 0 {
			res.ToolRequests = []application.ToolRequest{{Tool: "memory", Action: "search", Args: map[string]any{"query": "proyección ventas"}, Risk: "low"}}
		}
		return res, nil
	}
	w := wire(t, &spyRuntime{fakeRuntime: rt})
	store := memory.NewMemStore()
	svc := memory.New(memory.Config{Docs: store, Retriever: &memory.FTS{Docs: store}})
	w.orch.SetMemorySource(svc)
	w.gw.AddNative(&memory.GatewayTool{Svc: svc})

	first, err := w.orch.Submit(context.Background(), "primera: proyección de ventas por $50,000")
	if err != nil {
		t.Fatal(err)
	}
	w.waitFor("first done", func() bool { return w.tasks(first)["Responder correos"].Status == domain.TaskDone })
	if len(turns[0].Recalled) != 0 {
		t.Fatalf("nothing to recall yet: %+v", turns[0].Recalled)
	}
	ctx := application.WithOrg(context.Background(), org)
	w.waitFor("memory written", func() bool { l, _ := svc.List(ctx, org, "sales", 20); return len(l) == 2 })
	l, _ := svc.List(ctx, org, "sales", 20)
	kinds := map[string]int{}
	for _, it := range l {
		kinds[it.Kind]++
	}
	if kinds[memory.KindTask] != 1 || kinds[memory.KindNote] != 1 {
		t.Fatalf("the task and its note must be remembered: %+v", l)
	}

	second, err := w.orch.Submit(context.Background(), "segunda: otra proyección de ventas parecida")
	if err != nil {
		t.Fatal(err)
	}
	w.waitFor("second done", func() bool { return w.tasks(second)["Responder correos"].Status == domain.TaskDone })
	mu.Lock()
	defer mu.Unlock()
	var in, follow application.RunTaskRequest
	for _, tr := range turns[1:] {
		if len(tr.ExternalContent) == 0 {
			in = tr
		} else {
			follow = tr
		}
	}
	if len(in.Recalled) == 0 || in.Recalled[0].Ref == "" || !strings.HasPrefix(in.Recalled[0].Ref, "task:") && !strings.HasPrefix(in.Recalled[0].Ref, "memory:") {
		t.Fatalf("the second task must carry the memory of the first with its ref: %+v", in.Recalled)
	}
	for _, m := range in.Recalled {
		if m.Snippet == "" || len(in.Recalled) > memory.MaxItems {
			t.Fatalf("bad recalled item %+v", m)
		}
	}
	if len(follow.ExternalContent) == 0 || !strings.Contains(strings.Join(follow.ExternalContent, "\n"), "<untrusted_data") || !strings.Contains(strings.Join(follow.ExternalContent, "\n"), "task:") {
		t.Fatalf("memory.search must come back as delimited data with refs: %+v", follow.ExternalContent)
	}
}
