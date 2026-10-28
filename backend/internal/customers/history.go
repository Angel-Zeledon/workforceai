package customers

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Work done before the customer district (B12) has no customer: a customer-scoped task never recalls it, because
// unscoped work may hold anyone's data. History is NOT retagged silently. A person with files:grant explicitly
// assigns one project, request or memory to a customer (and can undo it): the request link is stored, the memory
// documents of that request move to the customer, and from then on that customer's tasks recall them. Every change
// is audited (customer.history.assigned / customer.history.unassigned) with the previous and the new customer.
//
// Work that is still running is never moved: its tasks resolve the scope from the request while they run, so a
// change in the middle would let a task see another customer's data.

// History kinds.
const (
	HistoryProject = "project"
	HistoryRequest = "request"
	HistoryMemory  = "memory"
)

// HistoryProjectInfo is what the history needs of a project.
type HistoryProjectInfo struct {
	CustomerID string
	RequestID  string
	// Movable: a draft or a finished project (not running).
	Movable bool
}

// HistoryMemoryInfo is what the history needs of a memory document.
type HistoryMemoryInfo struct {
	CustomerID string
	RequestID  string
}

// History reads and changes the tags of earlier work (implemented in cmd/server).
type History interface {
	Project(ctx context.Context, org, id string) (HistoryProjectInfo, error)
	// SetProjectCustomer changes the customer of a project that is not running ("" removes it).
	SetProjectCustomer(ctx context.Context, org, id, customerID string) error
	// RequestFinished reports whether a request has finished (domain.ErrNotFound when it does not exist).
	RequestFinished(ctx context.Context, org, id string) (bool, error)
	Memory(ctx context.Context, org, id string) (HistoryMemoryInfo, error)
	// RetagMemories moves the memory documents of a request (or one document, docID) tagged `from` to `to`.
	RetagMemories(ctx context.Context, org, requestID, docID, from, to string) (int, error)
	// Candidates lists earlier work without a customer: projects that are not running and finished requests that
	// are not a project's, newest first, at most limit of each (requests may still carry a link: filtered here).
	Candidates(ctx context.Context, org string, limit int) ([]ProjectInfo, []RequestInfo, error)
}

// HistoryCandidates is the earlier work without a customer a person may assign.
type HistoryCandidates struct {
	Projects []ProjectInfo `json:"projects"`
	Requests []RequestInfo `json:"requests"`
}

const maxCandidates = 30

// Candidates lists the earlier work without a customer (projects not running, finished requests not of a project).
func (s *Service) Candidates(ctx context.Context) (HistoryCandidates, error) {
	org := s.org(ctx)
	out := HistoryCandidates{Projects: []ProjectInfo{}, Requests: []RequestInfo{}}
	h := s.historyDep()
	if h == nil {
		return out, nil
	}
	ps, rs, err := h.Candidates(ctx, org, maxCandidates*2)
	if err != nil {
		return out, err
	}
	for _, p := range ps {
		if p.CustomerID == "" && len(out.Projects) < maxCandidates {
			out.Projects = append(out.Projects, p)
		}
	}
	for _, r := range rs {
		if len(out.Requests) >= maxCandidates {
			break
		}
		if cid, err := s.CustomerOfRequest(ctx, org, r.ID); err != nil {
			return out, err
		} else if cid == "" {
			out.Requests = append(out.Requests, r)
		}
	}
	return out, nil
}

// HistoryInput is one explicit assignment. CustomerID "" removes the customer (undo).
type HistoryInput struct {
	Kind       string `json:"kind"`
	RefID      string `json:"ref_id"`
	CustomerID string `json:"customer_id"`
}

// HistoryResult says what changed.
type HistoryResult struct {
	Kind  string `json:"kind"`
	RefID string `json:"ref_id"`
	From  string `json:"from"`
	To    string `json:"to"`
	// RequestID is the request of a project, moved with it ("" when none).
	RequestID string `json:"request_id,omitempty"`
	// Memories is the number of memory documents that changed customer.
	Memories int  `json:"memories"`
	Changed  bool `json:"changed"`
}

// SetHistory installs the reader/writer of earlier work.
func (s *Service) SetHistory(h History) { s.mu.Lock(); s.history = h; s.mu.Unlock() }

func (s *Service) historyDep() History {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.history
}

