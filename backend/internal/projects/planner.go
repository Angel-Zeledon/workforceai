package projects

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"aiworkforce/backend/internal/application"
)

// Hierarchical planning of projects created from a goal (W4):
//
//  1. one bounded planner call returns the phases (roadmap) with a rough size and
//     the dependencies between phases;
//  2. every phase is expanded into tasks by its own bounded call (a few in
//     parallel), each task with a complexity (S/M/L/XL), an agent of the
//     organization and dependencies inside the phase.
//
// The result is mapped onto the existing project structure: a phase is an
// objective, its tasks are grouped in workflows (groups) of at most
// MaxChildrenPerGroup, and the first tasks of a phase wait for the last tasks
// of the phases it depends on. Go validates everything again (agents, keys,
// cycles, limits): the runtime is never trusted with the shape of the DAG.
// Every call is reserved against and recorded in the cost ledger. A failure is
// never silent: the draft carries a PlannerInfo the UI shows (and an event and
// an audit entry are written).

// taskTargetBySize is how many tasks a phase of each size is asked to have.
// flatPlanTimeout bounds the single-call planner.
const flatPlanTimeout = 30 * time.Second

var taskTargetBySize = map[string]int{"S": 6, "M": 12, "L": 16, "XL": 22}

// planJob is one planner run; done closes when tpl and info are final.
type planJob struct {
	done       chan struct{}
	tpl        Template
	info       PlannerInfo
	doneN, tot atomic.Int32
	calls      int
	costUSD    float64
	statsMu    sync.Mutex
}

func (j *planJob) progress(done, total int) {
	j.doneN.Store(int32(done))
	j.tot.Store(int32(total))
}

func (j *planJob) addCall(usd float64) {
	j.statsMu.Lock()
	j.calls++
	j.costUSD += usd
	j.statsMu.Unlock()
}

func (s *Service) jobKey(org, id string) string { return org + "|" + id }

func (s *Service) jobOf(org, id string) *planJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobs[s.jobKey(org, id)]
}

// startPlanner runs the planner of a goal in the background and returns the job.
func (s *Service) startPlanner(ctx context.Context, pid, goal, locale string) *planJob {
	j := &planJob{done: make(chan struct{})}
	org := s.org(ctx)
	s.mu.Lock()
	s.jobs[s.jobKey(org, pid)] = j
	s.mu.Unlock()
	bg := context.WithoutCancel(ctx) // keeps the org/actor; the planner has its own timeouts
	go func() {
		defer close(j.done)
		j.tpl, j.info = s.runPlanner(bg, j, goal, locale)
		j.info.Calls, j.info.CostUSD = j.calls, j.costUSD
	}()
	return j
}

func (s *Service) dropJob(org, id string) {
	s.mu.Lock()
	delete(s.jobs, s.jobKey(org, id))
	s.mu.Unlock()
}

// genericTemplate is the fallback when the planner produced no plan.
func genericTemplate() Template {
	t, _ := builtinByID(idGeneric)
	return t
}

// placeholderTemplate is the empty draft shown while the planner still runs.
func placeholderTemplate(goal string) Template {
	name := truncRunes(strings.TrimSpace(goal), 60)
	return Template{ID: "tpl-planned", Key: "planned", Version: 1, Name: name, Params: []TemplateParam{},
		Objectives: []TemplateObjective{{Key: "o1", Title: name, Workflows: []TemplateWorkflow{{Key: "w1", Title: name, Nodes: []TemplateNode{}}}}}}
}

