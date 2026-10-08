package application

import (
	"context"
	"slices"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Config holds orchestrator tunables.
type Config struct {
	OrgID           string
	BudgetUSD       float64
	ApprovalActions map[string]bool
	MaxParallel     int // tasks of ONE request running at the same time
	// MaxParallelPerOrg caps the runtime calls (planning, tasks, consults,
	// synthesis) of one organization running at the same time, across all its
	// requests; the rest wait in a priority queue (orgqueue.go). <= 0: no cap.
	MaxParallelPerOrg int
	MaxDepth          int
	TaskTimeout       time.Duration // per runtime call attempt
	MaxRetries        int           // total attempts per runtime call
	RetryBase         time.Duration // backoff base (doubles each attempt)
	ApprovalTimeout   time.Duration
	LockTTL           time.Duration
	IdleDelay         time.Duration // how long agents stay "completed" after a request

	// Cost control (all optional; zero disables). See docs/architecture/07-seguridad-costos.md.
	RequestBudgetCapUSD float64       // default hard cap per request
	AgentBudgetCapUSD   float64       // default monthly hard cap per agent
	ConfirmThresholdUSD float64       // ask for confirmation when the estimated max exceeds this
	PauseTimeout        time.Duration // how long a capped request/agent waits for a raised cap

	// Chat layer (docs/architecture/chat-routing.md); zero values use the defaults noted here.
	ChatTimeout time.Duration // per runtime call of a chat turn (route, reply); default 30s
	ChatStagger time.Duration // pause before each additional responder of a turn (0 = none)
}

// DefaultConfig returns sane defaults.
func DefaultConfig() Config {
	return Config{
		OrgID: domain.DemoOrgID, BudgetUSD: 50,
		ApprovalActions: map[string]bool{"send_proposal": true, "send_contract": true},
		MaxParallel:     4, MaxParallelPerOrg: 8, MaxDepth: 5,
		TaskTimeout: 120 * time.Second, MaxRetries: 3, RetryBase: 500 * time.Millisecond,
		ApprovalTimeout: 30 * time.Minute, LockTTL: 10 * time.Minute, IdleDelay: 4 * time.Second,
		ConfirmThresholdUSD: 1.0, PauseTimeout: 30 * time.Minute,
		ChatTimeout: 30 * time.Second, ChatStagger: 900 * time.Millisecond,
	}
}

// Queries implements the read side used by the REST API.
type Queries struct {
	Store Store
	Cfg   Config
}

func (q *Queries) Agents(ctx context.Context) ([]domain.Agent, error) {
	agents, err := q.Store.ListAgents(ctx, q.org(ctx))
	if err != nil {
		return nil, err
	}
	tasks, err := q.Store.ListTasks(ctx, q.org(ctx), "", "")
	if err != nil {
		return nil, err
	}
	fillMetrics(agents, tasks)
	return agents, nil
}

func (q *Queries) Agent(ctx context.Context, id string) (domain.Agent, error) {
	agents, err := q.Agents(ctx)
	if err != nil {
		return domain.Agent{}, err
	}
	for _, a := range agents {
		if a.ID == id {
			return a, nil
		}
	}
	return domain.Agent{}, domain.ErrNotFound
}

func fillMetrics(agents []domain.Agent, tasks []domain.Task) {
	for i := range agents {
		var m domain.AgentMetrics
		var secs float64
		for _, t := range tasks {
			if t.AgentID != agents[i].ID {
				continue
			}
			m.CostUSD += t.CostUSD
			switch t.Status {
			case domain.TaskDone:
				m.TasksCompleted++
				if t.StartedAt != nil && t.FinishedAt != nil {
					secs += t.FinishedAt.Sub(*t.StartedAt).Seconds()
				}
			case domain.TaskPending, domain.TaskRunning, domain.TaskAwaitingApproval, domain.TaskBlocked:
				m.TasksPending++
			}
		}
		if m.TasksCompleted > 0 {
			m.AvgSeconds = secs / float64(m.TasksCompleted)
		}
		agents[i].Metrics = m
	}
}

type AgentDetail struct {
	Agent          domain.Agent          `json:"agent"`
	Tasks          []domain.Task         `json:"tasks"`
	Conversations  []domain.Conversation `json:"conversations"`
	Memory         []domain.Memory       `json:"memory"`
	RecentActivity []domain.ActivityItem `json:"recent_activity"`
}

func (q *Queries) AgentDetail(ctx context.Context, id string) (AgentDetail, error) {
	a, err := q.Agent(ctx, id)
	if err != nil {
		return AgentDetail{}, err
	}
	d := AgentDetail{Agent: a}
	if d.Tasks, err = q.Store.ListTasks(ctx, q.org(ctx), id, ""); err != nil {
		return d, err
	}
	if len(d.Tasks) > 20 {
		d.Tasks = d.Tasks[:20]
	}
	convs, err := q.Store.ListConversations(ctx, q.org(ctx))
	if err != nil {
		return d, err
	}
	d.Conversations = []domain.Conversation{}
	for _, c := range convs {
		if slices.Contains(c.Participants, id) {
			d.Conversations = append(d.Conversations, c)
		}
	}
	if d.Memory, err = q.Store.ListMemory(ctx, q.org(ctx), id); err != nil {
		return d, err
	}
	d.RecentActivity, err = q.Store.ListActivity(ctx, q.org(ctx), id, 20)
	return d, err
}

type PlanTask struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	AgentID   string   `json:"agent_id"`
	DependsOn []string `json:"depends_on"`
}

