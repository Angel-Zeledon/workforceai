package application_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// fakeCustomers is an application.CustomerScope that remembers the links and checks that the request did not
// exist yet when it was scoped.
type fakeCustomers struct {
	mu    sync.Mutex
	store interface {
		GetRequest(context.Context, string, string) (domain.Request, error)
	}
	known    map[string]bool
	links    map[string]string
	assigned map[string][]string
	early    bool // every link happened before the request was stored
}

func (f *fakeCustomers) LinkRequest(ctx context.Context, org, requestID, customerID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.known[customerID] {
		return fmt.Errorf("%w: customer", domain.ErrNotFound)
	}
	if _, err := f.store.GetRequest(ctx, org, requestID); err == nil {
		f.early = false
	}
	f.links[requestID] = customerID
	return nil
}

func (f *fakeCustomers) CustomerOfRequest(_ context.Context, _, requestID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.links[requestID], nil
}

func (f *fakeCustomers) AssignedAgents(_ context.Context, _, customerID string) ([]string, error) {
	return f.assigned[customerID], nil
}

func TestCustomerScopedRequestIsLinkedBeforeItExists(t *testing.T) {
	var mu sync.Mutex
	var planned [][]string
	rt := &fakeRuntime{plan: func(in application.PlanRequest) (application.PlanResponse, error) {
		ids := []string{}
		for _, a := range in.Agents {
			ids = append(ids, a.ID)
		}
		mu.Lock()
		planned = append(planned, ids)
		mu.Unlock()
		return application.PlanResponse{Tasks: []application.PlannedTask{{Key: "a", Title: "conciliar bancos", AgentID: in.Agents[0].ID}}}, nil
	}}
	h := newHarness(t, rt, nil)
	agents, _ := h.store.ListAgents(context.Background(), domain.DemoOrgID)
	clone := ""
	for _, a := range agents {
		if a.ID != "assistant" {
			clone = a.ID
			break
		}
	}
	fc := &fakeCustomers{store: h.store, known: map[string]bool{"cus_a": true, "cus_b": true}, links: map[string]string{},
		assigned: map[string][]string{"cus_a": {clone}}, early: true}
	h.orch.SetCustomerScope(fc)

	// Unknown customer: refused before anything is stored.
	before, _ := h.store.ListRequests(context.Background(), domain.DemoOrgID)
	if _, err := h.orch.Submit(application.WithCustomer(context.Background(), "cus_zzz"), "cierre"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an unknown customer must be refused: %v", err)
	}
	if after, _ := h.store.ListRequests(context.Background(), domain.DemoOrgID); len(after) != len(before) {
		t.Fatalf("a refused scope must not create a request: %d -> %d", len(before), len(after))
	}

	// Scoped request: linked before it exists; the planner only sees the customer's clones.
	reqA, err := h.orch.Submit(application.WithCustomer(context.Background(), "cus_a"), "cierre de mes de la ferretería")
	if err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	if got, _ := fc.CustomerOfRequest(context.Background(), "", reqA); got != "cus_a" || !fc.early {
		t.Fatalf("request must be scoped to cus_a before it was stored: got %q early=%v", got, fc.early)
	}
	// Customer without clones and unscoped request: every agent is available, nothing is linked.
	reqB, _ := h.orch.Submit(application.WithCustomer(context.Background(), "cus_b"), "cierre panadería")
	reqN, _ := h.orch.Submit(context.Background(), "tarea general")
	h.orch.Wait()
	if got, _ := fc.CustomerOfRequest(context.Background(), "", reqN); got != "" {
		t.Fatalf("an unscoped request must not be linked: %q", got)
	}
	_ = reqB
	mu.Lock()
	defer mu.Unlock()
	if len(planned) != 3 {
		t.Fatalf("three plans expected, got %d", len(planned))
	}
	if slices.ContainsFunc(planned[0], func(id string) bool { return id != clone && id != "assistant" }) || !slices.Contains(planned[0], clone) {
		t.Fatalf("the planner of a customer with clones must only see them: %v", planned[0])
	}
	if len(planned[1]) <= 2 || len(planned[2]) <= 2 {
		t.Fatalf("without clones every agent stays available: %v / %v", planned[1], planned[2])
	}
}

