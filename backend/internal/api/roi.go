package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
)

// mountROI registers the AI payroll and return report (B15, docs/architecture/ai-payroll-roi.md):
//
//	GET /roi?window_days=   metrics:read   AI cost per agent next to the ESTIMATED human time (methodology included)
//	GET /settings/roi       metrics:read   the org's model: hourly rate (optional), minutes per task, per role
//	PUT /settings/roi       org:manage     change it (owner/admin, audited)
//
// Read-only for agents: nothing here starts work or changes autonomy. No-op when Deps.ROI is nil.
func (s *server) mountROI(r chi.Router) {
	if s.ROI == nil {
		return
	}
	r.With(s.can(auth.PermMetricsRead)).Get("/roi", s.getROI)
	r.With(s.can(auth.PermMetricsRead)).Get("/settings/roi", s.getROISettings)
	r.With(s.can(auth.PermOrgManage)).Put("/settings/roi", s.putROISettings)
}

func (s *server) getROI(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("window_days"))
	v, err := s.ROI.Report(r.Context(), days)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// roiSettingsView says whether the model was ever saved: until then the minutes are an assumption.
type roiSettingsView struct {
	application.ROISettings
	Saved            bool `json:"saved"`
	DefaultAssumed   int  `json:"default_assumed_minutes"`
	MaxMinutes       int  `json:"max_minutes"`
	MaxHourlyRateUSD int  `json:"max_hourly_rate_usd"`
}

func (s *server) roiView(v application.ROISettings, saved bool) roiSettingsView {
	return roiSettingsView{ROISettings: v, Saved: saved, DefaultAssumed: application.DefaultROIMinutes,
		MaxMinutes: application.MaxROIMinutes, MaxHourlyRateUSD: application.MaxROIHourlyRateUSD}
}

func (s *server) getROISettings(w http.ResponseWriter, r *http.Request) {
	v, saved, err := s.ROI.Settings(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.roiView(v, saved))
}

func (s *server) putROISettings(w http.ResponseWriter, r *http.Request) {
	var in application.ROISettings
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.ROI.PutSettings(r.Context(), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.roiView(v, true))
}
