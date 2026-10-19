package presence

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/events"
)

// Config tunes the hub; zero values take the defaults.
type Config struct {
	MoveMinInterval time.Duration // minimum time between two broadcasts of the same person's position (default 120ms)
	MoveMinDelta    float64       // minimum movement worth broadcasting, in meters (default 0.06)
	AwayAfter       time.Duration // idle time before an available person becomes away (default 5m; negative disables)
	EnterRadius     float64       // distance at which two people become near (default 2.5 m)
	ExitRadius      float64       // distance at which they stop being near (default 3.2 m)
	Bounds          Bounds
	Now             func() time.Time
	// LeaveGrace is how long the last socket of a person may be gone before presence.left is announced; a
	// reconnect inside it (the websocket is renewed every WSMaxAge) is invisible to the others. Default 8s,
	// negative disables the grace (immediate leave).
	LeaveGrace time.Duration
	// Registry is the directory shared by the replicas (nil: this replica only). Replica identifies this process in it.
	Registry Registry
	Replica  string
	// SyncEvery is how often the live entries are refreshed in the registry (default 10s; its TTL must be larger).
	SyncEvery time.Duration
}

func (c *Config) defaults() {
	if c.MoveMinInterval <= 0 {
		c.MoveMinInterval = 120 * time.Millisecond
	}
	if c.MoveMinDelta <= 0 {
		c.MoveMinDelta = 0.06
	}
	if c.AwayAfter == 0 {
		c.AwayAfter = 5 * time.Minute
	}
	if c.EnterRadius <= 0 {
		c.EnterRadius = 2.5
	}
	if c.ExitRadius <= c.EnterRadius {
		c.ExitRadius = c.EnterRadius + 0.7
	}
	if c.Bounds == (Bounds{}) {
		c.Bounds = DefaultBounds
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.LeaveGrace == 0 {
		c.LeaveGrace = 8 * time.Second
	}
	if c.SyncEvery <= 0 {
		c.SyncEvery = 10 * time.Second
	}
	if c.Replica == "" {
		c.Replica = uuid.NewString()
	}
}

// Person is the public view of someone online (what travels to the other people of the organization).
type Person struct {
	UserID string  `json:"user_id"`
	Name   string  `json:"name"`
	Status Status  `json:"status"`
	X      float64 `json:"x"`
	Z      float64 `json:"z"`
	Zone   string  `json:"zone,omitempty"`
	Since  string  `json:"since"`
	// AutoAway: the status is away because the person was idle (it reverts by itself on activity).
	AutoAway bool `json:"auto_away,omitempty"`
}

// Proximity is the payload of presence.proximity and of the call hook: two people came near (or apart).
// CanTalk is false when either is not approachable (focus, meeting or away): the UI then shows no offer.
type Proximity struct {
	A        string  `json:"a"`
	B        string  `json:"b"`
	Near     bool    `json:"near"`
	Distance float64 `json:"distance"`
	CanTalk  bool    `json:"can_talk"`
}

type entry struct {
	p          Person
	conns      int
	lastActive time.Time
	// last broadcast position and time (throttle) and whether a newer position waits for the next flush
	sentAt       time.Time
	sentX, sentZ float64
	dirty        bool
	near         map[string]bool
	// leaveAt: the last socket is gone and the leave is announced at this time unless the person reconnects
	leaveAt time.Time
}

// Hub is the registry of people online, one namespace per organization.
type Hub struct {
	cfg Config
	pub events.Publisher

	mu   sync.Mutex
	orgs map[string]map[string]*entry // org -> user -> entry

	hookMu sync.RWMutex
	hook   func(org string, p Proximity)

	lastSync time.Time
}

// New builds a hub that publishes its events through pub (the same bus as the rest of the system).
func New(pub events.Publisher, cfg Config) *Hub {
	cfg.defaults()
	return &Hub{cfg: cfg, pub: pub, orgs: map[string]map[string]*entry{}}
}

// OnProximity registers the hook point of the call stream: it receives every proximity change (near and apart).
// It is called outside the hub lock, so it may call back into the hub.
func (h *Hub) OnProximity(fn func(org string, p Proximity)) {
	h.hookMu.Lock()
	h.hook = fn
	h.hookMu.Unlock()
}

var (
	// ErrBadFrame marks an inbound frame that is malformed or of an unknown type.
	ErrBadFrame = errors.New("presence: bad frame")
	// ErrNotOnline: the person has no registered socket.
	ErrNotOnline = errors.New("presence: not online")
)

type outEvent struct {
	typ     string
	payload any
}

func (h *Hub) emit(org string, evs []outEvent, hooks []Proximity) {
	for _, e := range evs {
		_ = h.pub.Publish(context.Background(), events.NewEvent(org, e.typ, e.payload))
	}
	h.hookMu.RLock()
	fn := h.hook
	h.hookMu.RUnlock()
	if fn != nil {
		for _, p := range hooks {
			fn(org, p)
		}
	}
}

// Join registers one socket of a person. The first socket announces presence.joined; more tabs of the same
// person share the entry. Start position is the entrance of the office.
func (h *Hub) Join(org, user, name string) Person {
	now := h.cfg.Now()
	h.mu.Lock()
	users := h.orgs[org]
	if users == nil {
		users = map[string]*entry{}
		h.orgs[org] = users
	}
	e := users[user]
	first := e == nil
	if first {
		e = &entry{p: Person{UserID: user, Name: CleanText(name, 80), Status: StatusAvailable, X: 0, Z: 6, Since: now.UTC().Format(time.RFC3339)}, near: map[string]bool{}}
		users[user] = e
	}
	e.conns++
	e.lastActive = now
	e.leaveAt = time.Time{} // a reconnect inside the grace period cancels the pending leave: nobody sees a flicker
	var evs []outEvent
	if first {
		e.sentAt, e.sentX, e.sentZ = now, e.p.X, e.p.Z
		evs = append(evs, outEvent{events.EvPresenceJoined, e.p})
	} else if e.p.AutoAway {
		evs = append(evs, h.wake(e))
	}
	p := e.p
	h.mu.Unlock()
	h.emit(org, evs, nil)
	h.regPut(org, p)
	return p
}

// Leave unregisters one socket. When the last one goes, the person stays visible for LeaveGrace (so a websocket
// reconnect does not flicker leave/join); Sweep then removes them, announces presence.left and ends their
// proximities. With the grace disabled the removal is immediate.
func (h *Hub) Leave(org, user string) {
	now := h.cfg.Now()
	h.mu.Lock()
	e := h.orgs[org][user]
	if e == nil || e.conns <= 0 {
		h.mu.Unlock()
		return
	}
	e.conns--
	if e.conns > 0 {
		h.mu.Unlock()
		return
	}
	if h.cfg.LeaveGrace > 0 {
		e.leaveAt = now.Add(h.cfg.LeaveGrace)
		h.mu.Unlock()
		return
	}
	evs, hooks := h.remove(org, user, e)
	h.mu.Unlock()
	h.finishLeave(org, user, evs, hooks)
}

// remove deletes e and ends its proximities (lock held). The presence.left event is added by finishLeave, which
// first asks the registry whether another replica still holds the person.
func (h *Hub) remove(org, user string, e *entry) ([]outEvent, []Proximity) {
	users := h.orgs[org]
	delete(users, user)
	var evs []outEvent
	var hooks []Proximity
	for other := range e.near {
		if o := users[other]; o != nil {
			delete(o.near, user)
		}
		pr := Proximity{A: user, B: other, Near: false}
		evs = append(evs, outEvent{events.EvPresenceProximity, pr})
		hooks = append(hooks, pr)
	}
	if len(users) == 0 {
		delete(h.orgs, org)
	}
	return evs, hooks
}

// finishLeave runs outside the lock: it drops the registry entry and announces presence.left unless the person is
// still connected through another replica.
func (h *Hub) finishLeave(org, user string, evs []outEvent, hooks []Proximity) {
	if !h.stillElsewhere(org, user) {
		evs = append(evs, outEvent{events.EvPresenceLeft, map[string]string{"user_id": user}})
	}
	h.emit(org, evs, hooks)
}

// wake reverts an automatic away (called with the lock held).
func (h *Hub) wake(e *entry) outEvent {
	e.p.Status, e.p.AutoAway = StatusAvailable, false
	return outEvent{events.EvPresenceStatus, map[string]any{"user_id": e.p.UserID, "status": e.p.Status, "auto_away": false}}
}

// inbound is the frame a client sends over its socket.
type inbound struct {
	Type   string   `json:"type"`
	X      *float64 `json:"x"`
	Z      *float64 `json:"z"`
	Zone   string   `json:"zone"`
	Status Status   `json:"status"`
}

// Handle processes one inbound frame of an authenticated person: "presence.move", "presence.status" or
// "presence.ping" (activity without movement, keeps auto-away at bay). Identity always comes from the caller, never
// from the frame.
func (h *Hub) Handle(org, user string, raw []byte) error {
	var in inbound
	if err := json.Unmarshal(raw, &in); err != nil {
		return ErrBadFrame
	}
	switch in.Type {
	case "presence.move":
		if in.X == nil || in.Z == nil {
			return ErrBadFrame
		}
		return h.Move(org, user, *in.X, *in.Z, in.Zone)
	case "presence.status":
		return h.SetStatus(org, user, in.Status)
	case "presence.ping":
		return h.Touch(org, user)
	}
	return ErrBadFrame
}

// Touch records activity.
func (h *Hub) Touch(org, user string) error {
	now := h.cfg.Now()
	h.mu.Lock()
	e := h.orgs[org][user]
	if e == nil {
		h.mu.Unlock()
		return ErrNotOnline
	}
	e.lastActive = now
	var evs []outEvent
	if e.p.AutoAway {
		evs = append(evs, h.wake(e))
	}
	h.mu.Unlock()
	h.emit(org, evs, nil)
	return nil
}

// Move updates a position. The state is always current; the broadcast (and the proximity work, which is O(people))
// is throttled: a position that arrives too soon is flushed by the next Sweep.
func (h *Hub) Move(org, user string, x, z float64, zone string) error {
	cx, cz, ok := h.cfg.Bounds.Clamp(x, z)
	if !ok {
		return ErrBadFrame
	}
	now := h.cfg.Now()
	h.mu.Lock()
	e := h.orgs[org][user]
	if e == nil {
		h.mu.Unlock()
		return ErrNotOnline
	}
	e.lastActive = now
	var evs []outEvent
	if e.p.AutoAway {
		evs = append(evs, h.wake(e))
	}
	e.p.X, e.p.Z, e.p.Zone = cx, cz, CleanText(zone, 40)
	var hooks []Proximity
	if ShouldSend(e.sentAt, e.sentX, e.sentZ, now, cx, cz, h.cfg.MoveMinInterval, h.cfg.MoveMinDelta) {
		more, hk := h.flush(org, e, now)
		evs, hooks = append(evs, more...), hk
	} else if Dist(e.sentX, e.sentZ, cx, cz) >= h.cfg.MoveMinDelta {
		e.dirty = true
	}
	h.mu.Unlock()
	h.emit(org, evs, hooks)
	return nil
}

// flush broadcasts e's position and recomputes its proximities (lock held).
func (h *Hub) flush(org string, e *entry, now time.Time) ([]outEvent, []Proximity) {
	e.sentAt, e.sentX, e.sentZ, e.dirty = now, e.p.X, e.p.Z, false
	evs := []outEvent{{events.EvPresenceMoved, map[string]any{"user_id": e.p.UserID, "x": round2(e.p.X), "z": round2(e.p.Z), "zone": e.p.Zone}}}
	more, hooks := h.proximities(org, e, false)
	return append(evs, more...), hooks
}

// proximities recomputes the pairs of e with everyone else of its organization (lock held). force re-emits the
// pairs that are near even if nothing changed (status changes alter CanTalk).
func (h *Hub) proximities(org string, e *entry, force bool) ([]outEvent, []Proximity) {
	var evs []outEvent
	var hooks []Proximity
	for id, o := range h.orgs[org] {
		if o == e {
			continue
		}
		d := Dist(e.p.X, e.p.Z, o.p.X, o.p.Z)
		was := e.near[id]
		now := NextNear(was, d, h.cfg.EnterRadius, h.cfg.ExitRadius)
		if now == was && !(force && now) {
			continue
		}
		if now {
			e.near[id], o.near[e.p.UserID] = true, true
		} else {
			delete(e.near, id)
			delete(o.near, e.p.UserID)
		}
		pr := Proximity{A: e.p.UserID, B: id, Near: now, Distance: round2(d), CanTalk: now && Approachable(e.p.Status) && Approachable(o.p.Status)}
		evs = append(evs, outEvent{events.EvPresenceProximity, pr})
		hooks = append(hooks, pr)
	}
	return evs, hooks
}

// SetStatus changes the status by choice. Choosing away is manual (not reverted by activity).
func (h *Hub) SetStatus(org, user string, s Status) error {
	if !ValidStatus(s) {
		return ErrBadFrame
	}
	now := h.cfg.Now()
	h.mu.Lock()
	e := h.orgs[org][user]
	if e == nil {
		h.mu.Unlock()
		return ErrNotOnline
	}
	e.lastActive = now
	var evs []outEvent
	var hooks []Proximity
	if e.p.Status != s || e.p.AutoAway {
		e.p.Status, e.p.AutoAway = s, false
		evs = append(evs, outEvent{events.EvPresenceStatus, map[string]any{"user_id": user, "status": s, "auto_away": false}})
		more, hk := h.proximities(org, e, true)
		evs, hooks = append(evs, more...), hk
	}
	h.mu.Unlock()
	h.emit(org, evs, hooks)
	return nil
}

// Sweep flushes the positions that were held back by the throttle and marks idle people away. Run calls it on a ticker.
func (h *Hub) Sweep() {
	now := h.cfg.Now()
	type batch struct {
		org   string
		evs   []outEvent
		hooks []Proximity
	}
	var out []batch
	type gone struct {
		org, user string
		b         batch
	}
	var leaving []gone
	h.mu.Lock()
	for org, users := range h.orgs {
		b := batch{org: org}
		for id, e := range users {
			if e.conns <= 0 && !e.leaveAt.IsZero() && !now.Before(e.leaveAt) {
				evs, hk := h.remove(org, id, e)
				leaving = append(leaving, gone{org: org, user: id, b: batch{org: org, evs: evs, hooks: hk}})
				continue
			}
			if e.dirty && now.Sub(e.sentAt) >= h.cfg.MoveMinInterval {
				evs, hk := h.flush(org, e, now)
				b.evs, b.hooks = append(b.evs, evs...), append(b.hooks, hk...)
			}
			if AutoAway(e.p.Status, e.lastActive, now, h.cfg.AwayAfter) {
				e.p.Status, e.p.AutoAway = StatusAway, true
				b.evs = append(b.evs, outEvent{events.EvPresenceStatus, map[string]any{"user_id": e.p.UserID, "status": StatusAway, "auto_away": true}})
				evs, hk := h.proximities(org, e, true)
				b.evs, b.hooks = append(b.evs, evs...), append(b.hooks, hk...)
			}
		}
		if len(b.evs) > 0 {
			out = append(out, b)
		}
	}
	h.mu.Unlock()
	for _, b := range out {
		h.emit(b.org, b.evs, b.hooks)
	}
	for _, g := range leaving {
		h.finishLeave(g.org, g.user, g.b.evs, g.b.hooks)
	}
	h.syncRegistry(now)
}

// Run sweeps until ctx ends.
func (h *Hub) Run(ctx context.Context) {
	t := time.NewTicker(h.cfg.MoveMinInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.Sweep()
		}
	}
}

