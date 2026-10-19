// Package events contains the WebSocket hub and publisher helpers.
package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"aiworkforce/backend/internal/domain"
)

// Client is one connected WebSocket peer.
type Client struct {
	Send chan []byte
	org  string
	user string // "" for clients registered without an identity (they never get addressed frames)
}

// Hub fans raw JSON frames out to the connected clients of the frame's
// organization (the frame's "org_id"); frames without org_id are dropped, so
// one tenant never receives another tenant's events.
type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]struct{}
	log     *slog.Logger
}

func NewHub(log *slog.Logger) *Hub {
	return &Hub{clients: map[*Client]struct{}{}, log: log}
}

// Register adds a client that only receives frames of orgID.
func (h *Hub) Register(orgID string) *Client { return h.RegisterUser(orgID, "") }

// RegisterUser adds a client of orgID that also receives the frames addressed to userID
// (frames with an "audience", e.g. private chat messages).
func (h *Hub) RegisterUser(orgID, userID string) *Client {
	c := &Client{Send: make(chan []byte, 256), org: orgID, user: userID}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	return c
}

func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.Send)
	}
	h.mu.Unlock()
}

// Broadcast delivers a frame to the clients of its organization; slow clients
// are dropped.
func (h *Hub) Broadcast(frame []byte) {
	var head struct {
		OrgID    string   `json:"org_id"`
		Audience []string `json:"audience"`
	}
	if err := json.Unmarshal(frame, &head); err != nil || head.OrgID == "" {
		h.log.Warn("dropping frame without org_id")
		return
	}
	var allow map[string]struct{}
	if len(head.Audience) > 0 {
		allow = make(map[string]struct{}, len(head.Audience))
		for _, u := range head.Audience {
			allow[u] = struct{}{}
		}
		frame = stripAudience(frame)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.org != head.OrgID {
			continue
		}
		if allow != nil {
			if _, ok := allow[c.user]; !ok || c.user == "" {
				continue
			}
		}
		select {
		case c.Send <- frame:
		default:
			h.log.Warn("dropping slow websocket client")
			delete(h.clients, c)
			close(c.Send)
		}
	}
}

// stripAudience removes the "audience" field so clients never learn who else received the frame.
func stripAudience(frame []byte) []byte {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(frame, &m); err != nil {
		return frame
	}
	delete(m, "audience")
	out, err := json.Marshal(m)
	if err != nil {
		return frame
	}
	return out
}

// Run forwards frames from a subscription (Redis) to the clients until ctx ends.
func (h *Hub) Run(ctx context.Context, in <-chan []byte) {
	for {
		select {
		case <-ctx.Done():
			return
		case f, ok := <-in:
			if !ok {
				return
			}
			h.Broadcast(f)
		}
	}
}

// ClientCount reports connected clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// LocalBus publishes straight to the hub (no Redis).
type LocalBus struct{ Hub *Hub }

func (b LocalBus) Publish(_ context.Context, e domain.Event) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	b.Hub.Broadcast(raw)
	return nil
}

// Publisher is the minimal publishing contract.
type Publisher interface {
	Publish(ctx context.Context, e domain.Event) error
}

// FallbackBus tries Primary (Redis) and falls back to the local hub when Redis
// is unreachable, so the UI keeps working in degraded mode.
type FallbackBus struct {
	Primary  Publisher
	Fallback Publisher
	Log      *slog.Logger
}

func (b FallbackBus) Publish(ctx context.Context, e domain.Event) error {
	if err := b.Primary.Publish(ctx, e); err != nil {
		b.Log.Warn("redis publish failed, delivering locally", "err", err)
		return b.Fallback.Publish(ctx, e)
	}
	return nil
}
