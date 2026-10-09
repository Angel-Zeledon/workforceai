package projects

import (
	"fmt"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Limits are the size limits of docs/architecture/workflow-visualization.md
// 2.9 plus the bounds of the hierarchical planner. All are configurable
// (environment, see internal/config) and enforced by the service; a zero value
// takes the default.
type Limits struct {
	// MaxNodesPerProject caps every node of a draft (groups, tasks and subtasks).
	MaxNodesPerProject int
	// MaxChildrenPerGroup caps the direct children of one group (workflow).
	MaxChildrenPerGroup int
	// MaxActiveProjects caps the launched, unfinished projects of an organization.
	MaxActiveProjects int

	// Hierarchical planner.
	MaxPhases          int           // phases asked of the planner (default 12)
	MaxTasksPerPhase   int           // hard cap of the tasks of one phase (default 40)
	PlannerConcurrency int           // phase expansions in flight at once (default 3)
	PhasesTimeout      time.Duration // the phases call (default 45s)
	PhaseTimeout       time.Duration // each phase expansion (default 75s)
	// SyncWait is how long CreateDraft waits for the planner before it answers
	// with a draft that is still being planned (the REST write timeout is 60s).
	SyncWait time.Duration
	// NoAuditNodes disables the planner-inserted audit task per phase (Q1,
	// QUALITY_AUDIT_NODES=false). The zero value keeps them enabled.
	NoAuditNodes bool
}

// Stable error codes of a limit (the frontend maps them to translated texts).
const (
	LimitMaxNodes    = "max_nodes_per_project"
	LimitMaxChildren = "max_children_per_group"
	LimitMaxActive   = "max_active_projects"
)

// DefaultLimits are the documented defaults.
func DefaultLimits() Limits {
	return Limits{MaxNodesPerProject: 20000, MaxChildrenPerGroup: 200, MaxActiveProjects: 20,
		MaxPhases: 12, MaxTasksPerPhase: 40, PlannerConcurrency: 3,
		PhasesTimeout: 45 * time.Second, PhaseTimeout: 75 * time.Second, SyncWait: 40 * time.Second}
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	pick := func(v, def int) int {
		if v > 0 {
			return v
		}
		return def
	}
	pickD := func(v, def time.Duration) time.Duration {
		if v > 0 {
			return v
		}
		return def
	}
	l.MaxNodesPerProject, l.MaxChildrenPerGroup = pick(l.MaxNodesPerProject, d.MaxNodesPerProject), pick(l.MaxChildrenPerGroup, d.MaxChildrenPerGroup)
	l.MaxActiveProjects, l.MaxPhases = pick(l.MaxActiveProjects, d.MaxActiveProjects), pick(l.MaxPhases, d.MaxPhases)
	l.MaxTasksPerPhase, l.PlannerConcurrency = pick(l.MaxTasksPerPhase, d.MaxTasksPerPhase), pick(l.PlannerConcurrency, d.PlannerConcurrency)
	l.PhasesTimeout, l.PhaseTimeout, l.SyncWait = pickD(l.PhasesTimeout, d.PhasesTimeout), pickD(l.PhaseTimeout, d.PhaseTimeout), pickD(l.SyncWait, d.SyncWait)
	return l
}

// limitError is the error of an exceeded limit: "limit_exceeded: <code> (<limit>): <detail>".
func limitError(kind error, code string, limit int, detail string) error {
	return fmt.Errorf("%w: limit_exceeded: %s (limit %d): %s", kind, code, limit, detail)
}

// checkSize enforces MaxNodesPerProject and MaxChildrenPerGroup on a plan.
func (l Limits) checkSize(nodes []NodeDef) error {
	if len(nodes) > l.MaxNodesPerProject {
		return limitError(domain.ErrInvalid, LimitMaxNodes, l.MaxNodesPerProject, fmt.Sprintf("the plan has %d nodes", len(nodes)))
	}
	children := map[string]int{}
	for _, n := range nodes {
		if n.ParentID != nil {
			children[*n.ParentID]++
		}
	}
	for id, c := range children {
		if c > l.MaxChildrenPerGroup {
			return limitError(domain.ErrInvalid, LimitMaxChildren, l.MaxChildrenPerGroup, fmt.Sprintf("node %s has %d children", id, c))
		}
	}
	return nil
}
