package projects_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/projects"
	"aiworkforce/backend/internal/roles"
)

// Every built-in template at its maximum size, and several large projects at
// once in one organization, run to completion with the fake runtime: the plan
// is parallel, nothing fails, every human gate is approved, every agent task
// leaves its deliverable (the Sink is what writes it to the drive) and the
// organization cap is respected.

// recSink records, per project, which agent nodes started and delivered output.
type recSink struct {
	mu        sync.Mutex
	delivered map[string]map[string]bool // project -> node -> output delivered
	order     []string                   // project of each delivered node, in delivery order
}

func (s *recSink) NodeChanged(_ context.Context, ev projects.NodeEvent) {
	if ev.State != projects.StateDone || ev.Output == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.delivered == nil {
		s.delivered = map[string]map[string]bool{}
	}
	if s.delivered[ev.ProjectID] == nil {
		s.delivered[ev.ProjectID] = map[string]bool{}
	}
	if !s.delivered[ev.ProjectID][ev.NodeID] {
		s.delivered[ev.ProjectID][ev.NodeID] = true
		s.order = append(s.order, ev.ProjectID)
	}
}

func (s *recSink) count(pid string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.delivered[pid])
}

type bigEnv struct {
	svc      *projects.Service
	rt       *fakeRuntime
	appr     *application.Approvals
	st       *memory.Store
	ctx      context.Context
	org      string
	orch     *application.Orchestrator
	sink     *recSink
	decided  map[string]bool
	approved int
}

func newBigEnv(t *testing.T, orgCap int) *bigEnv {
	t.Helper()
	rt := newRT()
	cfg := application.DefaultConfig()
	cfg.IdleDelay, cfg.RetryBase, cfg.TaskRetryBackoff = 0, time.Millisecond, time.Millisecond
	cfg.MaxParallelPerOrg = orgCap
	cfg.MaxPlanDepth = 60
	store := memory.New()
	if err := store.Seed(context.Background(), domainSeedOrg(cfg.BudgetUSD), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: store, Pub: &evLog{}, Log: log}
	appr := application.NewApprovals(cfg, store, rec)
	q := &application.Queries{Store: store, Cfg: cfg}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	orch := application.NewOrchestrator(ctx, cfg, store, rt, memory.NewLocker(), rec, appr, q, log)
	sink := &recSink{}
	svc := projects.New(ctx, projects.Config{Store: projects.NewMemStore(), Orch: orch, Core: store, Approvals: appr, Rec: rec, Runtime: rt,
		Catalog: catalog.Default(), OrgID: cfg.OrgID, Poll: 50 * time.Millisecond, PollIdleMax: 250 * time.Millisecond, Log: log, Sink: sink})
	return &bigEnv{svc: svc, rt: rt, appr: appr, st: store, ctx: ctx, org: cfg.OrgID, orch: orch, sink: sink, decided: map[string]bool{}}
}