// runPlanner picks the best planner the runtime supports and degrades visibly.
func (s *Service) runPlanner(ctx context.Context, j *planJob, goal, locale string) (Template, PlannerInfo) {
	if s.cfg.Runtime == nil {
		return genericTemplate(), PlannerInfo{Mode: PlannerGeneric, Status: PlannerFailed, Code: "no_runtime"}
	}
	agents, err := s.cfg.Core.ListAgents(ctx, s.org(ctx))
	if err != nil || len(agents) == 0 {
		return genericTemplate(), PlannerInfo{Mode: PlannerGeneric, Status: PlannerFailed, Code: "no_agents"}
	}
	pa := make([]application.PlanAgent, 0, len(agents))
	known := map[string]bool{}
	for _, a := range agents {
		pa = append(pa, application.NewPlanAgent(a, locale))
		known[a.ID] = true
	}
	var failed *PlannerInfo
	if hp, ok := s.cfg.Runtime.(application.HierarchicalPlanner); ok {
		tpl, info, err := s.planHierarchical(ctx, j, hp, goal, locale, pa, known)
		if err == nil {
			return tpl, info
		}
		s.cfg.Log.Warn("hierarchical planner failed; trying the flat planner", "err", err)
		failed = &PlannerInfo{Code: plannerCode(err), Message: clip(err.Error(), 200)}
		if failed.Code == "budget_exceeded" { // no budget: the flat call would be refused too
			return genericTemplate(), PlannerInfo{Mode: PlannerGeneric, Status: PlannerFailed, FellBackFrom: PlannerHierarchical, Code: failed.Code, Message: failed.Message}
		}
	}
	tpl, err := s.planFlat(ctx, j, goal, locale, pa, known)
	if err == nil {
		info := PlannerInfo{Mode: PlannerFlat, Status: PlannerOK, Tasks: countTasks(tpl)}
		if failed != nil {
			info.Status, info.FellBackFrom, info.Code, info.Message = PlannerDegraded, PlannerHierarchical, failed.Code, failed.Message
		}
		return tpl, info
	}
	info := PlannerInfo{Mode: PlannerGeneric, Status: PlannerFailed, Code: plannerCode(err), Message: clip(err.Error(), 200)}
	if failed != nil {
		info.FellBackFrom = PlannerHierarchical
	}
	return genericTemplate(), info
}

func countTasks(t Template) int {
	n := 0
	for _, o := range t.Objectives {
		for _, w := range o.Workflows {
			n += len(w.Nodes)
		}
	}
	return n
}

func clip(s string, n int) string { return truncRunes(strings.TrimSpace(s), n) }

var errPlannerBudget = errors.New("budget_exceeded: the organization budget does not allow another planner call")

// plannerCode is the stable reason code of a planner error.
func plannerCode(err error) string {
	switch {
	case errors.Is(err, errPlannerBudget):
		return "budget_exceeded"
	case errors.Is(err, context.DeadlineExceeded):
		return "planner_timeout"
	case errors.Is(err, errPlannerInvalid):
		return "planner_invalid"
	default:
		return "planner_unavailable"
	}
}

var errPlannerInvalid = errors.New("the planner returned an invalid plan")

func invalidf(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errPlannerInvalid, fmt.Sprintf(format, a...))
}

// call runs one planner call under a timeout, reserving budget before it and
// recording its usage in the cost ledger afterwards (spend caps apply).
func (s *Service) call(ctx context.Context, j *planJob, timeout func() (context.Context, context.CancelFunc), fn func(context.Context) (*application.Usage, error)) error {
	var res *application.Reservation
	if s.cfg.Orch != nil {
		r, ex := s.cfg.Orch.ReservePlanner(ctx)
		if ex != nil {
			return errPlannerBudget
		}
		res = r
	}
	cctx, cancel := timeout()
	defer cancel()
	u, err := fn(cctx)
	var usage application.Usage
	if u != nil {
		usage = *u
	}
	if s.cfg.Orch != nil {
		s.cfg.Orch.RecordPlannerUsage(ctx, usage, res) // also releases the reservation
	}
	j.addCall(usage.CostUSD)
	return err
}

// ---- flat planner (one call, the previous behavior) ----

