package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/roles"
)

// maxAgentsPerOrg keeps an office (and the shared open demo) readable.
const maxAgentsPerOrg = 20

// roleTemplateView is the localized catalog entry of a role template.
type roleTemplateView struct {
	ID                 string         `json:"id"`
	Version            int            `json:"version"`
	Category           string         `json:"category"`
	RiskTier           string         `json:"risk_tier"`
	SensitiveData      []string       `json:"sensitive_data"`
	Seed               bool           `json:"seed"`
	Title              string         `json:"title"`
	Description        string         `json:"description"`
	Responsibilities   []string       `json:"responsibilities"`
	Area               string         `json:"area"`
	Disclaimers        []string       `json:"disclaimers"`
	OutOfScope         []string       `json:"out_of_scope"`
	Display            roles.Display  `json:"display"`
	Tools              []string       `json:"tools"`
	Autonomy           roles.Autonomy `json:"autonomy"`
	Topic              string         `json:"topic"`
	SuggestedArtifacts []string       `json:"suggested_artifacts"`
	Hired              int            `json:"hired"`
	Locale             string         `json:"locale"`
}

func (s *server) mountRoles(r chi.Router) {
	r.With(s.can(auth.PermAgentsRead)).Get("/role-templates", s.listRoleTemplates)
	r.With(s.can(auth.PermAgentsRead)).Get("/role-templates/{id}", s.getRoleTemplate)
	r.With(s.can(auth.PermAgentsWrite)).Post("/agents/from-template", s.hireFromTemplate)
}

func templateLocale(r *http.Request) string {
	if q := r.URL.Query().Get("locale"); q != "" {
		return roles.Locale(q)
	}
	return roles.Locale(r.Header.Get("Accept-Language"))
}

func toTemplateView(t roles.Template, loc string, hired int) roleTemplateView {
	x := t.Text(loc)
	nz := func(v []string) []string {
		if v == nil {
			return []string{}
		}
		return v
	}
	return roleTemplateView{ID: t.ID, Version: t.Version, Category: t.Category, RiskTier: t.RiskTier, SensitiveData: nz(t.SensitiveData),
		Seed: t.Seed != nil, Title: x.Title, Description: x.Description, Responsibilities: nz(x.Responsibilities), Area: x.Area,
		Disclaimers: nz(x.Disclaimers), OutOfScope: nz(x.OutOfScope), Display: t.Display, Tools: nz(t.Tools), Autonomy: t.Autonomy,
		Topic: t.Routing.Topic, SuggestedArtifacts: nz(t.SuggestedArtifacts), Hired: hired, Locale: loc}
}

func (s *server) hiredByRole(r *http.Request) map[string]int {
	out := map[string]int{}
	agents, err := s.Store.ListAgents(r.Context(), s.org(r))
	if err != nil {
		return out
	}
	for _, a := range agents {
		out[a.Role]++
	}
	return out
}

func (s *server) listRoleTemplates(w http.ResponseWriter, r *http.Request) {
	loc, hired := templateLocale(r), s.hiredByRole(r)
	out := []roleTemplateView{}
	for _, t := range roles.All() {
		out = append(out, toTemplateView(t, loc, hired[t.ID]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *server) getRoleTemplate(w http.ResponseWriter, r *http.Request) {
	t, ok := roles.Get(chi.URLParam(r, "id"))
	if !ok {
		s.fail(w, domain.ErrNotFound)
		return
	}
	writeJSON(w, http.StatusOK, toTemplateView(t, templateLocale(r), s.hiredByRole(r)[t.ID]))
}

func (s *server) hireFromTemplate(w http.ResponseWriter, r *http.Request) {
	var body application.HireInput
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if strings.TrimSpace(body.TemplateID) == "" {
		s.fail(w, fmt.Errorf("%w: template_id is required", domain.ErrInvalid))
		return
	}
	total := 0
	for _, n := range s.hiredByRole(r) {
		total += n
	}
	if total >= maxAgentsPerOrg {
		s.fail(w, fmt.Errorf("%w: the office is full (%d agents)", domain.ErrConflict, maxAgentsPerOrg))
		return
	}
	a, err := s.Orch.HireFromTemplate(r.Context(), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"agent": a, "template_id": body.TemplateID})
}
