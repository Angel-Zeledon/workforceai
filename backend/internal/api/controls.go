package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/controls"
)

// Controls API (additive, v1). See docs/architecture/08-api.md section 13 and
// integrations-credentials.md section 14.5. Activating is easy (admin), lifting
// is deliberate (owner, with a written reason).

func (s *server) mountControls(r chi.Router) {
	if s.Controls == nil {
		return
	}
	r.With(s.can(auth.PermConnectionsRead)).Get("/org/controls", s.getControls)
	r.With(s.can(auth.PermControlsPause)).Put("/org/controls", s.putControls)
	r.With(s.can(auth.PermControlsKillSwitch)).Post("/org/controls/kill-switch", s.killSwitch)
	r.With(s.can(auth.PermControlsRelease)).Post("/org/controls/release", s.releaseKillSwitch)
	r.With(s.can(auth.PermControlsKillSwitch)).Put("/org/controls/tools/{tool}", s.putToolControl)
	r.With(s.can(auth.PermControlsPause)).Post("/agents/{id}/control", s.agentControl)
	r.With(s.can(auth.PermConnectionsRead)).Get("/org/operating-hours", s.getOperatingHours)
	r.With(s.can(auth.PermOrgManage)).Put("/org/operating-hours", s.putOperatingHours)
	r.With(s.can(auth.PermConnectionsRead)).Get("/org/anomaly-settings", s.getAnomalySettings)
	r.With(s.can(auth.PermOrgManage)).Put("/org/anomaly-settings", s.putAnomalySettings)
}

type controlsView struct {
	controls.OrgState
	Agents []controls.AgentControl `json:"agents"`
	// AdminCount (owners + admins): write grants need a second human only when > 1.
	AdminCount int `json:"admin_count"`
	// Viewer is the caller: what the UI may offer (release is owner-only).
	Viewer struct {
		ID         string `json:"id"`
		Role       string `json:"role"`
		CanRelease bool   `json:"can_release"`
		CanPause   bool   `json:"can_pause"`
	} `json:"viewer"`
}

func (s *server) controlsView(r *http.Request) (controlsView, error) {
	st, err := s.Controls.State(r.Context(), s.org(r))
	if err != nil {
		return controlsView{}, err
	}
	ag, err := s.Controls.Agents(r.Context(), s.org(r))
	v := controlsView{OrgState: st, Agents: ag, AdminCount: 1}
	if s.Conns != nil {
		v.AdminCount = s.Conns.AdminCount(r.Context(), s.org(r))
	}
	v.Viewer.ID, v.Viewer.Role = s.actor(r), "owner" // no auth: the demo org acts as owner
	if p, ok := auth.PrincipalFrom(r.Context()); ok && s.AuthEnabled {
		v.Viewer.ID, v.Viewer.Role = p.UserID, string(p.Role)
	}
	v.Viewer.CanRelease, v.Viewer.CanPause = s.has(r, auth.PermControlsRelease), s.has(r, auth.PermControlsPause)
	return v, err
}