// AssignHistory assigns earlier work to a customer, or removes the customer with CustomerID "".
func (s *Service) AssignHistory(ctx context.Context, actor string, in HistoryInput) (HistoryResult, error) {
	org := s.org(ctx)
	if err := s.gate(ctx, org); err != nil {
		return HistoryResult{}, err
	}
	h := s.historyDep()
	if h == nil {
		return HistoryResult{}, invalid("assigning earlier work is not available")
	}
	ref, to := strings.TrimSpace(in.RefID), strings.TrimSpace(in.CustomerID)
	if ref == "" {
		return HistoryResult{}, invalid("ref_id is required")
	}
	if to != "" {
		if _, err := s.Active(ctx, org, to); err != nil {
			return HistoryResult{}, err
		}
	}
	var (
		res HistoryResult
		err error
	)
	switch in.Kind {
	case HistoryRequest:
		res, err = s.moveRequest(ctx, h, org, actor, ref, to)
	case HistoryProject:
		res, err = s.moveProject(ctx, h, org, actor, ref, to)
	case HistoryMemory:
		res, err = s.moveMemory(ctx, h, org, ref, to)
	default:
		return HistoryResult{}, invalid("kind must be project, request or memory")
	}
	if err != nil {
		return HistoryResult{}, err
	}
	res.Kind, res.RefID = in.Kind, ref
	if res.Changed {
		action, who := "customer.history.assigned", res.To
		if res.To == "" {
			action, who = "customer.history.unassigned", res.From
		}
		s.audit(ctx, org, actor, action, who, map[string]any{"kind": res.Kind, "ref_id": ref, "from": res.From, "to": res.To,
			"request_id": res.RequestID, "memories": res.Memories})
	}
	return res, nil
}

func conflict(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrConflict}, a...)...)
}

// moveRequest moves a finished request: its link and the memory documents of it.
func (s *Service) moveRequest(ctx context.Context, h History, org, actor, id, to string) (HistoryResult, error) {
	done, err := h.RequestFinished(ctx, org, id)
	if err != nil {
		return HistoryResult{}, err
	}
	if !done {
		return HistoryResult{}, conflict("the request is still running: assign it when it has finished")
	}
	from, err := s.CustomerOfRequest(ctx, org, id)
	if err != nil {
		return HistoryResult{}, err
	}
	res := HistoryResult{From: from, To: to}
	if from == to {
		return res, nil
	}
	if from != "" && to != "" {
		return HistoryResult{}, conflict("the request already belongs to another customer: remove that customer first")
	}
	if to != "" {
		if err := s.cfg.Store.AddLink(ctx, org, Link{Kind: LinkRequest, RefID: id, CustomerID: to, CreatedBy: actor, CreatedAt: s.now()}); err != nil {
			return HistoryResult{}, err
		}
	} else if err := s.cfg.Store.DeleteLink(ctx, org, LinkRequest, id); err != nil {
		return HistoryResult{}, err
	}
	n, err := h.RetagMemories(ctx, org, id, "", from, to)
	if err != nil { // undo the link: the request and its memories never disagree
		if to != "" {
			_ = s.cfg.Store.DeleteLink(ctx, org, LinkRequest, id)
		} else {
			_ = s.cfg.Store.AddLink(ctx, org, Link{Kind: LinkRequest, RefID: id, CustomerID: from, CreatedBy: actor, CreatedAt: s.now()})
		}
		return HistoryResult{}, err
	}
	res.Memories, res.Changed = n, true
	return res, nil
}

// moveProject moves a draft or finished project and, when it was launched, its request.
func (s *Service) moveProject(ctx context.Context, h History, org, actor, id, to string) (HistoryResult, error) {
	p, err := h.Project(ctx, org, id)
	if err != nil {
		return HistoryResult{}, err
	}
	if !p.Movable {
		return HistoryResult{}, conflict("the project is running: assign it when it has finished")
	}
	res := HistoryResult{From: p.CustomerID, To: to, RequestID: p.RequestID}
	if p.CustomerID == to {
		return res, nil
	}
	if p.CustomerID != "" && to != "" {
		return HistoryResult{}, conflict("the project already belongs to another customer: remove that customer first")
	}
	var moved HistoryResult
	if p.RequestID != "" {
		if moved, err = s.moveRequest(ctx, h, org, actor, p.RequestID, to); err != nil && !errors.Is(err, ErrNotFound) {
			return HistoryResult{}, err
		}
	}
	if err := h.SetProjectCustomer(ctx, org, id, to); err != nil {
		if moved.Changed { // put the request back where it was
			_, _ = s.moveRequest(ctx, h, org, actor, p.RequestID, moved.From)
		}
		return HistoryResult{}, err
	}
	res.Memories, res.Changed = moved.Memories, true
	return res, nil
}

// moveMemory moves one memory document. A document of a scoped request follows its request (move the request).
func (s *Service) moveMemory(ctx context.Context, h History, org, id, to string) (HistoryResult, error) {
	m, err := h.Memory(ctx, org, id)
	if err != nil {
		return HistoryResult{}, err
	}
	if m.RequestID != "" {
		rc, err := s.CustomerOfRequest(ctx, org, m.RequestID)
		if err != nil {
			return HistoryResult{}, err
		}
		if rc != "" {
			return HistoryResult{}, conflict("this memory belongs to a request of a customer: assign the request instead")
		}
	}
	res := HistoryResult{From: m.CustomerID, To: to}
	if m.CustomerID == to {
		return res, nil
	}
	if m.CustomerID != "" && to != "" {
		return HistoryResult{}, conflict("the memory already belongs to another customer: remove that customer first")
	}
	n, err := h.RetagMemories(ctx, org, "", id, m.CustomerID, to)
	if err != nil {
		return HistoryResult{}, err
	}
	res.Memories, res.Changed = n, n > 0
	return res, nil
}