func (s *Service) planFlat(ctx context.Context, j *planJob, goal, locale string, pa []application.PlanAgent, known map[string]bool) (Template, error) {
	var resp application.PlanResponse
	err := s.call(ctx, j, func() (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, flatPlanTimeout) },
		func(c context.Context) (*application.Usage, error) {
			var err error
			resp, err = s.cfg.Runtime.Plan(c, application.PlanRequest{RequestText: goal, Agents: pa, Locale: locale})
			return resp.Usage, err
		})
	if err != nil {
		return Template{}, err
	}
	if len(resp.Tasks) == 0 {
		return Template{}, invalidf("the plan has no tasks")
	}
	keys := map[string]bool{}
	nodes := make([]TemplateNode, 0, len(resp.Tasks))
	for i, t := range resp.Tasks {
		if t.Key == "" {
			t.Key = fmt.Sprintf("t%d", i+1)
		}
		if keys[t.Key] || strings.ContainsAny(t.Key, ":~") {
			return Template{}, invalidf("duplicate or invalid task key %q", t.Key)
		}
		keys[t.Key] = true
		if !known[t.AgentID] {
			t.AgentID = "assistant"
		}
		nodes = append(nodes, TemplateNode{Key: t.Key, Title: t.Title, Description: t.Description, Agent: t.AgentID, Complexity: "M", Deps: t.DependsOn})
	}
	for i := range nodes {
		nodes[i].Deps = slices.DeleteFunc(slices.Clone(nodes[i].Deps), func(d string) bool { return !keys[d] })
	}
	name := truncRunes(goal, 60)
	return Template{ID: "tpl-planned", Key: "planned", Version: 1, Name: name, Params: []TemplateParam{},
		Objectives: []TemplateObjective{{Key: "o1", Title: name, Workflows: []TemplateWorkflow{{Key: "w1", Title: name, Nodes: nodes}}}}}, nil
}

// ---- hierarchical planner ----

func (s *Service) planHierarchical(ctx context.Context, j *planJob, hp application.HierarchicalPlanner, goal, locale string, pa []application.PlanAgent, known map[string]bool) (Template, PlannerInfo, error) {
	lim := s.cfg.Limits
	var pr application.PlanPhasesResponse
	err := s.call(ctx, j, func() (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, lim.PhasesTimeout) },
		func(c context.Context) (*application.Usage, error) {
			var err error
			pr, err = hp.PlanPhases(c, application.PlanPhasesRequest{RequestText: goal, Agents: pa, MaxPhases: lim.MaxPhases, Locale: locale})
			return pr.Usage, err
		})
	if err != nil {
		return Template{}, PlannerInfo{}, err
	}
	phases, err := cleanPhases(pr.Phases, lim.MaxPhases)
	if err != nil {
		return Template{}, PlannerInfo{}, err
	}
	j.progress(0, len(phases))

	brief := make([]application.PhaseBrief, len(phases))
	for i, p := range phases {
		brief[i] = application.PhaseBrief{Key: p.Key, Title: p.Title}
	}
	fallbackAgent := pickFallbackAgent(pa, known)
	// Q1: with an internal auditor in the organization every phase gets ONE audit task at its end.
	auditor, maxTasks := "", lim.MaxTasksPerPhase
	if !lim.NoAuditNodes {
		if auditor = auditorOf(pa); auditor != "" && maxTasks > 2 {
			maxTasks-- // the audit counts against the per-phase cap
		}
	}
	tasks := make([][]application.PhaseTask, len(phases))
	failedPhase := make([]error, len(phases))
	sem := make(chan struct{}, lim.PlannerConcurrency)
	var wg sync.WaitGroup
	var done atomic.Int32
	for i := range phases {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() { j.progress(int(done.Add(1)), len(phases)) }()
			ph := phases[i]
			target := min(taskTargetBySize[ph.Size], lim.MaxTasksPerPhase)
			var resp application.PlanPhaseResponse
			err := s.call(ctx, j, func() (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, lim.PhaseTimeout) },
				func(c context.Context) (*application.Usage, error) {
					var err error
					resp, err = hp.PlanPhase(c, application.PlanPhaseRequest{RequestText: goal, Phase: ph, OtherPhases: brief, Agents: pa,
						TargetTasks: target, MaxTasks: lim.MaxTasksPerPhase, Locale: locale})
					return resp.Usage, err
				})
			if err == nil {
				tasks[i], err = cleanPhaseTasks(resp.Tasks, known, fallbackAgent, maxTasks)
				if err == nil && auditor != "" && len(tasks[i]) >= 2 {
					tasks[i] = withAuditTask(tasks[i], ph, auditor, locale)
				}
			}
			if err != nil {
				failedPhase[i] = err
			}
		}(i)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return Template{}, PlannerInfo{}, err
	}

	info := PlannerInfo{Mode: PlannerHierarchical, Status: PlannerOK, Phases: len(phases)}
	var firstErr error
	for i, e := range failedPhase {
		if e == nil {
			continue
		}
		if firstErr == nil {
			firstErr = e
		}
		info.FailedPhases = append(info.FailedPhases, phases[i].Key)
		// The phase keeps one placeholder task so the DAG stays connected and the
		// user sees (and can edit) what the planner could not expand.
		tasks[i] = []application.PhaseTask{{Key: "t1", Title: phases[i].Title, Description: phases[i].Goal, AgentID: fallbackAgent,
			DependsOn: []string{}, Complexity: phases[i].Size}}
	}
	if len(info.FailedPhases) == len(phases) {
		return Template{}, PlannerInfo{}, firstErr // nothing expanded: let the flat planner try
	}
	if len(info.FailedPhases) > 0 {
		info.Status, info.Code, info.Message = PlannerDegraded, "phases_failed", clip(firstErr.Error(), 200)
	}
	tpl := buildPhaseTemplate(goal, phases, tasks, lim.MaxChildrenPerGroup)
	info.Tasks = countTasks(tpl)
	return tpl, info, nil
}

