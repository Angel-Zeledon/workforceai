package customers_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/customers"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/roles"
)

// Cross-customer leakage through artifacts (B12): the artifacts a person references in a project plan
// ("artifact:<id>", W3) and the sheet cells an output cites (B6 evidence chain) reach a customer-scoped task only
// when they belong to that customer. The plan-time refusal is tested in internal/projects (customer_test.go); this
// is the task-time filter, which must hold even when a reference slipped past the plan (fail closed).

const artAgent = "analyst"

type artWorld struct {
	ctx        context.Context
	org        string
	svc        *artifacts.Service
	a, b, g    artifacts.Artifact
	custA      string
	resolveCID func(ctx context.Context, org, requestID string) (string, error)
}

func sheetOf(text string) json.RawMessage {
	return json.RawMessage(`{"schema":"aiw.sheet/1","sheets":[{"id":"s1","name":"Saldos","rows":10,"cols":4,"cells":{"A1":{"v":"` + text + `"},"B1":{"v":` +
		map[bool]string{true: "222", false: "111"}[strings.Contains(text, "panaderia")] + `}}}]}`)
}

func newArtWorld(t *testing.T, resolver func(cust *customers.Service) func(context.Context, string, string) (string, error)) *artWorld {
	t.Helper()
	org := domain.DemoOrgID
	ctx := application.WithOrg(context.Background(), org)
	core := memory.New()
	if err := core.Seed(context.Background(), domain.SeedOrg(50), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	cust := customers.New(customers.Config{Store: customers.NewMemStore(), OrgID: org})
	a, err := cust.Create(ctx, "ana", customers.Input{Name: ptr("Ferretería López")})
	if err != nil {
		t.Fatal(err)
	}
	b, err := cust.Create(ctx, "ana", customers.Input{Name: ptr("Panadería San José")})
	if err != nil {
		t.Fatal(err)
	}
	for req, c := range map[string]string{"rq-a1": a.ID, "rq-b1": b.ID} {
		if err := cust.LinkRequest(ctx, org, req, c); err != nil {
			t.Fatal(err)
		}
	}
	res := resolver(cust)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := artifacts.New(artifacts.Config{Store: artifacts.NewMemStore(), Core: core, OrgID: org, Log: log, CustomerOf: res})
	me := artifacts.Actor{Kind: artifacts.ActorUser, ID: "ana"}
	mk := func(title, text, customerID string) artifacts.Artifact {
		art, err := svc.Create(ctx, me, artifacts.CreateInput{Kind: artifacts.KindSheet, Title: title, Content: sheetOf(text), AgentID: artAgent, CustomerID: customerID})
		if err != nil {
			t.Fatal(err)
		}
		return art
	}
	return &artWorld{ctx: ctx, org: org, svc: svc, custA: a.ID, resolveCID: res,
		a: mk("Saldos ferreteria", "saldo de bancos ferreteria", a.ID),
		b: mk("Saldos panaderia", "saldo de bancos panaderia harina", b.ID),
		// org-wide, without a customer: it may hold anyone's data (here, B's)
		g: mk("Resumen de todos los clientes", "panaderia y ferreteria", "")}
}

func realScope(cust *customers.Service) func(context.Context, string, string) (string, error) {
	return cust.CustomerOfRequest
}

func TestReferencedArtifactsNeverCrossCustomers(t *testing.T) {
	w := newArtWorld(t, realScope)
	ids := []string{w.a.ID, w.b.ID, w.g.ID}
	got := w.svc.ContextArtifacts(w.ctx, w.org, artAgent, "rq-a1", ids, 3000)
	if len(got) != 1 || got[0].ID != w.a.ID {
		t.Fatalf("a task of A must receive only A's referenced artifact: %+v", got)
	}
	for _, c := range got {
		if leaks(c.Title + " " + c.Text) {
			t.Fatalf("customer A received customer B's artifact: %+v", c)
		}
	}
	// B sees its own and never A's.
	if got := w.svc.ContextArtifacts(w.ctx, w.org, artAgent, "rq-b1", ids, 3000); len(got) != 1 || got[0].ID != w.b.ID {
		t.Fatalf("a task of B must receive only B's: %+v", got)
	}
	// A request without a customer keeps the old behaviour (every artifact the agent may read).
	if got := w.svc.ContextArtifacts(w.ctx, w.org, artAgent, "rq-unscoped", ids, 3000); len(got) != 3 {
		t.Fatalf("an unscoped task keeps the old rule: %+v", got)
	}
	// The scope comes from the request id, but a run that carries a customer in ctx is never widened.
	if got := w.svc.ContextArtifacts(application.WithCustomer(w.ctx, w.custA), w.org, artAgent, "rq-unscoped", ids, 3000); len(got) != 1 || got[0].ID != w.a.ID {
		t.Fatalf("the run's customer must narrow an unscoped request id: %+v", got)
	}
}

func TestEvidenceCellsNeverCrossCustomers(t *testing.T) {
	w := newArtWorld(t, realScope)
	if v, ok := w.svc.ResolveCell(w.ctx, w.org, artAgent, "rq-a1", w.b.ID, "Saldos", "B1"); ok {
		t.Fatalf("a task of A must not verify a citation of B's sheet: %q", v)
	}
	if _, ok := w.svc.ResolveCell(w.ctx, w.org, artAgent, "rq-a1", w.g.ID, "Saldos", "B1"); ok {
		t.Fatal("a task of A must not verify a citation of an unscoped sheet")
	}
	if v, ok := w.svc.ResolveCell(w.ctx, w.org, artAgent, "rq-a1", w.a.ID, "Saldos", "B1"); !ok || v != "111" {
		t.Fatalf("A's own cell must resolve: %q %v", v, ok)
	}
	if _, ok := w.svc.ResolveCell(w.ctx, w.org, artAgent, "rq-unscoped", w.b.ID, "Saldos", "B1"); !ok {
		t.Fatal("an unscoped task keeps the old rule")
	}
}

func TestArtifactScopeUnknownFailsClosed(t *testing.T) {
	broken := func(*customers.Service) func(context.Context, string, string) (string, error) {
		return func(context.Context, string, string) (string, error) { return "", errors.New("db down") }
	}
	w := newArtWorld(t, broken)
	if got := w.svc.ContextArtifacts(w.ctx, w.org, artAgent, "rq-a1", []string{w.a.ID, w.b.ID, w.g.ID}, 3000); len(got) != 0 {
		t.Fatalf("an unknown scope must hand nothing: %+v", got)
	}
	if _, ok := w.svc.ResolveCell(w.ctx, w.org, artAgent, "rq-a1", w.a.ID, "Saldos", "B1"); ok {
		t.Fatal("an unknown scope must verify nothing")
	}
}
