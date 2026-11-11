package memory_test

import (
	"context"
	"errors"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/memory"
)

// sealSvc is officeSvc plus who is a member of each office: ana is in Legal (private), beto is not; everybody is in
// Accounting (open).
func sealSvc(store memory.DocStore) *memory.Service {
	svc := officeSvc(store)
	svc.SetOfficeMembers(func(_ context.Context, _, office, user string) (bool, error) {
		return office == "acct" || office == "legal" && user == "ana", nil
	})
	return svc
}

// D3 (I4): a report that was remembered as organization memory before the offices existed, and that now mixes a
// private office with other authors (or whose office cannot be told), is taken out of every memory.
func TestMixedReportSuppressesTheEarlierOrgDocument(t *testing.T) {
	ctx := context.Background()
	for name, svcOf := range map[string]func(memory.DocStore) *memory.Service{
		"mixed with a private office": officeSvc,
		"office unknown": func(st memory.DocStore) *memory.Service {
			return memory.New(memory.Config{Docs: st, Retriever: &memory.FTS{Docs: st},
				AgentOffice: func(context.Context, string, string) (string, error) { return "", errors.New("model unavailable") }})
		},
	} {
		t.Run(name, func(t *testing.T) {
			store := memory.NewMemStore()
			r := report("r1", "acct1", "legal1")
			if err := store.Upsert(ctx, "o", memory.ReportDoc(r)); err != nil { // written before D3: organization memory
				t.Fatal(err)
			}
			svc := svcOf(store)
			svc.RememberReport(ctx, "o", r)
			if d, err := store.GetByRef(ctx, "o", "report:r1"); err == nil && !d.Suppressed {
				t.Fatalf("the earlier organization document is still remembered: %+v", d)
			}
			if items, _ := svc.Search(ctx, "o", "general1", "conciliación bancaria", 5); len(items) != 0 {
				t.Fatalf("a General agent still finds the mixed report: %+v", items)
			}
		})
	}
}

// D3 (I4): the earlier organization memory of the agents of a private office is moved to that office, with no cap
// (the backfill only re-reads the newest tasks). Open offices and General keep theirs.
func TestSealOfficeMemoryMovesPrivateOfficeAgentsOrgDocs(t *testing.T) {
	ctx := context.Background()
	store := memory.NewMemStore()
	for _, tk := range []domain.Task{
		finished("t1", "legal1", "Conciliación bancaria del trimestre"),
		finished("t2", "legal1", "Contrato de confidencialidad"),
		finished("t3", "acct1", "Conciliación bancaria del trimestre"),
		finished("t4", "general1", "Conciliación bancaria del trimestre"),
	} {
		if err := store.Upsert(ctx, "o", memory.TaskDoc(tk, "")); err != nil {
			t.Fatal(err)
		}
	}
	svc := sealSvc(store)
	n, err := svc.SealOfficeMemory(ctx, "o")
	if err != nil || n != 2 {
		t.Fatalf("sealed = %d, %v; want 2", n, err)
	}
	for ref, want := range map[string]string{"task:t1": "legal", "task:t2": "legal", "task:t3": "", "task:t4": ""} {
		d, err := store.GetByRef(ctx, "o", ref)
		if err != nil || d.OfficeID != want || (want == "") != (d.Scope == memory.ScopeOrg) {
			t.Fatalf("%s: %+v %v", ref, d, err)
		}
	}
	if recalledRef(svc, "general1", "task:t1") {
		t.Fatal("a General agent still recalls Legal's earlier work")
	}
	if !recalledRef(svc, "legal1", "task:t1") {
		t.Fatal("Legal's agent lost its earlier work")
	}
	// Idempotent.
	if n, _ := svc.SealOfficeMemory(ctx, "o"); n != 0 {
		t.Fatalf("second run sealed %d", n)
	}
}

// An unreadable model seals nothing (and reports the error): nothing is moved on a guess.
func TestSealOfficeMemoryWithoutModel(t *testing.T) {
	ctx := context.Background()
	store := memory.NewMemStore()
	_ = store.Upsert(ctx, "o", memory.TaskDoc(finished("t1", "legal1", "Conciliación bancaria del trimestre"), ""))
	svc := memory.New(memory.Config{Docs: store, Retriever: &memory.FTS{Docs: store},
		AgentOffice: func(context.Context, string, string) (string, error) { return "", errors.New("model unavailable") }})
	if n, _ := svc.SealOfficeMemory(ctx, "o"); n != 0 {
		t.Fatalf("sealed %d without a model", n)
	}
	if d, _ := store.GetByRef(ctx, "o", "task:t1"); d.Scope != memory.ScopeOrg {
		t.Fatalf("moved without a model: %+v", d)
	}
}

