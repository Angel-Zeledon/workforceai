package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/meetings"
)

// Integration test of migrations 740/741 (meetings, minutes, settings, calendar feeds). Needs
// TEST_DATABASE_URL (see store_rls_test.go); skipped otherwise.
func TestMeetingsPostgres(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	st := &MeetingsStore{S: env.Store}
	svc := meetings.New(meetings.Config{Store: st})
	start := time.Now().UTC().Truncate(time.Second).Add(time.Hour)

	m, err := svc.Create(ctx, "org-a", "ana", meetings.CreateInput{Title: "Pricing", Room: "boardroom", Start: start, End: start.Add(time.Hour),
		Attendees: []meetings.Attendee{{Kind: meetings.KindPerson, ID: "u1", Name: "Ana", Email: "ana@x.com"}},
		Agenda:    []meetings.AgendaItem{{Title: "Numbers", Minutes: 10, Owner: "u1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, "org-a", "ana", meetings.CreateInput{Title: "Clash", Room: "boardroom", Start: start.Add(30 * time.Minute), End: start.Add(2 * time.Hour)}); !errors.Is(err, meetings.ErrRoomBusy) {
		t.Fatalf("room conflict through SQL: %v", err)
	}
	if _, err := svc.Get(ctx, "org-b", m.ID); !errors.Is(err, meetings.ErrNotFound) {
		t.Fatalf("RLS: another organization must not read the meeting: %v", err)
	}
	if l, _ := svc.List(ctx, "org-a", meetings.Filter{Person: "ana@X.com"}); len(l) != 1 {
		t.Fatalf("person filter (e-mail, case-insensitive): %v", l)
	}
	if l, _ := svc.List(ctx, "org-a", meetings.Filter{Person: "stranger"}); len(l) != 0 {
		t.Fatalf("person filter: %v", l)
	}
	if _, err := svc.Start(ctx, "org-a", "ana", m.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateMeeting(ctx, meetings.Meeting{OrgID: "org-a", ID: m.ID, Title: "x", Start: start, End: start.Add(time.Hour), Status: meetings.StatusCancelled, Attendees: nil}, meetings.StatusScheduled); !errors.Is(err, meetings.ErrState) {
		t.Fatalf("compare-and-set on status: %v", err)
	}
	if _, err := svc.End(ctx, "org-a", "ana", m.ID); err != nil {
		t.Fatal(err)
	}
	min, err := svc.GenerateMinutes(ctx, "org-a", "ana", m.ID, meetings.MinutesInput{Notes: "Decisions:\n- go\nAction items:\n- Ana: send it by 2026-11-01"})
	if err != nil || len(min.ActionItems) != 1 {
		t.Fatalf("%+v %v", min, err)
	}
	got, err := svc.Minutes(ctx, "org-a", m.ID)
	if err != nil || got.ActionItems[0].Due != "2026-11-01" || got.Decisions[0] != "go" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := svc.Minutes(ctx, "org-b", m.ID); !errors.Is(err, meetings.ErrNotFound) {
		t.Fatalf("RLS on minutes: %v", err)
	}
	if _, err := svc.PutSettings(ctx, "org-a", "admin", meetings.Settings{DefaultProvider: meetings.ProviderZoom, Rooms: []meetings.Room{{ID: "annex", Capacity: 6}}}); err != nil {
		t.Fatal(err)
	}
	if s, _ := svc.EffectiveSettings(ctx, "org-a"); s.DefaultProvider != meetings.ProviderZoom || len(s.Rooms) != 1 {
		t.Fatalf("%+v", s)
	}
	if s, _ := svc.EffectiveSettings(ctx, "org-b"); s.DefaultProvider != meetings.ProviderJitsi {
		t.Fatalf("settings are per organization: %+v", s)
	}

	token, feed, err := svc.CreateFeed(ctx, "org-a", "ana", "phone")
	if err != nil {
		t.Fatal(err)
	}
	ics, err := svc.FeedICS(ctx, token)
	if err != nil || !strings.Contains(ics, "SUMMARY:Pricing") {
		t.Fatalf("%v\n%s", err, ics)
	}
	if _, err := svc.FeedICS(ctx, "org-b."+strings.SplitN(token, ".", 2)[1]); !errors.Is(err, meetings.ErrNotFound) {
		t.Fatalf("a token never works in another organization: %v", err)
	}
	if err := svc.RevokeFeed(ctx, "org-a", "ana", feed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.FeedICS(ctx, token); !errors.Is(err, meetings.ErrNotFound) {
		t.Fatalf("revoked: %v", err)
	}

	// Rollbacks leave the schema clean.
	owner, err := pgx.Connect(ctx, env.rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(ctx)
	for _, f := range []string{"741_meeting_settings_feeds_down.sql", "740_meetings_down.sql"} {
		down, err := os.ReadFile("../../../migrations/down/" + f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := owner.Exec(ctx, string(down)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	var c int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name IN ('meetings','meeting_minutes','meeting_settings','calendar_feeds')`).Scan(&c); err != nil || c != 0 {
		t.Fatalf("tables still there: %d %v", c, err)
	}
}
