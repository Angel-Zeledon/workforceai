package api

import (
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/billing"
	"aiworkforce/backend/internal/domain"
)

// Plans paid with ONVO Pay (internal/billing, docs/architecture/billing-onvo.md):
//
//	POST /plan/subscribe        billing:manage  {plan, period, name, email} -> {subscription_id, customer_id, publishable_key}
//	                                            the browser pays it with ONVO's SDK
//	POST /plan/confirm          billing:manage  {subscription_id} -> the plan view, once ONVO says it is paid
//	                                            (409 code "not_paid" while the payment is being confirmed)
//	POST /plan/cancel           billing:manage  {resume} -> end at the end of the paid period, or undo that
//	POST /billing/onvo/webhook  public          ONVO events, authenticated by the X-Webhook-Secret header

const onvoWebhookMaxBytes = 1 << 20

func (s *server) mountBillingPublic(r chi.Router) {
	if s.Billing == nil {
		return
	}
	r.Post("/billing/onvo/webhook", s.onvoWebhook)
}

func (s *server) onvoWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, onvoWebhookMaxBytes))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "event too large"})
		return
	}
	if err := s.Billing.HandleWebhook(r.Context(), body, r.Header.Get("X-Webhook-Secret")); err != nil {
		if errors.Is(err, billing.ErrWebhookSecret) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "bad webhook secret"})
			return
		}
		// Not answered 2xx: ONVO records the delivery as failed; the reconcile job catches up anyway.
		s.Log.Error("onvo webhook failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "try again"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"received": true})
}

// planSubscribe: POST /plan/subscribe.
func (s *server) planSubscribe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Plan   string `json:"plan"`
		Period string `json:"period"`
		Name   string `json:"name"`
		Email  string `json:"email"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	name := body.Name
	if name == "" {
		name = s.org(r)
	}
	st, err := s.Billing.Start(r.Context(), s.org(r), body.Plan, body.Period, name, body.Email)
	if err != nil {
		s.billingFail(w, err)
		return
	}
	if s.Rec != nil {
		s.Rec.Audit(r.Context(), domain.AuditLog{Actor: s.actor(r), Action: "plan.purchase_started", Entity: "plan", EntityID: s.org(r),
			Details: map[string]any{"plan": body.Plan, "period": body.Period, "subscription": st.SubscriptionID}})
	}
	writeJSON(w, http.StatusOK, st)
}

// planConfirm: POST /plan/confirm {"subscription_id": "..."}.
func (s *server) planConfirm(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SubscriptionID string `json:"subscription_id"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Billing.Confirm(r.Context(), s.org(r), body.SubscriptionID); err != nil {
		s.billingFail(w, err)
		return
	}
	s.writePlanView(w, r)
}

// planCancel: POST /plan/cancel {"resume": false}.
func (s *server) planCancel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Resume bool `json:"resume"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Billing.Cancel(r.Context(), s.org(r), body.Resume); err != nil {
		s.billingFail(w, err)
		return
	}
	s.writePlanView(w, r)
}

func (s *server) writePlanView(w http.ResponseWriter, r *http.Request) {
	v, err := s.planView(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) billingFail(w http.ResponseWriter, err error) {
	code := ""
	status := http.StatusConflict
	switch {
	case errors.Is(err, billing.ErrNotPaid):
		code = "not_paid"
	case errors.Is(err, billing.ErrSamePlan):
		code = "same_plan"
	case errors.Is(err, billing.ErrNoSubscription):
		code = "no_subscription"
	case errors.Is(err, billing.ErrForeign):
		code, status = "foreign_subscription", http.StatusForbidden
	case errors.Is(err, billing.ErrBadPlan):
		s.fail(w, errors.Join(domain.ErrInvalid, err))
		return
	default:
		// ONVO down, a bad key: the operator must see it; the person gets a plain 502.
		s.Log.Error("onvo request failed", "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "the payment provider did not answer", "code": "billing_unavailable"})
		return
	}
	writeJSON(w, status, map[string]string{"error": err.Error(), "code": code})
}
