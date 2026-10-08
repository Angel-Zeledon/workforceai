package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/push"
)

// Web Push API (additive, v1): docs/architecture/08-api.md. A push only tells a
// person that an approval is waiting (title + risk + id); deciding it still
// needs the normal authenticated POST /approvals/{id}/decision.

func (s *server) mountPush(r chi.Router) {
	// The config route always exists so the client can learn that push is off.
	r.With(s.can(auth.PermApprovalsRead)).Get("/push/config", s.pushConfig)
	if s.Push == nil {
		return
	}
	r.With(s.can(auth.PermApprovalsRead)).Post("/push/subscriptions", s.pushSubscribe)
	r.With(s.can(auth.PermApprovalsRead)).Delete("/push/subscriptions", s.pushUnsubscribe)
}

func (s *server) pushConfig(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil {
		writeJSON(w, 200, map[string]any{"enabled": false})
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": true, "public_key": s.Push.PublicKey()})
}

type pushSubBody struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

func (s *server) pushSubscribe(w http.ResponseWriter, r *http.Request) {
	var b pushSubBody
	if err := s.decode(w, r, &b); err != nil {
		s.fail(w, err)
		return
	}
	sub, err := s.Push.Subscribe(r.Context(), push.Subscription{OrgID: s.org(r), UserID: s.actor(r), Endpoint: b.Endpoint,
		P256dh: b.Keys.P256dh, Auth: b.Keys.Auth, UserAgent: r.UserAgent()})
	if err != nil {
		s.fail(w, err)
		return
	}
	s.auditPush(r, "push.subscribed", sub.ID)
	writeJSON(w, 201, sub)
}

func (s *server) pushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Endpoint string `json:"endpoint"`
	}
	if err := s.decode(w, r, &b); err != nil {
		s.fail(w, err)
		return
	}
	if b.Endpoint == "" {
		s.fail(w, domain.ErrInvalid)
		return
	}
	if err := s.Push.Unsubscribe(r.Context(), s.org(r), s.actor(r), b.Endpoint); err != nil {
		s.fail(w, err)
		return
	}
	s.auditPush(r, "push.unsubscribed", "")
	w.WriteHeader(http.StatusNoContent)
}

// auditPush records who (un)subscribed a device; never the endpoint or keys.
func (s *server) auditPush(r *http.Request, action, id string) {
	if s.Rec == nil {
		return
	}
	s.Rec.Audit(r.Context(), domain.AuditLog{Actor: s.actor(r), Action: action, Entity: "push_subscription", EntityID: id})
}
