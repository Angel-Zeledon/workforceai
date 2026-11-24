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
)

// scopeFake: agent "sales" has its base in Legal and also works for Ventas; "accounting" belongs to Ventas only.
type scopeFake struct{}

func (scopeFake) base(agent string) string {
	if agent == "sales" {
		return "o-legal"
	}
	return "o-ven"
}

func (s scopeFake) WorksFor(_ context.Context, _, agent, office string) (bool, error) {
	return office == s.base(agent) || agent == "sales" && office == "o-ven", nil
}

func (s scopeFake) Effective(ctx context.Context, org, agent string) (string, string, error) {
	eff := application.RequestOfficeFrom(ctx)
	if ok, _ := s.WorksFor(ctx, org, agent, eff); eff == "" || !ok {
		eff = s.base(agent)
	}
	return eff, s.base(agent), nil
}

func (s scopeFake) officeOf(ctx context.Context, org, agent string) (string, error) {
	eff, _, err := s.Effective(ctx, org, agent)
	return eff, err
}

// The adversarial isolation test of the plan docs/superpowers/plans/2026-11-23-aislamiento-oficinas.md: an agent with
// base Legal that also works for Ventas keeps different secrets in memory (task documents, notes, key/value), and a
// request of each office, looked at in the request that reaches the runtime, never contains the other one's secret.
func TestIsolation_RuntimeRequestNeverHasTheOtherOfficesSecrets(t *testing.T) {
	var mu sync.Mutex
	got := map[string]string{} // request text -> everything the runtime received
	var planAgents []string
	rt := &fakeRuntime{}
	rt.plan = func(in application.PlanRequest) (application.PlanResponse, error) {
		mu.Lock()
		planAgents = planAgents[:0]
		for _, a := range in.Agents {
			planAgents = append(planAgents, a.ID)
		}
		mu.Unlock()
		return application.PlanResponse{Tasks: []application.PlannedTask{{Key: "a", Title: "Responder", AgentID: "sales"}}}, nil
	}
	rt.runTask = func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		raw, _ := json.Marshal(in)
		mu.Lock()
		got[in.Context.RequestText] = string(raw)
		mu.Unlock()
		res := okResult("hecho")
		switch {
		case strings.Contains(in.Context.RequestText, "escribe legal"):
			res = okResult("LEGALSECRET propuesta confidencial contrato")
			res.Output.MemoryNotes = []string{"LEGALNOTE propuesta confidencial contrato"}
		case strings.Contains(in.Context.RequestText, "escribe ventas"):
			res = okResult("VENTASSECRET propuesta confidencial contrato")
			res.Output.MemoryNotes = []string{"VENTASNOTE propuesta confidencial contrato"}
		}
		return res, nil
	}
	h := newHarness(t, rt, nil)
	store := memory.NewMemStore()
	svc := memory.New(memory.Config{Docs: store, Retriever: &memory.FTS{Docs: store}})
	scope := scopeFake{}
	svc.SetOfficeScope(scope.officeOf, func(context.Context, string, string) (bool, error) { return false, nil })
	h.orch.SetMemorySource(svc)
	h.orch.SetOfficeScope(scope)

	run := func(office, text string) domain.Request {
		t.Helper()
		id, err := h.orch.Submit(application.WithRequestOffice(context.Background(), office), text)
		if err != nil {
			t.Fatal(err)
		}
		h.waitFor("done: "+text, func() bool {
			ts := h.tasks(id)
			return len(ts) == 1 && ts["Responder"].Status == domain.TaskDone
		})
		return h.request(id)
	}
	// each office teaches the shared agent its own secrets
	legal := run("o-legal", "escribe legal propuesta confidencial contrato")
	run("o-ven", "escribe ventas propuesta confidencial contrato")
	ctx := application.WithOrg(context.Background(), org)
	h.waitFor("memory written", func() bool { l, _ := svc.List(ctx, org, "sales", 20); return len(l) >= 4 })

	if legal.OfficeID != "o-legal" || h.tasks(legal.ID)["Responder"].OfficeID != "o-legal" {
		t.Fatalf("the request and its tasks keep the office: %+v", legal)
	}

	for _, c := range []struct{ office, text, mine, other string }{
		{"o-ven", "consulta ventas propuesta confidencial contrato", "VENTAS", "LEGAL"},
		{"o-legal", "consulta legal propuesta confidencial contrato", "LEGAL", "VENTAS"},
		{"", "consulta desconocida legal propuesta confidencial contrato", "LEGAL", "VENTAS"}, // unknown: the base, never another
	} {
		run(c.office, c.text)
		mu.Lock()
		raw := got[c.text]
		agents := strings.Join(planAgents, ",")
		mu.Unlock()
		if !strings.Contains(raw, c.mine+"NOTE") {
			t.Errorf("office %q: its own memory is missing from the runtime request: %s", c.office, raw[strings.Index(raw, "memory\":[")+0:])
		}
		if strings.Contains(raw, c.other+"SECRET") || strings.Contains(raw, c.other+"NOTE") || strings.Contains(raw, "escribe "+strings.ToLower(c.other)) {
			t.Errorf("office %q: the runtime request holds the other office's secret:\n%s", c.office, raw)
		}
		if c.office == "o-legal" && strings.Contains(agents, "accounting") {
			t.Errorf("a Legal request offered an agent of Ventas to the planner: %s", agents)
		}
	}
}
