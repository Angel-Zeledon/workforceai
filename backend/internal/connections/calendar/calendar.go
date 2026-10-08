// Package calendar is the Google Calendar adapter: list and read events of the
// calendars the connection allows, and create events (a write: the gateway
// always asks a human first and holds it before it goes out). Invites are
// created with sendUpdates=none, so Google itself emails nobody; attendees are
// still shown on the approval card as recipients.
package calendar

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/connections/googleauth"
)

// Config holds the endpoints (overridable for tests).
type Config struct {
	Auth    googleauth.Endpoints
	APIBase string // https://www.googleapis.com/calendar/v3
}

// Provider implements connections.Provider for Google Calendar.
type Provider struct {
	*googleauth.OAuth
	base string

	mu    sync.Mutex
	fakes map[string]*Fake
}

// New builds the provider with the embedded manifest.
func New(cfg Config) (*Provider, error) {
	ms, err := connections.LoadManifests()
	if err != nil {
		return nil, err
	}
	if cfg.APIBase == "" {
		cfg.APIBase = "https://www.googleapis.com/calendar/v3"
	}
	return &Provider{OAuth: &googleauth.OAuth{E: cfg.Auth.Defaults(), Manifest: ms["google_calendar"]}, base: cfg.APIBase, fakes: map[string]*Fake{}}, nil
}

func (p *Provider) Manifest() connections.Manifest { return p.OAuth.Manifest }

// FakeFor returns the simulated calendar of a connection.
func (p *Provider) FakeFor(connID string) *Fake {
	p.mu.Lock()
	defer p.mu.Unlock()
	f, ok := p.fakes[connID]
	if !ok {
		f = NewFake()
		p.fakes[connID] = f
	}
	return f
}

func (p *Provider) Open(h connections.Handle) connections.Executor {
	if h.Mode == connections.ModeSimulated {
		return &scoped{h: h, next: p.FakeFor(h.ConnectionID)}
	}
	return &scoped{h: h, next: &live{base: p.base, h: h}}
}

// Event is the normalized event (fake and live share it).
type Event struct {
	ID, CalendarID, Summary, Description, Location, Organizer string
	Start, End                                                time.Time
	Attendees                                                 []string
}

func (e Event) item() connections.Item {
	return connections.Item{ID: e.ID,
		Meta: map[string]any{"id": e.ID, "calendar_id": e.CalendarID, "start": e.Start.Format(time.RFC3339), "end": e.End.Format(time.RFC3339),
			"attendee_count": len(e.Attendees)},
		Texts: []connections.Text{{Name: "summary", Value: e.Summary}, {Name: "description", Value: e.Description},
			{Name: "location", Value: e.Location}, {Name: "organizer", Value: e.Organizer}, {Name: "attendees", Value: strings.Join(e.Attendees, ", ")}}}
}

// backend is what the scoped executor delegates to once arguments are checked.
type backend interface {
	list(ctx context.Context, calendarID, q string, from, to time.Time, max int) ([]Event, int, int64, error)
	get(ctx context.Context, calendarID, id string) (Event, int, error)
	create(ctx context.Context, calendarID string, e Event) (string, int, error)
}

// scoped validates arguments and enforces the connection's calendar allowlist
// before any call: an agent can never reach a calendar outside it.
type scoped struct {
	h    connections.Handle
	next backend
}

func (s *scoped) calendar(args map[string]any) (string, error) {
	id := connections.ArgStr(args, "calendar_id", "calendar")
	if id == "" {
		id = "primary"
	}
	allowed := s.h.ResourceScope.Calendars
	if len(allowed) == 0 {
		allowed = []string{"primary"} // least privilege: only the account's own calendar
	}
	if !connections.InList(allowed, id) {
		return "", connections.OutOfScope()
	}
	return id, nil
}

func (s *scoped) Execute(ctx context.Context, c connections.ExecCall) (connections.ExecResult, error) {
	cal, err := s.calendar(c.Args)
	if err != nil {
		return connections.ExecResult{}, err
	}
	switch c.Action {
	case "list_events":
		from, to, err := window(c.Args)
		if err != nil {
			return connections.ExecResult{}, err
		}
		q := connections.ArgStr(c.Args, "q", "query")
		evs, st, n, err := s.next.list(ctx, cal, q, from, to, connections.Limit(c.Args, 10, c.MaxItems))
		if err != nil {
			return connections.ExecResult{Status: st}, err
		}
		res := connections.ExecResult{Status: st, BytesIn: n, Summary: fmt.Sprintf("%d events", len(evs)),
			ResourceRef: fmt.Sprintf("calendar=%s days=%d q_len=%d", cal, int(to.Sub(from).Hours()/24), len(q))}
		for _, e := range evs {
			res.Items = append(res.Items, e.item())
		}
		return res, nil
	case "read_event":
		id := connections.ArgStr(c.Args, "event_id", "id")
		if id == "" {
			return connections.ExecResult{}, connections.InvalidArgs()
		}
		e, st, err := s.next.get(ctx, cal, id)
		if err != nil {
			return connections.ExecResult{Status: st}, err
		}
		return connections.ExecResult{Status: st, Summary: "1 event", ResourceRef: "calendar=" + cal, Items: []connections.Item{e.item()}}, nil
	case "create_event":
		e, err := eventArgs(c.Args)
		if err != nil {
			return connections.ExecResult{}, err
		}
		e.CalendarID = cal
		id, st, err := s.next.create(ctx, cal, e)
		if err != nil {
			return connections.ExecResult{Status: st}, err
		}
		return connections.ExecResult{Status: st, Summary: "event created", ResourceRef: fmt.Sprintf("calendar=%s attendees=%d", cal, len(e.Attendees)),
			Data: map[string]any{"event_id": id}}, nil
	}
	return connections.ExecResult{}, connections.InvalidArgs()
}

