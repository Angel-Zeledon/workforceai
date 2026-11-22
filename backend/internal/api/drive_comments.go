package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/filecomments"
)

// Anchored comments on files of the Drive (F7, docs/architecture/anchored-comments.md, 08-api.md section 52):
//
//	GET   /drive/files/{id}/comments[?version=n]            the comments, each anchor located in that version (default: current)
//	POST  /drive/files/{id}/comments                        {body, quote?, version?, parent_id?}
//	PATCH /drive/files/{id}/comments/{cid}                  {resolved}
//	POST  /drive/files/{id}/comments/{cid}/request-change   {agent_id, lang?}  asks the agent to change the passage
//
// Every route runs as a person and checks the person's level on the file's folder first, exactly like the rest of the
// drive: a file of a private department answers 404 to whoever is not a member (comments included). Writing a comment
// needs only to read the file; asking an agent to change it needs to be able to edit it.
func (s *server) mountDriveComments(r chi.Router) {
	if s.FileComments == nil {
		return
	}
	read, comment, post := s.can(auth.PermFilesRead), s.can(auth.PermArtifactsComment), s.can(auth.PermConversationsPost)
	r.With(read).Get("/drive/files/{id}/comments", s.listFileComments)
	r.With(comment).Post("/drive/files/{id}/comments", s.addFileComment)
	r.With(comment).Patch("/drive/files/{id}/comments/{cid}", s.resolveFileComment)
	r.With(comment, post).Post("/drive/files/{id}/comments/{cid}/request-change", s.requestFileChange)
}

func (s *server) commentActor(r *http.Request) filecomments.Actor {
	return filecomments.Actor{Kind: "person", ID: s.actor(r)}
}

func (s *server) listFileComments(w http.ResponseWriter, r *http.Request) {
	f, ok := s.driveFileFor(w, r, drive.AccessRead)
	if !ok {
		return
	}
	v := queryInt(r, "version")
	views, err := s.FileComments.List(r.Context(), f, v)
	if err != nil {
		s.driveFail(w, err)
		return
	}
	if v <= 0 {
		v = f.CurrentVersion
	}
	writeJSON(w, http.StatusOK, map[string]any{"comments": list(views), "version": v, "current_version": f.CurrentVersion})
}

func (s *server) addFileComment(w http.ResponseWriter, r *http.Request) {
	f, ok := s.driveFileFor(w, r, drive.AccessRead)
	if !ok {
		return
	}
	var body filecomments.AddInput
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	c, err := s.FileComments.Add(r.Context(), s.commentActor(r), f, body)
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *server) resolveFileComment(w http.ResponseWriter, r *http.Request) {
	f, ok := s.driveFileFor(w, r, drive.AccessRead)
	if !ok {
		return
	}
	var body struct {
		Resolved bool `json:"resolved"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	c, err := s.FileComments.Resolve(r.Context(), s.commentActor(r), f, chi.URLParam(r, "cid"), body.Resolved)
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// requestFileChange is "Ask {agent} to make the change": a chat message to that agent with the file attached, built by
// the comments service (the passage and the comment travel as untrusted data). It is the same road as "Ask for a
// change" in the viewer, so the agent's edit (fs.edit) is subject to the same zones, approvals, budget and audit.
func (s *server) requestFileChange(w http.ResponseWriter, r *http.Request) {
	f, ok := s.driveFileFor(w, r, drive.AccessEdit)
	if !ok {
		return
	}
	var body struct {
		AgentID string `json:"agent_id"`
		Lang    string `json:"lang"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if body.AgentID == "" {
		s.driveFail(w, &drive.Error{Code: "agent_required", Kind: domain.ErrInvalid, Msg: "agent_id is required"})
		return
	}
	conv := domain.ChatAgentPrefix + body.AgentID
	if err := s.checkAgentUse(r, conv); err != nil { // an agent the person may not use (D3, E2)
		s.fail(w, err)
		return
	}
	cid := chi.URLParam(r, "cid")
	ask := func(ctx context.Context, text string) (string, error) {
		ctx = application.WithActorName(ctx, s.authorName(ctx))
		ctx = application.WithAttachments(ctx, []string{f.Path})
		turn, _, err := s.Orch.PostChatKeyed(ctx, conv, text, "file-comment-change:"+cid)
		return turn.TurnID, err
	}
	c, err := s.FileComments.RequestChange(r.Context(), s.commentActor(r), f, cid, body.AgentID, body.Lang, ask)
	if err != nil {
		var de *drive.Error
		if errors.As(err, &de) {
			s.driveFail(w, err)
		} else {
			s.fail(w, err) // the chat's own errors (a text that is refused, an unknown agent)
		}
		return
	}
	writeJSON(w, http.StatusOK, c)
}
