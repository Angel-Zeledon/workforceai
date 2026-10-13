package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/webtool"
)

// Web tool API (docs/architecture/agent-web.md). The setting is owner/admin only (org:manage) and audited.
// Optional: no-op when Deps.Web is nil.
func (s *server) mountWeb(r chi.Router) {
	if s.Web == nil {
		return
	}
	r.With(s.can(auth.PermOrgManage)).Get("/settings/web", s.getWebSettings)
	r.With(s.can(auth.PermOrgManage)).Put("/settings/web", s.putWebSettings)
}

type webSettingsView struct {
	Enabled         bool     `json:"enabled"`
	Mode            string   `json:"mode"`
	AllowedDomains  []string `json:"allowed_domains"`
	BlockedDomains  []string `json:"blocked_domains"`
	MaxCallsPerTask int      `json:"max_calls_per_task"`
	MaxCallsPerDay  int      `json:"max_calls_per_day"`
	// SearchAvailable: a search provider is configured on the server (fetch works without it).
	SearchAvailable bool `json:"search_available"`
	// OperatorRestricted: the operator limits reachable hosts (WEB_ALLOWED_HOSTS); the organization can only narrow.
	OperatorRestricted bool `json:"operator_restricted"`
	// Platform ceiling of calls per day.
	CeilingCallsPerDay int `json:"ceiling_calls_per_day"`
	DefaultCallsPerDay int `json:"default_calls_per_day"`
	DefaultCallsTask   int `json:"default_calls_per_task"`
	// Usage today.
	SearchesToday int `json:"searches_today"`
	FetchesToday  int `json:"fetches_today"`
	CallsToday    int `json:"calls_today"`
	DayLimit      int `json:"day_limit"`
}

func (s *server) webView(r *http.Request, st webtool.Settings) (webSettingsView, error) {
	u, err := s.Web.Usage(r.Context(), s.org(r))
	if err != nil {
		return webSettingsView{}, err
	}
	return webSettingsView{Enabled: st.Enabled, Mode: st.Mode, AllowedDomains: st.AllowedDomains, BlockedDomains: st.BlockedDomains,
		MaxCallsPerTask: st.MaxCallsPerTask, MaxCallsPerDay: st.MaxCallsPerDay, SearchAvailable: s.Web.SearchAvailable(),
		OperatorRestricted: len(s.Web.OperatorHosts) > 0, CeilingCallsPerDay: s.Web.DayCeiling(),
		DefaultCallsPerDay: webtool.DefaultMaxCallsPerDay, DefaultCallsTask: webtool.DefaultMaxCallsPerTask,
		SearchesToday: u.Searches, FetchesToday: u.Fetches, CallsToday: u.Calls, DayLimit: u.DayLimit}, nil
}

func (s *server) getWebSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.Web.Settings(r.Context(), s.org(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.webView(r, st)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) putWebSettings(w http.ResponseWriter, r *http.Request) {
	var in webtool.Settings
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	st, err := s.Web.PutSettings(r.Context(), s.org(r), s.actor(r), webtool.Settings{Enabled: in.Enabled, Mode: in.Mode,
		AllowedDomains: in.AllowedDomains, BlockedDomains: in.BlockedDomains, MaxCallsPerTask: in.MaxCallsPerTask, MaxCallsPerDay: in.MaxCallsPerDay})
	if err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.webView(r, st)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
