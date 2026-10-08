package api

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// Artifacts API (additive, v1): docs/architecture/agent-workspaces.md sec. 5.5
// and 11; docs/architecture/08-api.md. Every route runs as a person: agents
// write through artifacts.Service (their attachment mode is checked there),
// never by sending an agent id in a request body.

// artifactBodyLimit: contents are validated against their own limits (sheet 5 MB);
// the generic body cap would reject them first.
const artifactBodyLimit = 6 << 20

func (s *server) decodeArtifact(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, max(s.MaxBodyBytes, artifactBodyLimit))
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return errTooLarge
		}
		return errors.Join(domain.ErrInvalid, err)
	}
	return nil
}

func (s *server) artifactActor(r *http.Request) artifacts.Actor {
	return artifacts.Actor{Kind: artifacts.ActorUser, ID: s.actor(r),
		CanApprove: s.has(r, auth.PermArtifactsApprove), CanDelete: s.has(r, auth.PermArtifactsDelete)}
}

func (s *server) mountArtifacts(r chi.Router) {
	if s.Artifacts == nil {
		return
	}
	read, create, write := s.can(auth.PermArtifactsRead), s.can(auth.PermArtifactsCreate), s.can(auth.PermArtifactsWrite)
	r.With(read).Get("/artifact-kinds", s.artifactKinds)
	r.With(read).Get("/artifact-templates", s.artifactTemplates)
	r.With(read).Get("/artifacts", s.listArtifacts)
	r.With(create).Post("/artifacts", s.createArtifact)
	r.With(read).Get("/artifacts/{id}", s.getArtifact)
	r.With(write).Patch("/artifacts/{id}", s.patchArtifact)
	r.With(write).Delete("/artifacts/{id}", s.archiveArtifact)
	r.With(write).Post("/artifacts/{id}/versions", s.saveArtifactVersion)
	r.With(read, s.can(auth.PermArtifactsExport)).Get("/artifacts/{id}/export", s.exportArtifact)
	r.With(read).Get("/artifacts/{id}/versions", s.artifactVersions)
	r.With(read).Get("/artifacts/{id}/versions/{n}", s.artifactVersion)
	r.With(write).Post("/artifacts/{id}/restore", s.restoreArtifact)
	r.With(write).Put("/artifacts/{id}/attachments/{agent_id}", s.attachArtifact)
	r.With(read).Get("/artifacts/{id}/links", s.artifactLinks)
	r.With(write).Post("/artifacts/{id}/links", s.addArtifactLink)
	r.With(write).Post("/artifacts/{id}/refresh-dependencies", s.refreshArtifact)
	comment := s.can(auth.PermArtifactsComment)
	r.With(read).Get("/artifacts/{id}/comments", s.listArtifactComments)
	r.With(comment).Post("/artifacts/{id}/comments", s.addArtifactComment)
	r.With(comment).Patch("/artifacts/{id}/comments/{cid}", s.resolveArtifactComment)
	r.With(read).Get("/artifacts/{id}/proposals", s.listArtifactProposals)
	r.With(comment).Post("/artifacts/{id}/proposals", s.proposeArtifact)
	r.With(s.can(auth.PermArtifactsApprove)).Post("/artifacts/{id}/proposals/{pid}/decision", s.decideArtifactProposal)
	r.With(s.can(auth.PermRequestsCreate)).Post("/artifacts/{id}/ask", s.askAboutArtifact)
	r.With(read).Get("/projects/{id}/workspace", s.projectWorkspace)
}

func (s *server) artifactKinds(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"items": s.Artifacts.Kinds(r.URL.Query().Get("agent_id"))})
}

func (s *server) artifactTemplates(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	writeJSON(w, 200, map[string]any{"items": s.Artifacts.Templates(q.Get("agent_id"), artifacts.Kind(q.Get("kind")))})
}

func (s *server) listArtifacts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	v, err := s.Artifacts.List(r.Context(), artifacts.Filter{Kind: q.Get("kind"), AgentID: q.Get("agent_id"), TaskID: q.Get("task_id"),
		Status: q.Get("status"), Q: q.Get("q"), ProjectID: q.Get("project_id")})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": list(v)})
}

