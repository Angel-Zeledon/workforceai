package application_test

import (
	"context"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Taking an agent out of an office blocks its tasks that have not started for requests of that office, with a reason,
// and leaves its work for other offices (and the ones that already ran) alone.
func TestRemoveOfficeBlocksPending(t *testing.T) {
	h := newHarness(t, &fakeRuntime{plan: onePlan("sales")}, nil)
	h.orch.SetOfficeScope(scopeFake{})
	ctx := application.WithOrg(context.Background(), org)
	mk := func(id, office string, st domain.TaskStatus) {
		if err := h.store.CreateTask(ctx, org, domain.Task{ID: id, RequestID: "r-" + id, AgentID: "sales", Status: st, OfficeID: office, Title: id}); err != nil {
			t.Fatal(err)
		}
	}
	mk("t-ven", "o-ven", domain.TaskPending)
	mk("t-legal", "o-legal", domain.TaskPending)
	mk("t-ven-done", "o-ven", domain.TaskDone)
	n, err := h.orch.BlockOfficeTasks(ctx, org, "sales", "o-ven")
	if err != nil || n != 1 {
		t.Fatalf("blocked %d %v", n, err)
	}
	for id, want := range map[string]domain.TaskStatus{"t-ven": domain.TaskBlocked, "t-legal": domain.TaskPending, "t-ven-done": domain.TaskDone} {
		got, _ := h.store.GetTask(ctx, org, id)
		if got.Status != want {
			t.Errorf("%s: %s, want %s", id, got.Status, want)
		}
	}
	if got, _ := h.store.GetTask(ctx, org, "t-ven"); got.Output == nil || got.Output.Summary == "" {
		t.Error("the blocked task must say why")
	}
}

// The cost of a call is annotated to the office that asked.
func TestUsageOffice(t *testing.T) {
	rt := &fakeRuntime{plan: onePlan("sales")}
	rt.runTask = func(application.RunTaskRequest) (application.RunTaskResponse, error) {
		res := okResult("hecho")
		res.Usage = application.Usage{Model: "m", InputTokens: 1000, OutputTokens: 200, CostUSD: 0.01}
		return res, nil
	}
	h := newHarness(t, rt, nil)
	h.orch.SetOfficeScope(scopeFake{})
	id, err := h.orch.Submit(application.WithRequestOffice(context.Background(), "o-ven"), "costo ventas")
	if err != nil {
		t.Fatal(err)
	}
	h.waitFor("done", func() bool { return h.tasks(id)["Responder"].Status == domain.TaskDone || h.tasks(id)["Responder correos"].Status == domain.TaskDone })
	us, _ := h.store.ListUsage(application.WithOrg(context.Background(), org), org)
	found := false
	for _, u := range us {
		if u.RequestID == id {
			found = true
			if u.OfficeID != "o-ven" {
				t.Errorf("usage of the request annotated to %q", u.OfficeID)
			}
		}
	}
	if !found {
		t.Fatal("no usage entries for the request")
	}
}
