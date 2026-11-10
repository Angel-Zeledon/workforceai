package api

import (
	"errors"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/projects"
)

// Projects mix the work of several departments in one plan (D3). A project is left out entirely (404) only when
// every node that has an agent belongs to agents whose work the person may not see: there is nothing of theirs in
// it. Otherwise the project stays readable (its structure, states, progress and cost are the organization's plan)
// and the hidden nodes are redacted: no title, error, files, review, audit, agent or task, and their approvals are
// left out. Hiding the whole project would take General's work away from the people who planned it; showing the
// hidden nodes whole would leak the private department's work.

// redactProject redacts d in place and reports whether every agent node is hidden (the project answers 404 then).
func redactProject(d *projects.Detail, sees func(agentID string) bool) (allHidden bool) {
	withAgent, hidden := 0, 0
	for i := range d.Nodes {
		n := &d.Nodes[i]
		if n.AgentID == nil || *n.AgentID == "" {
			continue
		}
		withAgent++
		if sees(*n.AgentID) {
			continue
		}
		hidden++
		n.Title, n.TitleKey, n.TitleParams, n.AgentID, n.Error, n.Files = "", "", nil, nil, nil, nil
		n.Review, n.Audit, n.TaskID, n.Acceptance, n.SkipReason, n.DelegationChain = nil, nil, "", nil, "", []string{}
	}
	d.Approvals = slices.DeleteFunc(d.Approvals, func(a projects.Approval) bool { return a.AgentID != nil && !sees(*a.AgentID) })
	if d.Estimate != nil {
		d.Estimate.ByAgent = slices.DeleteFunc(d.Estimate.ByAgent, func(e projects.EstAgent) bool { return e.AgentID != "" && !sees(e.AgentID) })
	}
	return withAgent > 0 && hidden == withAgent
}

// redactWorkspace leaves out the deliverables of hidden agents and the links that touch their artifacts, and
// reports whether every deliverable was hidden (the workspace answers 404 then).
func redactWorkspace(ws *artifacts.ProjectWorkspace, sees func(agentID string) bool) (allHidden bool) {
	gone := map[string]bool{}
	total := len(ws.Deliverables)
	ws.Deliverables = slices.DeleteFunc(ws.Deliverables, func(d artifacts.Deliverable) bool {
		if d.AgentID == "" || sees(d.AgentID) {
			return false
		}
		for _, a := range d.Artifacts {
			gone[a.ID] = true
		}
		return true
	})
	ws.Links = slices.DeleteFunc(ws.Links, func(l artifacts.Link) bool { return gone[l.From] || gone[l.To] })
	return total > 0 && len(ws.Deliverables) == 0
}

// hiddenNodes returns the ids of the nodes of d whose agent's work the person may not see.
func hiddenNodes(d projects.Detail, sees func(agentID string) bool) map[string]bool {
	out := map[string]bool{}
	for _, n := range d.Nodes {
		if n.AgentID != nil && *n.AgentID != "" && !sees(*n.AgentID) {
			out[n.ID] = true
		}
	}
	return out
}

// projectView is what the person sees of the project {id}: nil hidden = nothing hidden. all reports a project that
// is only the work of hidden agents (it answers 404). A missing project is left to the handler (ok false).
type projectView struct {
	ws     *workScope
	hidden map[string]bool
	all    bool
}

func (s *server) projectView(r *http.Request, id string) (projectView, bool, error) {
	ws := s.work(r)
	if err := ws.load(); err != nil {
		return projectView{}, false, err
	}
	pv := projectView{ws: ws}
	if ws.open() {
		return pv, true, nil
	}
	d, err := s.Projects.Get(r.Context(), id)
	if errors.Is(err, domain.ErrNotFound) {
		return pv, false, nil
	}
	if err != nil {
		return pv, false, err
	}
	pv.hidden = hiddenNodes(d, ws.agent)
	pv.all = redactProject(&d, ws.agent)
	return pv, true, nil
}

// workProject guards the routes about the project {id}: a project that is only a private office's work answers like
// a missing one (D3).
func (s *server) workProject(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		pv, ok, err := s.projectView(r, id)
		if err != nil {
			s.fail(w, err)
			return
		}
		if ok && pv.all {
			s.fail(w, notFound("project", id))
			return
		}
		if node := chi.URLParam(r, "nodeId"); node != "" && pv.hidden[node] { // retry, skip, replan of a hidden node
			s.fail(w, notFound("node", node))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// changeHidden reports whether a plan change belongs to the hidden part of a project: proposed by a hidden agent,
// about a hidden node, or adding tasks for a hidden agent.
func (pv projectView) changeHidden(c projects.PlanChange) bool {
	if pv.hidden == nil {
		return false
	}
	if c.ProposedBy == "agent" && !pv.ws.agent(c.Actor) || pv.hidden[c.SourceNodeID] {
		return true
	}
	return pv.opsHidden(c.Ops)
}

func (pv projectView) opsHidden(ops []projects.PlanChangeOp) bool {
	for _, op := range ops {
		if pv.hidden[op.NodeID] {
			return true
		}
		for _, t := range op.Tasks {
			if t.AgentID != "" && !pv.ws.agent(t.AgentID) {
				return true
			}
		}
		if op.Fields.AgentID != nil && *op.Fields.AgentID != "" && !pv.ws.agent(*op.Fields.AgentID) {
			return true
		}
	}
	return false
}

// redactHealth leaves out the risks of hidden agents and hidden nodes.
func (pv projectView) redactHealth(h *projects.Health) {
	if pv.hidden == nil {
		return
	}
	h.Risks = slices.DeleteFunc(h.Risks, func(r projects.Risk) bool {
		if r.AgentID != "" && !pv.ws.agent(r.AgentID) {
			return true
		}
		for _, n := range r.NodeIDs {
			if pv.hidden[n] {
				return true
			}
		}
		return false
	})
}
