package application

import (
	"context"
)

// Outcome is the result of executing one node.
type Outcome int

const (
	OutcomeDone Outcome = iota
	OutcomeFailed
	OutcomeBlocked
	// OutcomeSkipped is assigned to nodes never executed because a
	// dependency did not finish successfully (or the context was cancelled).
	OutcomeSkipped
)

// Node is a unit of work with dependencies.
type Node struct {
	ID        string
	DependsOn []string
}

// Scheduler runs nodes in parallel respecting dependencies and a concurrency limit.
type Scheduler struct {
	MaxParallel int
}

type result struct {
	id  string
	out Outcome
}

// Run executes nodes whose dependencies are all Done. Nodes whose dependencies
// ended Failed/Blocked/Skipped (or are unknown/cyclic) are not executed: skip
// is invoked for them and they get OutcomeSkipped. Run returns when every node
// has an outcome.
func (s Scheduler) Run(ctx context.Context, nodes []Node, exec func(ctx context.Context, id string) Outcome, skip func(id string)) map[string]Outcome {
	limit := s.MaxParallel
	if limit < 1 {
		limit = 1
	}
	outcomes := make(map[string]Outcome, len(nodes))
	started := make(map[string]bool, len(nodes))
	known := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		known[n.ID] = true
	}
	results := make(chan result)
	running := 0

	markSkipped := func(id string) {
		outcomes[id] = OutcomeSkipped
		if skip != nil {
			skip(id)
		}
	}

	for {
		// Resolve everything that can be decided without running: skips cascade.
		for changed := true; changed; {
			changed = false
			for _, n := range nodes {
				if _, decided := outcomes[n.ID]; decided || started[n.ID] {
					continue
				}
				if ctx.Err() != nil || hasDeadDep(n, outcomes, known) {
					markSkipped(n.ID)
					changed = true
				}
			}
		}
		// Launch ready nodes up to the limit.
		for _, n := range nodes {
			if running >= limit {
				break
			}
			if _, decided := outcomes[n.ID]; decided || started[n.ID] || !depsDone(n, outcomes) {
				continue
			}
			started[n.ID] = true
			running++
			go func(id string) { results <- result{id, exec(ctx, id)} }(n.ID)
		}
		if running == 0 {
			// Nothing running and nothing launchable: whatever is left is stuck (cycle).
			for _, n := range nodes {
				if _, decided := outcomes[n.ID]; !decided {
					markSkipped(n.ID)
				}
			}
			return outcomes
		}
		r := <-results
		running--
		outcomes[r.id] = r.out
	}
}

func depsDone(n Node, outcomes map[string]Outcome) bool {
	for _, d := range n.DependsOn {
		if o, ok := outcomes[d]; !ok || o != OutcomeDone {
			return false
		}
	}
	return true
}

func hasDeadDep(n Node, outcomes map[string]Outcome, known map[string]bool) bool {
	for _, d := range n.DependsOn {
		if !known[d] {
			return true
		}
		if o, ok := outcomes[d]; ok && o != OutcomeDone {
			return true
		}
	}
	return false
}
