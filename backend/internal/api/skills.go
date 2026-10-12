package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/skills"
)

// Skill library API (docs/architecture/skills.md, 08-api.md). Skills are
// reusable know-how in the open Agent Skills format. Members create drafts and
// edit; administrators review, activate, disable and delete. Agents never call
// this API: they receive name + description on every run and ask for the full
// text with the skill.load tool of the Tool Gateway.

// maxImportBytes caps an upload (a zip is at most 2 MB once inflated).
const maxImportBytes = 4 << 20

func (s *server) mountSkills(r chi.Router) {
	if s.Skills == nil {
		return
	}
	read, write, review := s.can(auth.PermSkillsRead), s.can(auth.PermSkillsWrite), s.can(auth.PermSkillsReview)
	r.With(read).Get("/skills", s.listSkills)
	r.With(write).Post("/skills", s.createSkill)
	r.With(write).Post("/skills/import", s.importSkill)
	r.With(read).Get("/skills/{id}", s.getSkill)
	r.With(write).Put("/skills/{id}", s.updateSkill)
	r.With(read).Get("/skills/{id}/versions", s.skillVersions)
	r.With(write).Post("/skills/{id}/versions/{n}/restore", s.restoreSkill)
	r.With(write).Post("/skills/{id}/submit", s.submitSkill)
	r.With(review).Post("/skills/{id}/review", s.reviewSkill)
	r.With(review).Post("/skills/{id}/disable", s.disableSkill)
	r.With(review).Delete("/skills/{id}", s.deleteSkill)
	r.With(read).Get("/skills/{id}/export", s.exportSkill)
	r.With(s.can(auth.PermAgentsRead)).Get("/agents/{id}/skills", s.agentSkills)
	r.With(s.can(auth.PermAgentsWrite)).Put("/agents/{id}/skills/{skillId}", s.assignSkill)
	r.With(s.can(auth.PermAgentsWrite)).Delete("/agents/{id}/skills/{skillId}", s.unassignSkill)
}

func (s *server) skillActor(r *http.Request) skills.Actor {
	a := skills.Actor{ID: s.actor(r), Admin: true}
	if s.AuthEnabled {
		p, ok := auth.PrincipalFrom(r.Context())
		a.Admin = ok && p.Role.Has(auth.PermSkillsReview)
	}
	return a
}

// skillFail maps skill (and drive) errors; the stable code travels in the body.
func (s *server) skillFail(w http.ResponseWriter, err error) {
	var de *drive.Error
	if errors.As(err, &de) || errors.Is(err, drive.ErrReadOnly) || errors.Is(err, drive.ErrQuota) || errors.Is(err, drive.ErrTooLarge) {
		s.driveFail(w, err)
		return
	}
	status := 0
	switch {
	case errors.Is(err, skills.ErrTooLarge):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, domain.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, domain.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, domain.ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, domain.ErrInvalid):
		status = http.StatusBadRequest
	default:
		s.fail(w, err)
		return
	}
	writeJSON(w, status, map[string]string{"code": skills.CodeOf(err), "error": err.Error()})
}

func (s *server) listSkills(w http.ResponseWriter, r *http.Request) {
	l, err := s.Skills.List(r.Context())
	if err != nil {
		s.skillFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list(l))
}

// skillBody is the maximum JSON body of a skill editor save (2 MB of files + overhead).
const skillBody = 3 << 20

func (s *server) decodeSkill(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, skillBody)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			s.fail(w, errTooLarge)
		} else {
			s.fail(w, errors.Join(domain.ErrInvalid, err))
		}
		return false
	}
	return true
}