// Snapshot lists the people online in org (never another organization's), ordered by name. With a shared registry
// it also lists the people connected through other replicas.
func (h *Hub) Snapshot(org string) []Person {
	h.mu.Lock()
	out := make([]Person, 0, len(h.orgs[org]))
	have := map[string]bool{}
	for _, e := range h.orgs[org] {
		out = append(out, e.p)
		have[e.p.UserID] = true
	}
	h.mu.Unlock()
	for _, r := range h.remotes(org) {
		if !have[r.Person.UserID] {
			have[r.Person.UserID] = true
			out = append(out, r.Person)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].UserID < out[j].UserID
	})
	return out
}

// Online reports whether user has a socket in org (on this replica or, with a registry, on another one).
func (h *Hub) Online(org, user string) bool {
	h.mu.Lock()
	local := h.orgs[org][user] != nil
	h.mu.Unlock()
	if local {
		return true
	}
	for _, r := range h.remotes(org) {
		if r.Person.UserID == user {
			return true
		}
	}
	return false
}

// ---- shared registry (best effort: a registry failure never breaks presence, it degrades to this replica) ----

func (h *Hub) regCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 2*time.Second)
}

func (h *Hub) regPut(org string, p Person) {
	if h.cfg.Registry == nil {
		return
	}
	ctx, cancel := h.regCtx()
	defer cancel()
	_ = h.cfg.Registry.Put(ctx, org, h.cfg.Replica, p)
}

