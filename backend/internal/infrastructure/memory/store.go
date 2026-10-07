// Package memory is an in-memory implementation of application.Store, used by
// tests and for running the backend without Postgres.
package memory

import (
	"context"
	"slices"
	"strings"
	"sync"

	"aiworkforce/backend/internal/audit"
	"aiworkforce/backend/internal/domain"
)

type Store struct {
	mu            sync.Mutex
	agents        map[string]domain.Agent
	agentOrder    []string
	tasks         map[string]domain.Task
	taskOrder     []string
	requests      map[string]domain.Request
	reqOrder      []string
	convs         map[string]domain.Conversation
	convOrder     []string
	messages      []domain.Message
	approvals     map[string]domain.Approval
	apprOrder     []string
	reports       map[string]domain.Report
	repOrder      []string
	memories      map[string]map[string]domain.Memory // agent -> scope/key
	events        []domain.Event
	activity      []domain.ActivityItem
	audit         []domain.AuditLog
	auditHeads    map[string]domain.AuditHead // org -> chain head
	budget        float64
	orgSeeded     bool
	failOnAddCost error
	cfg           configState // organization settings and schedules (config.go)
	usage         []domain.UsageEntry
	caps          map[string]domain.BudgetCap // scope/id
}

func New() *Store {
	s := &Store{}
	s.clear()
	return s
}

func (s *Store) clear() {
	s.tasks, s.requests, s.convs = map[string]domain.Task{}, map[string]domain.Request{}, map[string]domain.Conversation{}
	s.approvals, s.reports = map[string]domain.Approval{}, map[string]domain.Report{}
	s.memories = map[string]map[string]domain.Memory{}
	s.taskOrder, s.reqOrder, s.convOrder, s.apprOrder, s.repOrder = nil, nil, nil, nil, nil
	s.messages, s.events, s.activity = nil, nil, nil
	s.usage = nil
	// Request caps are execution data; agent caps are configuration and survive a reset.
	for k, c := range s.caps {
		if c.Scope == domain.ScopeRequest {
			delete(s.caps, k)
		}
	}
}

func (s *Store) Seed(_ context.Context, org domain.Organization, agents []domain.Agent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.budget, s.orgSeeded = org.BudgetUSD, true
	if s.agents == nil {
		s.agents = map[string]domain.Agent{}
	}
	for _, a := range agents {
		if _, ok := s.agents[a.ID]; !ok {
			s.agents[a.ID] = a
			s.agentOrder = append(s.agentOrder, a.ID)
		}
	}
	return nil
}

func (s *Store) Reset(_ context.Context, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clear()
	for id, a := range s.agents {
		a.State, a.Activity, a.CurrentTaskID, a.Progress = domain.StateIdle, "Disponible", nil, 0
		s.agents[id] = a
	}
	return nil
}

func (s *Store) ListAgents(_ context.Context, _ string) ([]domain.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.Agent, 0, len(s.agentOrder))
	for _, id := range s.agentOrder {
		out = append(out, s.agents[id])
	}
	return out, nil
}

func (s *Store) GetAgent(_ context.Context, _, id string) (domain.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[id]
	if !ok {
		return a, domain.ErrNotFound
	}
	return a, nil
}

func (s *Store) UpdateAgentState(_ context.Context, _, id string, st domain.AgentState, activity string, taskID *string, progress int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[id]
	if !ok {
		return domain.ErrNotFound
	}
	a.State, a.Activity, a.CurrentTaskID, a.Progress = st, activity, taskID, progress
	s.agents[id] = a
	return nil
}

func (s *Store) CreateTask(_ context.Context, _ string, t domain.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[t.ID] = t
	s.taskOrder = append(s.taskOrder, t.ID)
	return nil
}

func (s *Store) GetTask(_ context.Context, _, id string) (domain.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return t, domain.ErrNotFound
	}
	return t, nil
}

func (s *Store) UpdateTask(_ context.Context, _ string, t domain.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.tasks[t.ID]
	if !ok {
		return domain.ErrNotFound
	}
	t.CostUSD = old.CostUSD // cost only changes through AddCost
	s.tasks[t.ID] = t
	return nil
}

func (s *Store) ListTasks(_ context.Context, _, agentID, status string) ([]domain.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.Task{}
	for _, id := range s.taskOrder {
		t := s.tasks[id]
		if (agentID == "" || t.AgentID == agentID) && (status == "" || string(t.Status) == status) {
			out = append(out, t)
		}
	}
	slices.Reverse(out) // newest first
	return out, nil
}

