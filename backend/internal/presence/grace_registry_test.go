package presence

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// ---- leave grace period ----

func newGrace(grace time.Duration) (*Hub, *fakeBus, *clock) {
	b, c := &fakeBus{}, &clock{t: time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)}
	return New(b, Config{Now: c.now, LeaveGrace: grace}), b, c
}

func TestReconnectInsideGraceDoesNotFlicker(t *testing.T) {
	h, bus, c := newGrace(8 * time.Second)
	h.Join("o1", "ana", "Ana")
	bus.take()
	h.Leave("o1", "ana") // the websocket was renewed
	c.add(2 * time.Second)
	h.Sweep()
	if !h.Online("o1", "ana") || len(h.Snapshot("o1")) != 1 {
		t.Fatal("the person must stay visible during the grace period")
	}
	h.Join("o1", "ana", "Ana")
	c.add(30 * time.Second)
	h.Sweep()
	if evs := bus.take(); count(evs, "presence.left") != 0 || count(evs, "presence.joined") != 0 {
		t.Fatalf("a reconnect inside the grace period must emit neither left nor joined: %v", types(evs))
	}
	if !h.Online("o1", "ana") {
		t.Fatal("still online after the reconnect")
	}
}

func TestLeaveAnnouncedAfterGrace(t *testing.T) {
	h, bus, c := newGrace(8 * time.Second)
	h.Join("o1", "ana", "Ana")
	bus.take()
	h.Leave("o1", "ana")
	c.add(7 * time.Second)
	h.Sweep()
	if count(bus.take(), "presence.left") != 0 {
		t.Fatal("left must wait for the grace period")
	}
	c.add(2 * time.Second)
	h.Sweep()
	if count(bus.take(), "presence.left") != 1 {
		t.Fatal("left is announced once the grace period ends")
	}
	if h.Online("o1", "ana") {
		t.Fatal("gone after the grace period")
	}
	h.Sweep()
	if count(bus.take(), "presence.left") != 0 {
		t.Fatal("left is announced only once")
	}
}

func TestGraceKeepsOtherTabsSemantics(t *testing.T) {
	h, bus, c := newGrace(8 * time.Second)
	h.Join("o1", "ana", "Ana")
	h.Join("o1", "ana", "Ana")
	bus.take()
	h.Leave("o1", "ana")
	c.add(20 * time.Second)
	h.Sweep()
	if count(bus.take(), "presence.left") != 0 || !h.Online("o1", "ana") {
		t.Fatal("one tab still open: no leave at all")
	}
}

func TestGraceEndsProximityOnlyWhenTheLeaveHappens(t *testing.T) {
	h, bus, c := newGrace(8 * time.Second)
	h.Join("o1", "ana", "Ana")
	h.Join("o1", "bob", "Bob")
	_ = h.Move("o1", "ana", 0, 0, "")
	c.add(time.Second)
	_ = h.Move("o1", "bob", 1, 0, "")
	bus.take()
	h.Leave("o1", "bob")
	if count(bus.take(), "presence.proximity") != 0 {
		t.Fatal("proximity must survive the grace period")
	}
	c.add(9 * time.Second)
	h.Sweep()
	evs := bus.take()
	if count(evs, "presence.proximity") != 1 || count(evs, "presence.left") != 1 {
		t.Fatalf("proximity ends with the leave: %v", types(evs))
	}
}

func TestDefaultGraceIs8s(t *testing.T) {
	cfg := Config{}
	cfg.defaults()
	if cfg.LeaveGrace != 8*time.Second {
		t.Fatalf("default grace = %v", cfg.LeaveGrace)
	}
}

// ---- shared registry across replicas ----

func twoReplicas() (a, b *Hub, busA, busB *fakeBus, c *clock) {
	c = &clock{t: time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)}
	reg := &MemoryRegistry{Now: c.now}
	busA, busB = &fakeBus{}, &fakeBus{}
	a = New(busA, Config{Now: c.now, LeaveGrace: -1, Registry: reg, Replica: "A"})
	b = New(busB, Config{Now: c.now, LeaveGrace: -1, Registry: reg, Replica: "B"})
	return
}

