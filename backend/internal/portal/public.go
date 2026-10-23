package portal

import (
	"sort"
	"time"

	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/projects"
)

// The public view is an ALLOWLIST. Each type below is the complete list of what may leave the backend
// through a portal link; buildView copies field by field from the internal models and never embeds an
// internal struct. Adding a field here is a security decision: public_test.go fails for any JSON key that
// is not in its allowlist, and for any forbidden value (costs, agents, ids, approvals, the goal/prompt).
//
// Not shown on purpose: costs and budgets, agents and their prompts/thoughts/outputs, approvals and gates,
// waits, the project goal (it is the instruction given to the agents), internal notes and labels, ids of
// projects/nodes/artifacts/tasks, connection data, other projects.

// View is the public page of a link.
type View struct {
	Firm         Firm          `json:"firm"`
	Project      Project       `json:"project"`
	Milestones   []Milestone   `json:"milestones"`
	Deliverables []Deliverable `json:"deliverables"`
	UpdatedAt    *time.Time    `json:"updated_at"`
	GeneratedAt  time.Time     `json:"generated_at"`
	ExpiresAt    *time.Time    `json:"expires_at"`
}

// Firm is the branding shown on the page.
type Firm struct {
	Name        string `json:"name"`
	AccentColor string `json:"accent_color"`
	HasLogo     bool   `json:"has_logo"`
}

// Project is the header of the page.
type Project struct {
	Title       string            `json:"title"`
	TitleKey    string            `json:"title_key,omitempty"`
	TitleParams map[string]string `json:"title_params,omitempty"`
	Status      string            `json:"status"`
	ProgressPct int               `json:"progress_pct"`
	TasksDone   int               `json:"tasks_done"`
	TasksTotal  int               `json:"tasks_total"`
}

// Milestone is an objective of the project with its tasks.
type Milestone struct {
	Title       string            `json:"title"`
	TitleKey    string            `json:"title_key,omitempty"`
	TitleParams map[string]string `json:"title_params,omitempty"`
	Status      string            `json:"status"`
	ProgressPct int               `json:"progress_pct"`
	Tasks       []Task            `json:"tasks"`
}

// Task is one unit of work: a title and a coarse status.
type Task struct {
	Title       string            `json:"title"`
	TitleKey    string            `json:"title_key,omitempty"`
	TitleParams map[string]string `json:"title_params,omitempty"`
	Status      string            `json:"status"`
}

// Deliverable is a completed deliverable the firm chose to share.
type Deliverable struct {
	Title       string    `json:"title"`
	Kind        string    `json:"kind"`
	CompletedAt time.Time `json:"completed_at"`
}

// Public statuses: a deliberately small vocabulary. Internal reasons (approvals pending, failures,
// budget pauses) are not the client's business; the firm explains them in its own words.
const (
	TaskPending    = "pending"
	TaskInProgress = "in_progress"
	TaskDone       = "done"

	ProjectPlanning   = "planning"
	ProjectInProgress = "in_progress"
	ProjectOnHold     = "on_hold"
	ProjectCompleted  = "completed"
	ProjectClosed     = "closed"
)

func publicTaskStatus(state string) string {
	switch state {
	case projects.StateDone:
		return TaskDone
	case projects.StateRunning, projects.StateAwaitingApproval:
		return TaskInProgress
	}
	return TaskPending
}

func publicProjectStatus(status string) string {
	switch status {
	case projects.StatusDone:
		return ProjectCompleted
	case projects.StatusCancelled:
		return ProjectClosed
	case projects.StatusPaused, projects.StatusFailed:
		return ProjectOnHold
	case projects.StatusRunning, projects.StatusWaitingHuman:
		return ProjectInProgress
	}
	return ProjectPlanning // draft, planning
}

// visibleNode: real work only. Groups are structure, gates are approvals, waits are internal timing;
// superseded and cancelled nodes are no longer part of the plan.
func visibleNode(n projects.Node) bool {
	switch n.Kind {
	case projects.KindTask, projects.KindSubtask, projects.KindMilestone:
	default:
		return false
	}
	return n.Superseded == "" && n.State != projects.StateCancelled
}

