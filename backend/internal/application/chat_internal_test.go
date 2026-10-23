package application

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// A reply stamped in the same clock tick as the user's message (instant fallback, coarse Windows clock)
// must still sort after it once the store keeps microseconds (truncated or rounded).
func TestChatReplyTSSortsAfterTheUserMessage(t *testing.T) {
	future := time.Now().UTC().Add(time.Hour).Add(999 * time.Nanosecond) // "now" is always before it
	got := (&chatTurn{userTS: future}).replyTS()
	for _, prec := range []func(time.Time) time.Time{
		func(x time.Time) time.Time { return x.Truncate(time.Microsecond) },
		func(x time.Time) time.Time { return x.Round(time.Microsecond) },
	} {
		if !prec(got).After(prec(future)) {
			t.Fatalf("reply %v does not sort after user message %v", got, future)
		}
	}
	// same tick: it waits for the clock rather than stamping ahead of it (a later "now" read mark must
	// not see the reply as newer)
	soon := time.Now().UTC().Add(3 * time.Millisecond)
	got = (&chatTurn{userTS: soon}).replyTS()
	if !got.Truncate(time.Microsecond).After(soon.Truncate(time.Microsecond)) || got.After(time.Now().UTC()) {
		t.Fatalf("reply %v vs user %v: must be after it and not in the future", got, soon)
	}
	// a normal (later) reply keeps the real time
	before := time.Now().UTC()
	if got := (&chatTurn{userTS: before.Add(-time.Hour)}).replyTS(); got.Before(before) || got.Sub(before) > time.Minute {
		t.Fatalf("later reply re-stamped: %v", got)
	}
	if (&chatTurn{}).replyTS().IsZero() {
		t.Fatal("a turn without a user message must still be stamped")
	}
}

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