func (s *server) getControls(w http.ResponseWriter, r *http.Request) {
	v, err := s.controlsView(r)
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) putControls(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode     string `json:"mode"`
		Reason   string `json:"reason"`
		Settings *struct {
			PlanReview string `json:"plan_review"`
		} `json:"settings"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.connErr(w, err)
		return
	}
	ctx, org, actor := r.Context(), s.org(r), s.actor(r)
	if body.Mode != "" {
		// read-only ON = controls:pause (checked by the route); OFF = owner only.
		if body.Mode == controls.ModeNormal && !s.has(r, auth.PermControlsRelease) {
			codeErr(w, http.StatusForbidden, "forbidden", "only the owner can lift read-only mode")
			return
		}
		if _, err := s.Controls.SetMode(ctx, org, body.Mode, actor, body.Reason); err != nil {
			s.connErr(w, err)
			return
		}
	}
	if body.Settings != nil {
		// Switching plan review off weakens a safety net: owner only.
		if body.Settings.PlanReview == controls.PlanReviewNever && !s.has(r, auth.PermControlsRelease) {
			codeErr(w, http.StatusForbidden, "forbidden", "only the owner can disable plan review")
			return
		}
		if _, err := s.Controls.SetSettings(ctx, org, controls.Settings{PlanReview: body.Settings.PlanReview}, actor); err != nil {
			s.connErr(w, err)
			return
		}
	}
	v, err := s.controlsView(r)
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) killSwitch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Level  string `json:"level"`
		Reason string `json:"reason"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.connErr(w, err)
		return
	}
	if _, err := s.Controls.KillSwitch(r.Context(), s.org(r), body.Level, body.Reason, s.actor(r)); err != nil {
		s.connErr(w, err)
		return
	}
	v, err := s.controlsView(r)
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) releaseKillSwitch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason            string `json:"reason"`
		ResumeConnections string `json:"resume_connections"` // all | none
	}
	if err := s.decode(w, r, &body); err != nil {
		s.connErr(w, err)
		return
	}
	if _, err := s.Controls.Release(r.Context(), s.org(r), body.Reason, s.actor(r), strings.EqualFold(body.ResumeConnections, "all")); err != nil {
		s.connErr(w, err)
		return
	}
	v, err := s.controlsView(r)
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}

// putToolControl is the per-tool kill switch ("email" or "email.send").
func (s *server) putToolControl(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Disabled bool   `json:"disabled"`
		Reason   string `json:"reason"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.connErr(w, err)
		return
	}
	s.toolSwitch(w, r, chi.URLParam(r, "tool"), body.Disabled, body.Reason)
}

// toolSwitch switches a tool off (admin) or back on (owner, with a reason).
func (s *server) toolSwitch(w http.ResponseWriter, r *http.Request, tool string, disabled bool, reason string) {
	if !disabled && !s.has(r, auth.PermControlsRelease) {
		codeErr(w, http.StatusForbidden, "forbidden", "only the owner can re-enable a tool")
		return
	}
	if !disabled && strings.TrimSpace(reason) == "" {
		s.connErr(w, controls.ErrNoReason)
		return
	}
	if _, err := s.Controls.SetToolDisabled(r.Context(), s.org(r), tool, disabled, reason, s.actor(r)); err != nil {
		s.connErr(w, err)
		return
	}
	v, err := s.controlsView(r)
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}

// agentControl pauses / resumes an agent. Own-agent semantics for members are
// not offered: pausing needs controls:pause (admin).
func (s *server) agentControl(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"` // pause | resume | read_only | read_write
		Drain  string `json:"drain"`  // graceful | immediate
		Reason string `json:"reason"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.connErr(w, err)
		return
	}
	ctx, org, actor, id := r.Context(), s.org(r), s.actor(r), chi.URLParam(r, "id")
	if _, err := s.Store.GetAgent(ctx, org, id); err != nil {
		s.connErr(w, err)
		return
	}
	var (
		ac  controls.AgentControl
		err error
	)
	switch body.Action {
	case "pause":
		if body.Drain != "" && body.Drain != "graceful" && body.Drain != "immediate" {
			codeErr(w, http.StatusBadRequest, "invalid", "drain must be graceful or immediate")
			return
		}
		ac, err = s.Controls.PauseAgent(ctx, org, id, body.Drain, body.Reason, actor)
	case "resume":
		ac, err = s.Controls.ResumeAgent(ctx, org, id, actor)
	case "read_only", "read_write":
		if body.Action == "read_write" && !s.has(r, auth.PermControlsRelease) {
			codeErr(w, http.StatusForbidden, "forbidden", "only the owner can lift an agent's read-only switch")
			return
		}
		ac, err = s.Controls.SetAgentReadOnly(ctx, org, id, body.Action == "read_only", actor)
	default:
		codeErr(w, http.StatusBadRequest, "invalid", "action must be pause, resume, read_only or read_write")
		return
	}
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, ac)
}
