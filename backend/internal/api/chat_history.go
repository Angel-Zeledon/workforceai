package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
)

// Chat history (docs/architecture/chat-history.md). GET /conversations is the
// enriched list (title, preview, unread, linked ids, search, paging); /chats/*
// serves the two views that go hand in hand: General (the whole office stream)
// and one thread per agent. Both read the same stored messages.

func (s *server) mountChatHistory(r chi.Router) {
	if s.Chats == nil {
		return
	}
	read, write := s.can(auth.PermConversationsRead), s.can(auth.PermConversationsPost)
	r.With(write).Patch("/conversations/{id}", s.patchConversation)
	r.With(s.can(auth.PermConversationsDelete)).Delete("/conversations/{id}", s.deleteConversation)
	r.With(write).Post("/conversations/{id}/read", s.readConversation)
	r.With(read).Get("/chats/threads", s.listChatThreads)
	r.With(read).Get("/chats/threads/{key}/messages", s.listThreadMessages)
	r.With(write).Post("/chats/threads/{key}/read", s.readChatThread)
}

func intParam(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return 0, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": name + " must be a non-negative integer"})
		return 0, false
	}
	return n, true
}

// listConversationsHistory is GET /conversations?agent_id=&q=&kind=&archived=&limit=&offset=.
// The body stays a plain array; X-Has-More / X-Next-Offset page it.
func (s *server) listConversationsHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, ok := intParam(w, r, "limit")
	if !ok {
		return
	}
	offset, ok := intParam(w, r, "offset")
	if !ok {
		return
	}
	rows, more, err := s.Chats.Conversations(r.Context(), application.ChatConvQuery{
		AgentID: q.Get("agent_id"), Q: q.Get("q"), Kind: q.Get("kind"), Archived: q.Get("archived"), Limit: limit, Offset: offset})
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Access-Control-Expose-Headers", "X-Has-More, X-Next-Offset")
	w.Header().Set("X-Has-More", strconv.FormatBool(more))
	if more {
		w.Header().Set("X-Next-Offset", strconv.Itoa(offset+len(rows)))
	}
	writeJSON(w, http.StatusOK, list(rows))
}

func (s *server) patchConversation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title    *string `json:"title"`
		Archived *bool   `json:"archived"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	c, err := s.Chats.Update(r.Context(), chi.URLParam(r, "id"), application.ChatConvPatch{Title: body.Title, Archived: body.Archived})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *server) deleteConversation(w http.ResponseWriter, r *http.Request) {
	if err := s.Chats.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) readConversation(w http.ResponseWriter, r *http.Request) {
	if err := s.Chats.MarkRead(r.Context(), chi.URLParam(r, "id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) listChatThreads(w http.ResponseWriter, r *http.Request) {
	v, err := s.Chats.Threads(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list(v))
}

// listThreadMessages is GET /chats/threads/{key}/messages?limit=&before= with key
// "general" or an agent id. Oldest first; X-Has-More / X-Next-Before as in /conversations/{id}/messages.
func (s *server) listThreadMessages(w http.ResponseWriter, r *http.Request) {
	limit, ok := intParam(w, r, "limit")
	if !ok {
		return
	}
	msgs, more, err := s.Chats.Thread(r.Context(), application.ChatThreadQuery{
		Thread: strings.TrimSpace(chi.URLParam(r, "key")), BeforeID: r.URL.Query().Get("before"), Limit: limit})
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Access-Control-Expose-Headers", "X-Has-More, X-Next-Before")
	w.Header().Set("X-Has-More", strconv.FormatBool(more))
	if more && len(msgs) > 0 {
		w.Header().Set("X-Next-Before", msgs[0].ID)
	}
	writeJSON(w, http.StatusOK, list(msgs))
}

func (s *server) readChatThread(w http.ResponseWriter, r *http.Request) {
	if err := s.Chats.MarkThreadRead(r.Context(), chi.URLParam(r, "key")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
