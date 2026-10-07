package gateway

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/controls"
)

// Plan review states.
const (
	PlanPending  = "pending"
	PlanApproved = "approved"
	PlanRejected = "rejected"
)

type planReview struct {
	pf         application.PlanPreflight
	ch         chan application.PlanDecision
	removed    []string
	noExternal bool
	note       string
}

type planStore struct {
	mu    sync.Mutex
	items map[string]*planReview
}

var _ application.PlanReviewer = (*Gateway)(nil)

func (g *Gateway) plans() *planStore {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.planStore == nil {
		g.planStore = &planStore{items: map[string]*planReview{}}
	}
	return g.planStore
}

func planKey(org, req string) string { return org + "/" + req }

// Begin implements application.PlanReviewer (owner decision 7: a plan that can
// reach a connection with write capabilities is reviewed before anything runs).
func (g *Gateway) Begin(ctx context.Context, org, requestID string, tasks []application.PlanTaskInfo) (application.PlanPreflight, bool, <-chan application.PlanDecision, error) {
	pf := application.PlanPreflight{RequestID: requestID, State: PlanPending, Tasks: tasks, ReachableConnections: []application.ReachableConnection{}}
	pf.ApprovalsExpected.ReasonKeys = []string{}
	seen := map[string]bool{}
	for _, t := range tasks {
		gs, err := g.Conns.Store.ListGrantsByAgent(ctx, org, t.AgentID)
		if err != nil {
			return pf, true, nil, err
		}
		for _, gr := range gs {
			if gr.Status != connections.GrantActive && gr.Status != connections.GrantPendingApproval {
				continue
			}
			c, err := g.Conns.Store.GetConnection(ctx, org, gr.ConnectionID)
			if err != nil || c.Status == connections.StatusRevoked {
				continue
			}
			key := t.AgentID + "/" + c.ID
			if seen[key] {
				continue
			}
			seen[key] = true
			m := g.Conns.Manifests()[c.Provider]
			rc := application.ReachableConnection{ConnectionID: c.ID, Label: c.Label, AgentID: t.AgentID, Capabilities: gr.Capabilities}
			for _, cp := range gr.Capabilities {
				spec := m.Capabilities[cp]
				if spec.SideEffects {
					rc.Writes, pf.TouchesWrites = true, true
				}
				if spec.AlwaysApproval && !slices.Contains(pf.ApprovalsExpected.ReasonKeys, "send_email") {
					pf.ApprovalsExpected.ReasonKeys = append(pf.ApprovalsExpected.ReasonKeys, "send_email")
				}
			}
			pf.ReachableConnections = append(pf.ReachableConnections, rc)
		}
	}
	sort.Slice(pf.ReachableConnections, func(i, j int) bool {
		a, b := pf.ReachableConnections[i], pf.ReachableConnections[j]
		return a.AgentID+a.ConnectionID < b.AgentID+b.ConnectionID
	})
	if len(pf.ApprovalsExpected.ReasonKeys) > 0 {
		pf.ApprovalsExpected.Min = 1
	}
	required := true // fail closed when the setting cannot be read
	if st, err := g.Controls.State(ctx, org); err == nil {
		switch st.Settings.PlanReview {
		case controls.PlanReviewNever:
			required = false
		case controls.PlanReviewAlways:
			required = true
		default:
			required = pf.TouchesWrites
		}
	}
	if !required {
		return pf, false, nil, nil
	}
	ps := g.plans()
	r := &planReview{pf: pf, ch: make(chan application.PlanDecision, 1)}
	ps.mu.Lock()
	ps.items[planKey(org, requestID)] = r
	ps.mu.Unlock()
	g.Emit(ctx, org, "plan.review_requested", map[string]any{"request_id": requestID, "preflight": pf})
	g.Audit(ctx, org, "system", "plan.review_requested", "request", requestID, map[string]any{"touches_writes": pf.TouchesWrites, "connections": len(pf.ReachableConnections)})
	return pf, true, r.ch, nil
}