func (s *Store) ListTasksByRequest(_ context.Context, _, requestID string) ([]domain.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.Task{}
	for _, id := range s.taskOrder {
		if t := s.tasks[id]; t.RequestID == requestID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (s *Store) CreateRequest(_ context.Context, _ string, r domain.Request) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests[r.ID] = r
	s.reqOrder = append(s.reqOrder, r.ID)
	return nil
}

func (s *Store) GetRequest(_ context.Context, _, id string) (domain.Request, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.requests[id]
	if !ok {
		return r, domain.ErrNotFound
	}
	return r, nil
}

func (s *Store) UpdateRequest(_ context.Context, _ string, r domain.Request) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.requests[r.ID]
	if !ok {
		return domain.ErrNotFound
	}
	r.CostUSD = old.CostUSD
	s.requests[r.ID] = r
	return nil
}

func (s *Store) ListRequests(_ context.Context, _ string) ([]domain.Request, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.Request{}
	for _, id := range s.reqOrder {
		out = append(out, s.requests[id])
	}
	slices.Reverse(out)
	return out, nil
}

func (s *Store) AddCost(_ context.Context, _, requestID, taskID string, usd float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failOnAddCost != nil {
		return s.failOnAddCost
	}
	if r, ok := s.requests[requestID]; ok {
		r.CostUSD += usd
		s.requests[requestID] = r
	}
	if t, ok := s.tasks[taskID]; ok {
		t.CostUSD += usd
		s.tasks[taskID] = t
	}
	return nil
}

func (s *Store) OrgCost(_ context.Context, _ string) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var c float64
	for _, r := range s.requests {
		c += r.CostUSD
	}
	return c, nil
}

func (s *Store) AddUsage(_ context.Context, _ string, u domain.UsageEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usage = append(s.usage, u)
	return nil
}

func (s *Store) ListUsage(_ context.Context, _ string) ([]domain.UsageEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.usage), nil
}

func (s *Store) SetBudgetCap(_ context.Context, _ string, scope domain.BudgetScope, scopeID string, capUSD float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.caps == nil {
		s.caps = map[string]domain.BudgetCap{}
	}
	key := string(scope) + "/" + scopeID
	if capUSD <= 0 {
		delete(s.caps, key)
		return nil
	}
	s.caps[key] = domain.BudgetCap{Scope: scope, ScopeID: scopeID, CapUSD: capUSD}
	return nil
}

func (s *Store) ListBudgetCaps(_ context.Context, _ string) ([]domain.BudgetCap, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.BudgetCap, 0, len(s.caps))
	for _, c := range s.caps {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b domain.BudgetCap) int { return compare(string(a.Scope)+a.ScopeID, string(b.Scope)+b.ScopeID) })
	return out, nil
}

func (s *Store) CreateConversation(_ context.Context, _ string, c domain.Conversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.convs[c.ID] = c
	s.convOrder = append(s.convOrder, c.ID)
	return nil
}

func (s *Store) GetConversation(_ context.Context, _, id string) (domain.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.convs[id]
	if !ok {
		return c, domain.ErrNotFound
	}
	return c, nil
}

func (s *Store) ListConversations(_ context.Context, _ string) ([]domain.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.Conversation{}
	for _, id := range s.convOrder {
		out = append(out, s.convs[id])
	}
	slices.SortStableFunc(out, func(a, b domain.Conversation) int { return b.LastMessageAt.Compare(a.LastMessageAt) })
	return out, nil
}

func (s *Store) AddMessage(_ context.Context, _ string, m domain.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.convs[m.ConversationID]
	if !ok {
		return domain.ErrNotFound
	}
	c.LastMessageAt = m.TS
	for _, p := range []string{m.From, m.To} {
		if p != "all" && p != "system" && !slices.Contains(c.Participants, p) {
			c.Participants = append(c.Participants, p)
		}
	}
	s.convs[c.ID] = c
	s.messages = append(s.messages, m)
	return nil
}

func (s *Store) ListMessages(_ context.Context, _, conversationID string) ([]domain.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.Message{}
	for _, m := range s.messages {
		if m.ConversationID == conversationID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (s *Store) CreateApproval(_ context.Context, _ string, a domain.Approval) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approvals[a.ID] = a
	s.apprOrder = append(s.apprOrder, a.ID)
	return nil
}

func (s *Store) GetApproval(_ context.Context, _, id string) (domain.Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.approvals[id]
	if !ok {
		return a, domain.ErrNotFound
	}
	return a, nil
}

func (s *Store) UpdateApproval(_ context.Context, _ string, a domain.Approval) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.approvals[a.ID]; !ok {
		return domain.ErrNotFound
	}
	s.approvals[a.ID] = a
	return nil
}

func (s *Store) ListApprovals(_ context.Context, _, status string) ([]domain.Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.Approval{}
	for _, id := range s.apprOrder {
		if a := s.approvals[id]; status == "" || string(a.Status) == status {
			out = append(out, a)
		}
	}
	slices.Reverse(out)
	return out, nil
}

func (s *Store) CreateReport(_ context.Context, _ string, r domain.Report) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reports[r.ID] = r
	s.repOrder = append(s.repOrder, r.ID)
	if req, ok := s.requests[r.RequestID]; ok {
		req.ReportID = &r.ID
		s.requests[req.ID] = req
	}
	return nil
}

