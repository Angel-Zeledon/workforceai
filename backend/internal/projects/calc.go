package projects

import (
	"math"
	"slices"
	"strings"
	"time"
)

// Pure planning math, a Go port of frontend/src/lib/projects/calc.ts: DAG
// levels, cost/time priors, list-scheduling simulation, critical path, health.

const (
	// agentCapacity is how many tasks one agent is assumed to run at once when
	// the plan is simulated (the schedule shown in the timeline).
	agentCapacity = 2
	priceIn       = 0.3 / 1e6 // deepseek-chat peak rate per token
	priceOut      = 1.2 / 1e6
	humanWait     = 10.0 // seconds assumed for a human step
	estimateModel = "deepseek-chat"
)

type prior struct{ in, out, secs float64 }

var priors = map[string]prior{
	"S":  {1200, 400, 4},
	"M":  {2500, 900, 7},
	"L":  {5000, 1800, 10},
	"XL": {9000, 3000, 14},
}

func priorOf(c string) prior {
	if p, ok := priors[c]; ok {
		return p
	}
	return priors["S"]
}

func priorSeconds(c string) float64 { return priorOf(c).secs }

// priorCost estimates one node, with the dependency-context overhead.
func priorCost(c string, deps int) float64 {
	p := priorOf(c)
	ctx := math.Min(8000, 800*float64(deps))
	return (p.in+ctx)*priceIn + p.out*priceOut
}

func normComplexity(c string) string {
	if _, ok := priors[c]; ok {
		return c
	}
	return "S"
}

func leaves(nodes []NodeDef) []NodeDef {
	out := make([]NodeDef, 0, len(nodes))
	for _, n := range nodes {
		if !n.isGroup() {
			out = append(out, n)
		}
	}
	return out
}

// computeLevels sets DagLevel (longest dependency chain). It returns false when
// a cycle exists. Dependencies pointing outside the list are ignored.
func computeLevels(nodes []NodeDef) bool {
	idx := make(map[string]int, len(nodes))
	for i, n := range nodes {
		idx[n.ID] = i
	}
	level := make(map[string]int, len(nodes))
	state := make(map[string]int, len(nodes)) // 1 = visiting, 2 = done
	var visit func(id string) bool
	visit = func(id string) bool {
		switch state[id] {
		case 2:
			return true
		case 1:
			return false
		}
		state[id] = 1
		lv := 0
		for _, d := range nodes[idx[id]].DependsOn {
			if _, ok := idx[d]; !ok {
				continue
			}
			if !visit(d) {
				return false
			}
			lv = max(lv, level[d]+1)
		}
		level[id] = lv
		state[id] = 2
		return true
	}
	for _, n := range nodes {
		if !visit(n.ID) {
			return false
		}
	}
	for i := range nodes {
		nodes[i].DagLevel = level[nodes[i].ID]
	}
	return true
}

// topoOrder returns the nodes by (level, wbs path): dependencies first.
func topoOrder(nodes []NodeDef) []NodeDef {
	out := slices.Clone(nodes)
	slices.SortStableFunc(out, func(a, b NodeDef) int {
		if a.DagLevel != b.DagLevel {
			return a.DagLevel - b.DagLevel
		}
		return strings.Compare(a.WBSPath, b.WBSPath)
	})
	return out
}

// ---- simulation ----

type simNode struct {
	ID       string
	Agent    string
	Deps     []string
	Secs     float64
	Human    bool // a person resolves it: no agent slot
	Approval bool
	Done     bool
	Running  bool
	Progress float64 // 0..100
	Level    int
	WBS      string
}

type simResult struct {
	Start, End map[string]float64 // seconds from "now"
	Makespan   float64
}

