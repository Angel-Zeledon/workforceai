package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// mountCost registers the cost-control endpoints (additive, v1). See
// docs/architecture/08-api.md section 12.
func (s *server) mountCost(r chi.Router) {
	r.With(s.can(auth.PermMetricsRead)).Get("/budget", s.getBudget)
	r.With(s.can(auth.PermMetricsRead)).Get("/costs/breakdown", s.costBreakdown)
	r.With(s.can(auth.PermRequestsRead)).Get("/requests/{id}/cost", s.requestCost)
	r.With(s.can(auth.PermRequestsRead)).Get("/requests/{id}/estimate", s.requestEstimate)
	r.With(s.can(auth.PermRequestsCreate)).Post("/requests/{id}/confirm", s.confirmEstimate)
	// Raising a cap spends money: same privilege as deciding approvals.
	r.With(s.can(auth.PermApprovalsDecide)).Put("/requests/{id}/budget", s.putRequestBudget)
	r.With(s.can(auth.PermApprovalsDecide)).Put("/agents/{id}/budget", s.putAgentBudget)
}

func (s *server) getBudget(w http.ResponseWriter, r *http.Request) {
	st, err := s.Orch.Budget().Status(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, st)
}

func (s *server) costBreakdown(w http.ResponseWriter, r *http.Request) {
	b, err := s.Queries.CostBreakdown(r.Context(), r.URL.Query().Get("request_id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, b)
}

func (s *server) requestCost(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.Store.GetRequest(r.Context(), s.org(r), id); err != nil {
		s.fail(w, err)
		return
	}
	b, err := s.Queries.CostBreakdown(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, b)
}

func (s *server) requestEstimate(w http.ResponseWriter, r *http.Request) {
	e, err := s.Orch.EstimateFor(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, e)
}

func (s *server) confirmEstimate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Decision     string  `json:"decision"` // "proceed" | "cancel"
		BudgetCapUSD float64 `json:"budget_cap_usd"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if body.Decision != "proceed" && body.Decision != "cancel" {
		s.fail(w, errInvalid("decision must be proceed or cancel"))
		return
	}
	ctx := r.Context() // the tenant middleware already set the actor
	if err := s.Orch.Budget().Confirm(ctx, chi.URLParam(r, "id"), body.Decision == "proceed", body.BudgetCapUSD); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok", "decision": body.Decision})
}

func (s *server) putRequestBudget(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BudgetCapUSD float64 `json:"budget_cap_usd"` // 0 removes the cap
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	id := chi.URLParam(r, "id")
	ctx := r.Context() // the tenant middleware already set the actor
	if err := s.Orch.Budget().SetCap(ctx, domain.ScopeRequest, id, body.BudgetCapUSD); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"request_id": id, "budget_cap_usd": body.BudgetCapUSD})
}

func (s *server) putAgentBudget(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MonthlyBudgetUSD float64 `json:"monthly_budget_usd"` // 0 removes the explicit cap
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	id := chi.URLParam(r, "id")
	ctx := r.Context() // the tenant middleware already set the actor
	if err := s.Orch.Budget().SetCap(ctx, domain.ScopeAgent, id, body.MonthlyBudgetUSD); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"agent_id": id, "monthly_budget_usd": body.MonthlyBudgetUSD})
}

// requestWithCost is GET /requests/{id} plus the cost-control fields (additive).
type requestWithCost struct {
	application.RequestDetail
	BudgetCapUSD float64              `json:"budget_cap_usd"`
	Estimate     *domain.CostEstimate `json:"estimate,omitempty"`
}

func (s *server) withCost(r *http.Request, d application.RequestDetail) requestWithCost {
	out := requestWithCost{RequestDetail: d}
	if caps, err := s.Store.ListBudgetCaps(r.Context(), s.org(r)); err == nil {
		for _, c := range caps {
			if c.Scope == domain.ScopeRequest && c.ScopeID == d.ID {
				out.BudgetCapUSD = c.CapUSD
			}
		}
	}
	if e, ok := s.Orch.Budget().Estimate(r.Context(), d.ID); ok {
		out.Estimate = &e
	}
	return out
}

func errInvalid(msg string) error { return errors.Join(domain.ErrInvalid, errors.New(msg)) }
