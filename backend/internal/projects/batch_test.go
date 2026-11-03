package projects_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/policy"
	"aiworkforce/backend/internal/projects"
)

// batchEnv: an env whose projects know three customers (one without e-mail) and whose runtime can hold tasks.
type batchEnv struct {
	*env
	mu      sync.Mutex
	hold    bool
	release chan struct{}
}

var batchCustomers = map[string]projects.CustomerVars{
	"c1": {Name: "ACME", Contact: "Ana", Email: "ana@acme.com", Phone: "1"},
	"c2": {Name: "Beta", Contact: "Bo", Email: "bo@beta.com"},
	"c3": {Name: "Gama", Contact: "Gil"}, // no e-mail
}

func newBatchEnv(t *testing.T, tier policy.Tier) *batchEnv {
	t.Helper()
	be := &batchEnv{release: make(chan struct{})}
	rt := newRT()
	rt.onRun = func(application.RunTaskRequest) {
		be.mu.Lock()
		h := be.hold
		be.mu.Unlock()
		if h {
			<-be.release
		}
	}
	e := newEnv(t, rt, nil, nil)
	e.orch.SetCustomerScope(&scopeStub{links: map[string]string{}})
	cfg := e.svc.ConfigForTest()
	cfg.CustomerActive = func(_ context.Context, _, id string) error {
		if _, ok := batchCustomers[id]; !ok {
			return domain.ErrNotFound
		}
		return nil
	}
	cfg.CustomerInfo = func(_ context.Context, _, id string) (projects.CustomerVars, error) {
		v, ok := batchCustomers[id]
		if !ok {
			return v, domain.ErrNotFound
		}
		return v, nil
	}
	cfg.PlanLimits = func(context.Context, string) policy.Tier { return tier }
	e.svc = projects.New(context.Background(), cfg)
	be.env = e
	t.Cleanup(func() {
		be.mu.Lock()
		if be.hold {
			be.hold = false
			close(be.release)
		}
		be.mu.Unlock()
	})
	return be
}

type scopeStub struct {
	mu    sync.Mutex
	links map[string]string
}

func (s *scopeStub) LinkRequest(_ context.Context, _, requestID, customerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.links[requestID] = customerID
	return nil
}
func (s *scopeStub) CustomerOfRequest(_ context.Context, _, requestID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.links[requestID], nil
}
func (s *scopeStub) AssignedAgents(context.Context, string, string) ([]string, error) { return nil, nil }

func (be *batchEnv) holdTasks(on bool) {
	be.mu.Lock()
	defer be.mu.Unlock()
	if be.hold && !on {
		close(be.release)
	}
	be.hold = on
}

func batchIn(ids ...string) projects.BatchInput {
	return projects.BatchInput{TemplateID: "tpl-generic", CustomerIDs: ids, BudgetPerProjectUSD: budgetBig, Parallel: 1, Confirm: true,
		Params: map[string]string{}, Goal: "Contactar a {{cliente.nombre}} ({{cliente.empresa}}) en {{cliente.correo}}"}
}

func TestBatchPreviewMarksMissingCustomerData(t *testing.T) {
	be := newBatchEnv(t, policy.Tier{ID: "pro", MaxActiveProjects: 5, MaxBatchCustomers: 10, BatchParallel: 3})
	p, err := be.svc.PreviewBatch(be.ctx, batchIn("c1", "c3"))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Customers) != 2 || len(p.Customers[0].Missing) != 0 || strings.Join(p.Customers[1].Missing, ",") != "correo" {
		t.Fatalf("preview customers: %+v", p.Customers)
	}
	if p.TotalP50USD <= 0 || p.TotalP50USD < p.PerProjectP50USD*1.9 || p.MaxCustomers != 10 || p.Parallel != 1 {
		t.Fatalf("estimate: %+v", p)
	}
	if _, err := be.svc.CreateBatch(be.ctx, batchIn("c1", "c3")); !errors.Is(err, domain.ErrConflict) || !strings.Contains(err.Error(), "missing_customer_data") {
		t.Fatalf("create with missing data: %v", err)
	}
	in := batchIn("c1", "c2", "c3")
	in.Parallel = 99
	p, _ = be.svc.PreviewBatch(be.ctx, in)
	if p.Parallel != 3 {
		t.Fatalf("parallel = %d, want the plan's 3", p.Parallel)
	}
}

