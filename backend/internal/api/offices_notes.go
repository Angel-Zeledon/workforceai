package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/offices"
)

// Office and personal memory (E2, equipos-colaboracion.md §2.9): GET and POST /memory/notes, DELETE
// /memory/notes/{id} and POST /memory/notes/{id}/promote. The offices service decides who reads, writes and
// promotes (an office's members and its responsible; a personal memory only its owner).
func (s *server) mountTeamNotes(r chi.Router) {
	read := s.can(auth.PermAgentsRead)
	r.With(read).Get("/memory/notes", s.listTeamNotes)
	r.With(read).Post("/memory/notes", s.addTeamNote)
	r.With(read).Delete("/memory/notes/{id}", s.deleteTeamNote)
	r.With(read).Post("/memory/notes/{id}/promote", s.promoteTeamNote)
}

type noteView struct {
	offices.Note
	AuthorName string `json:"author_name"`
	CanDelete  bool   `json:"can_delete"`
	Promoted   bool   `json:"promoted,omitempty"` // an office note already copied to company knowledge
}

func (s *server) noteView(m *offices.Model, v offices.Viewer, n offices.Note) noteView {
	p, _ := m.Person(n.CreatedBy)
	return noteView{Note: n, AuthorName: p.Name, CanDelete: offices.CanDeleteNote(m, v, n)}
}

func (s *server) listTeamNotes(w http.ResponseWriter, r *http.Request) {
	v := s.viewer(r)
	q := r.URL.Query()
	l, err := s.Offices.Notes(r.Context(), s.org(r), v, q.Get("scope"), q.Get("ref"))
	if err != nil {
		s.fail(w, err)
		return
	}
	m, err := s.Offices.Model(r.Context(), s.org(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	promoted := map[string]bool{}
	if q.Get("scope") == offices.ScopeOffice {
		if c, err := s.Offices.Notes(r.Context(), s.org(r), v, offices.ScopeCompany, ""); err == nil {
			for _, n := range c.Notes {
				promoted[n.PromotedFrom] = true
			}
		}
	}
	out := make([]noteView, 0, len(l.Notes))
	for _, n := range l.Notes {
		nv := s.noteView(m, v, n)
		nv.Promoted = promoted[n.ID]
		out = append(out, nv)
	}
	writeJSON(w, 200, map[string]any{"notes": out, "can_write": l.CanWrite, "can_promote": l.CanPromote})
}

func (s *server) addTeamNote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Scope string `json:"scope"`
		Ref   string `json:"ref"`
		Text  string `json:"text"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	v := s.viewer(r)
	n, err := s.Offices.AddNote(r.Context(), s.org(r), v, body.Scope, body.Ref, body.Text)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.writeNote(w, r, v, n)
}

func (s *server) writeNote(w http.ResponseWriter, r *http.Request, v offices.Viewer, n offices.Note) {
	m, err := s.Offices.Model(r.Context(), s.org(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.noteView(m, v, n))
}

func (s *server) deleteTeamNote(w http.ResponseWriter, r *http.Request) {
	if err := s.Offices.DeleteNote(r.Context(), s.org(r), s.viewer(r), chi.URLParam(r, "id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) promoteTeamNote(w http.ResponseWriter, r *http.Request) {
	v := s.viewer(r)
	n, err := s.Offices.Promote(r.Context(), s.org(r), v, chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.writeNote(w, r, v, n)
}
