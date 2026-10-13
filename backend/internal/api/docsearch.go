package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/drive"
)

// Passage search over the drive (capability 6, docs/architecture/agent-search.md).
// People with files:read see every folder (like /drive/search); agents use
// fs.search, which is scoped to the folders they may read.

func (s *server) mountDocSearch(r chi.Router) {
	if s.DocIndex == nil || s.Drive == nil {
		return
	}
	read := s.can(auth.PermFilesRead)
	r.With(read).Get("/drive/passages", s.drivePassages)
	r.With(read).Get("/drive/index/status", s.driveIndexStatus)
	r.With(read).Get("/settings/doc-index", s.docIndexSettings)
	r.With(s.can(auth.PermOrgManage)).Put("/settings/doc-index", s.putDocIndexSettings)
}

// drivePassages: GET /drive/passages?q=&path=&mode=keyword|semantic|hybrid&limit=.
// {enabled, mode, note, hits: [{path, file_id, version, page, line, start, end, snippet, score}]}.
// enabled=false means the organization turned the index off: use /drive/search.
func (s *server) drivePassages(w http.ResponseWriter, r *http.Request) {
	q := drive.ChunkQuery{Query: r.URL.Query().Get("q"), Mode: r.URL.Query().Get("mode"), Limit: queryInt(r, "limit"), Locale: r.URL.Query().Get("locale")}
	switch q.Mode {
	case "", drive.SearchKeyword, drive.SearchSemantic, drive.SearchHybrid:
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mode must be keyword, semantic or hybrid", "code": "invalid_mode"})
		return
	}
	if p := r.URL.Query().Get("path"); p != "" && p != "/" {
		c, err := drive.CleanPath(p)
		if err != nil {
			s.driveFail(w, err)
			return
		}
		q.Under = []string{c}
	}
	res, err := s.DocIndex.SearchChunks(r.Context(), q)
	if errors.Is(err, drive.ErrIndexOff) {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "hits": []any{}, "mode": "off"})
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "hits": list(res.Hits), "mode": res.Mode, "note": res.Note})
}

// driveIndexStatus: GET /drive/index/status?path=. Counts of the index states
// of the live files under the folder, plus whether search by meaning is on.
func (s *server) driveIndexStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.DocIndex.Settings(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	out := map[string]any{"enabled": st.Enabled, "embeddings": s.DocIndex.Embeddings(r.Context())}
	if st.Enabled {
		c, err := s.DocIndex.Status(r.Context(), r.URL.Query().Get("path"))
		if err != nil {
			s.driveFail(w, err)
			return
		}
		out["status"] = c
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) docIndexSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.DocIndex.Settings(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": st, "embeddings": s.DocIndex.Embeddings(r.Context())})
}

// putDocIndexSettings: PUT /settings/doc-index {enabled} (owner/admin, audited).
// Off deletes the organization's index; on starts the backfill.
func (s *server) putDocIndexSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	if in.Enabled == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "enabled is required", "code": "invalid_args"})
		return
	}
	st, err := s.DocIndex.SetEnabled(r.Context(), s.actor(r), *in.Enabled)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": st, "embeddings": s.DocIndex.Embeddings(r.Context())})
}
