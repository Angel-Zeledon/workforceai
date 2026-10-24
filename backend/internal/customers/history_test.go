package customers_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/customers"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/memory"
)

// Earlier work (before B12) has no customer and a customer task never recalls it. It is never retagged silently:
// a person assigns it explicitly (audited, reversible) and only then does that customer's work recall it.

type fakeHistory struct {
	mu       sync.Mutex
	finished map[string]bool // request id -> finished
	projects map[string]*customers.HistoryProjectInfo
	mem      *memory.Service
}

func (h *fakeHistory) Project(_ context.Context, _, id string) (customers.HistoryProjectInfo, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.projects[id]
	if !ok {
		return customers.HistoryProjectInfo{}, domain.ErrNotFound
	}
	return *p, nil
}

func (h *fakeHistory) SetProjectCustomer(_ context.Context, _, id, c string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.projects[id].CustomerID = c
	return nil
}

func (h *fakeHistory) RequestFinished(_ context.Context, _, id string) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f, ok := h.finished[id]
	if !ok {
		return false, domain.ErrNotFound
	}
	return f, nil
}

func (h *fakeHistory) Memory(ctx context.Context, org, id string) (customers.HistoryMemoryInfo, error) {
	d, err := h.mem.Doc(ctx, org, id)
	return customers.HistoryMemoryInfo{CustomerID: d.CustomerID, RequestID: d.RequestID}, err
}

func (h *fakeHistory) RetagMemories(ctx context.Context, org, req, doc, from, to string) (int, error) {
	return h.mem.RetagCustomer(ctx, org, memory.RetagSelector{RequestID: req, DocID: doc}, from, to)
}

func (h *fakeHistory) Candidates(context.Context, string, int) ([]customers.ProjectInfo, []customers.RequestInfo, error) {
	return []customers.ProjectInfo{{ID: "prj_old", Name: "Cierre 2025"}}, []customers.RequestInfo{{ID: "rq-old"}, {ID: "rq-a1"}}, nil
}

type auditLog struct {
	mu      sync.Mutex
	entries []string
}

func (a *auditLog) add(_ context.Context, _, _, action, _ string, d map[string]any) {
	if !strings.HasPrefix(action, "customer.history.") {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, action+":"+d["kind"].(string)+":"+d["from"].(string)+">"+d["to"].(string))
}

func recalled(w *world, mem *memory.Service, req string) map[string]bool {
	out := map[string]bool{}
	for _, m := range mem.Recall(w.ctx, org, application.MemoryRecall{AgentID: agent, RequestID: req, Text: "saldo de bancos conciliacion historica"}) {
		out[m.Ref] = true
	}
	return out
}

