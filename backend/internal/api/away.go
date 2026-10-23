package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/away"
	"aiworkforce/backend/internal/policy"
)

// "While you were away" (B3, docs/architecture/away-summary.md, 08-api.md). The summary is read-only and
// deterministic (no model call, so no spend and nothing the kill switch or read-only mode has to stop); the
// heartbeat and dismiss only move the caller's own last-seen mark. Approvals are listed as "act now" only when the
// caller may decide them, and left out entirely without approvals:read.

func (s *server) mountAway(r chi.Router) {
	if s.Away == nil {
		return
	}
	read := s.can(auth.PermActivityRead)
	r.With(read).Get("/away-summary", s.awaySummary)
	r.With(read).Post("/away-summary/heartbeat", s.awayHeartbeat)
	r.With(read).Post("/away-summary/dismiss", s.awayDismiss)
}

// awayViewer is the caller as the summary sees it. Without auth the demo acts as the owner (like Approvals.Decide).
func (s *server) awayViewer(r *http.Request) away.Viewer {
	ctx := r.Context()
	v := away.Viewer{UserID: application.ActorFrom(ctx, "user"), Role: application.ActorRoleFrom(ctx, policy.RoleOwner), CanDecide: true, ReadApprovals: true}
	if s.AuthEnabled {
		p, _ := auth.PrincipalFrom(ctx)
		v.CanDecide, v.ReadApprovals = p.Can(auth.PermApprovalsDecide), p.Can(auth.PermApprovalsRead)
	}
	return v
}

// GET /away-summary?min_hours= — what happened since the caller's last visit. min_hours (0..168, default 8)
// only decides "show"; the summary is computed either way.
func (s *server) awaySummary(w http.ResponseWriter, r *http.Request) {
	minHours := -1.0
	if raw := r.URL.Query().Get("min_hours"); raw != "" {
		h, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			codeErr(w, http.StatusBadRequest, "invalid", "min_hours must be a number of hours")
			return
		}
		minHours = h
	}
	sum, err := s.Away.Summary(r.Context(), s.awayViewer(r), minHours)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

// POST /away-summary/heartbeat — the caller is looking now (the frontend sends it while the tab is visible).
func (s *server) awayHeartbeat(w http.ResponseWriter, r *http.Request) {
	m, err := s.Away.Heartbeat(r.Context(), s.awayViewer(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// POST /away-summary/dismiss — close the summary until the next absence.
func (s *server) awayDismiss(w http.ResponseWriter, r *http.Request) {
	m, err := s.Away.Dismiss(r.Context(), s.awayViewer(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}
