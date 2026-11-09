package api

import (
	"context"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// maxChatAttachments is application's limit of files per chat message (more is a 400 there).
const maxChatAttachments = 10

// postChat is POST /api/v1/messages {conversation, text}: the user talks to the
// office ("office") or to one agent ("agent:<id>"). The message is stored and
// the turn (routing, replies, maybe a request) runs asynchronously; everything
// that follows arrives as WebSocket events (docs/architecture/chat-routing.md).
func (s *server) postChat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Conversation string `json:"conversation"`
		Text         string `json:"text"`
		// ClientMessageID is an alternative to the Idempotency-Key header.
		ClientMessageID string `json:"client_message_id"`
		// Attachments are drive paths the user uploaded for this message (max 10; more is a 400); the agents read them with fs.read.
		Attachments []string `json:"attachments"`
		// CustomerID (B12, optional) scopes any request this message creates to one customer, like POST /requests:
		// the scope is stored before the request exists and its tasks only see that customer's memories and files.
		CustomerID string `json:"customer_id"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if body.Conversation == "" {
		body.Conversation = domain.ChatOffice
	}
	if err := s.checkAgentUse(r, body.Conversation); err != nil { // E2: an agent the person may not use
		s.fail(w, err)
		return
	}
	// A retry or double click with the same Idempotency-Key (or client_message_id) gets the first turn back.
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		key = body.ClientMessageID
	}
	ctx := application.WithCustomer(r.Context(), body.CustomerID)
	ctx = application.WithActorName(ctx, s.authorName(ctx)) // E0: the author shown on the message and told to the agent
	if len(body.Attachments) > 0 {
		// a path the person may not read (an office folder) is dropped exactly like one that does not resolve; more
		// than the limit is left whole so the count is refused as before, whatever is hidden
		if len(body.Attachments) <= maxChatAttachments {
			body.Attachments = driveVisible(s, r, body.Attachments, func(p string) string { return path.Clean("/" + strings.TrimSpace(p)) })
		}
		ctx = application.WithAttachments(ctx, body.Attachments)
	}
	turn, replayed, err := s.Orch.PostChatKeyed(ctx, body.Conversation, body.Text, key)
	if err != nil {
		s.fail(w, err)
		return
	}
	if replayed {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(w, http.StatusAccepted, turn)
}

// authorName is the display name of the signed-in person ("" without authentication): their account name,
// else the part of their e-mail before the @, else the directory's label.
func (s *server) authorName(ctx context.Context) string {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return ""
	}
	if s.Auth != nil {
		if v, err := s.Auth.Me(ctx, p); err == nil {
			if n := strings.TrimSpace(v.Name); n != "" {
				return n
			}
			if local, _, ok := strings.Cut(v.Email, "@"); ok && local != "" {
				return local
			}
		}
	}
	return s.personName(ctx, p.OrgID, p.UserID)
}

// listMessages is GET /conversations/{id}/messages. Without query parameters it
// returns every message (oldest first), as before. With ?limit=N (max 200) it
// returns the newest N; ?before=<message id> returns the N messages older than
// that one. The body stays a plain array; the headers X-Has-More and
// X-Next-Before say whether older messages exist and which id to pass next.
func (s *server) listMessages(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be a positive integer"})
			return
		}
		limit = n
	}
	before := r.URL.Query().Get("before")
	if before != "" && limit == 0 {
		limit = 50
	}
	msgs, more, err := s.Queries.ChatMessages(r.Context(), chi.URLParam(r, "id"), before, limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	if limit > 0 {
		w.Header().Set("Access-Control-Expose-Headers", "X-Has-More, X-Next-Before")
		w.Header().Set("X-Has-More", strconv.FormatBool(more))
		if more && len(msgs) > 0 {
			w.Header().Set("X-Next-Before", msgs[0].ID)
		}
	}
	writeJSON(w, http.StatusOK, list(msgs))
}