// simulate packs the nodes with agentCapacity concurrent tasks per agent.
// Finished nodes take no time; running ones finish after their remaining time.
func simulate(nodes []simNode) simResult {
	order := slices.Clone(nodes)
	slices.SortStableFunc(order, func(a, b simNode) int {
		if a.Level != b.Level {
			return a.Level - b.Level
		}
		return strings.Compare(a.WBS, b.WBS)
	})
	known := make(map[string]bool, len(order))
	for _, n := range order {
		known[n.ID] = true
	}
	res := simResult{Start: map[string]float64{}, End: map[string]float64{}}
	slots := map[string][]float64{}
	slotsOf := func(a string) []float64 {
		s, ok := slots[a]
		if !ok {
			s = make([]float64, agentCapacity)
			slots[a] = s
		}
		return s
	}
	minIdx := func(s []float64) int {
		best := 0
		for i, v := range s {
			if v < s[best] {
				best = i
			}
		}
		return best
	}
	for _, n := range order {
		if n.Done {
			res.Start[n.ID], res.End[n.ID] = 0, 0
			continue
		}
		depEnd := 0.0
		for _, d := range n.Deps {
			if known[d] {
				depEnd = math.Max(depEnd, res.End[d])
			}
		}
		var s, e float64
		switch {
		case n.Running:
			s = 0
			e = math.Max(0, n.Secs*(1-n.Progress/100))
			if n.Approval {
				e += humanWait
			}
			if n.Agent != "" {
				sl := slotsOf(n.Agent)
				i := minIdx(sl)
				sl[i] = math.Max(sl[i], e)
			}
		case n.Human:
			s = depEnd
			e = s + math.Max(n.Secs, 0)
			if n.Secs <= 0 {
				e = s + humanWait
			}
		default:
			sl := slotsOf(n.Agent)
			i := minIdx(sl)
			s = math.Max(depEnd, sl[i])
			e = s + n.Secs
			if n.Approval {
				e += humanWait
			}
			sl[i] = s + n.Secs
		}
		res.Start[n.ID], res.End[n.ID] = s, e
		res.Makespan = math.Max(res.Makespan, e)
	}
	return res
}

func simNodes(nodes []NodeDef) []simNode {
	out := make([]simNode, 0, len(nodes))
	for _, n := range leaves(nodes) {
		a := ""
		if n.AgentID != nil {
			a = *n.AgentID
		}
		out = append(out, simNode{ID: n.ID, Agent: a, Deps: n.DependsOn, Secs: n.EstSeconds, Human: n.human(),
			Approval: n.ApprovalAction != "" && n.Kind != KindGate, Level: n.DagLevel, WBS: n.WBSPath})
	}
	return out
}

// ---- estimate ----

func retryFactor(maxAttempts int) float64 { return 1 + 0.08*math.Max(0, float64(maxAttempts-1)) }

const planMaxAttempts = 3

func estimatePlan(nodes []NodeDef, objectives []Objective) *Estimate {
	ls := leaves(nodes)
	var p50 float64
	for _, n := range ls {
		p50 += n.EstCostUSD * retryFactor(planMaxAttempts)
	}
	sim := simulate(simNodes(nodes))
	human := 0
	for _, n := range ls {
		if n.Kind == KindGate || n.ApprovalAction != "" {
			human++
		}
	}
	e := &Estimate{Basis: "priors", Model: estimateModel, Confidence: "low",
		ByObjective: []EstObjective{}, ByAgent: []EstAgent{}, Warnings: []EstWarning{}}
	if len(ls) > 30 {
		e.Confidence = "medium"
	}
	e.Total.P50USD, e.Total.P90USD, e.Total.Calls = p50, p50*1.7, int(math.Round(float64(len(ls))*1.3))
	e.Duration.P50Seconds, e.Duration.P90Seconds, e.Duration.HumanWaitSeconds = sim.Makespan, sim.Makespan*1.4, float64(human*10)
	for _, o := range objectives {
		var v float64
		cnt := 0
		for _, n := range ls {
			if n.ObjectiveID == o.ID {
				v += n.EstCostUSD * retryFactor(planMaxAttempts)
				cnt++
			}
		}
		e.ByObjective = append(e.ByObjective, EstObjective{ID: o.ID, P50USD: v, P90USD: v * 1.7, Nodes: cnt})
	}
	agents := map[string]*EstAgent{}
	var order []string
	for _, n := range ls {
		if n.AgentID == nil {
			continue
		}
		a, ok := agents[*n.AgentID]
		if !ok {
			a = &EstAgent{AgentID: *n.AgentID}
			agents[*n.AgentID] = a
			order = append(order, *n.AgentID)
		}
		a.P50USD += n.EstCostUSD
		a.Calls++
	}
	for _, id := range order {
		e.ByAgent = append(e.ByAgent, *agents[id])
	}
	if len(objectives) > 1 {
		for _, o := range e.ByObjective {
			if p50 > 0 && o.P50USD/p50 > 0.4 {
				e.Warnings = append(e.Warnings, EstWarning{Key: "pv.est.warn.share", Params: map[string]any{"id": o.ID}})
			}
		}
	}
	return e
}

