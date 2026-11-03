package api

import (
	"context"
	"errors"
	"net/http"

	"aiworkforce/backend/internal/billing"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/notify"
	"aiworkforce/backend/internal/policy"
)

// Plans: Free, Pro and Max (internal/policy/plans.go, docs/architecture/approvals-notify.md section 3.1).

// planLimits returns the limits of the plan of org (none when plans are not wired).
func (s *server) planLimits(ctx context.Context, org string) policy.Tier {
	if s.Notify == nil || s.Notify.Plans == nil {
		return policy.Tier{ID: policy.PlanPaid}
	}
	return s.Notify.Plans.Limits(ctx, org)
}

// planUsed is what the organization has of each counted limit. -1 = not known on this deployment.
type planUsed struct {
	Agents         int   `json:"agents"`
	ActiveProjects int   `json:"active_projects"`
	Schedules      int   `json:"schedules"`
	Seats          int   `json:"seats"`
	StorageBytes   int64 `json:"storage_bytes"`
}

type planView struct {
	Plan      string        `json:"plan"`
	Limits    policy.Tier   `json:"limits"`
	Used      planUsed      `json:"used"`
	Usage     notify.Usage  `json:"usage"` // the AI allowance of the month (GET /plan/usage)
	Catalog   []policy.Tier `json:"catalog"`
	SelfServe bool          `json:"self_serve"`
	// Billing is how the plan changes: "onvo" (paid with ONVO Pay), "self_serve" (PUT /plan, nothing charged) or
	// "none" (the platform's team changes it).
	Billing string `json:"billing"`
	// Subscription (billing "onvo"): whether it pays, a cancellation at the end of the period, a failed payment.
	Subscription *billing.Status `json:"subscription,omitempty"`
	// Available (billing "onvo") are the plans and periods that can be bought: "pro_monthly", "max_yearly"...
	Available []string `json:"available,omitempty"`
}

func (s *server) planUsed(r *http.Request, org string) planUsed {
	ctx := r.Context()
	u := planUsed{Agents: -1, ActiveProjects: -1, Schedules: -1, Seats: -1, StorageBytes: -1}
	if a, err := s.Store.ListAgents(ctx, org); err == nil {
		u.Agents = len(a)
	}
	if s.Projects != nil {
		if n, err := s.Projects.ActiveCount(ctx); err == nil {
			u.ActiveProjects = n
		}
	}
	if s.OrgConfig != nil {
		if v, err := s.OrgConfig.ListSchedules(ctx); err == nil {
			u.Schedules = len(v)
		}
	}
	if s.Auth != nil {
		if n, err := s.Auth.Seats(ctx, org); err == nil {
			u.Seats = n
		}
	}
	if s.Drive != nil {
		if d, err := s.Drive.Usage(ctx); err == nil {
			u.StorageBytes = d.UsedBytes
		}
	}
	return u
}

func (s *server) planView(r *http.Request) (planView, error) {
	org := s.org(r)
	usage, err := s.notifyAPI().UsageOf(r.Context(), org)
	if err != nil {
		return planView{}, err
	}
	lim := s.planLimits(r.Context(), org)
	v := planView{Plan: usage.Plan, Limits: lim, Used: s.planUsed(r, org), Usage: usage, Catalog: policy.Catalog,
		SelfServe: s.PlanSelfServe && s.Billing == nil, Billing: "none"}
	switch {
	case s.Billing != nil:
		st := s.Billing.StatusOf(r.Context(), org)
		v.Billing, v.Subscription, v.Available = "onvo", &st, s.Billing.Available()
	case s.PlanSelfServe:
		v.Billing = "self_serve"
	}
	return v, nil
}

// getOrgPlan: GET /plan.
func (s *server) getOrgPlan(w http.ResponseWriter, r *http.Request) {
	v, err := s.planView(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// putOrgPlan: PUT /plan {"plan":"pro"}. Audited. Off unless PLAN_SELF_SERVE: nothing charges anyone yet.
func (s *server) putOrgPlan(w http.ResponseWriter, r *http.Request) {
	if s.Billing != nil {
		s.fail(w, errors.Join(domain.ErrForbidden, errors.New("plans are paid on this deployment: use POST /plan/subscribe")))
		return
	}
	if !s.PlanSelfServe {
		s.fail(w, errors.Join(domain.ErrForbidden, errors.New("plan changes are not self-serve on this deployment: contact sales")))
		return
	}
	var body struct {
		Plan string `json:"plan"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	org, actor := s.org(r), s.actor(r)
	before := s.planLimits(r.Context(), org).ID
	if _, err := s.Notify.Plans.Change(r.Context(), org, body.Plan, actor); err != nil {
		if errors.Is(err, notify.ErrUnknownPlan) {
			err = errors.Join(domain.ErrInvalid, err)
		}
		s.fail(w, err)
		return
	}
	if s.Rec != nil {
		s.Rec.Audit(r.Context(), domain.AuditLog{Actor: actor, Action: "plan.changed", Entity: "plan", EntityID: org,
			Details: map[string]any{"from": before, "to": body.Plan}})
	}
	v, err := s.planView(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// listPlans: GET /plans (public): the catalog, for the pricing pages.
func (s *server) listPlans(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"plans": policy.Catalog})
}
