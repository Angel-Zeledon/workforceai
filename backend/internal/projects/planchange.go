package projects

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Mid-flight replanning (Q2, docs/plans/large-workflows.md "Q2 replanning").
//
// A launched project's plan used to be static. A PlanChange is a proposal to
// change it: add tasks, replace a failed or pending node by new nodes (the
// dependents move over), remove or update a pending node. Agents (their
// suggested_tasks, the replanner of the runtime) can only PROPOSE; a human
// approves through the approvals mechanism (kind plan_change). A change by a
// human from the UI is recorded the same way and auto-approved by that human.
// Approved changes are validated again against the live DAG and applied to the
// running project: the record, the tasks and the running scheduler.

// Plan change statuses, proposers, sources and operations.
const (
	ChangePending  = "pending"
	ChangeApproved = "approved"
	ChangeRejected = "rejected"
	ChangeApplied  = "applied"

	ProposerAgent = "agent"
	ProposerHuman = "human"

	SourceSuggested = "suggested_tasks"
	SourceReplan    = "replan"
	SourceManual    = "manual"

	OpAddTask       = "add_task"
	OpReplaceTask   = "replace_task"
	OpRemovePending = "remove_pending_task"
	OpUpdatePending = "update_pending_task"

	// ActionPlanChange is the kind of the approval a proposal raises.
	ActionPlanChange = "plan_change"

	maxChangeOps         = 20
	maxSuggestedPerNode  = 5
	maxChangeReason      = 500
	maxProposedDescLen   = 2000
	maxProposedTitleLen  = 300
	maxChangeKeyLen      = 40
	changeTaskIDPrefix   = "planchange:"
	approvalPlanChangeOf = "plan_change_id"
)

// ProposedTask is a task a change adds. DependsOn holds ids of existing nodes
// or keys of other tasks of the same change. In a replace_task the tasks that
// depend on nothing of the change inherit the dependencies of the replaced node.
type ProposedTask struct {
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	AgentID     string   `json:"agent_id,omitempty"`
	Complexity  string   `json:"complexity,omitempty"` // S|M|L|XL (default M)
	DependsOn   []string `json:"depends_on,omitempty"`
	// ParentID is the workflow group the task belongs to (default: the group of its first dependency).
	ParentID string `json:"parent_id,omitempty"`
}

// PlanChangeFields are the editable fields of update_pending_task.
type PlanChangeFields struct {
	Title       *string `json:"title,omitempty"`
	AgentID     *string `json:"agent_id,omitempty"`
	Description *string `json:"description,omitempty"`
}

// PlanChangeOp is one operation of a plan change.
type PlanChangeOp struct {
	Op     string           `json:"op"`
	NodeID string           `json:"node_id,omitempty"` // replace_task, remove_pending_task, update_pending_task
	Tasks  []ProposedTask   `json:"tasks,omitempty"`   // add_task, replace_task
	Fields PlanChangeFields `json:"fields,omitempty"`  // update_pending_task
}

// ChangeImpact is the diff summary of a change against the live plan.
type ChangeImpact struct {
	Added    int `json:"added"`
	Replaced int `json:"replaced"`
	Removed  int `json:"removed"`
	Updated  int `json:"updated"`
	// EstCostDeltaUSD / EstSecondsDelta: change of the remaining estimate.
	EstCostDeltaUSD float64 `json:"est_cost_delta_usd"`
	EstSecondsDelta float64 `json:"est_seconds_delta"`
	// ProjectedCostUSD is spent + remaining estimate after the change.
	ProjectedCostUSD float64 `json:"projected_cost_usd"`
	BudgetUSD        float64 `json:"budget_usd"`
	ExceedsBudget    bool    `json:"exceeds_budget"`
	NodesAfter       int     `json:"nodes_after"`
}

