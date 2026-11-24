package application_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/memory"
	"aiworkforce/backend/internal/policy"
)

// A request asked from an office and split between temporary helpers stays in that office: its parts, its batch
// joins and its final join are the office's work (tasks with its office_id, run under its context), and what the
// runtime receives for each of them holds nothing of another office: the part is a neutral helper (no memory, notes,
// files or workspace), and the joining task is the responsible's own work for THIS office.
func TestHelpersOfARequestOfAnOfficeStayInThatOffice(t *testing.T) {
	var mu sync.Mutex
	inputs := map[string]string{} // task title -> everything the runtime received
	rt := &fakeRuntime{plan: func(application.PlanRequest) (application.PlanResponse, error) {
		return application.PlanResponse{Tasks: []application.PlannedTask{{Key: "a", Title: "Responder", AgentID: "sales"}}}, nil
	}}
	rt.runTask = func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		raw, _ := json.Marshal(in)
		mu.Lock()
		inputs[in.Task.Title] = string(raw)
		mu.Unlock()
		res := okResult("hecho: " + in.Task.Title)
		res.Usage = application.Usage{Model: "sim", CostUSD: 0.01}
		switch {
		case strings.HasPrefix(in.Context.RequestText, "escribe legal"):
			res = okResult("LEGALSECRET contrato confidencial")
			res.Output.MemoryNotes = []string{"LEGALNOTE contrato confidencial"}
		case strings.HasPrefix(in.Context.RequestText, "escribe ventas"):
			res = okResult("VENTASSECRET contrato confidencial")
			res.Output.MemoryNotes = []string{"VENTASNOTE contrato confidencial"}
		}
		return res, nil
	}
	h := newHarness(t, rt, helperCfg)
	h.orch.SetHelperLimits(func(context.Context, string) policy.Tier { return policy.Tier{ID: "pro", HelpersParallel: 3, HelperParts: 20} })
	store := memory.NewMemStore()
	svc := memory.New(memory.Config{Docs: store, Retriever: &memory.FTS{Docs: store}})
	scope := scopeFake{} // sales: base Legal, also works for Ventas
	svc.SetOfficeScope(scope.officeOf, func(context.Context, string, string) (bool, error) { return false, nil })
	h.orch.SetMemorySource(svc)
	h.orch.SetOfficeScope(scope)

	run := func(office, text string) string {
		t.Helper()
		id, err := h.orch.Submit(application.WithRequestOffice(context.Background(), office), text)
		if err != nil {
			t.Fatal(err)
		}
		h.waitFor("done: "+text, func() bool { return h.tasks(id)["Responder"].Status == domain.TaskDone })
		return id
	}
	run("o-legal", "escribe legal contrato confidencial")
	run("o-ven", "escribe ventas contrato confidencial")
	ctx := application.WithOrg(context.Background(), org)
	h.waitFor("memory written", func() bool { l, _ := svc.List(ctx, org, "sales", 20); return len(l) >= 4 })

	// the request to split is asked from Ventas; whoever accepts the helpers does it with no office in the context
	reqID := run("o-ven", "revisa contrato confidencial uno y dos")
	g := domain.HelperGroup{RequestID: reqID, OwnerAgentID: "sales", Mode: domain.HelperModePosition, Estimate: domain.HelperEstimate{MinUSD: 0.01, MaxUSD: 0.2}}
	for i := 1; i <= 3; i++ {
		g.Parts = append(g.Parts, domain.HelperPart{Index: i, Label: "Contratos", Text: "contrato confidencial parte"})
	}
	if _, err := h.orch.LaunchHelpers(context.Background(), g, 3, 1, "alice"); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()

	var parts, joins int
	for title, tk := range h.tasks(reqID) {
		if tk.Helper == nil {
			continue
		}
		if tk.OfficeID != "o-ven" {
			t.Errorf("task %q of the helpers is in office %q, want o-ven", title, tk.OfficeID)
		}
		if tk.Status != domain.TaskDone {
			t.Errorf("task %q = %s", title, tk.Status)
		}
		if tk.Helper.Index > 0 {
			parts++
		} else {
			joins++
		}
	}
	if parts != 3 || joins == 0 {
		t.Fatalf("parts=%d joins=%d", parts, joins)
	}
	mu.Lock()
	defer mu.Unlock()
	for title, raw := range inputs {
		if !strings.HasPrefix(title, "Ayudante") && !strings.HasPrefix(title, "Juntar") {
			continue
		}
		if strings.Contains(raw, "LEGAL") || strings.Contains(raw, "escribe legal") {
			t.Errorf("%q received what sales knows for Legal: %s", title, raw)
		}
		if strings.HasPrefix(title, "Ayudante") && (strings.Contains(raw, "VENTAS") || strings.Contains(raw, "escribe ventas")) {
			t.Errorf("a part carries the office's memory, a helper carries none: %s", raw)
		}
	}
	if len(inputs) < 5 {
		t.Fatalf("the helpers did not run: %v", len(inputs))
	}
}