const maxWindow = 92 * 24 * time.Hour

// window parses time_min/time_max (RFC 3339); default: the next 7 days.
func window(a map[string]any) (time.Time, time.Time, error) {
	from, to := time.Now().UTC(), time.Now().UTC().Add(7*24*time.Hour)
	if v := connections.ArgStr(a, "time_min", "from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return from, to, connections.InvalidArgs()
		}
		from = t
		to = t.Add(7 * 24 * time.Hour)
	}
	if v := connections.ArgStr(a, "time_max", "to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return from, to, connections.InvalidArgs()
		}
		to = t
	}
	if !to.After(from) || to.Sub(from) > maxWindow {
		return from, to, connections.InvalidArgs()
	}
	return from, to, nil
}

const maxAttendees = 20

func eventArgs(a map[string]any) (Event, error) {
	e := Event{Summary: connections.Truncate(connections.ArgStr(a, "title", "summary"), 200),
		Description: connections.Truncate(connections.ArgStr(a, "description"), 8000),
		Location:    connections.Truncate(connections.ArgStr(a, "location"), 300),
		Attendees:   connections.ArgList(a, "attendees")}
	var err1, err2 error
	e.Start, err1 = time.Parse(time.RFC3339, connections.ArgStr(a, "start"))
	e.End, err2 = time.Parse(time.RFC3339, connections.ArgStr(a, "end"))
	if e.Summary == "" || err1 != nil || err2 != nil || !e.End.After(e.Start) || len(e.Attendees) > maxAttendees {
		return e, connections.InvalidArgs()
	}
	for _, at := range e.Attendees {
		if !strings.Contains(at, "@") {
			return e, connections.InvalidArgs()
		}
	}
	return e, nil
}

// ---- live client ----

type live struct {
	base string
	h    connections.Handle
}

type gEvent struct {
	ID          string `json:"id"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Location    string `json:"location"`
	Organizer   struct {
		Email string `json:"email"`
	} `json:"organizer"`
	Start     gTime `json:"start"`
	End       gTime `json:"end"`
	Attendees []struct {
		Email string `json:"email"`
	} `json:"attendees"`
}

type gTime struct {
	DateTime string `json:"dateTime,omitempty"`
	Date     string `json:"date,omitempty"`
}

func (t gTime) time() time.Time {
	if v, err := time.Parse(time.RFC3339, t.DateTime); err == nil {
		return v
	}
	v, _ := time.Parse("2006-01-02", t.Date)
	return v
}

func (g gEvent) event(cal string) Event {
	e := Event{ID: g.ID, CalendarID: cal, Summary: g.Summary, Description: g.Description, Location: g.Location,
		Organizer: g.Organizer.Email, Start: g.Start.time(), End: g.End.time()}
	for _, a := range g.Attendees {
		e.Attendees = append(e.Attendees, a.Email)
	}
	return e
}

func (l *live) events(cal string) string {
	return l.base + "/calendars/" + url.PathEscape(cal) + "/events"
}

func (l *live) list(ctx context.Context, cal, q string, from, to time.Time, max int) ([]Event, int, int64, error) {
	v := url.Values{"timeMin": {from.Format(time.RFC3339)}, "timeMax": {to.Format(time.RFC3339)},
		"maxResults": {fmt.Sprint(max)}, "singleEvents": {"true"}, "orderBy": {"startTime"}}
	if q != "" {
		v.Set("q", q)
	}
	var out struct {
		Items []gEvent `json:"items"`
	}
	st, n, err := connections.DoJSON(ctx, l.h, http.MethodGet, l.events(cal), v, nil, &out)
	if err != nil {
		return nil, st, n, err
	}
	evs := make([]Event, 0, len(out.Items))
	for i, g := range out.Items {
		if i >= max {
			break
		}
		evs = append(evs, g.event(cal))
	}
	return evs, st, n, nil
}

func (l *live) get(ctx context.Context, cal, id string) (Event, int, error) {
	var g gEvent
	st, _, err := connections.DoJSON(ctx, l.h, http.MethodGet, l.events(cal)+"/"+url.PathEscape(id), nil, nil, &g)
	return g.event(cal), st, err
}

func (l *live) create(ctx context.Context, cal string, e Event) (string, int, error) {
	body := map[string]any{"summary": e.Summary, "description": e.Description, "location": e.Location,
		"start": gTime{DateTime: e.Start.Format(time.RFC3339)}, "end": gTime{DateTime: e.End.Format(time.RFC3339)}}
	if len(e.Attendees) > 0 {
		var at []map[string]string
		for _, a := range e.Attendees {
			at = append(at, map[string]string{"email": a})
		}
		body["attendees"] = at
	}
	var out gEvent
	// sendUpdates=none: Google sends no invitation emails on our behalf.
	st, _, err := connections.DoJSON(ctx, l.h, http.MethodPost, l.events(cal), url.Values{"sendUpdates": {"none"}}, body, &out)
	return out.ID, st, err
}