// checkingCustomers adds the optional CustomerChecker to fakeCustomers.
type checkingCustomers struct{ *fakeCustomers }

func (c checkingCustomers) CheckCustomer(_ context.Context, _, id string) error {
	if !c.known[id] {
		return fmt.Errorf("%w: customer", domain.ErrNotFound)
	}
	return nil
}

// B12: a chat message sent for a customer (the chat's customer picker) scopes the request the turn creates, stored
// before the request exists like POST /requests; a bad customer is refused before the message is stored.
func TestChatBornRequestCarriesTheCustomer(t *testing.T) {
	rt := newChatRuntime(fixedRoute("task", "sales", primary("assistant")))
	rt.fakeRuntime.plan = func(application.PlanRequest) (application.PlanResponse, error) {
		return application.PlanResponse{Objectives: []string{"conciliar"}, Tasks: []application.PlannedTask{{Key: "a", Title: "Conciliar bancos", AgentID: "sales"}}}, nil
	}
	h := newChat(t, rt, nil)
	ctx := context.Background()

	// Without customers configured a customer is refused, not ignored.
	if _, err := h.orch.PostChat(application.WithCustomer(ctx, "cus_a"), "office", "concilia los bancos"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("customers unavailable: %v", err)
	}
	fc := &fakeCustomers{store: h.store, known: map[string]bool{"cus_a": true}, links: map[string]string{}, assigned: map[string][]string{}, early: true}
	h.orch.SetCustomerScope(checkingCustomers{fc})
	before := len(h.messages("office"))
	if _, err := h.orch.PostChat(application.WithCustomer(ctx, "cus_zzz"), "office", "concilia los bancos"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an unknown customer must be refused at once: %v", err)
	}
	if after := len(h.messages("office")); after != before {
		t.Fatalf("a refused message must not be stored: %d -> %d", before, after)
	}

	if _, err := h.orch.PostChat(application.WithCustomer(ctx, "cus_a"), "office", "concilia los bancos de la ferretería"); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	h.say("office", "prepara el resumen general") // an unscoped message keeps the old behaviour
	reqs, _ := h.store.ListRequests(ctx, domain.DemoOrgID)
	if len(reqs) != 2 {
		t.Fatalf("two chat-born requests expected: %+v", reqs)
	}
	scoped := 0
	for _, r := range reqs {
		got, _ := fc.CustomerOfRequest(ctx, "", r.ID)
		switch {
		case strings.Contains(r.Text, "ferretería") && got == "cus_a":
			scoped++
		case strings.Contains(r.Text, "general") && got != "":
			t.Fatalf("an unscoped message must not scope its request: %q", got)
		case strings.Contains(r.Text, "ferretería"):
			t.Fatalf("the chat-born request must be scoped to cus_a: %q", got)
		}
	}
	if scoped != 1 || !fc.early {
		t.Fatalf("the scope must be stored before the request exists: scoped=%d early=%v", scoped, fc.early)
	}
}

func TestScopeCustomer(t *testing.T) {
	ctx := context.Background()
	if c, ok := application.ScopeCustomer(ctx, nil, "o", "r"); !ok || application.CustomerFrom(c) != "" {
		t.Fatal("no resolver: unscoped")
	}
	res := func(_ context.Context, _, req string) (string, error) {
		if req == "bad" {
			return "", errors.New("db down")
		}
		return map[string]string{"ra": "cus_a"}[req], nil
	}
	if c, ok := application.ScopeCustomer(ctx, res, "o", "ra"); !ok || application.CustomerFrom(c) != "cus_a" {
		t.Fatal("scoped request must carry its customer")
	}
	if _, ok := application.ScopeCustomer(ctx, res, "o", "bad"); ok {
		t.Fatal("an unknown scope must fail closed")
	}
	if application.CustomerFrom(application.WithCustomer(ctx, "  ")) != "" {
		t.Fatal("blank customer must not scope")
	}
}