// holdUntilOverlap keeps each call a moment until `want` calls overlapped once, so the peak measures what
// the scheduler allows and not goroutine timing in a loaded -race run.
func (e *bigEnv) holdUntilOverlap(want int) {
	e.rt.onRun = func(application.RunTaskRequest) {
		for deadline := time.Now().Add(80 * time.Millisecond); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
			e.rt.mu.Lock()
			p := e.rt.peak
			e.rt.mu.Unlock()
			if p >= want {
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
}

// approvePending decides every pending approval (the human gates) and counts them.
func (e *bigEnv) approvePending() {
	all, _ := e.st.ListApprovals(e.ctx, e.org, "")
	for _, a := range all {
		if a.Status == domain.ApprovalPending && !e.decided[a.ID] {
			if _, err := e.appr.Decide(e.ctx, a.ID, "approve", "reviewed by the owner of the gate"); err == nil {
				e.decided[a.ID] = true
				e.approved++
			}
		}
	}
}

// waitDone approves gates until every project is done (or fails the test).
func (e *bigEnv) waitDone(t *testing.T, ids []string, timeout time.Duration) map[string]projects.Detail {
	t.Helper()
	out := map[string]projects.Detail{}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		e.approvePending()
		left := 0
		for _, id := range ids {
			if _, ok := out[id]; ok {
				continue
			}
			d, err := e.svc.Get(e.ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if d.Project.Status == projects.StatusDone {
				out[id] = d
			} else if d.Project.Status == projects.StatusFailed || d.Project.Status == projects.StatusCancelled {
				t.Fatalf("project %s ended %s (%d/%d done, %d failed)", id, d.Project.Status, d.Project.TasksDone, d.Project.TasksTotal, d.Project.Failed)
			} else {
				left++
			}
		}
		if left == 0 {
			return out
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, id := range ids {
		if _, ok := out[id]; !ok {
			d, _ := e.svc.Get(e.ctx, id)
			t.Errorf("timeout (deadlock?): project %s %d/%d done, status %s, running %d, awaiting %d", id, d.Project.TasksDone, d.Project.TasksTotal, d.Project.Status, d.Project.Running, d.Project.Awaiting)
		}
	}
	t.FailNow()
	return nil
}

// maxParamsOf returns the largest parameters a template accepts.
func maxParamsOf(tpl projects.Template) map[string]string {
	out := map[string]string{}
	for _, p := range tpl.Params {
		switch p.Type {
		case "number":
			if p.Max > 0 {
				out[p.Key] = strconv.Itoa(p.Max)
			}
		case "list":
			n := p.Max
			if n <= 0 {
				n = projects.DefaultListItems
			}
			items := make([]string, n)
			for i := range items {
				items[i] = fmt.Sprintf("%s%02d", strings.ToUpper(p.Key[:1]), i+1)
			}
			out[p.Key] = strings.Join(items, ", ")
		}
	}
	return out
}

func (e *bigEnv) draftAndLaunch(t *testing.T, tplID string, params map[string]string, parallel int) string {
	t.Helper()
	r, err := e.svc.CreateDraft(e.ctx, projects.NewProject{TemplateID: tplID, Params: params, Locale: "es"})
	if err != nil {
		t.Fatalf("%s: %v", tplID, err)
	}
	if _, err := e.svc.Launch(e.ctx, r.ID, projects.LaunchBody{ApprovedBudgetUSD: 2000, AcknowledgeUnderbudget: true, ProceedWithoutInputs: true, MaxParallel: parallel}); err != nil {
		t.Fatalf("%s: %v", tplID, err)
	}
	return r.ID
}

func widestLevel(d projects.Detail) int {
	per := map[int]int{}
	w := 0
	for _, n := range d.Nodes {
		if n.Kind != projects.KindGroup {
			per[n.DagLevel]++
			w = max(w, per[n.DagLevel])
		}
	}
	return w
}

func TestEveryBuiltinTemplateRunsAtMaximumSize(t *testing.T) {
	probe := newBigEnv(t, 32)
	tpls, err := probe.svc.Templates(probe.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var builtin []projects.Template
	for _, tp := range tpls {
		if tp.Builtin && !strings.HasPrefix(tp.ID, "wf:") {
			builtin = append(builtin, tp)
		}
	}
	if len(builtin) < 13 {
		t.Fatalf("only %d built-in templates", len(builtin))
	}
	slots := make(chan struct{}, 4) // a few templates at a time: each has its own organization state
	for _, tp := range builtin {
		t.Run(tp.Key, func(t *testing.T) {
			t.Parallel()
			slots <- struct{}{}
			defer func() { <-slots }()
			e := newBigEnv(t, 32)
			if tp.Key == "quarterly_cash_close" { // the only built-in that asks for a hired role
				if _, err := e.hireTreasury(); err != nil {
					t.Fatal(err)
				}
			}
			e.holdUntilOverlap(9)
			params := maxParamsOf(tp)
			id := e.draftAndLaunch(t, tp.ID, params, 24)
			d0, _ := e.svc.Get(e.ctx, id)
			total, gatesPlanned, agentTasks := 0, 0, 0
			for _, n := range d0.Nodes {
				if n.Kind == projects.KindGroup {
					continue
				}
				total++
				if n.Kind == projects.KindGate {
					gatesPlanned++
				} else if n.AgentID != nil && n.Kind != projects.KindMilestone && n.Kind != projects.KindWait {
					agentTasks++
				}
			}
			if tp.Stats != nil && tp.Stats.TasksMax != total {
				t.Fatalf("the library says %d tasks at the maximum size, the project has %d", tp.Stats.TasksMax, total)
			}
			if gatesPlanned < 1 && tp.Key != "generic" && tp.Key != "branch_opening" {
				t.Fatalf("%s has no human gate", tp.Key)
			}
			dd := e.waitDone(t, []string{id}, 300*time.Second)[id]
			if dd.Project.TasksDone != dd.Project.TasksTotal || dd.Project.Failed != 0 {
				t.Fatalf("%d/%d done, %d failed", dd.Project.TasksDone, dd.Project.TasksTotal, dd.Project.Failed)
			}
			agents := map[string]bool{}
			for _, n := range dd.Nodes {
				if n.Kind == projects.KindGroup {
					continue
				}
				if n.State != projects.StateDone {
					t.Fatalf("node %q is %s", n.Title, n.State)
				}
				if n.AgentID != nil {
					agents[*n.AgentID] = true
				}
			}
			for a := range agents {
				if tp.Key != "quarterly_cash_close" && a != "accounting" && a != "analyst" && a != "legal" && a != "sales" && a != "hr" && a != "operations" && a != "assistant" {
					t.Fatalf("agent %q is not a role of the demo office", a)
				}
			}
			if e.approved < gatesPlanned {
				t.Fatalf("%d approvals decided for %d gates", e.approved, gatesPlanned)
			}
			// deliverables: every agent task delivered its output (written to the drive by the sink)
			deadline := time.Now().Add(5 * time.Second)
			for e.sink.count(id) < agentTasks && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if got := e.sink.count(id); got < agentTasks {
				t.Fatalf("%d deliverables for %d agent tasks", got, agentTasks)
			}
			if w := widestLevel(dd); w >= 12 && e.rt.peak < 9 {
				t.Fatalf("peak parallelism %d with a plan %d wide", e.rt.peak, w)
			}
			t.Logf("%s: %d tasks, %d gates, peak %d, %d roles", tp.Key, total, gatesPlanned, e.rt.peak, len(agents))
		})
	}
}

func (e *bigEnv) hireTreasury() (domain.Agent, error) {
	return e.orch.HireFromTemplate(e.ctx, application.HireInput{TemplateID: "finance_treasury", Locale: "es"})
}

func TestLargeProjectsRunConcurrentlyInOneOrganization(t *testing.T) {
	const orgCap = 10
	e := newBigEnv(t, orgCap)
	e.holdUntilOverlap(orgCap)
	type job struct {
		tpl    string
		params map[string]string
	}
	jobs := []job{
		{"tpl-annual-budget", map[string]string{"units": "Retail, Servicios, Industrial, Digital", "countries": "México, Chile, Perú, España"}},
		{"tpl-erp-migration", map[string]string{"streams": "Finanzas, Compras, Ventas, Inventario", "waves": "Piloto, Ola 1, Ola 2"}},
		{"tpl-product-launch", map[string]string{"markets": "México, Colombia, Chile, Perú, España, Brasil", "channels": "Online, Retail"}},
		{"tpl-compliance-program", map[string]string{"regulations": "GDPR, SOX, ISO 27001", "countries": "México, España, Chile, Perú"}},
		{"tpl-year-end-audit", map[string]string{"entities": "Matriz, Filial 1, Filial 2, Filial 3", "areas": "Ingresos, Inventarios, Nómina, Impuestos, Tesorería"}},
	}
	// Drafts first (planning a big template is CPU work), then every launch at the same moment.
	var ids []string
	totals := map[string]int{}
	for _, j := range jobs {
		r, err := e.svc.CreateDraft(e.ctx, projects.NewProject{TemplateID: j.tpl, Params: j.params, Locale: "es"})
		if err != nil {
			t.Fatal(err)
		}
		d, _ := e.svc.Get(e.ctx, r.ID)
		ids = append(ids, r.ID)
		totals[r.ID] = d.Project.TasksTotal
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.svc.Launch(e.ctx, id, projects.LaunchBody{ApprovedBudgetUSD: 2000, AcknowledgeUnderbudget: true, ProceedWithoutInputs: true, MaxParallel: 16}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	done := e.waitDone(t, ids, 300*time.Second)
	grand := 0
	for _, id := range ids {
		d := done[id]
		grand += d.Project.TasksTotal
		if d.Project.TasksDone != d.Project.TasksTotal || d.Project.Failed != 0 {
			t.Fatalf("project %s: %d/%d done, %d failed", id, d.Project.TasksDone, d.Project.TasksTotal, d.Project.Failed)
		}
	}
	e.rt.mu.Lock()
	peak := e.rt.peak
	e.rt.mu.Unlock()
	if peak > orgCap {
		t.Fatalf("peak %d: the organization cap of %d was exceeded", peak, orgCap)
	}
	if peak < orgCap-2 {
		t.Fatalf("peak %d: five large projects did not use the organization capacity (%d)", peak, orgCap)
	}
	if e.approved < len(jobs)+3 { // at least one gate per project, plus the three go-lives of the migration
		t.Fatalf("%d approvals decided", e.approved)
	}
	// Fairness, measured on the order in which the runtime finished the tasks (not on the monitor, whose
	// ticks lag under -race): titles tell the projects apart because each one has its own items.
	owner := map[string]string{}
	dup := map[string]bool{}
	for _, id := range ids {
		for _, n := range done[id].Nodes {
			if n.Kind == projects.KindGroup || n.AgentID == nil {
				continue
			}
			if o, ok := owner[n.Title]; ok && o != id {
				dup[n.Title] = true
			}
			owner[n.Title] = id
		}
	}
	e.rt.mu.Lock()
	type fin struct {
		title string
		at    int
	}
	var fins []fin
	for title, at := range e.rt.finished {
		if !dup[title] && owner[title] != "" {
			fins = append(fins, fin{title, at})
		}
	}
	e.rt.mu.Unlock()
	sort.Slice(fins, func(i, j int) bool { return fins[i].at < fins[j].at })
	first := map[string]int{}
	for i, f := range fins {
		if _, ok := first[owner[f.title]]; !ok {
			first[owner[f.title]] = i
		}
	}
	// Launching a plan of hundreds of nodes takes a while (the launches are serialized), so projects start at
	// different moments (and a gate waits for its approver). From the moment every project has delivered its
	// first task, the next half of the work must include some of each project that still has work: nobody is
	// locked out by the others. The turns themselves are tested in application/orgqueue_test.go.
	from := 0
	for _, id := range ids {
		from = max(from, first[id])
	}
	window := fins[min(from, len(fins)):min(from+len(fins)/2, len(fins))]
	inWindow := map[string]int{}
	before := map[string]int{}
	for i, f := range fins {
		if i < from {
			before[owner[f.title]]++
		}
	}
	for _, f := range window {
		inWindow[owner[f.title]]++
	}
	var shares []string
	for _, id := range ids {
		shares = append(shares, fmt.Sprintf("%d@%d", inWindow[id], first[id]))
		remaining := totals[id] - before[id]
		if remaining >= 6 && inWindow[id] < 3 {
			t.Errorf("project %s starved: %d of its %d remaining tasks finished in the window after every project had started (first tasks at %v)", id, inWindow[id], remaining, first)
		}
	}
	t.Logf("%d projects, %d tasks, peak %d, tasks finished per project in the fairness window @ first finish: %v, approvals %d", len(ids), grand, peak, shares, e.approved)
}
