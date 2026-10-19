package calendar

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Google is the Google Calendar v3 client of one connection (the primary calendar).
type Google struct {
	HTTP *http.Client
	// Base is https://www.googleapis.com/calendar/v3 (overridable for tests).
	Base string
	// CalendarID defaults to "primary".
	CalendarID string
}

func (g *Google) Provider() string { return ProviderGoogle }

func (g *Google) base() string {
	if g.Base == "" {
		return "https://www.googleapis.com/calendar/v3"
	}
	return strings.TrimRight(g.Base, "/")
}

func (g *Google) events() string {
	cal := g.CalendarID
	if cal == "" {
		cal = "primary"
	}
	return g.base() + "/calendars/" + url.PathEscape(cal) + "/events"
}

type gTime struct {
	DateTime string `json:"dateTime,omitempty"`
	Date     string `json:"date,omitempty"`
}

func (t gTime) parse() (time.Time, bool) {
	if v, err := time.Parse(time.RFC3339, t.DateTime); err == nil {
		return v.UTC(), false
	}
	v, _ := time.Parse("2006-01-02", t.Date)
	return v.UTC(), true
}

type gEvent struct {
	ID          string `json:"id"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Location    string `json:"location"`
	HangoutLink string `json:"hangoutLink"`
	Organizer   struct {
		Email string `json:"email"`
	} `json:"organizer"`
	Start     gTime  `json:"start"`
	End       gTime  `json:"end"`
	Status    string `json:"status"`
	Attendees []struct {
		Email string `json:"email"`
	} `json:"attendees"`
}

func (e gEvent) event() Event {
	ev := Event{ID: e.ID, Title: e.Summary, Description: e.Description, Location: e.Location, JoinURL: e.HangoutLink, Organizer: e.Organizer.Email}
	ev.Start, ev.AllDay = e.Start.parse()
	ev.End, _ = e.End.parse()
	for _, a := range e.Attendees {
		ev.Attendees = append(ev.Attendees, a.Email)
	}
	return ev
}

func (g *Google) Events(ctx context.Context, from, to time.Time) ([]Event, error) {
	if err := checkWindow(from, to); err != nil {
		return nil, err
	}
	q := url.Values{"timeMin": {from.UTC().Format(time.RFC3339)}, "timeMax": {to.UTC().Format(time.RFC3339)},
		"maxResults": {"250"}, "singleEvents": {"true"}, "orderBy": {"startTime"}}
	var out struct {
		Items []gEvent `json:"items"`
	}
	if _, err := doJSON(ctx, g.HTTP, http.MethodGet, g.events(), q, nil, nil, &out); err != nil {
		return nil, err
	}
	evs := make([]Event, 0, len(out.Items))
	for _, it := range out.Items {
		if it.Status == "cancelled" {
			continue
		}
		evs = append(evs, it.event())
	}
	return evs, nil
}

func (g *Google) body(in Input, withConference bool) map[string]any {
	desc := in.Description
	if in.ExistingJoinURL != "" && !strings.Contains(desc, in.ExistingJoinURL) {
		desc = strings.TrimSpace(desc + "\n\n" + in.ExistingJoinURL)
	}
	b := map[string]any{"summary": in.Title, "description": desc, "location": in.Location,
		"start": gTime{DateTime: in.Start.UTC().Format(time.RFC3339)}, "end": gTime{DateTime: in.End.UTC().Format(time.RFC3339)}}
	if at := cleanAddrs(in.Attendees); len(at) > 0 {
		var list []map[string]string
		for _, a := range at {
			list = append(list, map[string]string{"email": a})
		}
		b["attendees"] = list
	}
	if withConference && in.Conference == ConfMeet {
		b["conferenceData"] = map[string]any{"createRequest": map[string]any{
			"requestId": uuid.NewString(), "conferenceSolutionKey": map[string]string{"type": "hangoutsMeet"}}}
	}
	return b
}

func (g *Google) Create(ctx context.Context, in Input) (Event, error) {
	if in.Title == "" || !in.End.After(in.Start) {
		return Event{}, ErrInvalid
	}
	q := url.Values{"sendUpdates": {"none"}} // the platform emails nobody on its own
	if in.Conference == ConfMeet {
		q.Set("conferenceDataVersion", "1")
	}
	var out gEvent
	if _, err := doJSON(ctx, g.HTTP, http.MethodPost, g.events(), q, nil, g.body(in, true), &out); err != nil {
		return Event{}, err
	}
	return out.event(), nil
}

func (g *Google) Update(ctx context.Context, id string, in Input) (Event, error) {
	if id == "" || in.Title == "" || !in.End.After(in.Start) {
		return Event{}, ErrInvalid
	}
	var out gEvent
	// PATCH keeps the conference data of the event untouched.
	if _, err := doJSON(ctx, g.HTTP, http.MethodPatch, g.events()+"/"+url.PathEscape(id), url.Values{"sendUpdates": {"none"}}, nil, g.body(in, false), &out); err != nil {
		return Event{}, err
	}
	return out.event(), nil
}

func (g *Google) Delete(ctx context.Context, id string) error {
	if id == "" {
		return ErrInvalid
	}
	_, err := doJSON(ctx, g.HTTP, http.MethodDelete, g.events()+"/"+url.PathEscape(id), url.Values{"sendUpdates": {"none"}}, nil, nil, nil)
	if err == ErrNotFound { // already gone is the goal
		return nil
	}
	return err
}

func (g *Google) FreeBusy(ctx context.Context, from, to time.Time, who []string) (map[string][]Busy, error) {
	if err := checkWindow(from, to); err != nil {
		return nil, err
	}
	if len(who) == 0 || len(who) > 50 {
		return nil, ErrInvalid
	}
	items := make([]map[string]string, 0, len(who))
	for _, w := range who {
		id := w
		if w == "me" {
			id = "primary"
		}
		items = append(items, map[string]string{"id": id})
	}
	var out struct {
		Calendars map[string]struct {
			Busy []struct {
				Start string `json:"start"`
				End   string `json:"end"`
			} `json:"busy"`
		} `json:"calendars"`
	}
	body := map[string]any{"timeMin": from.UTC().Format(time.RFC3339), "timeMax": to.UTC().Format(time.RFC3339), "items": items}
	if _, err := doJSON(ctx, g.HTTP, http.MethodPost, g.base()+"/freeBusy", nil, nil, body, &out); err != nil {
		return nil, err
	}
	res := map[string][]Busy{}
	for _, w := range who {
		id := w
		if w == "me" {
			id = "primary"
		}
		res[w] = nil
		for _, b := range out.Calendars[id].Busy {
			s, e1 := time.Parse(time.RFC3339, b.Start)
			e, e2 := time.Parse(time.RFC3339, b.End)
			if e1 != nil || e2 != nil {
				return nil, fmt.Errorf("%w: bad busy block", ErrProvider)
			}
			res[w] = append(res[w], Busy{Start: s.UTC(), End: e.UTC()})
		}
	}
	return res, nil
}
