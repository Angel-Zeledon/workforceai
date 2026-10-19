package calendar

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 20, 15, 0, 0, 0, time.UTC)

func TestGoogleCreateUpdateDeleteAndConference(t *testing.T) {
	var got []string
	var createBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		switch r.Method {
		case http.MethodPost:
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &createBody)
			_, _ = w.Write([]byte(`{"id":"ev1","summary":"Sync","hangoutLink":"https://meet.google.com/abc-defg-hij","start":{"dateTime":"2026-10-20T15:00:00Z"},"end":{"dateTime":"2026-10-20T16:00:00Z"}}`))
		case http.MethodPatch:
			_, _ = w.Write([]byte(`{"id":"ev1","summary":"Sync 2","start":{"dateTime":"2026-10-20T15:00:00Z"},"end":{"dateTime":"2026-10-20T16:00:00Z"}}`))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	g := &Google{HTTP: srv.Client(), Base: srv.URL}
	ev, err := g.Create(context.Background(), Input{Title: "Sync", Start: t0, End: t0.Add(time.Hour), Attendees: []string{"a@x.com", "bad", "A@x.com"}, Conference: ConfMeet})
	if err != nil {
		t.Fatal(err)
	}
	if ev.ID != "ev1" || ev.JoinURL != "https://meet.google.com/abc-defg-hij" {
		t.Fatalf("event: %+v", ev)
	}
	if !strings.Contains(got[0], "conferenceDataVersion=1") || !strings.Contains(got[0], "sendUpdates=none") {
		t.Fatalf("query: %s", got[0])
	}
	if at, _ := createBody["attendees"].([]any); len(at) != 1 {
		t.Fatalf("attendees must be deduplicated and validated: %v", createBody["attendees"])
	}
	if _, ok := createBody["conferenceData"]; !ok {
		t.Fatal("meet link must be requested through conferenceData")
	}
	if _, err := g.Update(context.Background(), "ev1", Input{Title: "Sync 2", Start: t0, End: t0.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := g.Delete(context.Background(), "ev1"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Create(context.Background(), Input{Title: "", Start: t0, End: t0.Add(time.Hour)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty title: %v", err)
	}
}

func TestGoogleFreeBusyAndErrors(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"secret provider text"}`))
			return
		}
		_, _ = w.Write([]byte(`{"calendars":{"primary":{"busy":[{"start":"2026-10-20T15:00:00Z","end":"2026-10-20T16:00:00Z"}]},"bob@x.com":{"busy":[]}}}`))
	}))
	defer srv.Close()
	g := &Google{HTTP: srv.Client(), Base: srv.URL}
	fb, err := g.FreeBusy(context.Background(), t0, t0.Add(8*time.Hour), []string{"me", "bob@x.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fb["me"]) != 1 || !fb["me"][0].Start.Equal(t0) || len(fb["bob@x.com"]) != 0 {
		t.Fatalf("freebusy: %+v", fb)
	}
	status = http.StatusUnauthorized
	if _, err := g.FreeBusy(context.Background(), t0, t0.Add(time.Hour), []string{"me"}); !errors.Is(err, ErrReauth) {
		t.Fatalf("401 must ask for re-authorization: %v", err)
	}
	status = http.StatusInternalServerError
	_, err = g.Events(context.Background(), t0, t0.Add(time.Hour))
	if !errors.Is(err, ErrProvider) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("5xx must be a body-free provider error: %v", err)
	}
	if _, err := g.Events(context.Background(), t0, t0.Add(200*24*time.Hour)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("window too large: %v", err)
	}
}

func TestMicrosoftEventsCreateAndSchedule(t *testing.T) {
	var prefer string
	var createBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefer = r.Header.Get("Prefer")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/me/calendarView":
			_, _ = w.Write([]byte(`{"value":[{"id":"m1","subject":"Budget","start":{"dateTime":"2026-10-20T15:00:00.0000000","timeZone":"UTC"},"end":{"dateTime":"2026-10-20T16:00:00.0000000","timeZone":"UTC"},"onlineMeeting":{"joinUrl":"https://teams.microsoft.com/l/meetup-join/x"},"attendees":[{"emailAddress":{"address":"a@x.com"}}]},{"id":"m2","subject":"gone","isCancelled":true,"start":{"dateTime":"2026-10-20T17:00:00"},"end":{"dateTime":"2026-10-20T18:00:00"}}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/me/events":
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &createBody)
			_, _ = w.Write([]byte(`{"id":"m3","subject":"New","start":{"dateTime":"2026-10-21T10:00:00.0000000"},"end":{"dateTime":"2026-10-21T11:00:00.0000000"},"onlineMeeting":{"joinUrl":"https://teams.microsoft.com/l/meetup-join/y"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/me/calendar/getSchedule":
			_, _ = w.Write([]byte(`{"value":[{"scheduleId":"BOB@x.com","scheduleItems":[{"status":"busy","start":{"dateTime":"2026-10-20T15:30:00.0000000","timeZone":"UTC"},"end":{"dateTime":"2026-10-20T16:00:00.0000000","timeZone":"UTC"}},{"status":"free","start":{"dateTime":"2026-10-20T17:00:00.0000000"},"end":{"dateTime":"2026-10-20T18:00:00.0000000"}}]}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	m := &Microsoft{HTTP: srv.Client(), Base: srv.URL}
	evs, err := m.Events(context.Background(), t0, t0.Add(8*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if prefer != `outlook.timezone="UTC"` {
		t.Fatalf("Prefer header: %q", prefer)
	}
	if len(evs) != 1 || !evs[0].Start.Equal(t0) || evs[0].JoinURL == "" || evs[0].Attendees[0] != "a@x.com" {
		t.Fatalf("events (cancelled must be dropped): %+v", evs)
	}
	ev, err := m.Create(context.Background(), Input{Title: "New", Start: t0, End: t0.Add(time.Hour), Conference: ConfTeams, Attendees: []string{"a@x.com"}})
	if err != nil || ev.JoinURL == "" {
		t.Fatalf("create: %+v %v", ev, err)
	}
	if createBody["isOnlineMeeting"] != true || createBody["onlineMeetingProvider"] != "teamsForBusiness" {
		t.Fatalf("teams link must be requested: %v", createBody)
	}
	fb, err := m.FreeBusy(context.Background(), t0, t0.Add(8*time.Hour), []string{"bob@x.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fb["bob@x.com"]) != 1 || !fb["bob@x.com"][0].Start.Equal(t0.Add(30*time.Minute)) {
		t.Fatalf("schedule must keep busy items only, keyed by the asked address: %+v", fb)
	}
}

func TestFreeSlots(t *testing.T) {
	busy := map[string][]Busy{
		"me":  {{Start: t0, End: t0.Add(time.Hour)}},
		"bob": {{Start: t0.Add(30 * time.Minute), End: t0.Add(90 * time.Minute)}},
	}
	slots := FreeSlots(busy, t0, t0.Add(4*time.Hour), time.Hour, 30*time.Minute)
	if len(slots) == 0 || !slots[0].Start.Equal(t0.Add(90*time.Minute)) {
		t.Fatalf("first common free hour starts at +90m: %+v", slots)
	}
	if got := MergeBusy([]Busy{{t0.Add(time.Hour), t0.Add(2 * time.Hour)}, {t0, t0.Add(time.Hour)}}); len(got) != 1 || !got[0].End.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("touching blocks merge: %+v", got)
	}
}

func TestICS(t *testing.T) {
	out := BuildICS("Mi, calendario", []ICSEvent{{
		UID: "m1@aiworkforce", Title: "Revisión; Q4, plan\nlínea 2", Description: strings.Repeat("palabra ", 30), URL: "https://meet.jit.si/room-abc",
		Start: t0, End: t0.Add(time.Hour), Organizer: "boss@x.com", Updated: t0,
		Attendees: []ICSAttendee{{Name: `Ana "A" Pérez`, Email: "ana@x.com"}, {Name: "NoMail"}},
	}, {UID: "m2@aiworkforce", Title: "Cancelled", Start: t0, End: t0.Add(time.Hour), Cancelled: true, Sequence: 2}}, t0)
	for _, want := range []string{"BEGIN:VCALENDAR\r\n", "VERSION:2.0\r\n", "X-WR-CALNAME:Mi\\, calendario\r\n", "DTSTART:20261020T150000Z\r\n", "DTEND:20261020T160000Z\r\n",
		`SUMMARY:Revisión\; Q4\, plan\nlínea 2`, "ORGANIZER:mailto:boss@x.com\r\n", "URL:https://meet.jit.si/room-abc\r\n",
		"STATUS:CONFIRMED\r\n", "STATUS:CANCELLED\r\n", "SEQUENCE:2\r\n", "END:VCALENDAR\r\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "NoMail") {
		t.Error("attendees without e-mail are skipped")
	}
	if !strings.Contains(out, `CN="Ana 'A' Pérez"`) {
		t.Errorf("quotes in CN must be neutralized:\n%s", out)
	}
	if strings.Count(out, "BEGIN:VEVENT") != 2 || strings.Count(out, "END:VEVENT") != 2 {
		t.Error("two events expected")
	}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\r\n"), "\r\n") {
		if len(line) > 75 {
			t.Errorf("line longer than 75 octets (%d): %q", len(line), line)
		}
	}
	// Unfolding gives back the full description.
	un := strings.ReplaceAll(out, "\r\n ", "")
	if !strings.Contains(un, strings.TrimSpace(strings.Repeat("palabra ", 30))) {
		t.Error("folded description must unfold to the original")
	}
}

func TestICSInjectionInHeaders(t *testing.T) {
	out := BuildICS("", []ICSEvent{{UID: "x\r\nEND:VEVENT", Title: "t\r\nBEGIN:VEVENT", Start: t0, End: t0.Add(time.Hour), Organizer: "a@x.com\r\nX-EVIL:1"}}, t0)
	if strings.Count(out, "\r\nBEGIN:VEVENT") != 1 || strings.Count(out, "\r\nEND:VEVENT") != 1 || strings.Contains(out, "\r\nX-EVIL") {
		t.Fatalf("CRLF injection must be neutralized:\n%s", out)
	}
}

func TestFakeCalendar(t *testing.T) {
	f := NewFake(ProviderMicrosoft)
	ev, err := f.Create(context.Background(), Input{Title: "x", Start: t0, End: t0.Add(time.Hour), Conference: ConfTeams})
	if err != nil || !strings.Contains(ev.JoinURL, "teams.microsoft.com") {
		t.Fatalf("%+v %v", ev, err)
	}
	fb, _ := f.FreeBusy(context.Background(), t0, t0.Add(2*time.Hour), []string{"me"})
	if len(fb["me"]) != 1 {
		t.Fatalf("own events count as busy: %+v", fb)
	}
	if err := f.Delete(context.Background(), ev.ID); err != nil || len(f.Snapshot()) != 0 {
		t.Fatal("delete")
	}
}