// remotes lists the registry entries held by OTHER replicas.
func (h *Hub) remotes(org string) []Remote {
	if h.cfg.Registry == nil {
		return nil
	}
	ctx, cancel := h.regCtx()
	defer cancel()
	list, err := h.cfg.Registry.List(ctx, org)
	if err != nil {
		return nil
	}
	out := list[:0]
	for _, r := range list {
		if r.Replica != h.cfg.Replica {
			out = append(out, r)
		}
	}
	return out
}

// stillElsewhere: another replica holds user (also drops this replica's own entry when the person is gone here).
func (h *Hub) stillElsewhere(org, user string) bool {
	if h.cfg.Registry == nil {
		return false
	}
	h.mu.Lock()
	local := h.orgs[org][user] != nil
	h.mu.Unlock()
	if !local {
		ctx, cancel := h.regCtx()
		_ = h.cfg.Registry.Delete(ctx, org, h.cfg.Replica, user)
		cancel()
	}
	for _, r := range h.remotes(org) {
		if r.Person.UserID == user {
			return true
		}
	}
	return false
}

// syncRegistry refreshes the TTL of every live local entry, at most every SyncEvery.
func (h *Hub) syncRegistry(now time.Time) {
	if h.cfg.Registry == nil {
		return
	}
	h.mu.Lock()
	if now.Sub(h.lastSync) < h.cfg.SyncEvery {
		h.mu.Unlock()
		return
	}
	h.lastSync = now
	type row struct {
		org string
		p   Person
	}
	var rows []row
	for org, users := range h.orgs {
		for _, e := range users {
			rows = append(rows, row{org, e.p})
		}
	}
	h.mu.Unlock()
	for _, r := range rows {
		h.regPut(r.org, r.p)
	}
}

func round2(v float64) float64 { return float64(int(v*100+sign(v)*0.5)) / 100 }

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}