func TestEarlierWorkIsAssignedExplicitlyAndReversibly(t *testing.T) {
	w := newWorld(t)
	audit := &auditLog{}
	cust := customers.New(customers.Config{Store: customers.NewMemStore(), OrgID: org, Audit: audit.add})
	a, _ := cust.Create(w.ctx, "ana", customers.Input{Name: ptr("Ferretería López")})
	b, _ := cust.Create(w.ctx, "ana", customers.Input{Name: ptr("Panadería San José")})
	for req, c := range map[string]string{"rq-a1": a.ID, "rq-b1": b.ID, "rq-a-proj": a.ID} {
		if err := cust.LinkRequest(w.ctx, org, req, c); err != nil {
			t.Fatal(err)
		}
	}
	docs := memory.NewMemStore()
	mem := memory.New(memory.Config{Docs: docs, CustomerOf: cust.CustomerOfRequest, Retriever: &memory.FTS{Docs: docs}})
	fin := time.Now().UTC()
	task := func(id, req, summary string) domain.Task {
		return domain.Task{ID: id, RequestID: req, AgentID: agent, Title: "Conciliación de saldo de bancos", Status: domain.TaskDone, FinishedAt: &fin,
			Output: &domain.StructuredOutput{Summary: summary}}
	}
	// Work done before the district: untagged.
	mem.RememberTask(w.ctx, org, task("t-old", "rq-old", "saldo de bancos historica ferreteria 2025 cuadra"), "conciliacion historica")
	mem.RememberTask(w.ctx, org, task("t-proj", "rq-proj", "saldo de bancos historica cierre del proyecto 2025"), "cierre historica")
	mem.RememberTask(w.ctx, org, task("t-run", "rq-running", "saldo de bancos historica en curso"), "en curso")
	h := &fakeHistory{finished: map[string]bool{"rq-old": true, "rq-proj": true, "rq-running": false, "rq-a1": true},
		projects: map[string]*customers.HistoryProjectInfo{"prj_old": {RequestID: "rq-proj", Movable: true}, "prj_live": {Movable: false}}, mem: mem}

	if _, err := cust.AssignHistory(w.ctx, "admin", customers.HistoryInput{Kind: customers.HistoryRequest, RefID: "rq-old", CustomerID: a.ID}); err == nil ||
		!strings.Contains(err.Error(), "not available") {
		t.Fatalf("without the history wired nothing moves: %v", err)
	}
	cust.SetHistory(h)

	if recalled(w, mem, "rq-a1")["task:t-old"] {
		t.Fatal("before the assignment, a task of A must not recall unscoped earlier work")
	}
	res, err := cust.AssignHistory(w.ctx, "admin", customers.HistoryInput{Kind: customers.HistoryRequest, RefID: "rq-old", CustomerID: a.ID})
	if err != nil || !res.Changed || res.From != "" || res.To != a.ID || res.Memories != 1 {
		t.Fatalf("assign request: %+v %v", res, err)
	}
	if got := recalled(w, mem, "rq-a1"); !got["task:t-old"] {
		t.Fatalf("after the assignment A recalls it: %+v", got)
	}
	if recalled(w, mem, "rq-b1")["task:t-old"] {
		t.Fatal("B must never recall A's assigned work")
	}
	if cid, _ := cust.CustomerOfRequest(w.ctx, org, "rq-old"); cid != a.ID {
		t.Fatalf("the request is linked to A: %q", cid)
	}
	// Moving it to B directly is refused: remove A first (no silent re-scoping between customers).
	if _, err := cust.AssignHistory(w.ctx, "admin", customers.HistoryInput{Kind: customers.HistoryRequest, RefID: "rq-old", CustomerID: b.ID}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("A -> B must be refused: %v", err)
	}
	// Undo.
	res, err = cust.AssignHistory(w.ctx, "admin", customers.HistoryInput{Kind: customers.HistoryRequest, RefID: "rq-old"})
	if err != nil || !res.Changed || res.From != a.ID || res.To != "" || res.Memories != 1 {
		t.Fatalf("undo: %+v %v", res, err)
	}
	if recalled(w, mem, "rq-a1")["task:t-old"] {
		t.Fatal("after the undo A no longer recalls it")
	}
	// Running work never moves; a born-scoped request keeps its customer unless removed explicitly.
	if _, err := cust.AssignHistory(w.ctx, "admin", customers.HistoryInput{Kind: customers.HistoryRequest, RefID: "rq-running", CustomerID: a.ID}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a running request must not move: %v", err)
	}
	if _, err := cust.AssignHistory(w.ctx, "admin", customers.HistoryInput{Kind: customers.HistoryProject, RefID: "prj_live", CustomerID: a.ID}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a running project must not move: %v", err)
	}

	// A finished project moves with its request and the request's memories.
	res, err = cust.AssignHistory(w.ctx, "admin", customers.HistoryInput{Kind: customers.HistoryProject, RefID: "prj_old", CustomerID: a.ID})
	if err != nil || !res.Changed || res.RequestID != "rq-proj" || res.Memories != 1 || h.projects["prj_old"].CustomerID != a.ID {
		t.Fatalf("assign project: %+v %v", res, err)
	}
	if !recalled(w, mem, "rq-a1")["task:t-proj"] {
		t.Fatal("the project's work is recalled by A after the assignment")
	}

	// One memory document: allowed only when its request has no customer.
	doc, err := mem.Doc(w.ctx, org, docIDOf(t, mem, w, "task:t-run"))
	if err != nil {
		t.Fatal(err)
	}
	if res, err := cust.AssignHistory(w.ctx, "admin", customers.HistoryInput{Kind: customers.HistoryMemory, RefID: doc.ID, CustomerID: a.ID}); err != nil || res.Memories != 1 {
		t.Fatalf("assign memory: %+v %v", res, err)
	}
	projDoc := docIDOf(t, mem, w, "task:t-proj")
	if _, err := cust.AssignHistory(w.ctx, "admin", customers.HistoryInput{Kind: customers.HistoryMemory, RefID: projDoc}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a memory of a scoped request follows its request: %v", err)
	}

	// Bad input.
	if _, err := cust.AssignHistory(w.ctx, "admin", customers.HistoryInput{Kind: "artifact", RefID: "x", CustomerID: a.ID}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown kind: %v", err)
	}
	if _, err := cust.AssignHistory(w.ctx, "admin", customers.HistoryInput{Kind: customers.HistoryRequest, RefID: "rq-old", CustomerID: "cus_nope"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown customer: %v", err)
	}

	want := []string{"customer.history.assigned:request:>" + a.ID, "customer.history.unassigned:request:" + a.ID + ">",
		"customer.history.assigned:project:>" + a.ID, "customer.history.assigned:memory:>" + a.ID}
	if strings.Join(audit.entries[len(audit.entries)-4:], "|") != strings.Join(want, "|") {
		t.Fatalf("every change is audited with from/to: %v", audit.entries)
	}

	// Candidates leave out what already has a customer.
	cands, err := cust.Candidates(w.ctx)
	if err != nil || len(cands.Projects) != 1 || len(cands.Requests) != 1 || cands.Requests[0].ID != "rq-old" {
		t.Fatalf("candidates: %+v %v", cands, err)
	}
}

// docIDOf finds the id of the memory document with a ref (unscoped recall sees everything).
func docIDOf(t *testing.T, mem *memory.Service, w *world, ref string) string {
	t.Helper()
	items, err := mem.Search(w.ctx, org, agent, "saldo bancos historica", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Ref == ref {
			return it.ID
		}
	}
	t.Fatalf("no memory document %s in %+v", ref, items)
	return ""
}