// PlanChange is a proposal (and, once decided, the record) of a change of the
// plan of a launched project.
type PlanChange struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	OrgID     string `json:"org_id,omitempty"`
	// ProposedBy: "agent" (suggested_tasks, the runtime's replanner) or "human".
	ProposedBy string `json:"proposed_by"`
	// Actor is the proposing principal: an agent id, or the person.
	Actor        string         `json:"actor"`
	Source       string         `json:"source"`
	SourceNodeID string         `json:"source_node_id,omitempty"`
	Reason       string         `json:"reason"`
	Ops          []PlanChangeOp `json:"ops"`
	Status       string         `json:"status"`
	Impact       ChangeImpact   `json:"impact"`
	Risk         string         `json:"risk"`
	// ApprovalID is the approval a human decides (absent for a human's own change).
	ApprovalID        string     `json:"approval_id,omitempty"`
	RequiredApprovals int        `json:"required_approvals,omitempty"`
	Approvals         int        `json:"approvals,omitempty"` // received so far (double approval)
	DecidedBy         string     `json:"decided_by,omitempty"`
	DecidedAt         *time.Time `json:"decided_at,omitempty"`
	AppliedAt         *time.Time `json:"applied_at,omitempty"`
	// Error says why an approved change could not be applied (it is then rejected).
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (c PlanChange) open() bool { return c.Status == ChangePending }

// NewPlanChange is the body a human posts to change the plan directly.
type NewPlanChange struct {
	Reason string         `json:"reason"`
	Ops    []PlanChangeOp `json:"ops"`
}

// ChangeStore persists plan changes. The Postgres ProjectStore implements it
// (migration 440); MemStore serves demo mode and tests. Every call is scoped by org.
type ChangeStore interface {
	PutChange(ctx context.Context, c PlanChange) error
	GetChange(ctx context.Context, org, project, id string) (PlanChange, error)
	// ListChanges returns the changes of a project, oldest first.
	ListChanges(ctx context.Context, org, project string) ([]PlanChange, error)
}

// MemChangeStore is the in-memory ChangeStore.
type MemChangeStore struct {
	mu   sync.Mutex
	rows map[string]PlanChange // org|project|id
}

func NewMemChangeStore() *MemChangeStore { return &MemChangeStore{rows: map[string]PlanChange{}} }

func cloneChange(c PlanChange) PlanChange {
	out := c
	out.Ops = make([]PlanChangeOp, len(c.Ops))
	for i, op := range c.Ops {
		op.Tasks = slices.Clone(op.Tasks)
		for j := range op.Tasks {
			op.Tasks[j].DependsOn = slices.Clone(op.Tasks[j].DependsOn)
		}
		out.Ops[i] = op
	}
	return out
}

func (m *MemChangeStore) PutChange(_ context.Context, c PlanChange) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[c.OrgID+"|"+c.ProjectID+"|"+c.ID] = cloneChange(c)
	return nil
}

func (m *MemChangeStore) GetChange(_ context.Context, org, project, id string) (PlanChange, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.rows[org+"|"+project+"|"+id]
	if !ok {
		return PlanChange{}, domain.ErrNotFound
	}
	return cloneChange(c), nil
}

func (m *MemChangeStore) ListChanges(_ context.Context, org, project string) ([]PlanChange, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []PlanChange{}
	for k, c := range m.rows {
		if strings.HasPrefix(k, org+"|"+project+"|") {
			out = append(out, cloneChange(c))
		}
	}
	slices.SortFunc(out, func(a, b PlanChange) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

var _ ChangeStore = (*MemChangeStore)(nil)

// ---- validation against the live DAG ----

var changeKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)

// changePlan is the result of validating a change against a record: the plan
// after the change plus its impact.
type changePlan struct {
	nodes  []NodeDef
	impact ChangeImpact
}

func invalidChange(format string, a ...any) error {
	return fmt.Errorf("%w: invalid_plan_change: %s", domain.ErrInvalid, fmt.Sprintf(format, a...))
}

func conflictChange(format string, a ...any) error {
	return fmt.Errorf("%w: plan_change_conflict: %s", domain.ErrConflict, fmt.Sprintf(format, a...))
}

func changeNodeID(projectID, changeID, key string) string {
	return projectID + ":" + changeID + "-" + key
}

// editableState: the node has not started (its task may still be edited).
func editableState(st string) bool {
	switch st {
	case StatePending, StateReady, StatePaused, StateBlocked, "waiting":
		return true
	}
	return false
}

func cleanText(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s)
	return truncRunes(strings.TrimSpace(s), n)
}