func pickFallbackAgent(pa []application.PlanAgent, known map[string]bool) string {
	if known["assistant"] {
		return "assistant"
	}
	return pa[0].ID
}

// cleanPhases validates the roadmap: 1..max phases, keys remapped to p1..pn
// (the dependencies follow), unknown/self dependencies dropped, no cycles.
func cleanPhases(in []application.PlanPhase, max int) ([]application.PlanPhase, error) {
	if len(in) == 0 {
		return nil, invalidf("the plan has no phases")
	}
	if len(in) > max {
		in = in[:max]
	}
	newKey := map[string]string{}
	out := make([]application.PlanPhase, 0, len(in))
	for _, p := range in {
		if _, dup := newKey[p.Key]; dup || strings.TrimSpace(p.Title) == "" {
			continue
		}
		k := fmt.Sprintf("p%d", len(out)+1)
		newKey[p.Key] = k
		size := strings.ToUpper(strings.TrimSpace(p.Size))
		if _, ok := taskTargetBySize[size]; !ok {
			size = "M"
		}
		out = append(out, application.PlanPhase{Key: k, Title: clip(p.Title, 120), Goal: clip(p.Goal, 400), Size: size, DependsOn: p.DependsOn})
	}
	if len(out) == 0 {
		return nil, invalidf("the plan has no usable phases")
	}
	for i := range out {
		var deps []string
		for _, d := range out[i].DependsOn {
			if nk, ok := newKey[d]; ok && nk != out[i].Key && !slices.Contains(deps, nk) {
				deps = append(deps, nk)
			}
		}
		out[i].DependsOn = nonNil(deps)
	}
	if hasCycle(len(out), func(i int) []int {
		var r []int
		for _, d := range out[i].DependsOn {
			r = append(r, slices.IndexFunc(out, func(p application.PlanPhase) bool { return p.Key == d }))
		}
		return r
	}) {
		return nil, invalidf("the phases have a dependency cycle")
	}
	return out, nil
}

// cleanPhaseTasks validates the tasks of ONE phase: keys remapped to t1..tn,
// unknown agents replaced, unknown/self dependencies dropped, cycles broken by
// keeping only dependencies on earlier tasks.
func cleanPhaseTasks(in []application.PhaseTask, known map[string]bool, fallbackAgent string, max int) ([]application.PhaseTask, error) {
	if len(in) == 0 {
		return nil, invalidf("the phase has no tasks")
	}
	if len(in) > max {
		in = in[:max]
	}
	newKey := map[string]string{}
	out := make([]application.PhaseTask, 0, len(in))
	for _, t := range in {
		if _, dup := newKey[t.Key]; dup || strings.TrimSpace(t.Title) == "" {
			continue
		}
		k := fmt.Sprintf("t%d", len(out)+1)
		newKey[t.Key] = k
		agent := t.AgentID
		if !known[agent] {
			agent = fallbackAgent
		}
		cx := strings.ToUpper(strings.TrimSpace(t.Complexity))
		if _, ok := priors[cx]; !ok {
			cx = "M"
		}
		out = append(out, application.PhaseTask{Key: k, Title: clip(t.Title, 200), Description: clip(t.Description, 2000), AgentID: agent,
			DependsOn: t.DependsOn, Complexity: cx, Reason: clip(t.Reason, 200), Acceptance: cleanAcceptance(t.Acceptance, plannerAcceptance)})
	}
	if len(out) == 0 {
		return nil, invalidf("the phase has no usable tasks")
	}
	for i := range out {
		var deps []string
		for _, d := range out[i].DependsOn {
			if nk, ok := newKey[d]; ok && nk != out[i].Key && !slices.Contains(deps, nk) {
				deps = append(deps, nk)
			}
		}
		out[i].DependsOn = nonNil(deps)
	}
	idx := map[string]int{}
	for i, t := range out {
		idx[t.Key] = i
	}
	if hasCycle(len(out), func(i int) []int {
		var r []int
		for _, d := range out[i].DependsOn {
			r = append(r, idx[d])
		}
		return r
	}) {
		for i := range out {
			out[i].DependsOn = slices.DeleteFunc(slices.Clone(out[i].DependsOn), func(d string) bool { return idx[d] >= i })
		}
	}
	return out, nil
}

