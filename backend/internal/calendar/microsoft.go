package calendar

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Microsoft is the Microsoft Graph client of one connection (the signed-in
// user's default calendar).
type Microsoft struct {
	HTTP *http.Client
	// Base is https://graph.microsoft.com/v1.0 (overridable for tests).
	Base string
}

func (m *Microsoft) Provider() string { return ProviderMicrosoft }

func (m *Microsoft) base() string {
	if m.Base == "" {
		return "https://graph.microsoft.com/v1.0"
	}
	return strings.TrimRight(m.Base, "/")
}

// Graph returns date-times without a zone designator in the zone we ask for
// (Prefer: outlook.timezone="UTC"), e.g. "2026-10-20T15:00:00.0000000".
const graphLayout = "2006-01-02T15:04:05.9999999"

var utcHeader = map[string]string{"Prefer": `outlook.timezone="UTC"`}

type mTime struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

func (t mTime) parse() time.Time {
	for _, l := range []string{graphLayout, time.RFC3339, "2006-01-02T15:04:05"} {
		if v, err := time.ParseInLocation(l, t.DateTime, time.UTC); err == nil {
			return v.UTC()
		}
	}
	return time.Time{}
}

func mt(t time.Time) mTime {
	return mTime{DateTime: t.UTC().Format("2006-01-02T15:04:05"), TimeZone: "UTC"}
}

type mEvent struct {
	ID       string `json:"id"`
	Subject  string `json:"subject"`
	BodyPrev string `json:"bodyPreview"`
	Location struct {
		DisplayName string `json:"displayName"`
	} `json:"location"`
	Start         mTime `json:"start"`
	End           mTime `json:"end"`
	IsAllDay      bool  `json:"isAllDay"`
	IsCancelled   bool  `json:"isCancelled"`
	OnlineMeeting *struct {
		JoinURL string `json:"joinUrl"`
	} `json:"onlineMeeting"`
	Organizer struct {
		EmailAddress struct {
			Address string `json:"address"`
		} `json:"emailAddress"`
	} `json:"organizer"`
	Attendees []struct {
		EmailAddress struct {
			Address string `json:"address"`
		} `json:"emailAddress"`
	} `json:"attendees"`
}

func (e mEvent) event() Event {
	ev := Event{ID: e.ID, Title: e.Subject, Description: e.BodyPrev, Location: e.Location.DisplayName,
		Start: e.Start.parse(), End: e.End.parse(), AllDay: e.IsAllDay, Organizer: e.Organizer.EmailAddress.Address}
	if e.OnlineMeeting != nil {
		ev.JoinURL = e.OnlineMeeting.JoinURL
	}
	for _, a := range e.Attendees {
		ev.Attendees = append(ev.Attendees, a.EmailAddress.Address)
	}
	return ev
}

func (m *Microsoft) Events(ctx context.Context, from, to time.Time) ([]Event, error) {
	if err := checkWindow(from, to); err != nil {
		return nil, err
	}
	q := url.Values{"startDateTime": {from.UTC().Format(time.RFC3339)}, "endDateTime": {to.UTC().Format(time.RFC3339)},
		"$top": {"250"}, "$orderby": {"start/dateTime"}}
	var out struct {
		Value []mEvent `json:"value"`
	}
	if _, err := doJSON(ctx, m.HTTP, http.MethodGet, m.base()+"/me/calendarView", q, utcHeader, nil, &out); err != nil {
		return nil, err
	}
	evs := make([]Event, 0, len(out.Value))
	for _, it := range out.Value {
		if it.IsCancelled {
			continue
		}
		evs = append(evs, it.event())
	}
	return evs, nil
}

func (m *Microsoft) body(in Input, conference bool) map[string]any {
	content := in.Description
	if in.ExistingJoinURL != "" && !strings.Contains(content, in.ExistingJoinURL) {
		content = strings.TrimSpace(content + "\n\n" + in.ExistingJoinURL)
	}
	b := map[string]any{"subject": in.Title, "body": map[string]string{"contentType": "text", "content": content},
		"start": mt(in.Start), "end": mt(in.End)}
	if in.Location != "" {
		b["location"] = map[string]string{"displayName": in.Location}
	}
	// Microsoft Graph e-mails an invitation to every attendee added to an event and has no switch to
	// prevent it (Google has sendUpdates=none). Nothing is sent to other people from this screen, so the
	// guests are written in the body of the event instead of in its attendee list.
	if guests := cleanAddrs(in.Attendees); len(guests) > 0 {
		content = strings.TrimSpace(content + "\n\nGuests: " + strings.Join(guests, ", "))
		b["body"] = map[string]string{"contentType": "text", "content": content}
	}
	if conference && in.Conference == ConfTeams {
		b["isOnlineMeeting"] = true
		b["onlineMeetingProvider"] = "teamsForBusiness"
	}
	return b
}

func (m *Microsoft) Create(ctx context.Context, in Input) (Event, error) {
	if in.Title == "" || !in.End.After(in.Start) {
		return Event{}, ErrInvalid
	}
	var out mEvent
	if _, err := doJSON(ctx, m.HTTP, http.MethodPost, m.base()+"/me/events", nil, utcHeader, m.body(in, true), &out); err != nil {
		return Event{}, err
	}
	return out.event(), nil
}

func (m *Microsoft) Update(ctx context.Context, id string, in Input) (Event, error) {
	if id == "" || in.Title == "" || !in.End.After(in.Start) {
		return Event{}, ErrInvalid
	}
	var out mEvent
	if _, err := doJSON(ctx, m.HTTP, http.MethodPatch, m.base()+"/me/events/"+url.PathEscape(id), nil, utcHeader, m.body(in, false), &out); err != nil {
		return Event{}, err
	}
	return out.event(), nil
}

func (m *Microsoft) Delete(ctx context.Context, id string) error {
	if id == "" {
		return ErrInvalid
	}
	_, err := doJSON(ctx, m.HTTP, http.MethodDelete, m.base()+"/me/events/"+url.PathEscape(id), nil, nil, nil, nil)
	if err == ErrNotFound {
		return nil
	}
	return err
}

// FreeBusy uses getSchedule. "me" is resolved by Graph through the schedule of the
// signed-in user, which needs the address: callers pass real addresses for others and
// "me" is answered from calendarView.
func (m *Microsoft) FreeBusy(ctx context.Context, from, to time.Time, who []string) (map[string][]Busy, error) {
	if err := checkWindow(from, to); err != nil {
		return nil, err
	}
	if len(who) == 0 || len(who) > 20 {
		return nil, ErrInvalid
	}
	res := map[string][]Busy{}
	var others []string
	for _, w := range who {
		if w == "me" {
			evs, err := m.Events(ctx, from, to)
			if err != nil {
				return nil, err
			}
			res["me"] = nil
			for _, e := range evs {
				res["me"] = append(res["me"], Busy{Start: e.Start, End: e.End})
			}
			continue
		}
		others = append(others, w)
	}
	if len(others) == 0 {
		return res, nil
	}
	body := map[string]any{"schedules": others, "startTime": mt(from), "endTime": mt(to), "availabilityViewInterval": 30}
	var out struct {
		Value []struct {
			ScheduleID    string `json:"scheduleId"`
			ScheduleItems []struct {
				Status string `json:"status"`
				Start  mTime  `json:"start"`
				End    mTime  `json:"end"`
			} `json:"scheduleItems"`
		} `json:"value"`
	}
	if _, err := doJSON(ctx, m.HTTP, http.MethodPost, m.base()+"/me/calendar/getSchedule", nil, utcHeader, body, &out); err != nil {
		return nil, err
	}
	for _, o := range others {
		res[o] = nil
	}
	for _, v := range out.Value {
		key := v.ScheduleID
		for _, o := range others { // Graph echoes the address; match ignoring case
			if strings.EqualFold(o, v.ScheduleID) {
				key = o
			}
		}
		for _, it := range v.ScheduleItems {
			if it.Status == "free" {
				continue
			}
			res[key] = append(res[key], Busy{Start: it.Start.parse(), End: it.End.parse()})
		}
	}
	return res, nil
}