// planChange applies ch to a copy of rec.Nodes and validates the result: no
// cycles, no edits to running or finished nodes, existing agents, the size
// limits. states is the derived state of every node (Detail). It never
// mutates rec. spent is what the project has spent so far.
func (s *Service) planChange(rec Record, states map[string]string, agentIDs []string, spent float64, ch PlanChange) (*changePlan, error) {
	lim := s.cfg.Limits
	if len(ch.Ops) == 0 || len(ch.Ops) > maxChangeOps {
		return nil, invalidChange("a change has 1 to %d operations", maxChangeOps)
	}
	nodes := slices.Clone(rec.Nodes)
	idx := map[string]int{}
	for i, n := range nodes {
		idx[n.ID] = i
	}
	// Local keys -> node ids.
	keyID := map[string]string{}
	total := 0
	for _, op := range ch.Ops {
		total += len(op.Tasks)
		for _, t := range op.Tasks {
			if !changeKeyRe.MatchString(t.Key) {
				return nil, invalidChange("task keys are 1-%d characters of letters, digits, _ and -", maxChangeKeyLen)
			}
			if _, dup := keyID[t.Key]; dup {
				return nil, invalidChange("duplicate task key %q", t.Key)
			}
			keyID[t.Key] = changeNodeID(rec.ID, ch.ID, t.Key)
		}
	}
	if total > lim.MaxTasksPerChange {
		return nil, limitError(domain.ErrInvalid, LimitTasksPerChange, lim.MaxTasksPerChange, fmt.Sprintf("the change adds %d tasks", total))
	}
	live := func(id string) (NodeDef, bool) {
		i, ok := idx[id]
		if !ok {
			return NodeDef{}, false
		}
		return nodes[i], true
	}
	leafOK := func(n NodeDef) bool { return !n.isGroup() && n.Superseded == "" }
	touched := map[string]bool{}
	touch := func(id string) error {
		if touched[id] {
			return invalidChange("node %s is changed by more than one operation", id)
		}
		touched[id] = true
		return nil
	}
	var impact ChangeImpact
	var removedEst, removedSecs []float64
	_ = removedSecs
	var addedNew []NodeDef
	childrenOf := func(id string) []NodeDef {
		var out []NodeDef
		for _, n := range nodes {
			if n.ParentID != nil && *n.ParentID == id && n.Kind == KindSubtask && n.Superseded == "" {
				out = append(out, n)
			}
		}
		return out
	}
	supersede := func(n NodeDef, how string) {
		i := idx[n.ID]
		nodes[i].Superseded, nodes[i].SupersededBy = how, ch.ID
		if !(states[n.ID] == StateFailed || states[n.ID] == StateDone) {
			removedEst = append(removedEst, n.EstCostUSD*retryFactor(planMaxAttempts))
		}
	}
	// Dependents of a node move to the given dependencies.
	repoint := func(from string, to []string) {
		for i := range nodes {
			if nodes[i].isGroup() || nodes[i].Superseded != "" || !slices.Contains(nodes[i].DependsOn, from) {
				continue
			}
			nd := make([]string, 0, len(nodes[i].DependsOn)+len(to))
			for _, d := range nodes[i].DependsOn {
				if d == from {
					for _, t := range to {
						if !slices.Contains(nd, t) {
							nd = append(nd, t)
						}
					}
				} else if !slices.Contains(nd, d) {
					nd = append(nd, d)
				}
			}
			nodes[i].DependsOn = nd
		}
	}
	// resolveDeps maps the dependency references of a proposed task.
	resolveDeps := func(refs []string) ([]string, error) {
		out := make([]string, 0, len(refs))
		for _, r := range refs {
			if id, ok := keyID[r]; ok {
				out = append(out, id)
				continue
			}
			n, ok := live(r)
			switch {
			case !ok || !leafOK(n):
				return nil, invalidChange("unknown dependency %q", r)
			case touched[r]:
				return nil, invalidChange("a task cannot depend on %q: another operation of the change replaces or removes it", r)
			case states[r] == StateFailed || states[r] == StateCancelled || (states[r] == StateBlocked && n.Superseded == ""):
				return nil, conflictChange("dependency %q is %s: replace or skip it first", n.Title, states[r])
			}
			out = append(out, r)
		}
		return out, nil
	}
	newDef := func(t ProposedTask, deps []string, parent *NodeDef) (NodeDef, error) {
		title := cleanText(t.Title, maxProposedTitleLen)
		if title == "" {
			return NodeDef{}, invalidChange("task %q has no title", t.Key)
		}
		agent := strings.TrimSpace(t.AgentID)
		if agent == "" {
			return NodeDef{}, invalidChange("task %q has no agent", t.Key)
		}
		if len(agentIDs) > 0 && !slices.Contains(agentIDs, agent) {
			return NodeDef{}, invalidChange("task %q: unknown agent %q", t.Key, agent)
		}
		cx := normComplexity(strings.ToUpper(strings.TrimSpace(t.Complexity)))
		if t.Complexity == "" {
			cx = "M"
		}
		id := keyID[t.Key]
		d := NodeDef{ID: id, Key: t.Key, ObjectiveID: parent.ObjectiveID, ParentID: ptrStr(parent.ID), Kind: KindTask, Title: title,
			Description: cleanText(t.Description, maxProposedDescLen), AgentID: ptrStr(agent), DependsOn: deps, DelegationDepth: 1,
			DelegationChain: []string{agent}, Complexity: cx, PlanChangeID: ch.ID}
		d.EstSeconds = priorSeconds(cx)
		d.EstCostUSD = priorCost(cx, len(deps))
		return d, nil
	}
	pickParent := func(t ProposedTask, deps []string) (*NodeDef, error) {
		if t.ParentID != "" {
			p, ok := live(t.ParentID)
			if !ok || !p.isGroup() {
				return nil, invalidChange("parent %q is not a workflow group", t.ParentID)
			}
			return &p, nil
		}
		for _, d := range deps {
			if n, ok := live(d); ok && n.ParentID != nil {
				if p, ok := live(*n.ParentID); ok && p.isGroup() {
					return &p, nil
				}
			}
		}
		for _, n := range nodes {
			if n.isGroup() {
				p := n
				return &p, nil
			}
		}
		return nil, invalidChange("the project has no workflow group to hold the new tasks")
	}
	appendNew := func(d NodeDef) {
		nodes = append(nodes, d)
		idx[d.ID] = len(nodes) - 1
		addedNew = append(addedNew, d)
	}
	wbs := func(parent *NodeDef) string {
		n := 1
		for _, x := range nodes {
			if x.ParentID != nil && *x.ParentID == parent.ID {
				n++
			}
		}
		return parent.WBSPath + "." + pad(n)
	}

	for _, op := range ch.Ops {
		switch op.Op {
		case OpAddTask:
			if len(op.Tasks) == 0 {
				return nil, invalidChange("add_task needs tasks")
			}
			for _, t := range op.Tasks {
				deps, err := resolveDeps(t.DependsOn)
				if err != nil {
					return nil, err
				}
				parent, err := pickParent(t, deps)
				if err != nil {
					return nil, err
				}
				d, err := newDef(t, deps, parent)
				if err != nil {
					return nil, err
				}
				d.WBSPath = wbs(parent)
				appendNew(d)
			}
		case OpReplaceTask:
			target, ok := live(op.NodeID)
			if !ok || !leafOK(target) || (target.Kind != KindTask && target.Kind != KindSubtask) {
				return nil, invalidChange("node %q cannot be replaced", op.NodeID)
			}
			if st := states[op.NodeID]; !(st == StateFailed || editableState(st)) {
				return nil, conflictChange("node %q is %s: only a failed or not-yet-started node can be replaced", target.Title, st)
			}
			if len(op.Tasks) == 0 {
				return nil, invalidChange("replace_task needs the replacement tasks")
			}
			if err := touch(op.NodeID); err != nil {
				return nil, err
			}
			for _, c := range childrenOf(target.ID) {
				if st := states[c.ID]; !(st == StateFailed || editableState(st)) {
					return nil, conflictChange("the subtask %q of %q is %s", c.Title, target.Title, st)
				}
			}
			// The replacement tasks: the roots inherit the dependencies of the replaced node.
			local := map[string]bool{}
			for _, t := range op.Tasks {
				local[t.Key] = true
			}
			dependedOn := map[string]bool{}
			var defs []NodeDef
			parentGroup := target.ParentID
			for _, t := range op.Tasks {
				deps, err := resolveDeps(t.DependsOn)
				if err != nil {
					return nil, err
				}
				isRoot := true
				for _, r := range t.DependsOn {
					if local[r] {
						isRoot = false
						dependedOn[r] = true
					}
				}
				if isRoot {
					for _, d := range target.DependsOn {
						if !slices.Contains(deps, d) {
							deps = append(deps, d)
						}
					}
				}
				var parent *NodeDef
				if parentGroup != nil {
					if p, ok := live(*parentGroup); ok {
						parent = &p
					}
				}
				if parent == nil || !parent.isGroup() {
					var err error
					if parent, err = pickParent(t, deps); err != nil {
						return nil, err
					}
				}
				d, err := newDef(t, deps, parent)
				if err != nil {
					return nil, err
				}
				d.WBSPath = wbs(parent)
				defs = append(defs, d)
			}
			var terminals []string
			for _, d := range defs {
				if !dependedOn[d.Key] {
					terminals = append(terminals, d.ID)
				}
			}
			for _, d := range defs {
				appendNew(d)
			}
			supersede(target, "replaced")
			for _, c := range childrenOf(target.ID) {
				supersede(c, "replaced")
			}
			repoint(target.ID, terminals)
			impact.Replaced++
		case OpRemovePending:
			target, ok := live(op.NodeID)
			if !ok || !leafOK(target) || (target.Kind != KindTask && target.Kind != KindSubtask) {
				return nil, invalidChange("node %q cannot be removed", op.NodeID)
			}
			if st := states[op.NodeID]; !editableState(st) {
				return nil, conflictChange("node %q is %s: only a not-yet-started node can be removed", target.Title, st)
			}
			if err := touch(op.NodeID); err != nil {
				return nil, err
			}
			for _, c := range childrenOf(target.ID) {
				if !editableState(states[c.ID]) {
					return nil, conflictChange("the subtask %q of %q is %s", c.Title, target.Title, states[c.ID])
				}
			}
			repoint(target.ID, target.DependsOn)
			supersede(target, "removed")
			for _, c := range childrenOf(target.ID) {
				supersede(c, "removed")
			}
			impact.Removed++
		case OpUpdatePending:
			target, ok := live(op.NodeID)
			if !ok || !leafOK(target) || (target.Kind != KindTask && target.Kind != KindSubtask) {
				return nil, invalidChange("node %q cannot be updated", op.NodeID)
			}
			if st := states[op.NodeID]; !editableState(st) {
				return nil, conflictChange("node %q is %s: only a not-yet-started node can be updated", target.Title, st)
			}
			if err := touch(op.NodeID); err != nil {
				return nil, err
			}
			f := op.Fields
			if f.Title == nil && f.AgentID == nil && f.Description == nil {
				return nil, invalidChange("update_pending_task needs at least one field")
			}
			i := idx[target.ID]
			if f.Title != nil {
				t := cleanText(*f.Title, maxProposedTitleLen)
				if t == "" {
					return nil, invalidChange("the title cannot be empty")
				}
				nodes[i].Title, nodes[i].TitleKey, nodes[i].TitleParams = t, "", nil
			}
			if f.Description != nil {
				nodes[i].Description = cleanText(*f.Description, maxProposedDescLen)
			}
			if f.AgentID != nil {
				a := strings.TrimSpace(*f.AgentID)
				if a == "" || target.human() || (len(agentIDs) > 0 && !slices.Contains(agentIDs, a)) {
					return nil, invalidChange("unknown agent %q", a)
				}
				nodes[i].AgentID = ptrStr(a)
				nodes[i].DelegationChain = []string{a}
			}
			impact.Updated++
		default:
			return nil, invalidChange("unsupported operation %q", op.Op)
		}
	}
	// The plan after the change: cycles, size limits.
	if !computeLevels(nodes) {
		return nil, invalidChange("cycle: the change creates a dependency cycle")
	}
	if len(nodes) > lim.MaxNodesPerProject {
		return nil, limitError(domain.ErrInvalid, LimitMaxNodes, lim.MaxNodesPerProject, fmt.Sprintf("the plan would have %d nodes", len(nodes)))
	}
	if err := lim.checkSize(nodes); err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, n := range nodes {
		ids[n.ID] = true
	}
	for _, n := range nodes {
		if n.Superseded != "" || n.isGroup() {
			continue
		}
		for _, d := range n.DependsOn {
			if !ids[d] {
				return nil, invalidChange("node %q has an unknown dependency %q", n.Title, d)
			}
		}
	}
	impact.Added = len(addedNew)
	impact.NodesAfter = len(activeLeaves(nodes))
	// Cost and time against the remaining plan.
	var added, removed float64
	for _, d := range addedNew {
		added += d.EstCostUSD * retryFactor(planMaxAttempts)
	}
	for _, v := range removedEst {
		removed += v
	}
	impact.EstCostDeltaUSD = round6(added - removed)
	before := remainingPlan(rec.Nodes, states)
	after := remainingPlan(nodes, states)
	impact.EstSecondsDelta = round6(simulate(after).Makespan - simulate(before).Makespan)
	remaining := 0.0
	for _, n := range leaves(nodes) {
		if n.Superseded == "" && states[n.ID] != StateDone {
			remaining += n.EstCostUSD * retryFactor(planMaxAttempts)
		}
	}
	impact.BudgetUSD = rec.BudgetUSD
	impact.ProjectedCostUSD = round6(spent + remaining)
	impact.ExceedsBudget = rec.BudgetUSD > 0 && impact.ProjectedCostUSD > rec.BudgetUSD
	return &changePlan{nodes: nodes, impact: impact}, nil
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

// remainingPlan is the simulation input of the unfinished plan.
func remainingPlan(nodes []NodeDef, states map[string]string) []simNode {
	var sn []simNode
	for _, n := range simNodes(activeLeaves(nodes)) {
		st := states[n.ID]
		n.Done = st == StateDone
		n.Running = st == StateRunning || st == StateAwaitingApproval
		sn = append(sn, n)
	}
	return sn
}

// activeLeaves are the leaf nodes that are part of the plan (not replaced or removed).
func activeLeaves(nodes []NodeDef) []NodeDef {
	out := make([]NodeDef, 0, len(nodes))
	for _, n := range nodes {
		if !n.isGroup() && n.Superseded == "" {
			out = append(out, n)
		}
	}
	return out
}

// riskOf grades a change by its budget impact: low (small), medium (it
// removes work or adds more than 5% of the budget), high (more than 20%, or
// the project would exceed its budget).
func riskOf(im ChangeImpact) string {
	pct := 0.0
	if im.BudgetUSD > 0 {
		pct = im.EstCostDeltaUSD / im.BudgetUSD
	}
	switch {
	case im.ExceedsBudget || pct > 0.2:
		return "high"
	case pct > 0.05 || im.Removed+im.Replaced > 0:
		return "medium"
	}
	return "low"
}

// normTitle is the key to deduplicate suggested tasks.
func normTitle(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}