// hasCycle runs an iterative DFS over n nodes (no recursion: plans can be large).
func hasCycle(n int, deps func(i int) []int) bool {
	state := make([]int8, n) // 0 new, 1 on stack, 2 done
	type frame struct {
		node int
		next int
		ds   []int
	}
	for s := 0; s < n; s++ {
		if state[s] != 0 {
			continue
		}
		stack := []frame{{node: s, ds: deps(s)}}
		state[s] = 1
		for len(stack) > 0 {
			f := &stack[len(stack)-1]
			if f.next >= len(f.ds) {
				state[f.node] = 2
				stack = stack[:len(stack)-1]
				continue
			}
			d := f.ds[f.next]
			f.next++
			switch {
			case d < 0 || d >= n:
			case state[d] == 1:
				return true
			case state[d] == 0:
				state[d] = 1
				stack = append(stack, frame{node: d, ds: deps(d)})
			}
		}
	}
	return false
}

// buildPhaseTemplate maps phases and their tasks onto objectives and
// workflows. Node keys are "<phase>_<task>" (unique across the project). The
// tasks of a phase that depend on nothing inside it wait for the tasks of the
// phases it depends on that nothing else in their phase needs (their exits).
func buildPhaseTemplate(goal string, phases []application.PlanPhase, tasks [][]application.PhaseTask, maxChildren int) Template {
	exits := map[string][]string{} // phase key -> node keys of its exit tasks
	for i, p := range phases {
		needed := map[string]bool{}
		for _, t := range tasks[i] {
			for _, d := range t.DependsOn {
				needed[d] = true
			}
		}
		for _, t := range tasks[i] {
			if !needed[t.Key] {
				exits[p.Key] = append(exits[p.Key], p.Key+"_"+t.Key)
			}
		}
	}
	name := truncRunes(strings.TrimSpace(goal), 60)
	tpl := Template{ID: "tpl-planned", Key: "planned", Version: 1, Name: name, Params: []TemplateParam{}}
	for i, p := range phases {
		var upstream []string
		for _, d := range p.DependsOn {
			upstream = append(upstream, exits[d]...)
		}
		nodes := make([]TemplateNode, 0, len(tasks[i]))
		for _, t := range tasks[i] {
			deps := make([]string, 0, len(t.DependsOn))
			for _, d := range t.DependsOn {
				deps = append(deps, p.Key+"_"+d)
			}
			if len(t.DependsOn) == 0 {
				deps = append(deps, upstream...)
			}
			nodes = append(nodes, TemplateNode{Key: p.Key + "_" + t.Key, Title: t.Title, Description: t.Description, Agent: t.AgentID,
				Complexity: t.Complexity, Deps: deps, Acceptance: t.Acceptance})
		}
		obj := TemplateObjective{Key: p.Key, Title: p.Title}
		chunks := (len(nodes) + maxChildren - 1) / maxChildren
		for c := 0; c < chunks; c++ {
			lo, hi := c*maxChildren, min((c+1)*maxChildren, len(nodes))
			title := p.Title
			if chunks > 1 {
				title = fmt.Sprintf("%s (%d/%d)", p.Title, c+1, chunks)
			}
			obj.Workflows = append(obj.Workflows, TemplateWorkflow{Key: fmt.Sprintf("%s_w%d", p.Key, c+1), Title: title, Nodes: nodes[lo:hi]})
		}
		tpl.Objectives = append(tpl.Objectives, obj)
	}
	return tpl
}