// D3 (I4): the backfill re-reads only the newest reports, but an older one that mixes a private office is still
// taken out of the organization memory, and an older one of a private office moves to it.
func TestBackfillSealsOlderReportsBeyondTheCap(t *testing.T) {
	ctx := context.Background()
	store := memory.NewMemStore()
	reports := []domain.Report{report("r0", "general1"), report("r1", "acct1", "legal1"), report("r2", "legal1")}
	for _, r := range reports {
		_ = store.Upsert(ctx, "o", memory.ReportDoc(r)) // before D3: all organization memory
	}
	svc := officeSvc(store)
	svc.Backfill(ctx, "o", reportsSrc(reports), 0, 1)
	if d, err := store.GetByRef(ctx, "o", "report:r1"); err == nil && !d.Suppressed {
		t.Fatalf("an older mixed report is still organization memory: %+v", d)
	}
	if d, _ := store.GetByRef(ctx, "o", "report:r2"); d.Scope != memory.ScopeOffice || d.OfficeID != "legal" {
		t.Fatalf("an older report of Legal stayed: %+v", d)
	}
}

type reportsSrc []domain.Report

func (reportsSrc) ListTasks(context.Context, string, string, string) ([]domain.Task, error) {
	return nil, nil
}
func (r reportsSrc) ListReports(context.Context, string) ([]domain.Report, error) { return r, nil }
func (reportsSrc) GetRequest(context.Context, string, string) (domain.Request, error) {
	return domain.Request{}, nil
}

func recalledRef(svc *memory.Service, agent, ref string) bool {
	return recalledRefCtx(context.Background(), svc, agent, ref)
}

func recalledRefCtx(ctx context.Context, svc *memory.Service, agent, ref string) bool {
	got := svc.Recall(ctx, "o", application.MemoryRecall{AgentID: agent, RequestID: "other", Text: "conciliación bancaria del trimestre"})
	for _, m := range got {
		if m.Ref == ref {
			return true
		}
	}
	return false
}

// D3 (I6): an agent of a private office working for a person who is not a member of that office (a shared or
// directory agent) does not bring the office's memory; for a member, and for the agent's own work (no person), it
// does. An open office's memory is everybody's.
func TestOfficeMemoryFollowsThePersonTheAgentWorksFor(t *testing.T) {
	ctx := context.Background()
	store := memory.NewMemStore()
	svc := sealSvc(store)
	svc.RememberTask(ctx, "o", finished("t1", "legal1", "Conciliación bancaria del trimestre"), "conciliación bancaria")
	svc.RememberTask(ctx, "o", finished("t2", "acct1", "Conciliación bancaria del trimestre"), "conciliación bancaria")
	member, outsider := application.WithActor(ctx, "ana"), application.WithActor(ctx, "beto")

	if !recalledRefCtx(member, svc, "legal1", "task:t1") {
		t.Fatal("a member does not get the office's memory")
	}
	if !recalledRefCtx(ctx, svc, "legal1", "task:t1") {
		t.Fatal("the agent's own work (no person) does not get the office's memory")
	}
	if recalledRefCtx(outsider, svc, "legal1", "task:t1") {
		t.Fatal("a non-member gets the private office's memory through its agent")
	}
	if !recalledRefCtx(outsider, svc, "acct1", "task:t2") {
		t.Fatal("an open office's memory must stay everybody's")
	}

	search := func(c context.Context) int {
		items, err := svc.Search(c, "o", "legal1", "conciliación bancaria", 5)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, it := range items {
			if it.Ref == "task:t1" {
				n++
			}
		}
		return n
	}
	if search(member) != 1 || search(ctx) != 1 {
		t.Fatal("search: a member or the agent itself does not find the office's memory")
	}
	if search(outsider) != 0 {
		t.Fatal("search: a non-member finds the private office's memory")
	}
	if _, err := svc.Open(outsider, "o", "legal1", "task:t1"); err == nil {
		t.Fatal("open: a non-member opens the private office's memory")
	}
	for _, c := range []context.Context{member, ctx} {
		if _, err := svc.Open(c, "o", "legal1", "task:t1"); err != nil {
			t.Fatalf("open: %v", err)
		}
	}
}

// Without the membership rule installed, a person's work never reaches a private office's memory (fails closed).
func TestOfficeMemoryWithoutMembershipRuleFailsClosed(t *testing.T) {
	ctx := context.Background()
	store := memory.NewMemStore()
	svc := officeSvc(store)
	svc.RememberTask(ctx, "o", finished("t1", "legal1", "Conciliación bancaria del trimestre"), "conciliación bancaria")
	if recalledRefCtx(application.WithActor(ctx, "ana"), svc, "legal1", "task:t1") {
		t.Fatal("recalled for a person without a membership rule")
	}
	if !recalledRefCtx(ctx, svc, "legal1", "task:t1") {
		t.Fatal("the agent's own work lost its office's memory")
	}
}