// ---- validation ----

// validatePlan checks a draft: cycles, dangling dependencies, empty titles and
// agents (they must exist in the organization).
func validatePlan(nodes []NodeDef, agentIDs []string) []Issue {
	issues := []Issue{}
	ls := leaves(nodes)
	if len(ls) == 0 {
		issues = append(issues, Issue{Severity: "error", Code: "no_nodes"})
	}
	if !computeLevels(slices.Clone(nodes)) {
		issues = append(issues, Issue{Severity: "error", Code: "cycle"})
	}
	ids := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		ids[n.ID] = true
	}
	for _, n := range ls {
		if strings.TrimSpace(n.Title) == "" {
			issues = append(issues, Issue{Severity: "error", NodeID: n.ID, Code: "empty_title"})
		}
		for _, d := range n.DependsOn {
			if !ids[d] {
				issues = append(issues, Issue{Severity: "error", NodeID: n.ID, Code: "dangling_dependency"})
			}
		}
		if n.human() {
			continue
		}
		switch {
		case n.AgentID == nil || *n.AgentID == "":
			issues = append(issues, Issue{Severity: "error", NodeID: n.ID, Code: "no_agent"})
		case len(agentIDs) > 0 && !slices.Contains(agentIDs, *n.AgentID):
			issues = append(issues, Issue{Severity: "error", NodeID: n.ID, Code: "unknown_agent"})
		}
	}
	return issues
}

func hasErrors(issues []Issue) bool {
	for _, i := range issues {
		if i.Severity == "error" {
			return true
		}
	}
	return false
}

// ---- health ----

func isDone(state string) bool { return state == StateDone }

// rollupState is the derived state of a group: worst problem first, then activity.
func rollupState(children []Node) string {
	ls := make([]Node, 0, len(children))
	for _, c := range children {
		if c.Kind != KindGroup {
			ls = append(ls, c)
		}
	}
	if len(ls) == 0 {
		return StatePending
	}
	any := func(f func(Node) bool) bool {
		for _, n := range ls {
			if f(n) {
				return true
			}
		}
		return false
	}
	all := func(f func(Node) bool) bool { return !any(func(n Node) bool { return !f(n) }) }
	switch {
	case any(func(n Node) bool { return n.State == StateFailed }):
		return StateFailed
	case all(func(n Node) bool { return isDone(n.State) }):
		return StateDone
	case any(func(n Node) bool { return n.State == StateRunning }):
		return StateRunning
	case any(func(n Node) bool { return n.State == StateAwaitingApproval }):
		return StateAwaitingApproval
	case any(func(n Node) bool { return n.State == StateBlocked }):
		return StateBlocked
	case all(func(n Node) bool { return n.State == StateCancelled || isDone(n.State) }):
		return StateCancelled
	case any(func(n Node) bool { return n.State == StateReady || n.State == StatePaused }):
		return StateReady
	}
	return StatePending
}

type counts struct{ done, running, ready, pending, awaiting, failed, total int }

func countStates(nodes []Node) counts {
	var c counts
	for _, n := range nodes {
		if n.Kind == KindGroup {
			continue
		}
		c.total++
		switch {
		case isDone(n.State):
			c.done++
		case n.State == StateRunning:
			c.running++
		case n.State == StateReady:
			c.ready++
		case n.State == StateAwaitingApproval:
			c.awaiting++
		case n.State == StateFailed || n.State == StateBlocked:
			c.failed++
		default:
			c.pending++
		}
	}
	return c
}

