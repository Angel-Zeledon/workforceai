package memory_test

import (
	"context"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/memory"
)

func officeSvc(store memory.DocStore) *memory.Service {
	offices := map[string]string{"acct1": "acct", "acct2": "acct", "legal1": "legal"} // others: General ("")
	return memory.New(memory.Config{Docs: store, Retriever: &memory.FTS{Docs: store},
		AgentOffice: func(_ context.Context, _, agentID string) string { return offices[agentID] }})
}

func finished(id, agent, summary string) domain.Task {
	fin := time.Now()
	return domain.Task{ID: id, RequestID: "r-" + id, AgentID: agent, Status: domain.TaskDone, FinishedAt: &fin, Title: "Cierre trimestral",
		Output: &domain.StructuredOutput{Summary: summary}}
}

func recalled(svc *memory.Service, agent string) bool {
	got := svc.Recall(context.Background(), "o", application.MemoryRecall{AgentID: agent, RequestID: "other", Text: "conciliación bancaria del trimestre"})
	for _, m := range got {
		if m.Ref == "task:t1" {
			return true
		}
	}
	return false
}

func TestTaskMemoryStaysInOffice(t *testing.T) {
	store := memory.NewMemStore()
	svc := officeSvc(store)
	svc.RememberTask(context.Background(), "o", finished("t1", "acct1", "Conciliación bancaria del trimestre"), "conciliación bancaria")
	d, err := store.GetByRef(context.Background(), "o", "task:t1")
	if err != nil || d.Scope != memory.ScopeOffice || d.OfficeID != "acct" {
		t.Fatalf("doc: %+v %v", d, err)
	}
	if !recalled(svc, "acct2") {
		t.Fatal("another agent of the same office must recall it")
	}
	if recalled(svc, "legal1") || recalled(svc, "general1") {
		t.Fatal("agents of Legal or General must not recall it")
	}
	if items, _ := svc.Search(context.Background(), "o", "legal1", "conciliación bancaria", 5); len(items) != 0 {
		t.Fatalf("search leaks: %+v", items)
	}
	if _, err := svc.Open(context.Background(), "o", "legal1", "task:t1"); err == nil {
		t.Fatal("open leaks")
	}
	if _, err := svc.Open(context.Background(), "o", "acct2", "task:t1"); err != nil {
		t.Fatalf("open own office: %v", err)
	}
}

func TestGeneralAgentsKeepOrgMemory(t *testing.T) {
	store := memory.NewMemStore()
	svc := officeSvc(store)
	svc.RememberTask(context.Background(), "o", finished("t1", "general1", "Conciliación bancaria del trimestre"), "conciliación bancaria")
	d, _ := store.GetByRef(context.Background(), "o", "task:t1")
	if d.Scope != memory.ScopeOrg || d.OfficeID != "" {
		t.Fatalf("doc: %+v", d)
	}
	for _, a := range []string{"acct1", "legal1", "general2"} {
		if !recalled(svc, a) {
			t.Fatalf("%s must recall org memory", a)
		}
	}
}

func TestOldOrgDocsStillVisible(t *testing.T) {
	store := memory.NewMemStore()
	// written before offices had a scope: org scope from an agent that now belongs to an office
	old := memory.TaskDoc(finished("t1", "acct1", "Conciliación bancaria del trimestre"), "conciliación bancaria")
	if err := store.Upsert(context.Background(), "o", old); err != nil {
		t.Fatal(err)
	}
	svc := officeSvc(store)
	for _, a := range []string{"acct2", "legal1", "general1"} {
		if !recalled(svc, a) {
			t.Fatalf("%s must still see old org docs", a)
		}
	}
}

func TestNoOfficeServiceKeepsOrgMemory(t *testing.T) {
	store := memory.NewMemStore()
	svc := memory.New(memory.Config{Docs: store, Retriever: &memory.FTS{Docs: store}})
	svc.RememberTask(context.Background(), "o", finished("t1", "acct1", "Conciliación bancaria del trimestre"), "conciliación bancaria")
	if d, _ := store.GetByRef(context.Background(), "o", "task:t1"); d.Scope != memory.ScopeOrg {
		t.Fatalf("scope: %+v", d)
	}
}
