// Package calendar talks to the calendars of the people who run the organization:
// Google Calendar and Microsoft 365 (Graph) through a connection handle (the
// token lives in the vault and is injected by the handle's transport; nothing
// here ever sees it), plus the ICS format (ics.go) and an in-memory fake used
// by simulated connections and tests.
//
// This is the "meetings" side of calendars: a person schedules a meeting and
// chooses to write it to their calendar, or asks when people are free. The
// agent-facing tools (read events / create event behind human approval) stay in
// internal/connections/calendar.
package calendar

import (
	"context"
	"errors"
	"time"
)

// Provider ids (also the connection provider ids of internal/connections/meetingcal).
const (
	ProviderGoogle    = "google_calendar_sync"
	ProviderMicrosoft = "microsoft_calendar"
)

// Conference kinds a calendar can attach to an event on its own.
const (
	ConfNone  = ""
	ConfMeet  = "meet"  // Google Meet (Google Calendar only)
	ConfTeams = "teams" // Microsoft Teams (Microsoft 365 only)
)

// Event is a calendar event, normalized across providers.
type Event struct {
	ID          string
	Title       string
	Description string
	Location    string
	Start, End  time.Time
	Attendees   []string // e-mail addresses
	Organizer   string
	JoinURL     string // Meet / Teams link when the event has one
	AllDay      bool
}

// Input is what is written to a calendar.
type Input struct {
	Title       string
	Description string
	Location    string
	Start, End  time.Time
	Attendees   []string
	// Conference asks the provider to create its own video link (ConfMeet / ConfTeams).
	Conference string
	// ExistingJoinURL is put in the location/description when the link comes from elsewhere (Jitsi, Zoom).
	ExistingJoinURL string
}

// Busy is one busy block.
type Busy struct{ Start, End time.Time }

// Client is one connected calendar.
type Client interface {
	Provider() string
	// Events lists the events overlapping [from, to).
	Events(ctx context.Context, from, to time.Time) ([]Event, error)
	Create(ctx context.Context, in Input) (Event, error)
	Update(ctx context.Context, id string, in Input) (Event, error)
	Delete(ctx context.Context, id string) error
	// FreeBusy returns the busy blocks of each address ("me" means the connected account).
	FreeBusy(ctx context.Context, from, to time.Time, who []string) (map[string][]Busy, error)
}

// Errors. Codes are stable and body free (the UI translates them).
var (
	ErrNotConfigured = errors.New("calendar: no calendar connection")
	ErrProvider      = errors.New("calendar: provider error")
	ErrReauth        = errors.New("calendar: connection needs re-authorization")
	ErrNotFound      = errors.New("calendar: event not found")
	ErrInvalid       = errors.New("calendar: invalid input")
	ErrReadOnly      = errors.New("calendar: this connection is read only")
	ErrBlocked       = errors.New("calendar: blocked by the kill switch or read-only mode")
)

const maxWindow = 92 * 24 * time.Hour

func checkWindow(from, to time.Time) error {
	if !to.After(from) || to.Sub(from) > maxWindow {
		return ErrInvalid
	}
	return nil
}

// MergeBusy sorts blocks and merges the overlapping or touching ones.
func MergeBusy(in []Busy) []Busy {
	if len(in) == 0 {
		return nil
	}
	out := append([]Busy(nil), in...)
	// insertion sort: the lists are short
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Start.Before(out[j-1].Start); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	res := []Busy{out[0]}
	for _, b := range out[1:] {
		last := &res[len(res)-1]
		if !b.Start.After(last.End) {
			if b.End.After(last.End) {
				last.End = b.End
			}
			continue
		}
		res = append(res, b)
	}
	return res
}

// Slot is a free window.
type Slot struct{ Start, End time.Time }

// FreeSlots returns the windows of at least `length` inside [from, to) where nobody in
// `busy` is busy. Slots are cut on `step` boundaries from `from` (e.g. 30 minutes).
func FreeSlots(busy map[string][]Busy, from, to time.Time, length, step time.Duration) []Slot {
	if length <= 0 || step <= 0 || !to.After(from) {
		return nil
	}
	var all []Busy
	for _, bs := range busy {
		all = append(all, bs...)
	}
	merged := MergeBusy(all)
	var out []Slot
	for start := from; !start.Add(length).After(to); start = start.Add(step) {
		end := start.Add(length)
		free := true
		for _, b := range merged {
			if b.Start.Before(end) && b.End.After(start) {
				free = false
				break
			}
		}
		if free {
			out = append(out, Slot{Start: start, End: end})
		}
	}
	return out
}
