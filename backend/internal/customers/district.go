package customers

import (
	"context"
	"sort"
	"time"
)

// Project statuses as internal/projects names them (kept here as plain strings: no import cycle).
const (
	projectRunning      = "running"
	projectPaused       = "paused"
	projectWaitingHuman = "waiting_human"
	projectFailed       = "failed"
)

// Request statuses as domain.RequestStatus names them.
const (
	requestPlanning         = "planning"
	requestRunning          = "running"
	requestAwaitingApproval = "awaiting_approval"
	requestFailed           = "failed"
)

// recentFailure is how long a failed project or request keeps a building red.
const recentFailure = 7 * 24 * time.Hour

// maxRequests bounds the requests read per customer (the most recent ones).
const maxRequests = 20

// Health is the state of one building.
type Health struct {
	Status           string `json:"status"`
	ProjectsActive   int    `json:"projects_active"`
	ApprovalsWaiting int    `json:"approvals_waiting"`
	Failed           int    `json:"failed"`
}

// HealthOf derives the status of a customer from its projects and requests: blocked (something failed in the
// last week or a project is paused) beats waiting_approval (a human gate or approval pending) beats on_track
// (work running) beats idle.
func HealthOf(projects []ProjectInfo, requests []RequestInfo, now time.Time) Health {
	var h Health
	running := false
	recent := func(t *time.Time) bool { return t != nil && now.Sub(*t) <= recentFailure }
	for _, p := range projects {
		switch p.Status {
		case projectRunning, projectPaused, projectWaitingHuman:
			h.ProjectsActive++
			h.ApprovalsWaiting += p.Awaiting
			h.Failed += p.Failed
			if p.Status == projectPaused {
				h.Failed++ // a paused project needs a person: it counts as blocked
			}
			if p.Status == projectWaitingHuman && p.Awaiting == 0 {
				h.ApprovalsWaiting++
			}
			running = true
		case projectFailed:
			if recent(p.FinishedAt) {
				h.Failed++
			}
		}
	}
	for _, r := range requests {
		switch r.Status {
		case requestPlanning, requestRunning:
			running = true
		case requestAwaitingApproval:
			h.ApprovalsWaiting++
		case requestFailed:
			if at := r.CreatedAt; now.Sub(at) <= recentFailure {
				h.Failed++
			}
		}
	}
	switch {
	case h.Failed > 0:
		h.Status = StatusBlocked
	case h.ApprovalsWaiting > 0:
		h.Status = StatusWaitingApproval
	case running:
		h.Status = StatusOnTrack
	default:
		h.Status = StatusIdle
	}
	return h
}

// DistrictEntry is one building of the district.
type DistrictEntry struct {
	Customer Customer `json:"customer"`
	Health
	Assignments int `json:"assignments"`
}

// District is the neighbourhood: every active customer with its status.
type District struct {
	GeneratedAt time.Time       `json:"generated_at"`
	Customers   []DistrictEntry `json:"customers"`
}

// Detail is the scoped view of one customer.
type Detail struct {
	Customer Customer `json:"customer"`
	Health
	Assignments []Assignment  `json:"assignments"`
	Projects    []ProjectInfo `json:"projects"`
	Requests    []RequestInfo `json:"requests"`
	FolderPath  string        `json:"folder_path"`
}

// work reads the projects (grouped by customer) and the recent requests of one customer.
func (s *Service) projectsByCustomer(ctx context.Context, org string) (map[string][]ProjectInfo, error) {
	_, work := s.deps()
	out := map[string][]ProjectInfo{}
	if work == nil {
		return out, nil
	}
	ps, err := work.Projects(ctx, org)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if p.CustomerID != "" {
			out[p.CustomerID] = append(out[p.CustomerID], p)
		}
	}
	return out, nil
}

func (s *Service) requestsOf(ctx context.Context, org, customerID string) ([]RequestInfo, error) {
	_, work := s.deps()
	if work == nil {
		return []RequestInfo{}, nil
	}
	ls, err := s.cfg.Store.Links(ctx, org, customerID, LinkRequest, maxRequests)
	if err != nil {
		return nil, err
	}
	out := make([]RequestInfo, 0, len(ls))
	for _, l := range ls {
		r, err := work.Request(ctx, org, l.RefID)
		if err != nil {
			continue // a request removed by a demo reset: its link stays harmless
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// District returns every active customer with the status of its building.
func (s *Service) District(ctx context.Context) (District, error) {
	org := s.org(ctx)
	cs, err := s.List(ctx, false)
	if err != nil {
		return District{}, err
	}
	byCustomer, err := s.projectsByCustomer(ctx, org)
	if err != nil {
		return District{}, err
	}
	clones, err := s.cfg.Store.Assignments(ctx, org, "")
	if err != nil {
		return District{}, err
	}
	nClones := map[string]int{}
	for _, a := range clones {
		nClones[a.CustomerID]++
	}
	now := s.now()
	out := District{GeneratedAt: now, Customers: make([]DistrictEntry, 0, len(cs))}
	for _, c := range cs {
		reqs, err := s.requestsOf(ctx, org, c.ID)
		if err != nil {
			return District{}, err
		}
		out.Customers = append(out.Customers, DistrictEntry{Customer: c, Health: HealthOf(byCustomer[c.ID], reqs, now), Assignments: nClones[c.ID]})
	}
	return out, nil
}

// Detail returns the scoped view of one customer.
func (s *Service) Detail(ctx context.Context, id string) (Detail, error) {
	org := s.org(ctx)
	c, err := s.Get(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	byCustomer, err := s.projectsByCustomer(ctx, org)
	if err != nil {
		return Detail{}, err
	}
	reqs, err := s.requestsOf(ctx, org, c.ID)
	if err != nil {
		return Detail{}, err
	}
	clones, err := s.cfg.Store.Assignments(ctx, org, c.ID)
	if err != nil {
		return Detail{}, err
	}
	projects := byCustomer[c.ID]
	if projects == nil {
		projects = []ProjectInfo{}
	}
	return Detail{Customer: c, Health: HealthOf(projects, reqs, s.now()), Assignments: clones, Projects: projects, Requests: reqs, FolderPath: c.FolderPath}, nil
}
