package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/domain"
)

// mountOrgConfig registers the (additive, v1) endpoints of workflow templates,
// onboarding, organization settings, regional tone, schedules and tax-template
// examples. It is a no-op when Deps.OrgConfig is nil, so existing deployments
// and tests are unaffected. See docs/architecture/08-api.md section 11.
func (s *server) mountOrgConfig(r chi.Router) {
	if s.OrgConfig == nil {
		return
	}
	read := s.can(auth.PermAgentsRead)
	r.With(read).Get("/workflow-templates", s.listWorkflowTemplates)
	r.With(read).Get("/workflow-templates/{key}", s.getWorkflowTemplate)
	r.With(s.can(auth.PermRequestsCreate)).Post("/workflow-templates/{key}/instantiate", s.instantiateTemplate)

	r.With(read).Get("/onboarding/packs", s.listPacks)
	r.With(read).Get("/onboarding", s.onboardingStatus)
	r.With(s.can(auth.PermOrgManage)).Post("/onboarding", s.completeOnboarding)

	r.With(read).Get("/org/settings", s.getOrgSettings)
	r.With(s.can(auth.PermOrgManage)).Put("/org/settings", s.putOrgSettings)
	r.With(s.can(auth.PermAgentsWrite)).Put("/agents/{id}/tone", s.putAgentTone)

	r.With(read).Get("/schedules", s.listSchedules)
	r.With(s.can(auth.PermOrgManage)).Post("/schedules", s.createSchedule)
	r.With(s.can(auth.PermOrgManage)).Put("/schedules/{id}", s.updateSchedule)
	r.With(s.can(auth.PermOrgManage)).Delete("/schedules/{id}", s.deleteSchedule)

	r.With(read).Get("/tax-templates", s.listTaxTemplates)
}

func (s *server) locale(r *http.Request) string {
	return s.OrgConfig.Locale(r.Context(), r.URL.Query().Get("locale"))
}

func (s *server) listWorkflowTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.OrgConfig.Catalog().Workflows(s.locale(r)))
}

func (s *server) getWorkflowTemplate(w http.ResponseWriter, r *http.Request) {
	v, ok := s.OrgConfig.Catalog().Workflow(chi.URLParam(r, "key"), s.locale(r))
	if !ok {
		s.fail(w, domain.ErrNotFound)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) instantiateTemplate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Params map[string]string `json:"params"`
		Locale string            `json:"locale"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	id, err := s.OrgConfig.InstantiateTemplate(r.Context(), chi.URLParam(r, "key"), body.Params, body.Locale)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"request_id": id})
}

func (s *server) listPacks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.OrgConfig.Catalog().Packs(s.locale(r)))
}

type settingsResponse struct {
	application.OrgSettings
	Tones   []string `json:"available_tones"`
	Locales []string `json:"available_locales"`
}

func (s *server) settingsJSON(r *http.Request) (settingsResponse, error) {
	st, err := s.OrgConfig.Settings(r.Context())
	return settingsResponse{OrgSettings: st, Tones: catalog.Tones, Locales: catalog.Locales}, err
}

func (s *server) onboardingStatus(w http.ResponseWriter, r *http.Request) {
	v, err := s.settingsJSON(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) getOrgSettings(w http.ResponseWriter, r *http.Request) { s.onboardingStatus(w, r) }

func (s *server) completeOnboarding(w http.ResponseWriter, r *http.Request) {
	var body application.OnboardInput
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	res, err := s.OrgConfig.Onboard(r.Context(), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	if res.Settings.AgentTones == nil {
		res.Settings.AgentTones = map[string]string{}
	}
	writeJSON(w, 200, res)
}

func (s *server) putOrgSettings(w http.ResponseWriter, r *http.Request) {
	var body application.SettingsUpdate
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if _, err := s.OrgConfig.UpdateSettings(r.Context(), body); err != nil {
		s.fail(w, err)
		return
	}
	s.onboardingStatus(w, r)
}

func (s *server) putAgentTone(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tone string `json:"tone"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.OrgConfig.SetAgentTone(r.Context(), chi.URLParam(r, "id"), body.Tone); err != nil {
		s.fail(w, err)
		return
	}
	s.onboardingStatus(w, r)
}

func (s *server) listSchedules(w http.ResponseWriter, r *http.Request) {
	v, err := s.OrgConfig.ListSchedules(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, list(v))
}

func (s *server) createSchedule(w http.ResponseWriter, r *http.Request) {
	var body application.ScheduleInput
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.OrgConfig.CreateSchedule(r.Context(), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (s *server) updateSchedule(w http.ResponseWriter, r *http.Request) {
	var body application.ScheduleInput
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.OrgConfig.UpdateSchedule(r.Context(), chi.URLParam(r, "id"), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	if err := s.OrgConfig.DeleteSchedule(r.Context(), chi.URLParam(r, "id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) listTaxTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.OrgConfig.Catalog().TaxTemplates(r.URL.Query().Get("country"), s.locale(r)))
}