// criticalPath returns the longest chain of unfinished nodes and its remaining seconds.
func criticalPath(nodes []Node) ([]string, float64) {
	var ls []Node
	for _, n := range nodes {
		if n.Kind != KindGroup {
			ls = append(ls, n)
		}
	}
	slices.SortStableFunc(ls, func(a, b Node) int {
		if a.DagLevel != b.DagLevel {
			return a.DagLevel - b.DagLevel
		}
		return strings.Compare(a.WBSPath, b.WBSPath)
	})
	best := map[string]float64{}
	prev := map[string]string{}
	w := func(n Node) float64 {
		if isDone(n.State) {
			return 0
		}
		return n.EstSeconds * (1 - float64(n.Progress)/100)
	}
	tail, maxv := "", -1.0
	for _, n := range ls {
		b, p := 0.0, ""
		for _, d := range n.DependsOn {
			if v, ok := best[d]; ok && v > b {
				b, p = v, d
			}
		}
		best[n.ID] = b + w(n)
		prev[n.ID] = p
		if best[n.ID] > maxv && !isDone(n.State) {
			maxv, tail = best[n.ID], n.ID
		}
	}
	byID := make(map[string]Node, len(ls))
	for _, n := range ls {
		byID[n.ID] = n
	}
	var ids []string
	for tail != "" {
		if n, ok := byID[tail]; ok && !isDone(n.State) {
			ids = append([]string{tail}, ids...)
		}
		tail = prev[tail]
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, math.Max(0, maxv)
}

func computeHealth(d Detail, now time.Time) Health {
	p := d.Project
	h := Health{ProjectID: p.ID, ComputedAt: now, Risks: []Risk{}, ByObjective: []HealthByObj{}}
	var ls []Node
	for _, n := range d.Nodes {
		if n.Kind != KindGroup {
			ls = append(ls, n)
		}
	}
	c := countStates(d.Nodes)
	cpIDs, cpSecs := criticalPath(d.Nodes)
	// Remaining schedule: simulate the unfinished nodes from "now".
	sn := make([]simNode, 0, len(ls))
	for _, n := range ls {
		a := ""
		if n.AgentID != nil {
			a = *n.AgentID
		}
		sn = append(sn, simNode{ID: n.ID, Agent: a, Deps: n.DependsOn, Secs: n.EstSeconds,
			Human:    n.Kind == KindGate || n.Kind == KindMilestone || n.Kind == KindWait,
			Approval: n.ApprovalAction != "" && n.Kind != KindGate,
			Done:     isDone(n.State), Running: n.State == StateRunning || n.State == StateAwaitingApproval,
			Progress: float64(n.Progress), Level: n.DagLevel, WBS: n.WBSPath})
	}
	sim := simulate(sn)
	finished := p.Status == StatusDone
	etaP50 := now.Add(time.Duration(sim.Makespan * float64(time.Second)))
	etaP90 := now.Add(time.Duration(sim.Makespan * 1.4 * float64(time.Second)))
	if finished && p.FinishedAt != nil {
		etaP50, etaP90 = *p.FinishedAt, *p.FinishedAt
	}
	h.Schedule.DeadlineAt = p.DeadlineAt
	h.Schedule.EtaP50, h.Schedule.EtaP90 = &etaP50, &etaP90
	if p.DeadlineAt != nil {
		slip := int64(etaP50.Sub(*p.DeadlineAt).Seconds())
		h.Schedule.SlipSecondsP50 = &slip
	}
	var remaining float64
	for _, n := range ls {
		if !isDone(n.State) {
			remaining += n.EstCostUSD * (1 - float64(n.Progress)/100)
		}
	}
	forecast, forecastP90 := p.SpentUSD+remaining, p.SpentUSD+remaining*1.5
	hours := 1.0 / 3600
	if p.StartedAt != nil {
		hours = math.Max(1.0/3600, now.Sub(*p.StartedAt).Hours())
	}
	var pend []Approval
	for _, a := range d.Approvals {
		if a.Status == "pending" {
			pend = append(pend, a)
		}
	}
	oldest := 0.0
	for _, a := range pend {
		oldest = math.Max(oldest, now.Sub(a.CreatedAt).Seconds())
	}
	cpSet := map[string]bool{}
	for _, id := range cpIDs {
		cpSet[id] = true
	}
	blocking := 0
	for _, a := range pend {
		if cpSet[a.NodeID] {
			blocking++
		}
	}
	var retry, failed []string
	queue := map[string][]string{}
	var queueOrder []string
	for _, n := range ls {
		if n.Attempt > 1 {
			retry = append(retry, n.ID)
		}
		if n.State == StateFailed || n.State == StateBlocked {
			failed = append(failed, n.ID)
		}
		if n.State == StateReady && n.AgentID != nil {
			if _, ok := queue[*n.AgentID]; !ok {
				queueOrder = append(queueOrder, *n.AgentID)
			}
			queue[*n.AgentID] = append(queue[*n.AgentID], n.ID)
		}
	}
	if len(retry) > 0 {
		h.Risks = append(h.Risks, Risk{Kind: "retry", NodeIDs: retry, Count: len(retry)})
	}
	if len(failed) > 0 {
		h.Risks = append(h.Risks, Risk{Kind: "failed", NodeIDs: failed, Count: len(failed)})
	}
	for _, a := range queueOrder {
		if len(queue[a]) >= 3 {
			h.Risks = append(h.Risks, Risk{Kind: "agent_saturated", NodeIDs: queue[a], AgentID: a, Count: len(queue[a])})
		}
	}
	over := p.BudgetUSD > 0 && forecast > p.BudgetUSD
	light := "green"
	late50 := p.DeadlineAt != nil && etaP50.After(*p.DeadlineAt)
	late90 := p.DeadlineAt != nil && etaP90.After(*p.DeadlineAt)
	if !finished && (late50 || over || len(failed) > 0) {
		light = "red"
	} else if !finished && (late90 || (p.BudgetUSD > 0 && forecastP90 > 0.95*p.BudgetUSD) || oldest > 60 || len(h.Risks) > 0) {
		light = "amber"
	}
	h.Light = light
	h.Progress.TasksDone, h.Progress.TasksTotal = c.done, c.total
	if c.total > 0 {
		h.Progress.WeightedPct = float64(c.done) / float64(c.total)
	}
	h.CriticalPath.LengthSeconds, h.CriticalPath.NodeIDs, h.CriticalPath.BlockedOnHuman = cpSecs, cpIDs, blocking
	h.Budget.LimitUSD, h.Budget.SpentUSD, h.Budget.BurnUSDPerH = p.BudgetUSD, p.SpentUSD, p.SpentUSD/hours
	h.Budget.ForecastAtCompletionUSD, h.Budget.ForecastP90USD = forecast, forecastP90
	h.Budget.Warning = "none"
	switch {
	case over:
		h.Budget.Warning = "over"
	case p.BudgetUSD > 0 && forecastP90 > 0.95*p.BudgetUSD:
		h.Budget.Warning = "p90_near_limit"
	}
	h.WaitingHuman.Count, h.WaitingHuman.OldestAgeSeconds, h.WaitingHuman.BlockingCritical = len(pend), oldest, blocking
	for _, o := range d.Objectives {
		var own []Node
		done := 0
		var spent float64
		for _, n := range ls {
			if n.ObjectiveID == o.ID {
				own = append(own, n)
				if isDone(n.State) {
					done++
				}
				spent += n.CostUSD
			}
		}
		pct := 0.0
		if len(own) > 0 {
			pct = float64(done) / float64(len(own))
		}
		h.ByObjective = append(h.ByObjective, HealthByObj{ID: o.ID, Pct: pct, State: rollupState(own), SpentUSD: spent})
	}
	return h
}
