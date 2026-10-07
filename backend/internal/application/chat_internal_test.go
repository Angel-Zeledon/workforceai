package application

import (
	"errors"
	"strings"
	"testing"
)

func TestChatSafeCauseNeverLeaksInternalAddresses(t *testing.T) {
	for _, e := range []error{
		errors.New(`agent-runtime no disponible (plan): runtime /v1/plan: Post "http://127.0.0.1:8000/v1/plan": dial tcp 127.0.0.1:8000: connectex: connection refused`),
		errors.New("context deadline exceeded"),
		errors.New("unexpected EOF"),
	} {
		got := chatSafeCause("es", e)
		if strings.Contains(got, "http") || strings.Contains(got, "127.0.0.1") || got != chatText("es", "cause_unavailable") {
			t.Errorf("%v leaked: %q", e, got)
		}
		if en := chatSafeCause("en", e); en != chatText("en", "cause_unavailable") || en == got {
			t.Errorf("english cause missing: %q", en)
		}
	}
	// plain domain messages are kept (they are written for people) and long ones are trimmed
	if got := chatSafeCause("es", errors.New("el plan no fue aprobado")); got != "el plan no fue aprobado" {
		t.Errorf("a human message must survive: %q", got)
	}
	if got := chatSafeCause("es", errors.New(strings.Repeat("a", 500))); len([]rune(got)) > 201 {
		t.Errorf("long causes are trimmed: %d", len(got))
	}
}