func copyParams(p map[string]string) map[string]string {
	if len(p) == 0 {
		return nil
	}
	out := make(map[string]string, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

func pct(done, total int) int {
	if total == 0 {
		return 0
	}
	return done * 100 / total
}

func later(cur *time.Time, t *time.Time) *time.Time {
	if t == nil || t.IsZero() {
		return cur
	}
	if cur == nil || t.After(*cur) {
		x := t.UTC()
		return &x
	}
	return cur
}

// buildView copies the allowed fields only.
func buildView(b Branding, d projects.Detail, shared []artifacts.Meta, l Link, now time.Time) View {
	v := View{
		Firm:         Firm{Name: b.DisplayName, AccentColor: b.AccentColor, HasLogo: b.HasLogo},
		Milestones:   []Milestone{},
		Deliverables: []Deliverable{},
		GeneratedAt:  now,
	}
	if l.ExpiresAt != nil {
		x := l.ExpiresAt.UTC()
		v.ExpiresAt = &x
	}
	p := d.Project
	v.Project = Project{Title: p.Name, TitleKey: p.NameKey, Status: publicProjectStatus(p.Status)}
	if l.Title != "" { // the firm chose what the client reads
		v.Project.Title, v.Project.TitleKey = l.Title, ""
	}
	created := p.CreatedAt
	v.UpdatedAt = later(v.UpdatedAt, &created)
	v.UpdatedAt = later(v.UpdatedAt, p.StartedAt)
	v.UpdatedAt = later(v.UpdatedAt, p.FinishedAt)

	objs := append([]projects.Objective{}, d.Objectives...)
	sort.SliceStable(objs, func(i, j int) bool { return objs[i].Position < objs[j].Position })
	idx := map[string]int{}
	for _, o := range objs {
		idx[o.ID] = len(v.Milestones)
		v.Milestones = append(v.Milestones, Milestone{Title: o.Title, TitleKey: o.TitleKey, TitleParams: copyParams(o.TitleParams), Tasks: []Task{}})
	}
	other := -1 // tasks of no known objective
	done, total := 0, 0
	for _, n := range d.Nodes {
		if !visibleNode(n) {
			continue
		}
		i, ok := idx[n.ObjectiveID]
		if !ok {
			if other < 0 {
				other = len(v.Milestones)
				v.Milestones = append(v.Milestones, Milestone{Tasks: []Task{}})
			}
			i = other
		}
		st := publicTaskStatus(n.State)
		v.Milestones[i].Tasks = append(v.Milestones[i].Tasks, Task{Title: n.Title, TitleKey: n.TitleKey, TitleParams: copyParams(n.TitleParams), Status: st})
		total++
		if st == TaskDone {
			done++
		}
		v.UpdatedAt = later(v.UpdatedAt, n.StartedAt)
		v.UpdatedAt = later(v.UpdatedAt, n.FinishedAt)
	}
	kept := v.Milestones[:0]
	for _, m := range v.Milestones {
		if len(m.Tasks) == 0 {
			continue // an objective with no visible work says nothing to the client
		}
		md, inProgress := 0, false
		for _, t := range m.Tasks {
			switch t.Status {
			case TaskDone:
				md++
			case TaskInProgress:
				inProgress = true
			}
		}
		m.ProgressPct = pct(md, len(m.Tasks))
		switch {
		case md == len(m.Tasks):
			m.Status = TaskDone
		case md > 0 || inProgress:
			m.Status = TaskInProgress
		default:
			m.Status = TaskPending
		}
		kept = append(kept, m)
	}
	v.Milestones = kept
	v.Project.TasksDone, v.Project.TasksTotal, v.Project.ProgressPct = done, total, pct(done, total)
	if v.Project.Status == ProjectCompleted && total > 0 {
		v.Project.ProgressPct = 100
	}

	sort.SliceStable(shared, func(i, j int) bool { return shared[i].UpdatedAt.After(shared[j].UpdatedAt) })
	for _, m := range shared {
		at := m.UpdatedAt.UTC()
		v.Deliverables = append(v.Deliverables, Deliverable{Title: m.Title, Kind: string(m.Kind), CompletedAt: at})
		v.UpdatedAt = later(v.UpdatedAt, &at)
	}
	return v
}
