package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/peoplechat"
	"aiworkforce/backend/internal/presence"
)

// Human presence and person-to-person chat (docs/architecture/presence.md). Optional: no-op when the services are nil.
//
//	GET  /presence                              who is online, where and with which status
//	PUT  /presence/status                       choose available | focus | meeting | away
//	GET  /people-chat/directory                 people and agents of the organization (DM picker, @mentions)
//	GET  /people-chat/channels                  my channels with unread and mention counts
//	POST /people-chat/channels                  create a department / project / group channel
//	POST /people-chat/dm                        open (or get) the DM with a person
//	GET  /people-chat/channels/{id}/messages    history page (?before=<seq>&limit=)
//	POST /people-chat/channels/{id}/messages    write
//	POST /people-chat/channels/{id}/read        move my read marker
//	POST /people-chat/channels/{id}/members     add a person or an agent
//	DELETE /people-chat/channels/{id}/members/{kind}/{mid}
//
// Position updates travel over the WebSocket (see ws.go), not here.

// demoUser is the single person of an open (auth off) demo server.
const demoUser = "demo-user"

func (s *server) mountPresence(r chi.Router) {
	if s.Presence != nil {
		r.With(s.can(auth.PermMembersRead)).Get("/presence", s.getPresence)
		r.With(s.can(auth.PermMembersRead)).Put("/presence/status", s.putPresenceStatus)
	}
	if s.PeopleChat != nil {
		r.With(s.can(auth.PermConversationsRead)).Get("/people-chat/directory", s.pcDirectory)
		r.With(s.can(auth.PermConversationsRead)).Get("/people-chat/channels", s.pcChannels)
		r.With(s.can(auth.PermConversationsRead)).Post("/people-chat/channels", s.pcCreate)
		r.With(s.can(auth.PermConversationsRead)).Post("/people-chat/dm", s.pcDM)
		r.With(s.can(auth.PermConversationsRead)).Get("/people-chat/channels/{id}/messages", s.pcMessages)
		r.With(s.can(auth.PermConversationsRead)).Post("/people-chat/channels/{id}/messages", s.pcPost)
		r.With(s.can(auth.PermConversationsRead)).Post("/people-chat/channels/{id}/read", s.pcRead)
		r.With(s.can(auth.PermConversationsRead)).Post("/people-chat/channels/{id}/members", s.pcAddMember)
		r.With(s.can(auth.PermConversationsRead)).Delete("/people-chat/channels/{id}/members/{kind}/{mid}", s.pcRemoveMember)
	}
}

// identity is the verified person of the request: from the token when auth is on, the demo user otherwise.
func (s *server) identity(r *http.Request) peoplechat.Actor {
	if p, ok := auth.PrincipalFrom(r.Context()); ok {
		return peoplechat.Actor{UserID: p.UserID, Role: p.Role}
	}
	return peoplechat.Actor{UserID: demoUser, Role: auth.RoleOwner}
}

// personName is the display name shown on the avatar; it falls back to a neutral label.
func (s *server) personName(ctx context.Context, org, user string) string {
	if s.PeopleChat != nil {
		if dir, err := s.PeopleChat.Store.Directory(ctx, org); err == nil {
			for _, p := range dir {
				if p.ID == user {
					return p.Name
				}
			}
		}
	}
	if user == demoUser {
		return "You"
	}
	return "Guest"
}

func (s *server) pcFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, peoplechat.ErrForbidden):
		err = errors.Join(domain.ErrForbidden, err)
	case errors.Is(err, peoplechat.ErrNotFound):
		err = errors.Join(domain.ErrNotFound, err)
	case errors.Is(err, peoplechat.ErrInvalid):
		err = errors.Join(domain.ErrInvalid, err)
	}
	s.fail(w, err)
}

func (s *server) getPresence(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"people": s.Presence.Snapshot(s.org(r)),
		"self":   s.identity(r).UserID,
		"config": map[string]any{"enter_radius": 2.5, "exit_radius": 3.2},
	})
}

func (s *server) putPresenceStatus(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status presence.Status `json:"status"`
	}
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Presence.SetStatus(s.org(r), s.identity(r).UserID, in.Status); err != nil {
		if errors.Is(err, presence.ErrNotOnline) {
			s.fail(w, errors.Join(domain.ErrConflict, err)) // no open socket: nothing to change
			return
		}
		s.fail(w, errors.Join(domain.ErrInvalid, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": string(in.Status)})
}

func (s *server) pcDirectory(w http.ResponseWriter, r *http.Request) {
	people, agents, err := s.PeopleChat.Directory(r.Context(), s.org(r), s.identity(r))
	if err != nil {
		s.pcFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"people": list(people), "agents": list(agents), "self": s.identity(r).UserID})
}

func (s *server) pcChannels(w http.ResponseWriter, r *http.Request) {
	v, err := s.PeopleChat.Channels(r.Context(), s.org(r), s.identity(r))
	if err != nil {
		s.pcFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list(v))
}

type pcMember struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

func toMembers(in []pcMember) []peoplechat.Member {
	out := make([]peoplechat.Member, 0, len(in))
	for _, m := range in {
		out = append(out, peoplechat.Member{Kind: m.Kind, ID: m.ID})
	}
	return out
}

func (s *server) pcCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind     string     `json:"kind"`
		Name     string     `json:"name"`
		ScopeKey string     `json:"scope_key"`
		Members  []pcMember `json:"members"`
	}
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	if in.Kind == peoplechat.KindDM || in.Kind == peoplechat.KindOrg { // those have their own entry points
		s.fail(w, domain.ErrInvalid)
		return
	}
	v, err := s.PeopleChat.Create(r.Context(), s.org(r), s.identity(r), peoplechat.CreateInput{Kind: in.Kind, Name: in.Name, ScopeKey: in.ScopeKey, Members: toMembers(in.Members)})
	if err != nil {
		s.pcFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (s *server) pcDM(w http.ResponseWriter, r *http.Request) {
	var in struct {
		UserID string `json:"user_id"`
	}
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.PeopleChat.OpenDM(r.Context(), s.org(r), s.identity(r), in.UserID)
	if err != nil {
		s.pcFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) pcMessages(w http.ResponseWriter, r *http.Request) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	v, err := s.PeopleChat.Messages(r.Context(), s.org(r), s.identity(r), chi.URLParam(r, "id"), before, limit)
	if err != nil {
		s.pcFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list(v))
}

func (s *server) pcPost(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
	}
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	m, err := s.PeopleChat.Post(r.Context(), s.org(r), s.identity(r), chi.URLParam(r, "id"), in.Text)
	if err != nil {
		s.pcFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *server) pcRead(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Seq int64 `json:"seq"`
	}
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.PeopleChat.MarkRead(r.Context(), s.org(r), s.identity(r), chi.URLParam(r, "id"), in.Seq); err != nil {
		s.pcFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) pcAddMember(w http.ResponseWriter, r *http.Request) {
	var in pcMember
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.PeopleChat.AddMember(r.Context(), s.org(r), s.identity(r), chi.URLParam(r, "id"), peoplechat.Member{Kind: in.Kind, ID: in.ID}); err != nil {
		s.pcFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) pcRemoveMember(w http.ResponseWriter, r *http.Request) {
	if err := s.PeopleChat.RemoveMember(r.Context(), s.org(r), s.identity(r), chi.URLParam(r, "id"), chi.URLParam(r, "kind"), chi.URLParam(r, "mid")); err != nil {
		s.pcFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
