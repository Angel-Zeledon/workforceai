package api

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/linkimports"
)

// Link imports API (docs/architecture/link-imports.md, 08-api.md): "import from a link". Reading follows
// connections:read (members see that an import exists, its status and history, with the link's query string
// hidden: published links often carry a token there); creating, editing, running and deleting follow org:manage
// (admins). The auth header value is write-only: no response ever carries it. Every write and every run is audited
// by the service. Agents never reach these endpoints.

func (s *server) mountLinkImports(r chi.Router) {
	if s.LinkImports == nil {
		return
	}
	read, manage := s.can(auth.PermConnectionsRead), s.can(auth.PermOrgManage)
	r.With(read).Get("/link-imports", s.linkImportsList)
	r.With(manage).Post("/link-imports", s.linkImportsCreate)
	r.With(read).Get("/link-imports/{id}", s.linkImportsGet)
	r.With(manage).Put("/link-imports/{id}", s.linkImportsUpdate)
	r.With(manage).Delete("/link-imports/{id}", s.linkImportsDelete)
	r.With(manage).Post("/link-imports/{id}/run", s.linkImportsRun)
	r.With(read).Get("/link-imports/{id}/runs", s.linkImportsRuns)
}

// redactLink hides the query string and fragment of a link from callers who cannot manage imports.
func (s *server) redactLink(r *http.Request, v linkimports.View) linkimports.View {
	if s.has(r, auth.PermOrgManage) {
		return v
	}
	if u, err := url.Parse(v.URL); err == nil {
		if u.RawQuery != "" {
			u.RawQuery = "…"
		}
		u.Fragment = ""
		v.URL = u.String()
	}
	return v
}

func (s *server) linkImportsList(w http.ResponseWriter, r *http.Request) {
	vs, err := s.LinkImports.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	for i := range vs {
		vs[i] = s.redactLink(r, vs[i])
	}
	writeJSON(w, 200, map[string]any{"imports": list(vs), "schedules": linkimports.AllSchedules, "types": linkimports.AllowedExts})
}

func (s *server) linkImportsGet(w http.ResponseWriter, r *http.Request) {
	v, err := s.LinkImports.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, s.redactLink(r, v))
}

func (s *server) linkImportsCreate(w http.ResponseWriter, r *http.Request) {
	var in linkimports.Input
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.LinkImports.Create(r.Context(), s.actor(r), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 201, v)
}

func (s *server) linkImportsUpdate(w http.ResponseWriter, r *http.Request) {
	var in linkimports.Input
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.LinkImports.Update(r.Context(), s.actor(r), chi.URLParam(r, "id"), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) linkImportsDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.LinkImports.Delete(r.Context(), s.actor(r), chi.URLParam(r, "id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// linkImportsRun runs an import now. The answer is the recorded run (200), whatever its status: a skipped run
// (read-only mode, kill switch) or a failed download is a result, not an HTTP error. 409 means a run of the same
// import is in progress.
func (s *server) linkImportsRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.LinkImports.RunNow(r.Context(), s.actor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	v, gerr := s.LinkImports.Get(r.Context(), run.ImportID)
	if gerr != nil {
		writeJSON(w, 200, map[string]any{"run": run})
		return
	}
	writeJSON(w, 200, map[string]any{"run": run, "import": v})
}

func (s *server) linkImportsRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rs, err := s.LinkImports.Runs(r.Context(), chi.URLParam(r, "id"), limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, list(rs))
}
