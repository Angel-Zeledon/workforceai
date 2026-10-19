package calendar

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Fake is an in-memory calendar: the client of simulated connections and of tests.
// Nothing leaves the process.
type Fake struct {
	Kind string // ProviderGoogle | ProviderMicrosoft (default google)

	mu     sync.Mutex
	events map[string]Event
	// BusyOf are extra busy blocks per address (people the connected account can see).
	BusyOf map[string][]Busy
	calls  int
	// FailWith makes every call fail with this error (tests of the error paths).
	FailWith error
}

// NewFake returns an empty calendar.
func NewFake(kind string) *Fake {
	return &Fake{Kind: kind, events: map[string]Event{}, BusyOf: map[string][]Busy{}}
}

func (f *Fake) Provider() string {
	if f.Kind == "" {
		return ProviderGoogle
	}
	return f.Kind
}

// Calls returns how many calls the fake served.
func (f *Fake) Calls() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

// Snapshot returns a copy of the stored events.
func (f *Fake) Snapshot() []Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Event, 0, len(f.events))
	for _, e := range f.events {
		out = append(out, e)
	}
	return out
}

func (f *Fake) enter() error {
	f.calls++
	return f.FailWith
}

func (f *Fake) Events(_ context.Context, from, to time.Time) ([]Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter(); err != nil {
		return nil, err
	}
	var out []Event
	for _, e := range f.events {
		if e.End.After(from) && e.Start.Before(to) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *Fake) fill(e *Event, in Input) {
	e.Title, e.Description, e.Location = in.Title, in.Description, in.Location
	e.Start, e.End, e.Attendees = in.Start.UTC(), in.End.UTC(), cleanAddrs(in.Attendees)
	switch {
	case in.ExistingJoinURL != "":
		e.JoinURL = in.ExistingJoinURL
	case in.Conference == ConfMeet && f.Provider() == ProviderGoogle && e.JoinURL == "":
		e.JoinURL = "https://meet.google.com/" + strings.ToLower(uuid.NewString()[:12])
	case in.Conference == ConfTeams && f.Provider() == ProviderMicrosoft && e.JoinURL == "":
		e.JoinURL = "https://teams.microsoft.com/l/meetup-join/" + uuid.NewString()
	}
}

func (f *Fake) Create(_ context.Context, in Input) (Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter(); err != nil {
		return Event{}, err
	}
	if in.Title == "" || !in.End.After(in.Start) {
		return Event{}, ErrInvalid
	}
	e := Event{ID: "fake_" + uuid.NewString()[:8]}
	f.fill(&e, in)
	f.events[e.ID] = e
	return e, nil
}

func (f *Fake) Update(_ context.Context, id string, in Input) (Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter(); err != nil {
		return Event{}, err
	}
	e, ok := f.events[id]
	if !ok {
		return Event{}, ErrNotFound
	}
	if in.Title == "" || !in.End.After(in.Start) {
		return Event{}, ErrInvalid
	}
	f.fill(&e, in)
	f.events[id] = e
	return e, nil
}

func (f *Fake) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter(); err != nil {
		return err
	}
	delete(f.events, id)
	return nil
}

func (f *Fake) FreeBusy(_ context.Context, from, to time.Time, who []string) (map[string][]Busy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter(); err != nil {
		return nil, err
	}
	if err := checkWindow(from, to); err != nil {
		return nil, err
	}
	res := map[string][]Busy{}
	for _, w := range who {
		res[w] = nil
		clip := func(b Busy) {
			if b.End.After(from) && b.Start.Before(to) {
				res[w] = append(res[w], b)
			}
		}
		for _, b := range f.BusyOf[w] {
			clip(b)
		}
		if w == "me" {
			for _, e := range f.events {
				clip(Busy{Start: e.Start, End: e.End})
			}
		}
	}
	return res, nil
}
