package presence

import (
	"context"
	"encoding/json"
	"math"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
)

type fakeBus struct {
	mu sync.Mutex
	ev []domain.Event
}

func (b *fakeBus) Publish(_ context.Context, e domain.Event) error {
	b.mu.Lock()
	b.ev = append(b.ev, e)
	b.mu.Unlock()
	return nil
}
func (b *fakeBus) take() []domain.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.ev
	b.ev = nil
	return out
}
func types(evs []domain.Event) []string {
	var t []string
	for _, e := range evs {
		t = append(t, e.Type)
	}
	return t
}
func count(evs []domain.Event, typ string) int {
	n := 0
	for _, e := range evs {
		if e.Type == typ {
			n++
		}
	}
	return n
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }
func newTest() (*Hub, *fakeBus, *clock) {
	b, c := &fakeBus{}, &clock{t: time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)}
	return New(b, Config{Now: c.now}), b, c
}

func TestNextNearHysteresis(t *testing.T) {
	if NextNear(false, 2.6, 2.5, 3.2) {
		t.Error("outside the enter radius must not become near")
	}
	if !NextNear(false, 2.4, 2.5, 3.2) {
		t.Error("inside the enter radius becomes near")
	}
	if !NextNear(true, 3.0, 2.5, 3.2) {
		t.Error("already near stays near between enter and exit (no flicker)")
	}
	if NextNear(true, 3.3, 2.5, 3.2) {
		t.Error("beyond the exit radius stops being near")
	}
}

func TestShouldSendThrottle(t *testing.T) {
	t0 := time.Unix(1000, 0)
	if !ShouldSend(time.Time{}, 0, 0, t0, 0, 0, 100*time.Millisecond, 0.05) {
		t.Error("first update always goes")
	}
	if ShouldSend(t0, 0, 0, t0.Add(50*time.Millisecond), 5, 5, 100*time.Millisecond, 0.05) {
		t.Error("too soon must be held back")
	}
	if ShouldSend(t0, 0, 0, t0.Add(200*time.Millisecond), 0.01, 0, 100*time.Millisecond, 0.05) {
		t.Error("a tiny movement is not worth a frame")
	}
	if !ShouldSend(t0, 0, 0, t0.Add(200*time.Millisecond), 0.5, 0, 100*time.Millisecond, 0.05) {
		t.Error("enough time and distance sends")
	}
}

func TestClampRejectsNonFinite(t *testing.T) {
	if _, _, ok := DefaultBounds.Clamp(math.NaN(), 0); ok {
		t.Error("NaN must be rejected")
	}
	if _, _, ok := DefaultBounds.Clamp(0, math.Inf(1)); ok {
		t.Error("Inf must be rejected")
	}
	x, z, _ := DefaultBounds.Clamp(999, -999)
	if x != DefaultBounds.X1 || z != DefaultBounds.Z0 {
		t.Errorf("clamp = %v,%v", x, z)
	}
}

func TestJoinLeaveAndMultiTab(t *testing.T) {
	h, bus, _ := newTest()
	h.Join("o1", "ana", "Ana")
	h.Join("o1", "ana", "Ana") // second tab
	if got := count(bus.take(), "presence.joined"); got != 1 {
		t.Fatalf("joined announced %d times, want 1", got)
	}
	h.Leave("o1", "ana")
	if !h.Online("o1", "ana") || len(bus.take()) != 0 {
		t.Fatal("closing one of two tabs must not make the person leave")
	}
	h.Leave("o1", "ana")
	if h.Online("o1", "ana") || count(bus.take(), "presence.left") != 1 {
		t.Fatal("last tab closed: presence.left expected")
	}
}

func TestOrgIsolation(t *testing.T) {
	h, bus, _ := newTest()
	h.Join("o1", "ana", "Ana")
	h.Join("o2", "bob", "Bob")
	for _, e := range bus.take() {
		if e.Type == "presence.joined" && e.OrgID == "" {
			t.Fatal("every event must carry the org")
		}
	}
	if s := h.Snapshot("o1"); len(s) != 1 || s[0].UserID != "ana" {
		t.Errorf("snapshot o1 = %+v", s)
	}
	// same coordinates in two organizations never produce proximity
	_ = h.Move("o1", "ana", 1, 1, "")
	_ = h.Move("o2", "bob", 1, 1, "")
	if n := count(bus.take(), "presence.proximity"); n != 0 {
		t.Errorf("proximity across organizations: %d events", n)
	}
	// the same user id in another org is a different person
	h.Join("o2", "ana", "Ana (other org)")
	if h.Snapshot("o1")[0].Name != "Ana" {
		t.Error("o2 join changed o1 state")
	}
}

func TestMoveThrottleAndSweepFlush(t *testing.T) {
	h, bus, c := newTest()
	h.Join("o", "ana", "Ana")
	bus.take()
	c.add(time.Second)
	_ = h.Move("o", "ana", 1, 1, "lobby") // first move after join: allowed
	if count(bus.take(), "presence.moved") != 1 {
		t.Fatal("expected one moved frame")
	}
	c.add(10 * time.Millisecond)
	for i := 0; i < 50; i++ {
		_ = h.Move("o", "ana", 1+float64(i)*0.1, 1, "lobby")
	}
	if n := count(bus.take(), "presence.moved"); n != 0 {
		t.Fatalf("a burst within the interval must be held, got %d frames", n)
	}
	c.add(200 * time.Millisecond)
	h.Sweep()
	ev := bus.take()
	if count(ev, "presence.moved") != 1 {
		t.Fatalf("sweep must flush the latest position once, got %v", types(ev))
	}
	p := h.Snapshot("o")[0]
	if math.Abs(p.X-5.9) > 1e-9 {
		t.Errorf("state must hold the latest position, x=%v", p.X)
	}
	if count(bus.take(), "presence.moved") != 0 {
		t.Error("nothing pending after the flush")
	}
}

