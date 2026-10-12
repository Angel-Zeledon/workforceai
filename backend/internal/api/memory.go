package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/memory"
)

// Long-term memory API (docs/architecture/memory.md): what an agent remembers,
// with the sources it comes from. Agents never call it; they get memories in
// their task and ask for more with the memory.search tool of the Tool Gateway.

func (s *server) mountMemory(r chi.Router) {
	if s.Memory == nil {
		return
	}
	r.With(s.can(auth.PermMemoriesRead)).Get("/agents/{id}/memory", s.agentMemory)
	r.With(s.can(auth.PermMemoriesWrite)).Delete("/agents/{id}/memory/{docId}", s.deleteAgentMemory)
}

// agentMemory lists what the agent produced and remembers (its notes first,
// then newest first) or, with ?q=, what it would recall for that query
// (documents and the drive files it may read).
func (s *server) agentMemory(w http.ResponseWriter, r *http.Request) {
	org := s.org(r)
	id := chi.URLParam(r, "id")
	if _, err := s.Store.GetAgent(r.Context(), org, id); err != nil {
		s.fail(w, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	var items []memory.Item
	var err error
	if q := r.URL.Query().Get("q"); q != "" {
		items, err = s.Memory.Search(r.Context(), org, id, q, limit)
		for i := range items {
			items[i].Deletable = items[i].Deletable && items[i].AgentID == id
		}
	} else {
		items, err = s.Memory.List(r.Context(), org, id, limit)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list(items))
}

func (s *server) deleteAgentMemory(w http.ResponseWriter, r *http.Request) {
	if err := s.Memory.Delete(r.Context(), s.org(r), chi.URLParam(r, "id"), chi.URLParam(r, "docId"), s.actor(r)); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
