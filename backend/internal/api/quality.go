package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/quality"
)

// Quality score and golden cases API (docs/architecture/quality-score.md, 08-api.md). The score is advisory:
// no endpoint here changes an agent's autonomy or permissions, and every response carries its methodology.
// Reading follows agents:read; defining golden cases and recording runs follows agents:write. Every
// write is audited by the service.

func (s *server) mountQuality(r chi.Router) {
	if s.Quality == nil {
		return
	}
	read, write := s.can(auth.PermAgentsRead), s.can(auth.PermAgentsWrite)
	r.With(read).Get("/quality/agents", s.qualityOverview)
	r.With(read).Get("/agents/{id}/quality", s.qualityAgent)
	r.With(read).Get("/agents/{id}/golden-cases", s.goldenList)
	r.With(write).Post("/agents/{id}/golden-cases", s.goldenCreate)
	r.With(write).Put("/golden-cases/{id}", s.goldenUpdate)
	r.With(write).Delete("/golden-cases/{id}", s.goldenDelete)
	r.With(read).Get("/golden-cases/{id}/runs", s.goldenRuns)
	r.With(write).Post("/golden-cases/{id}/runs", s.goldenRecordRun)
}

// windowDays reads ?window_days= (the service clamps it).
func windowDays(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("window_days"))
	return n
}

func (s *server) qualityOverview(w http.ResponseWriter, r *http.Request) {
	rep, err := s.Quality.Overview(r.Context(), windowDays(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, rep)
}

func (s *server) qualityAgent(w http.ResponseWriter, r *http.Request) {
	rep, err := s.Quality.ForAgent(r.Context(), chi.URLParam(r, "id"), windowDays(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"methodology": rep.Methodology, "generated_at": rep.GeneratedAt, "agent": rep.Agents[0]})
}

func (s *server) goldenList(w http.ResponseWriter, r *http.Request) {
	v, err := s.Quality.ListCases(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, list(v))
}

func (s *server) goldenCreate(w http.ResponseWriter, r *http.Request) {
	var in quality.CaseInput
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	c, err := s.Quality.CreateCase(r.Context(), s.actor(r), chi.URLParam(r, "id"), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 201, c)
}

func (s *server) goldenUpdate(w http.ResponseWriter, r *http.Request) {
	var in quality.CaseInput
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	c, err := s.Quality.UpdateCase(r.Context(), s.actor(r), chi.URLParam(r, "id"), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, c)
}

func (s *server) goldenDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Quality.DeleteCase(r.Context(), s.actor(r), chi.URLParam(r, "id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) goldenRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	v, err := s.Quality.ListRuns(r.Context(), chi.URLParam(r, "id"), limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, list(v))
}

func (s *server) goldenRecordRun(w http.ResponseWriter, r *http.Request) {
	var in quality.RunInput
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	run, err := s.Quality.RecordRun(r.Context(), s.actor(r), chi.URLParam(r, "id"), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 201, run)
}
