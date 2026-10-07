package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/gateway"
)

// Outbox, plan reviews, spend status and the agent-controls list: the read
// models the "Centro de control" screen needs (docs/architecture/08-api.md 13).

func (s *server) mountOutbox(r chi.Router) {
	if s.Gateway == nil {
		return
	}
	decide := s.can(auth.PermApprovalsDecide)
	r.With(s.can(auth.PermApprovalsRead)).Get("/outbox", s.listOutbox)
	r.With(decide).Patch("/outbox/{id}", s.editOutbox)
	r.With(decide).Post("/outbox/{id}/approve", s.approveOutbox)
	r.With(decide).Post("/outbox/{id}/reject", s.rejectOutbox)
	r.With(s.can(auth.PermRequestsRead)).Get("/plan-reviews", s.listPlanReviews)
	if s.Controls != nil {
		r.With(s.can(auth.PermConnectionsRead)).Get("/agent-controls", s.listAgentControls)
		r.With(s.can(auth.PermControlsKillSwitch)).Post("/org/controls/tools", s.postToolControl)
	}
	r.With(s.can(auth.PermMetricsRead)).Get("/spend-limits/status", s.spendStatus)
}

func (s *server) listOutbox(w http.ResponseWriter, r *http.Request) {
	items, err := s.Gateway.Outbox(r.Context(), s.org(r), r.URL.Query().Get("status"))
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *server) editOutbox(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To      *[]string `json:"to"`
		Cc      *[]string `json:"cc"`
		Subject *string   `json:"subject"`
		Body    *string   `json:"body"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.connErr(w, err)
		return
	}
	it, err := s.Gateway.OutboxEdit(r.Context(), s.org(r), chi.URLParam(r, "id"), gateway.OutboxPatch{To: body.To, Cc: body.Cc, Subject: body.Subject, Body: body.Body})
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, it)
}

func (s *server) outboxErr(w http.ResponseWriter, err error) {
	var de *gateway.DeniedError
	switch {
	case errors.Is(err, gateway.ErrVersionMismatch):
		codeErr(w, http.StatusConflict, "version_mismatch", err.Error())
	case errors.As(err, &de):
		codeErr(w, http.StatusConflict, de.Code, "the action was denied when it was about to be scheduled: "+de.Code)
	default:
		s.connErr(w, err)
	}
}

func (s *server) approveOutbox(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version int `json:"version"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.connErr(w, err)
		return
	}
	ctx := r.Context()
	it, err := s.Gateway.OutboxApprove(ctx, s.org(r), chi.URLParam(r, "id"), body.Version, s.actor(r), func(approvalID string) error {
		_, e := s.Approvals.Decide(ctx, approvalID, "approve", "approved from the outbox")
		return e
	})
	if err != nil {
		s.outboxErr(w, err)
		return
	}
	writeJSON(w, 200, it)
}

func (s *server) rejectOutbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	it, err := s.Gateway.OutboxReject(ctx, s.org(r), chi.URLParam(r, "id"), s.actor(r), func(approvalID string) error {
		_, e := s.Approvals.Decide(ctx, approvalID, "reject", "rejected from the outbox")
		return e
	})
	if err != nil {
		s.outboxErr(w, err)
		return
	}
	writeJSON(w, 200, it)
}

// ---- plan reviews (shape shared by GET /plan-reviews and /requests/{id}/plan) ----

func (s *server) planShape(r *http.Request, pv gateway.PlanView) map[string]any {
	ctx, org := r.Context(), s.org(r)
	text := ""
	if rq, err := s.Store.GetRequest(ctx, org, pv.RequestID); err == nil {
		text = rq.Text
	}
	status := "plan_ready"
	switch pv.State {
	case gateway.PlanApproved:
		status = "approved"
	case gateway.PlanRejected:
		status = "rejected"
	}
	var p50, p90 float64
	if s.Orch != nil {
		if est, err := s.Orch.EstimateFor(ctx, pv.RequestID); err == nil {
			p50, p90 = (est.Total.MinUSD+est.Total.MaxUSD)/2, est.Total.MaxUSD
		}
	}
	return map[string]any{
		"id": pv.RequestID, "request_id": pv.RequestID, "request_text": text, "status": status,
		"tasks": pv.Tasks, "reachable_connections": pv.ReachableConnections,
		"approvals_expected": pv.ApprovalsExpected,
		"est_cost_usd":       map[string]float64{"p50": p50, "p90": p90},
		"est_duration_s":     map[string]float64{"p50": 0},
		"review_required":    pv.TouchesWrites, "touches_writes": pv.TouchesWrites,
		"no_external_actions": pv.NoExternalActions, "removed_task_ids": pv.RemovedTaskIDs, "note": pv.Note,
	}
}

func (s *server) listPlanReviews(w http.ResponseWriter, r *http.Request) {
	views := s.Gateway.PlanList(s.org(r))
	out := make([]map[string]any, 0, len(views))
	for _, v := range views {
		out = append(out, s.planShape(r, v))
	}
	writeJSON(w, 200, map[string]any{"items": out})
}

func (s *server) listAgentControls(w http.ResponseWriter, r *http.Request) {
	l, err := s.Controls.Agents(r.Context(), s.org(r))
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": l})
}

// postToolControl is POST /org/controls/tools {tool, disabled, reason} (the
// same switch as PUT /org/controls/tools/{tool}).
func (s *server) postToolControl(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tool     string `json:"tool"`
		Disabled bool   `json:"disabled"`
		Reason   string `json:"reason"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.connErr(w, err)
		return
	}
	s.toolSwitch(w, r, body.Tool, body.Disabled, body.Reason)
}

// spendStatus maps the existing budget caps (cost control, GET /budget) to the
// unified list the control center shows. Editing caps stays in /agents/{id}/budget.
func (s *server) spendStatus(w http.ResponseWriter, r *http.Request) {
	items := []map[string]any{}
	if s.Orch != nil {
		if st, err := s.Orch.Budget().Status(r.Context()); err == nil {
			if st.Org.BudgetUSD > 0 {
				items = append(items, map[string]any{"id": "org", "scope_type": "org", "scope_id": nil, "period": "total",
					"limit_usd": st.Org.BudgetUSD, "used_usd": st.Org.UsedUSD, "on_hit": "block"})
			}
			for _, a := range st.Agents {
				if a.CapUSD > 0 {
					items = append(items, map[string]any{"id": "agent:" + a.AgentID, "scope_type": "agent", "scope_id": a.AgentID, "period": "month",
						"limit_usd": a.CapUSD, "used_usd": a.SpentUSD, "on_hit": "pause_and_ask"})
				}
			}
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