func (s *server) createArtifact(w http.ResponseWriter, r *http.Request) {
	var body artifacts.CreateInput
	if err := s.decodeArtifact(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	a, err := s.Artifacts.Create(r.Context(), s.artifactActor(r), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (s *server) getArtifact(w http.ResponseWriter, r *http.Request) {
	ver, _ := strconv.Atoi(r.URL.Query().Get("version"))
	a, err := s.Artifacts.Get(r.Context(), chi.URLParam(r, "id"), ver)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, a)
}

func (s *server) patchArtifact(w http.ResponseWriter, r *http.Request) {
	var body artifacts.PatchInput
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	m, err := s.Artifacts.Patch(r.Context(), s.artifactActor(r), chi.URLParam(r, "id"), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, m)
}

func (s *server) archiveArtifact(w http.ResponseWriter, r *http.Request) {
	if err := s.Artifacts.Archive(r.Context(), s.artifactActor(r), chi.URLParam(r, "id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// saveArtifactVersion: 201 {version, merged, content?} or 409 with the conflicting units.
func (s *server) saveArtifactVersion(w http.ResponseWriter, r *http.Request) {
	var body artifacts.SaveInput
	if err := s.decodeArtifact(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	res, err := s.Artifacts.Save(r.Context(), s.artifactActor(r), chi.URLParam(r, "id"), body)
	var c *artifacts.Conflict
	if errors.As(err, &c) {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "conflict", "error": c.Error(), "head_version": c.HeadVersion, "merged": false, "conflicts": list(c.Conflicts)})
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

// exportArtifact streams a docx/xlsx copy of the content (never stored) and audits it.
func (s *server) exportArtifact(w http.ResponseWriter, r *http.Request) {
	ver, _ := strconv.Atoi(r.URL.Query().Get("version"))
	f, err := s.Artifacts.Export(r.Context(), s.artifactActor(r), chi.URLParam(r, "id"), ver, r.URL.Query().Get("format"))
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", f.MIME)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.Filename}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(f.Data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(f.Data)
}

func (s *server) artifactVersions(w http.ResponseWriter, r *http.Request) {
	v, err := s.Artifacts.Versions(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": list(v)})
}

func (s *server) artifactVersion(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(chi.URLParam(r, "n"))
	if err != nil || n < 1 {
		s.fail(w, errInvalid("version must be a positive integer"))
		return
	}
	a, err := s.Artifacts.Get(r.Context(), chi.URLParam(r, "id"), n)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, a)
}

func (s *server) restoreArtifact(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version int `json:"version"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	m, err := s.Artifacts.Restore(r.Context(), s.artifactActor(r), chi.URLParam(r, "id"), body.Version)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, m)
}

func (s *server) attachArtifact(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	m, err := s.Artifacts.Attach(r.Context(), s.artifactActor(r), chi.URLParam(r, "id"), chi.URLParam(r, "agent_id"), body.Mode)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, m)
}

func (s *server) artifactLinks(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("direction")
	if dir == "" {
		dir = "both"
	}
	v, err := s.Artifacts.Links(r.Context(), chi.URLParam(r, "id"), dir)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": list(v)})
}

func (s *server) addArtifactLink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To       string            `json:"to"`
		Relation string            `json:"relation"`
		Anchor   *artifacts.Anchor `json:"anchor"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	l, err := s.Artifacts.AddLink(r.Context(), s.artifactActor(r), chi.URLParam(r, "id"), body.To, body.Relation, body.Anchor)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, l)
}

func (s *server) refreshArtifact(w http.ResponseWriter, r *http.Request) {
	res, err := s.Artifacts.RefreshDependencies(r.Context(), s.artifactActor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *server) askAboutArtifact(w http.ResponseWriter, r *http.Request) {
	var body artifacts.AskInput
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	id, err := s.Artifacts.Ask(r.Context(), s.artifactActor(r), chi.URLParam(r, "id"), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"request_id": id})
}

func (s *server) projectWorkspace(w http.ResponseWriter, r *http.Request) {
	ws, err := s.Artifacts.Workspace(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, ws)
}

func (s *server) listArtifactComments(w http.ResponseWriter, r *http.Request) {
	v, err := s.Artifacts.Comments(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": list(v)})
}

func (s *server) addArtifactComment(w http.ResponseWriter, r *http.Request) {
	var body artifacts.CommentInput
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	c, err := s.Artifacts.AddComment(r.Context(), s.artifactActor(r), chi.URLParam(r, "id"), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *server) resolveArtifactComment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Resolved bool `json:"resolved"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	c, err := s.Artifacts.ResolveComment(r.Context(), s.artifactActor(r), chi.URLParam(r, "id"), chi.URLParam(r, "cid"), body.Resolved)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, c)
}

func (s *server) listArtifactProposals(w http.ResponseWriter, r *http.Request) {
	v, err := s.Artifacts.Proposals(r.Context(), chi.URLParam(r, "id"), r.URL.Query().Get("status"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": list(v)})
}

func (s *server) proposeArtifact(w http.ResponseWriter, r *http.Request) {
	var body artifacts.ProposalInput
	if err := s.decodeArtifact(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	p, err := s.Artifacts.Propose(r.Context(), s.artifactActor(r), chi.URLParam(r, "id"), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

// decideArtifactProposal: 200 with the proposal, or 409 with the conflicting units when the accepted content collides with newer edits.
func (s *server) decideArtifactProposal(w http.ResponseWriter, r *http.Request) {
	var body artifacts.DecisionInput
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	p, err := s.Artifacts.Decide(r.Context(), s.artifactActor(r), chi.URLParam(r, "id"), chi.URLParam(r, "pid"), body)
	var c *artifacts.Conflict
	if errors.As(err, &c) {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "conflict", "error": c.Error(), "head_version": c.HeadVersion, "conflicts": list(c.Conflicts)})
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, p)
}
