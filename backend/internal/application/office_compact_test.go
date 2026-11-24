package application_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
)

// The summary of /compact may hold what another office said in the chat: a turn of an office starts from its own
// messages and never gets it (the model sees it again only in a turn asked with no office, the old behaviour).
func TestIsolation_CompactSummaryNeverReachesATurnOfAnOffice(t *testing.T) {
	for _, c := range []struct {
		office string
		sees   bool
	}{{"o-ven", false}, {"o-legal", false}, {"", true}} {
		rt := newChatRuntime(fixedRoute("question", "general", primary("sales")))
		h := newChat(t, rt, nil)
		h.orch.SetOfficeScope(scopeFake{})
		h.orch.SetConvContext(h.store)
		seedOfficeHistory(t, h, "agent:sales")
		cc := application.ConvContext{ConversationID: "agent:sales", Epoch: 1, Summary: "LEGALSUMMARY de lo que se habló del contrato", SummaryUpto: time.Now().Add(-30 * time.Minute)}
		if err := h.orch.SaveConvContext(context.Background(), cc); err != nil {
			t.Fatal(err)
		}
		if _, err := h.orch.PostChat(application.WithRequestOffice(context.Background(), c.office), "agent:sales", "¿cómo vamos?"); err != nil {
			t.Fatal(err)
		}
		h.orch.Wait()
		rt.mu.Lock()
		raw, _ := json.Marshal(rt.replies)
		n := len(rt.replies)
		rt.mu.Unlock()
		if n == 0 {
			t.Fatalf("office %q: nobody answered", c.office)
		}
		if got := strings.Contains(string(raw), "LEGALSUMMARY"); got != c.sees {
			t.Errorf("office %q: the model sees the /compact summary = %v, want %v: %s", c.office, got, c.sees, raw)
		}
	}
}
