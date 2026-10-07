package events

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

func recv(c *Client) string {
	select {
	case f := <-c.Send:
		return string(f)
	case <-time.After(50 * time.Millisecond):
		return ""
	}
}

func TestHubDeliversOnlyToTheFramesOrganization(t *testing.T) {
	h := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	a, b := h.Register("org-a"), h.Register("org-b")
	h.Broadcast([]byte(`{"org_id":"org-a","type":"x"}`))
	if recv(a) == "" {
		t.Error("org-a client must receive its frame")
	}
	if f := recv(b); f != "" {
		t.Errorf("org-b client received org-a frame: %s", f)
	}
	h.Broadcast([]byte(`{"type":"no-org"}`))
	h.Broadcast([]byte(`not json`))
	if f := recv(a) + recv(b); f != "" {
		t.Errorf("frames without org must be dropped, got %s", f)
	}
}