func TestProximityEventsAndHook(t *testing.T) {
	h, bus, c := newTest()
	var hooked []Proximity
	h.OnProximity(func(org string, p Proximity) {
		if org != "o" {
			t.Errorf("hook org = %s", org)
		}
		hooked = append(hooked, p)
	})
	h.Join("o", "ana", "Ana")
	h.Join("o", "bob", "Bob")
	c.add(time.Second)
	_ = h.Move("o", "ana", -8, -5, "")
	_ = h.Move("o", "bob", -5, -5, "")
	bus.take()
	c.add(time.Second)
	_ = h.Move("o", "bob", -7, -5, "") // 1 m from ana
	ev := bus.take()
	if count(ev, "presence.proximity") != 1 || len(hooked) != 1 || !hooked[0].Near || !hooked[0].CanTalk {
		t.Fatalf("expected one near event with an offer, events=%v hooked=%+v", types(ev), hooked)
	}
	// ana goes into focus: the pair is re-announced without the offer
	_ = h.SetStatus("o", "ana", StatusFocus)
	if last := hooked[len(hooked)-1]; !last.Near || last.CanTalk {
		t.Errorf("focus must remove the offer: %+v", last)
	}
	_ = h.SetStatus("o", "ana", StatusAvailable)
	// bob walks away beyond the exit radius
	c.add(time.Second)
	_ = h.Move("o", "bob", -2, -5, "")
	if last := hooked[len(hooked)-1]; last.Near {
		t.Errorf("must be apart: %+v", last)
	}
	// leaving while near ends the pair
	c.add(time.Second)
	_ = h.Move("o", "bob", -7.5, -5, "")
	n := len(hooked)
	h.Leave("o", "bob")
	if len(hooked) != n+1 || hooked[n].Near {
		t.Errorf("leave must close the proximity: %+v", hooked[n:])
	}
}

func TestAutoAwayAndWake(t *testing.T) {
	h, bus, c := newTest()
	h.Join("o", "ana", "Ana")
	h.Join("o", "bob", "Bob")
	_ = h.SetStatus("o", "bob", StatusFocus)
	bus.take()
	c.add(4 * time.Minute)
	h.Sweep()
	if len(bus.take()) != 0 {
		t.Fatal("not idle long enough yet")
	}
	c.add(2 * time.Minute)
	h.Sweep()
	var st map[string]Status
	st = map[string]Status{}
	for _, p := range h.Snapshot("o") {
		st[p.UserID] = p.Status
	}
	if st["ana"] != StatusAway {
		t.Errorf("idle available person must be away, got %v", st["ana"])
	}
	if st["bob"] != StatusFocus {
		t.Errorf("focus is deliberate and must not be overridden, got %v", st["bob"])
	}
	bus.take()
	if err := h.Touch("o", "ana"); err != nil {
		t.Fatal(err)
	}
	if h.Snapshot("o")[0].Status != StatusAvailable {
		t.Error("activity must revert an automatic away")
	}
	// a manual away is not reverted by activity
	_ = h.SetStatus("o", "ana", StatusAway)
	_ = h.Touch("o", "ana")
	if h.Snapshot("o")[0].Status != StatusAway {
		t.Error("manual away must stay")
	}
}

func TestHandleFramesUseCallerIdentity(t *testing.T) {
	h, _, c := newTest()
	h.Join("o", "ana", "Ana")
	h.Join("o", "bob", "Bob")
	c.add(time.Second)
	// the frame tries to impersonate bob: the extra field is ignored, the caller moves
	raw, _ := json.Marshal(map[string]any{"type": "presence.move", "x": 3, "z": 2, "user_id": "bob"})
	if err := h.Handle("o", "ana", raw); err != nil {
		t.Fatal(err)
	}
	for _, p := range h.Snapshot("o") {
		if p.UserID == "bob" && p.X == 3 {
			t.Fatal("a frame must not move someone else")
		}
	}
	for _, bad := range []string{`{"type":"presence.move"}`, `{"type":"nope"}`, `not json`, `{"type":"presence.status","status":"busy"}`} {
		if err := h.Handle("o", "ana", []byte(bad)); err == nil {
			t.Errorf("frame %q must be rejected", bad)
		}
	}
	if err := h.Handle("o", "ghost", []byte(`{"type":"presence.ping"}`)); err != ErrNotOnline {
		t.Errorf("unknown person: %v", err)
	}
}

func TestZoneIsSanitized(t *testing.T) {
	if got := CleanText("  lo\nbby\x00  ", 40); got != "lobby" {
		t.Errorf("CleanText = %q", got)
	}
	if got := CleanText("abcdef", 3); got != "abc" {
		t.Errorf("CleanText cut = %q", got)
	}
}