type RequestDetail struct {
	domain.Request
	Tasks []domain.Task `json:"tasks"`
	Plan  struct {
		domain.Plan
		Tasks []PlanTask `json:"tasks"`
	} `json:"plan"`
}

func (q *Queries) RequestDetail(ctx context.Context, id string) (RequestDetail, error) {
	r, err := q.Store.GetRequest(ctx, q.org(ctx), id)
	if err != nil {
		return RequestDetail{}, err
	}
	tasks, err := q.Store.ListTasksByRequest(ctx, q.org(ctx), id)
	if err != nil {
		return RequestDetail{}, err
	}
	d := RequestDetail{Request: r, Tasks: tasks}
	d.Plan.Plan = r.Plan
	if d.Plan.Objectives == nil {
		d.Plan.Objectives = []string{}
	}
	if d.Plan.ClarifyingQuestions == nil {
		d.Plan.ClarifyingQuestions = []string{}
	}
	d.Plan.Tasks = planTasks(tasks)
	return d, nil
}

func planTasks(tasks []domain.Task) []PlanTask {
	out := make([]PlanTask, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, PlanTask{ID: t.ID, Title: t.Title, AgentID: t.AgentID, DependsOn: t.DependsOn})
	}
	return out
}

// Metrics computes the org-level counters.
func (q *Queries) Metrics(ctx context.Context) (domain.Metrics, error) {
	m := domain.Metrics{BudgetUSD: q.Cfg.BudgetUSD}
	tasks, err := q.Store.ListTasks(ctx, q.org(ctx), "", "")
	if err != nil {
		return m, err
	}
	for _, t := range tasks {
		switch t.Status {
		case domain.TaskRunning, domain.TaskAwaitingApproval:
			m.TasksActive++
		case domain.TaskDone:
			m.TasksDone++
		case domain.TaskFailed:
			m.Errors++
		}
	}
	pending, err := q.Store.ListApprovals(ctx, q.org(ctx), string(domain.ApprovalPending))
	if err != nil {
		return m, err
	}
	m.ApprovalsPending = len(pending)
	reqs, err := q.Store.ListRequests(ctx, q.org(ctx))
	if err != nil {
		return m, err
	}
	m.RequestsTotal = len(reqs)
	for _, r := range reqs {
		m.CostUSD += r.CostUSD
		if r.Status == domain.RequestFailed {
			m.Errors++
		}
	}
	return m, nil
}
