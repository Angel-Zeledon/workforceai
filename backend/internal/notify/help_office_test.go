package notify

import (
	"context"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
)

type nopInner struct{}

func (nopInner) Publish(context.Context, domain.Event) error { return nil }

// A help notice about a question of a private office reaches the members through their live events (the recorder
// sealed it with an audience) and never the e-mail addresses and chats of the whole organization: the same rule as the
// approvals of a private office. One that is not sealed is sent as before.
func TestHelpNoticeOfAPrivateOfficeIsNotBroadcast(t *testing.T) {
	s, _, fm, _ := newSvc(t, pending("low"))
	obs := &Observer{Inner: nopInner{}}
	obs.Bind(s)
	payload := map[string]any{"count": 1, "need": "SECRETO-necesidad"}
	if err := obs.Publish(context.Background(), domain.Event{Type: domain.EvHelpNotice, OrgID: "o1", Payload: payload, Audience: []string{"u-member"}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	fm.mu.Lock()
	n := len(fm.sent)
	fm.mu.Unlock()
	if n != 0 {
		t.Fatalf("a sealed help notice was e-mailed to the organization: %d", n)
	}
	if err := obs.Publish(context.Background(), domain.Event{Type: domain.EvHelpNotice, OrgID: "o1", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		fm.mu.Lock()
		n = len(fm.sent)
		fm.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("an unsealed help notice must still be sent")
}
