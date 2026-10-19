package events

import (
	"io"
	"log/slog"
	"strings"
	"testing"
)

func TestHubAudienceOnlyReachesTheAddressedUsers(t *testing.T) {
	h := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ana, bob := h.RegisterUser("org-a", "ana"), h.RegisterUser("org-a", "bob")
	anon := h.Register("org-a")
	other := h.RegisterUser("org-b", "ana") // same user id in another organization
	h.Broadcast([]byte(`{"org_id":"org-a","type":"pchat.message","audience":["ana"],"payload":{}}`))
	f := recv(ana)
	if f == "" {
		t.Fatal("addressed user must receive the frame")
	}
	if strings.Contains(f, "audience") {
		t.Errorf("the audience must be stripped before delivery: %s", f)
	}
	if x := recv(bob) + recv(anon) + recv(other); x != "" {
		t.Errorf("non addressed clients received a private frame: %s", x)
	}
	h.Broadcast([]byte(`{"org_id":"org-a","type":"presence.joined"}`))
	if recv(ana) == "" || recv(bob) == "" || recv(anon) == "" {
		t.Error("frames without audience go to everyone of the org")
	}
	if recv(other) != "" {
		t.Error("org isolation broken")
	}
}