func (s *Store) GetReport(_ context.Context, _, id string) (domain.Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.reports[id]
	if !ok {
		return r, domain.ErrNotFound
	}
	return r, nil
}

func (s *Store) ListReports(_ context.Context, _ string) ([]domain.Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.Report{}
	for _, id := range s.repOrder {
		out = append(out, s.reports[id])
	}
	slices.Reverse(out)
	return out, nil
}

func (s *Store) SetMemory(_ context.Context, _, agentID string, m domain.Memory) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.memories[agentID] == nil {
		s.memories[agentID] = map[string]domain.Memory{}
	}
	s.memories[agentID][m.Scope+"/"+m.Key] = m
	return nil
}

func (s *Store) ListMemory(_ context.Context, _, agentID string) ([]domain.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.Memory{}
	for _, m := range s.memories[agentID] {
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b domain.Memory) int { return compare(a.Scope+a.Key, b.Scope+b.Key) })
	return out, nil
}

func compare(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func (s *Store) SaveEvent(_ context.Context, _ string, e domain.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}

func (s *Store) AddActivity(_ context.Context, _ string, a domain.ActivityItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activity = append(s.activity, a)
	return nil
}

func (s *Store) ListActivity(_ context.Context, _, agentID string, limit int) ([]domain.ActivityItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.ActivityItem{}
	for i := len(s.activity) - 1; i >= 0 && len(out) < limit; i-- {
		a := s.activity[i]
		if agentID == "" || (a.AgentID != nil && *a.AgentID == agentID) {
			out = append(out, a)
		}
	}
	return out, nil
}

// AddAudit appends the entry to the organization's hash chain.
func (s *Store) AddAudit(_ context.Context, orgID string, a domain.AuditLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.auditHeads == nil {
		s.auditHeads = map[string]domain.AuditHead{}
	}
	sealed := audit.Seal(orgID, s.auditHeads[orgID], a)
	s.auditHeads[orgID] = domain.AuditHead{Seq: sealed.Seq, Hash: sealed.Hash, TS: sealed.TS}
	s.audit = append(s.audit, sealed)
	return nil
}

var _ interface {
	QueryAudit(context.Context, string, domain.AuditQuery) (domain.AuditPage, error)
	AuditChain(context.Context, string, int64, int) ([]domain.AuditLog, error)
	AuditHead(context.Context, string) (domain.AuditHead, bool, error)
} = (*Store)(nil)

// QueryAudit filters and pages the audit trail of one organization.
func (s *Store) QueryAudit(_ context.Context, orgID string, q domain.AuditQuery) (domain.AuditPage, error) {
	q = audit.NormalizeQuery(q)
	var cur *audit.Cursor
	if q.Cursor != "" {
		c, err := audit.DecodeCursor(q.Cursor)
		if err != nil {
			return domain.AuditPage{}, err
		}
		cur = &c
	}
	s.mu.Lock()
	var all []domain.AuditLog
	for _, e := range s.audit {
		if e.OrgID == orgID && audit.Match(q, e) {
			all = append(all, e)
		}
	}
	s.mu.Unlock()
	slices.SortStableFunc(all, func(a, b domain.AuditLog) int {
		c := a.TS.Compare(b.TS)
		if c == 0 {
			c = strings.Compare(a.ID, b.ID)
		}
		if q.Desc {
			return -c
		}
		return c
	})
	page := domain.AuditPage{Items: []domain.AuditLog{}}
	for _, e := range all {
		if cur != nil && !cur.After(e, q.Desc) {
			continue
		}
		if len(page.Items) == q.Limit {
			page.NextCursor = audit.CursorOf(page.Items[len(page.Items)-1]).Encode()
			break
		}
		page.Items = append(page.Items, e)
	}
	return page, nil
}

// AuditChain returns chained entries with seq > after, ascending.
func (s *Store) AuditChain(_ context.Context, orgID string, after int64, limit int) ([]domain.AuditLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.AuditLog
	for _, e := range s.audit {
		if e.OrgID == orgID && e.Seq > after {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b domain.AuditLog) int { return int(a.Seq - b.Seq) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// AuditHead returns the recorded chain head.
func (s *Store) AuditHead(_ context.Context, orgID string) (domain.AuditHead, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.auditHeads[orgID]
	return h, ok, nil
}

// UnsafeMutateAudit lets tests simulate tampering with stored entries (the
// in-memory store has no database privileges to enforce append-only). Never
// call it from production code.
func (s *Store) UnsafeMutateAudit(fn func(entries *[]domain.AuditLog)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.audit)
}

// ---- helpers for tests ----

// Audit returns a copy of the audit trail.
func (s *Store) Audit() []domain.AuditLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.audit)
}

// Events returns a copy of the persisted events.
func (s *Store) Events() []domain.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.events)
}