// Forget implements application.PlanReviewer.
func (g *Gateway) Forget(org, requestID string) {
	ps := g.plans()
	ps.mu.Lock()
	delete(ps.items, planKey(org, requestID))
	ps.mu.Unlock()
}

// PlanView is the API view of a review.
type PlanView struct {
	application.PlanPreflight
	RemovedTaskIDs    []string `json:"removed_task_ids"`
	NoExternalActions bool     `json:"no_external_actions"`
}

func (g *Gateway) planFor(org, req string) (*planReview, error) {
	ps := g.plans()
	ps.mu.Lock()
	defer ps.mu.Unlock()
	r, ok := ps.items[planKey(org, req)]
	if !ok {
		return nil, connections.ErrNotFound
	}
	return r, nil
}

func (r *planReview) view() PlanView {
	pf := r.pf
	pf.Note = r.note
	return PlanView{PlanPreflight: pf, RemovedTaskIDs: append([]string{}, r.removed...), NoExternalActions: r.noExternal}
}

// PlanList returns the pending reviews of an organization.
func (g *Gateway) PlanList(org string) []PlanView {
	ps := g.plans()
	ps.mu.Lock()
	defer ps.mu.Unlock()
	out := []PlanView{}
	for k, r := range ps.items {
		if len(k) > len(org) && k[:len(org)+1] == org+"/" {
			out = append(out, r.view())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RequestID < out[j].RequestID })
	return out
}

// PlanGet returns the pending review of a request.
func (g *Gateway) PlanGet(org, req string) (PlanView, error) {
	r, err := g.planFor(org, req)
	if err != nil {
		return PlanView{}, err
	}
	ps := g.plans()
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return r.view(), nil
}

// PlanPatch edits the review: remove tasks, add a note, mark "no external actions".
func (g *Gateway) PlanPatch(org, req string, remove []string, noExternal *bool, note *string) (PlanView, error) {
	r, err := g.planFor(org, req)
	if err != nil {
		return PlanView{}, err
	}
	ps := g.plans()
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if r.pf.State != PlanPending {
		return PlanView{}, fmt.Errorf("%w: plan is %s", connections.ErrConflict, r.pf.State)
	}
	for _, id := range remove {
		known := false
		for _, t := range r.pf.Tasks {
			known = known || t.ID == id
		}
		if !known {
			return PlanView{}, fmt.Errorf("%w: unknown task %q", connections.ErrInvalid, id)
		}
		if !slices.Contains(r.removed, id) {
			r.removed = append(r.removed, id)
		}
	}
	if noExternal != nil {
		r.noExternal = *noExternal
	}
	if note != nil {
		r.note = *note
	}
	return r.view(), nil
}

// PlanDecide approves or rejects; the orchestrator is waiting on the channel.
func (g *Gateway) PlanDecide(ctx context.Context, org, req, actor string, approve bool) (PlanView, error) {
	r, err := g.planFor(org, req)
	if err != nil {
		return PlanView{}, err
	}
	ps := g.plans()
	ps.mu.Lock()
	if r.pf.State != PlanPending {
		ps.mu.Unlock()
		return PlanView{}, fmt.Errorf("%w: plan is %s", connections.ErrConflict, r.pf.State)
	}
	r.pf.State = PlanApproved
	if !approve {
		r.pf.State = PlanRejected
	}
	d := application.PlanDecision{Approved: approve, RemoveTaskIDs: append([]string{}, r.removed...), NoExternalActions: r.noExternal, Note: r.note}
	v := r.view()
	delete(ps.items, planKey(org, req))
	ps.mu.Unlock()
	r.ch <- d
	ev := "plan.approved"
	if !approve {
		ev = "plan.rejected"
	}
	g.Emit(ctx, org, ev, map[string]any{"request_id": req, "preflight": v.PlanPreflight})
	g.Audit(ctx, org, actor, ev, "request", req, map[string]any{"removed_tasks": len(d.RemoveTaskIDs), "no_external_actions": d.NoExternalActions})
	return v, nil
}
