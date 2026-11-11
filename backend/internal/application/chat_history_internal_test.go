package application

import (
	"testing"
	"time"
)

// D3: the person's own thread with an agent and the earlier shared history are one row; when the newer of the two
// has its preview sealed (it quoted an agent whose work the person does not see), the row keeps the preview of the
// person's own thread instead of going blank.
func TestPublicRowsKeepTheOwnPreviewWhenTheNewerIsSealed(t *testing.T) {
	older, newer := time.Now().UTC().Add(-time.Hour), time.Now().UTC()
	own := ChatConversation{ID: "agent:sales@me", OwnerID: "me", Preview: "mi-pregunta", PreviewFrom: "user", UpdatedAt: older, LastMessageAt: older, MessageCount: 2}
	shared := ChatConversation{ID: "agent:sales", UpdatedAt: newer, LastMessageAt: newer, MessageCount: 3} // preview sealed
	for _, rows := range [][]ChatConversation{{own, shared}, {shared, own}} {
		got := publicRows("me", rows)
		if len(got) != 1 || got[0].Preview != "mi-pregunta" || got[0].PreviewFrom != "user" || !got[0].UpdatedAt.Equal(newer) || got[0].MessageCount != 5 {
			t.Fatalf("merged row = %+v", got)
		}
	}
	// a visible newer preview still wins
	shared.Preview, shared.PreviewFrom = "respuesta-nueva", "sales"
	if got := publicRows("me", []ChatConversation{own, shared}); got[0].Preview != "respuesta-nueva" {
		t.Fatalf("merged row = %+v", got[0])
	}
}
