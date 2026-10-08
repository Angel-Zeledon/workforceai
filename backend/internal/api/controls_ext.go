package api

import (
	"net/http"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/controls"
)

// Operating hours and anomaly settings (workstream A7b). Reading needs
// connections:read; writing needs org:manage (owner and admin) and is audited
// by the controls service.

func (s *server) getOperatingHours(w http.ResponseWriter, r *http.Request) {
	v, err := s.Controls.OperatingHours(r.Context(), s.org(r))
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) putOperatingHours(w http.ResponseWriter, r *http.Request) {
	var body controls.OperatingHours
	if err := s.decode(w, r, &body); err != nil {
		s.connErr(w, err)
		return
	}
	v, err := s.Controls.SetOperatingHours(r.Context(), s.org(r), body, s.actor(r))
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}

type anomalyView struct {
	controls.AnomalySettings
	Rules []string `json:"rules"`
}

func anomalyRules() []string {
	return []string{controls.RuleSpendSpike, controls.RuleToolBurst, controls.RuleRejectedBurst, controls.RuleNewRecipientSpan}
}

func (s *server) getAnomalySettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.Controls.State(r.Context(), s.org(r))
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, anomalyView{AnomalySettings: st.Settings.Anomaly, Rules: anomalyRules()})
}

func (s *server) putAnomalySettings(w http.ResponseWriter, r *http.Request) {
	var body controls.AnomalySettings
	if err := s.decode(w, r, &body); err != nil {
		s.connErr(w, err)
		return
	}
	if body.Disabled && !s.has(r, auth.PermControlsRelease) {
		codeErr(w, http.StatusForbidden, "forbidden", "only the owner can switch anomaly detection off")
		return
	}
	st, err := s.Controls.SetAnomalySettings(r.Context(), s.org(r), body, s.actor(r))
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, anomalyView{AnomalySettings: st.Settings.Anomaly, Rules: anomalyRules()})
}