func TestBatchMaxCustomersPerPlan(t *testing.T) {
	be := newBatchEnv(t, policy.Tier{ID: "free", MaxActiveProjects: 1, MaxBatchCustomers: 2, BatchParallel: 1})
	var pl *domain.PlanLimitError
	if _, err := be.svc.CreateBatch(be.ctx, batchIn("c1", "c2", "c3")); !errors.As(err, &pl) || pl.Kind != "batch_customers" || pl.Limit != 2 {
		t.Fatalf("too many customers: %v", err)
	}
	in := batchIn("c1", "c2")
	in.Confirm = false
	if p, _ := be.svc.PreviewBatch(be.ctx, in); !p.NeedsConfirm || p.TotalBudgetUSD != 2*budgetBig {
		t.Fatalf("a total above the threshold must be confirmed: %+v", p)
	}
	if _, err := be.svc.CreateBatch(be.ctx, in); !errors.Is(err, domain.ErrConflict) || !strings.Contains(err.Error(), "needs_confirm") {
		t.Fatalf("unconfirmed: %v", err)
	}
}

func TestBatchRunsAtMostKAndCountsAsOneActive(t *testing.T) {
	be := newBatchEnv(t, policy.Tier{ID: "free", MaxActiveProjects: 1, MaxBatchCustomers: 10, BatchParallel: 1})
	be.holdTasks(true)
	in := batchIn("c1", "c2")
	b, err := be.svc.CreateBatch(application.WithActor(be.ctx, "ana"), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Items) != 2 {
		t.Fatalf("items = %+v", b.Items)
	}
	// one customer's project at a time (K=1) and the batch is the organization's one active project
	be.waitFor("first launched", func() bool {
		v, _ := be.svc.BatchView(be.ctx, b.ID)
		return v.Summary["running"] == 1 && v.Summary["queued"] == 1
	})
	rec0, _ := be.svc.Get(be.ctx, b.Items[0].ProjectID)
	if !strings.Contains(rec0.Project.Goal, "Ana") || !strings.Contains(rec0.Project.Goal, "ana@acme.com") || rec0.Project.CustomerID != "c1" {
		t.Fatalf("customer data in the project: %q / %s", rec0.Project.Goal, rec0.Project.CustomerID)
	}
	other := be.draft("tpl-generic", 0)
	if _, err := be.svc.Launch(be.ctx, other, projects.LaunchBody{ApprovedBudgetUSD: budgetBig, AcknowledgeUnderbudget: true, ProceedWithoutInputs: true}); err == nil {
		t.Fatal("a separate project must not start while the batch is the one active project")
	}
	be.holdTasks(false)
	be.waitFor("batch done", func() bool {
		be.approveGates(b.Items[0].ProjectID)
		if v, _ := be.svc.BatchView(be.ctx, b.ID); len(v.Rows) == 2 && v.Rows[1].ProjectID != "" {
			be.approveGates(v.Rows[1].ProjectID)
		}
		v, _ := be.svc.BatchView(be.ctx, b.ID)
		return v.Batch.Status == projects.BatchDone
	})
	v, _ := be.svc.BatchView(be.ctx, b.ID)
	if v.Batch.Name != "Contactar a … (…) en …" {
		t.Fatalf("batch name = %q", v.Batch.Name)
	}
	if v.Summary["done"] != 2 || v.Rows[0].CustomerName != "ACME" || v.Rows[0].Progress != 100 {
		t.Fatalf("view: %+v rows %+v", v.Summary, v.Rows)
	}
	list, _ := be.svc.ListBatches(be.ctx)
	if len(list) != 1 || list[0].ID != b.ID {
		t.Fatalf("list = %+v", list)
	}
}

func TestBatchPauseAndCancelCustomer(t *testing.T) {
	be := newBatchEnv(t, policy.Tier{ID: "pro", MaxActiveProjects: 5, MaxBatchCustomers: 10, BatchParallel: 1})
	be.holdTasks(true)
	b, err := be.svc.CreateBatch(be.ctx, batchIn("c1", "c2"))
	if err != nil {
		t.Fatal(err)
	}
	be.waitFor("first launched", func() bool {
		v, _ := be.svc.BatchView(be.ctx, b.ID)
		return v.Summary["running"] == 1
	})
	if _, err := be.svc.BatchControl(be.ctx, b.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	if _, err := be.svc.CancelBatchCustomer(be.ctx, b.ID, "c2"); err != nil {
		t.Fatal(err)
	}
	be.holdTasks(false)
	time.Sleep(100 * time.Millisecond)
	v, _ := be.svc.BatchView(be.ctx, b.ID)
	if v.Rows[1].Status != "cancelled" || v.Batch.Control != projects.ControlPaused {
		t.Fatalf("after pause+cancel: %+v %s", v.Rows, v.Batch.Control)
	}
	if _, err := be.svc.BatchControl(be.ctx, b.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	be.waitFor("batch cancelled", func() bool {
		v, _ := be.svc.BatchView(be.ctx, b.ID)
		return v.Batch.Status == projects.BatchCancelled && v.Rows[0].Status == "cancelled"
	})
}