func TestSnapshotListsPeopleOfOtherReplicas(t *testing.T) {
	a, b, _, _, _ := twoReplicas()
	a.Join("o1", "ana", "Ana")
	b.Join("o1", "bob", "Bob")
	b.Join("o2", "zed", "Zed")
	for name, h := range map[string]*Hub{"A": a, "B": b} {
		snap := h.Snapshot("o1")
		if len(snap) != 2 || snap[0].UserID != "ana" || snap[1].UserID != "bob" {
			t.Errorf("replica %s snapshot = %+v", name, snap)
		}
		if !h.Online("o1", "ana") || !h.Online("o1", "bob") || h.Online("o1", "zed") {
			t.Errorf("replica %s Online is wrong", name)
		}
	}
}

func TestLeaveNotAnnouncedWhileAnotherReplicaHoldsThePerson(t *testing.T) {
	a, b, busA, _, _ := twoReplicas()
	a.Join("o1", "ana", "Ana")
	b.Join("o1", "ana", "Ana") // tabs on two replicas
	busA.take()
	a.Leave("o1", "ana")
	if count(busA.take(), "presence.left") != 0 {
		t.Fatal("ana is still connected through replica B: no left")
	}
}

func TestLastReplicaAnnouncesLeaveAndRegistryIsCleaned(t *testing.T) {
	a, b, _, busB, _ := twoReplicas()
	a.Join("o1", "ana", "Ana")
	b.Join("o1", "ana", "Ana")
	a.Leave("o1", "ana")
	busB.take()
	b.Leave("o1", "ana")
	if count(busB.take(), "presence.left") != 1 {
		t.Fatal("the last socket anywhere announces left")
	}
	if len(a.Snapshot("o1")) != 0 {
		t.Fatal("registry must be empty")
	}
}

func TestDeadReplicaLeavesNoGhosts(t *testing.T) {
	a, b, _, _, c := twoReplicas()
	b.Join("o1", "bob", "Bob")
	if len(a.Snapshot("o1")) != 1 {
		t.Fatal("bob is visible from A")
	}
	c.add(31 * time.Second) // B dies without leaving: its entries expire (TTL 30s)
	if len(a.Snapshot("o1")) != 0 {
		t.Fatal("an expired entry must disappear")
	}
}

func TestSweepRefreshesRegistryTTL(t *testing.T) {
	a, b, _, _, c := twoReplicas()
	b.Join("o1", "bob", "Bob")
	for i := 0; i < 5; i++ { // 50 s in 10 s steps: B keeps sweeping, so bob never expires
		c.add(10 * time.Second)
		b.Sweep()
	}
	if len(a.Snapshot("o1")) != 1 {
		t.Fatal("a live replica keeps its people in the registry")
	}
}

type brokenRegistry struct{}

var errBroken = errors.New("redis down")

func (brokenRegistry) Put(context.Context, string, string, Person) error    { return errBroken }
func (brokenRegistry) Delete(context.Context, string, string, string) error { return errBroken }
func (brokenRegistry) List(context.Context, string) ([]Remote, error)       { return nil, errBroken }

func TestRegistryFailureDegradesToLocal(t *testing.T) {
	b, c := &fakeBus{}, &clock{t: time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)}
	h := New(b, Config{Now: c.now, LeaveGrace: -1, Registry: brokenRegistry{}})
	h.Join("o1", "ana", "Ana")
	if len(h.Snapshot("o1")) != 1 || !h.Online("o1", "ana") {
		t.Fatal("local presence must work with a broken registry")
	}
	h.Leave("o1", "ana")
	if count(b.take(), "presence.left") != 1 {
		t.Fatal("left still announced")
	}
}

func TestParseRegistryRows(t *testing.T) {
	now := time.Unix(1000, 0)
	good, _ := json.Marshal(redisRow{Exp: 1010, P: Person{UserID: "u1", Name: "Ana"}})
	old, _ := json.Marshal(redisRow{Exp: 990, P: Person{UserID: "u2"}})
	got := parseRegistryRows([]string{"A|u1", string(good), "A|u2", string(old), "bad", "{", "B|u3", "not json", "dangling"}, now)
	if len(got) != 1 || got[0].Replica != "A" || got[0].Person.UserID != "u1" {
		t.Fatalf("rows = %+v", got)
	}
}
