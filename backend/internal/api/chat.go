package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/domain"
)

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
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if body.Conversation == "" {
		body.Conversation = domain.ChatOffice
	}
	// A retry or double click with the same Idempotency-Key (or client_message_id) gets the first turn back.
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		key = body.ClientMessageID
	}
	turn, replayed, err := s.Orch.PostChatKeyed(r.Context(), body.Conversation, body.Text, key)
	if err != nil {
		s.fail(w, err)
		return
	}
	if replayed {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(w, http.StatusAccepted, turn)
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
