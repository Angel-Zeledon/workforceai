package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/domain"
)

// Batches (D3): controlling a batch (pause, resume, cancel), cancelling one of its customers or retrying its failed
// projects acts on its projects, so it follows the rule of /projects/{id}. A batch whose projects are all only the
// work of departments the person does not see answers like a missing one; a batch where any project has some of that
// work is refused (403): skipping the hidden nodes would leave half a batch running, which is worse than asking
// someone who sees all of it. The projects of a batch come from one template, so in practice it is all or nothing.

// batchRule decides from the views of the batch's projects.
func batchRule(id string, views []projectView) error {
	all, some := len(views) > 0, false
	for _, v := range views {
		if !v.all {
			all = false
		}
		if v.all || len(v.hidden) > 0 {
			some = true
		}
	}
	switch {
	case all:
		return notFound("batch", id)
	case some:
		return fmt.Errorf("%w: the batch has work of a private department you do not see", domain.ErrForbidden)
	}
	return nil
}

// workBatch guards the routes that act on the batch {id} (a missing batch is left to the handler).
func (s *server) workBatch(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, id := s.work(r), chi.URLParam(r, "id")
		if err := ws.load(); err != nil {
			s.fail(w, err)
			return
		}
		if !ws.open() {
			b, err := s.Projects.BatchView(r.Context(), id)
			if err != nil && !errors.Is(err, domain.ErrNotFound) {
				s.fail(w, err)
				return
			}
			var views []projectView
			for _, row := range b.Rows {
				if row.ProjectID == "" {
					continue
				}
				pv, ok, err := s.projectView(r, row.ProjectID)
				if err != nil {
					s.fail(w, err)
					return
				}
				if ok {
					views = append(views, pv)
				}
			}
			if err := batchRule(id, views); err != nil {
				s.fail(w, err)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