func (s *server) createSkill(w http.ResponseWriter, r *http.Request) {
	var in skills.Input
	if !s.decodeSkill(w, r, &in) {
		return
	}
	d, err := s.Skills.Create(r.Context(), s.skillActor(r), in)
	if err != nil {
		s.skillFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (s *server) importSkill(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes+(64<<10))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"code": "upload_too_large", "error": "the upload is larger than 4 MB"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "upload_invalid", "error": "expected a multipart form with a file field"})
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "upload_invalid", "error": "the file field is required"})
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxImportBytes+1))
	if err != nil || len(data) > maxImportBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"code": "upload_too_large", "error": "the upload is larger than 4 MB"})
		return
	}
	res, err := s.Skills.Import(r.Context(), s.skillActor(r), data, r.FormValue("name"))
	if err != nil {
		s.skillFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

func (s *server) getSkill(w http.ResponseWriter, r *http.Request) {
	d, err := s.Skills.Get(r.Context(), chi.URLParam(r, "id"), queryInt(r, "version"))
	if err != nil {
		s.skillFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *server) updateSkill(w http.ResponseWriter, r *http.Request) {
	var in skills.Input
	if !s.decodeSkill(w, r, &in) {
		return
	}
	d, changed, err := s.Skills.Update(r.Context(), s.skillActor(r), chi.URLParam(r, "id"), in)
	if err != nil {
		s.skillFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		skills.Detail
		Changed bool `json:"changed"`
	}{d, changed})
}

func (s *server) skillVersions(w http.ResponseWriter, r *http.Request) {
	v, err := s.Skills.Versions(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.skillFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list(v))
}

func (s *server) restoreSkill(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(chi.URLParam(r, "n"))
	d, err := s.Skills.Restore(r.Context(), s.skillActor(r), chi.URLParam(r, "id"), n)
	if err != nil {
		s.skillFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *server) submitSkill(w http.ResponseWriter, r *http.Request) {
	sk, err := s.Skills.Submit(r.Context(), s.skillActor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.skillFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sk)
}

func (s *server) reviewSkill(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Decision string `json:"decision"` // approve | reject
		Version  int    `json:"version"`
		Note     string `json:"note"`
	}
	if !s.decodeSkill(w, r, &body) {
		return
	}
	if body.Decision != "approve" && body.Decision != "reject" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "decision_invalid", "error": "decision must be approve or reject"})
		return
	}
	sk, err := s.Skills.Review(r.Context(), s.skillActor(r), chi.URLParam(r, "id"), body.Decision == "approve", body.Version, body.Note)
	if err != nil {
		s.skillFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sk)
}

func (s *server) disableSkill(w http.ResponseWriter, r *http.Request) {
	sk, err := s.Skills.Disable(r.Context(), s.skillActor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.skillFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sk)
}

func (s *server) deleteSkill(w http.ResponseWriter, r *http.Request) {
	if err := s.Skills.Delete(r.Context(), s.skillActor(r), chi.URLParam(r, "id")); err != nil {
		s.skillFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// exportSkill: GET /skills/{id}/export?format=zip|md&version=n. A zip in the
// standard layout (<name>/SKILL.md + resources), or only the SKILL.md.
func (s *server) exportSkill(w http.ResponseWriter, r *http.Request) {
	id, ver := chi.URLParam(r, "id"), queryInt(r, "version")
	var data []byte
	var name, ctype string
	var err error
	if r.URL.Query().Get("format") == "md" {
		data, name, err = s.Skills.ExportMD(r.Context(), s.skillActor(r), id, ver)
		ctype = "text/markdown; charset=utf-8"
	} else {
		data, name, err = s.Skills.Export(r.Context(), s.skillActor(r), id, ver)
		ctype = "application/zip"
	}
	if err != nil {
		s.skillFail(w, err)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

func (s *server) agentSkills(w http.ResponseWriter, r *http.Request) {
	l, err := s.Skills.AgentSkills(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.skillFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list(l))
}

func (s *server) assignSkill(w http.ResponseWriter, r *http.Request) {
	if err := s.Skills.Assign(r.Context(), s.skillActor(r), chi.URLParam(r, "id"), chi.URLParam(r, "skillId")); err != nil {
		s.skillFail(w, err)
		return
	}
	l, err := s.Skills.AgentSkills(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.skillFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list(l))
}

func (s *server) unassignSkill(w http.ResponseWriter, r *http.Request) {
	if err := s.Skills.Unassign(r.Context(), s.skillActor(r), chi.URLParam(r, "id"), chi.URLParam(r, "skillId")); err != nil {
		s.skillFail(w, err)
		return
	}
	l, err := s.Skills.AgentSkills(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.skillFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list(l))
}
