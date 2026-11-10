package api

import (
	"slices"

	"aiworkforce/backend/internal/artifacts"
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
