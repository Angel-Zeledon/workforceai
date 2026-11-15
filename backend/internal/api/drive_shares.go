package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/drive"
)

// Shares between departments (D4, docs/superpowers/plans/2026-11-15-d4-entre-departamentos.md): a department shares a
// file or folder of its folder with another one, read only, with the approval of a responsible of the owner. They are
// not agent approvals, so they have their own routes; the rules live in drive.Service (shares.go).

func (s *server) mountDriveShares(r chi.Router, read, write func(http.Handler) http.Handler) {
	r.With(write).Post("/drive/shares", s.driveShareRequest)
	r.With(read).Get("/drive/shares", s.driveShareList)
	r.With(write).Post("/drive/shares/{id}/decision", s.driveShareDecide)
	r.With(write).Delete("/drive/shares/{id}", s.driveShareRevoke)
}

// driveShareRequest: POST /drive/shares {path, target_office_id}. 201 Share.
func (s *server) driveShareRequest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path           string `json:"path"`
		TargetOfficeID string `json:"target_office_id"`
	}
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.driveNeedAt(r.Context(), in.Path, drive.AccessEdit, "folder"); err != nil {
		s.driveFail(w, err)
		return
	}
	sh, err := s.Drive.RequestShare(r.Context(), application.PersonFrom(r.Context()), in.Path, in.TargetOfficeID)
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sh)
}

// driveShareList: GET /drive/shares?office=<id>&dir=out|in[&status=a,b] or GET /drive/shares?path=<p>. 200 Share[].
func (s *server) driveShareList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	user := application.PersonFrom(r.Context())
	var (
		out []drive.Share
		err error
	)
	if p := q.Get("path"); p != "" {
		if err = s.driveNeedAt(r.Context(), p, drive.AccessRead, "folder"); err == nil {
			out, err = s.Drive.SharesAt(r.Context(), user, p)
		}
	} else {
		var statuses []string
		for _, v := range q["status"] {
			for _, st := range strings.Split(v, ",") {
				if st = strings.TrimSpace(st); st != "" {
					statuses = append(statuses, st)
				}
			}
		}
		out, err = s.Drive.OfficeShares(r.Context(), user, q.Get("office"), q.Get("dir"), statuses)
	}
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list(out))
}

// driveShareDecide: POST /drive/shares/{id}/decision {decision: approve|reject}. 200 Share.
func (s *server) driveShareDecide(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Decision string `json:"decision"`
	}
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	sh, err := s.Drive.DecideShare(r.Context(), application.PersonFrom(r.Context()), chi.URLParam(r, "id"), in.Decision)
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sh)
}

// driveShareRevoke: DELETE /drive/shares/{id}. 204.
func (s *server) driveShareRevoke(w http.ResponseWriter, r *http.Request) {
	if err := s.Drive.RevokeShare(r.Context(), application.PersonFrom(r.Context()), chi.URLParam(r, "id")); err != nil {
		s.driveFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
