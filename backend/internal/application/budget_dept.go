package application

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Department spend limits (docs/architecture/governance.md). The monthly limit of a department is set in the
// admin console (department_limits) and applies to the sum of what the agents tagged with that department
// spent this calendar month (UTC), exactly like an agent cap does for one agent: Reserve refuses a call that
// would cross it, the call pauses until the limit is raised (Wake), and when an agent also has its own cap
// the strictest of the two blocks first.

type deptCheck struct {
	name  string
	limit float64
	spent float64
}

// deptKey normalizes a department tag for matching (the tag is free text in the console).
func deptKey(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// limitOf finds the limit of a department tag (exact match, then case-insensitive).
func deptLimitOf(limits map[string]float64, dept string) (string, float64) {
	if v, ok := limits[dept]; ok {
		return dept, v
	}
	k := deptKey(dept)
	for n, v := range limits {
		if deptKey(n) == k {
			return n, v
		}
	}
	return dept, 0
}

func (b *Budget) deptLimits(ctx context.Context, org string) map[string]float64 {
	if b.DepartmentLimitsFor == nil {
		return nil
	}
	return b.DepartmentLimitsFor(ctx, org)
}

// agentsOfDept lists the ids of the agents tagged with dept.
func agentsOfDept(agents []domain.Agent, dept string) map[string]bool {
	k := deptKey(dept)
	out := map[string]bool{}
	for _, a := range agents {
		if a.Department != nil && deptKey(*a.Department) == k {
			out[a.ID] = true
		}
	}
	return out
}

func deptSpend(usage []domain.UsageEntry, members map[string]bool, since time.Time) float64 {
	var s float64
	for _, u := range usage {
		if members[u.AgentID] && !u.TS.Before(since) {
			s += u.CostUSD
		}
	}
	return s
}

// departmentCheck resolves the department limit that applies to agentID: ok=false when the agent has no
// department or its department has no limit. Called with b.mu held (Reserve).
func (b *Budget) departmentCheck(ctx context.Context, org, agentID string) (deptCheck, bool, error) {
	limits := b.deptLimits(ctx, org)
	if len(limits) == 0 {
		return deptCheck{}, false, nil
	}
	a, err := b.store.GetAgent(ctx, org, agentID)
	if err != nil || a.Department == nil || strings.TrimSpace(*a.Department) == "" {
		return deptCheck{}, false, nil // unknown agent (the assistant) or no department: no department limit
	}
	name, limit := deptLimitOf(limits, *a.Department)
	if limit <= 0 {
		return deptCheck{}, false, nil
	}
	agents, err := b.store.ListAgents(ctx, org)
	if err != nil {
		return deptCheck{}, false, err
	}
	usage, err := b.store.ListUsage(ctx, org)
	if err != nil {
		return deptCheck{}, false, err
	}
	return deptCheck{name: name, limit: limit, spent: deptSpend(usage, agentsOfDept(agents, name), PeriodStart(time.Now()))}, true, nil
}

// Wake makes every call paused on a cap check again. The admin console calls it after a department limit
// changes (the limits live outside the budget service).
func (b *Budget) Wake() {
	b.mu.Lock()
	close(b.wake)
	b.wake = make(chan struct{})
	b.mu.Unlock()
}

// DepartmentBudget is the month state of one department, as /budget reports it.
type DepartmentBudget struct {
	Department  string  `json:"department"`
	LimitUSD    float64 `json:"limit_usd"` // 0 = no limit
	SpentUSD    float64 `json:"spent_usd"`
	ReservedUSD float64 `json:"reserved_usd"`
	UsedPct     float64 `json:"used_pct"`
	Over        bool    `json:"over"`
	Agents      int     `json:"agents"`
	Paused      bool    `json:"paused"`
}

// departmentStatus fills the department section of the budget status. b.mu is held by the caller.
func (b *Budget) departmentStatus(ctx context.Context, org string, agents []domain.Agent, usage []domain.UsageEntry, since time.Time) []DepartmentBudget {
	limits := b.deptLimits(ctx, org)
	names := map[string]string{} // key -> display name
	for n := range limits {
		names[deptKey(n)] = n
	}
	for _, a := range agents {
		if a.Department != nil && strings.TrimSpace(*a.Department) != "" {
			if _, ok := names[deptKey(*a.Department)]; !ok {
				names[deptKey(*a.Department)] = *a.Department
			}
		}
	}
	out := make([]DepartmentBudget, 0, len(names))
	for _, n := range names {
		members := agentsOfDept(agents, n)
		_, limit := deptLimitOf(limits, n)
		d := DepartmentBudget{Department: n, LimitUSD: limit, Agents: len(members), SpentUSD: deptSpend(usage, members, since),
			ReservedUSD: b.reserved[bkey(org, domain.ScopeDepartment, n)]}
		_, d.Paused = b.pauses[bkey(org, domain.ScopeDepartment, n)]
		if limit > 0 {
			d.UsedPct = math.Round(d.SpentUSD/limit*1000) / 10
			d.Over = d.SpentUSD >= limit
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Department < out[j].Department })
	return out
}
