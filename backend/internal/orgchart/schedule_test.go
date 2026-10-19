package orgchart

import (
	"strings"
	"testing"
	"time"
)

func at(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func weekdays(tz, start, end string) Schedule {
	return Schedule{Enabled: true, Timezone: tz, Days: []int{1, 2, 3, 4, 5}, Start: start, End: end}
}

func TestScheduleValidate(t *testing.T) {
	if err := (Schedule{}).Validate(); err != nil {
		t.Fatalf("a disabled schedule is always valid: %v", err)
	}
	if err := weekdays("America/New_York", "09:00", "17:00").Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []struct {
		name string
		s    Schedule
		want string
	}{
		{"timezone", weekdays("Mars/Olympus", "09:00", "17:00"), "timezone"},
		{"empty timezone", weekdays("", "09:00", "17:00"), "timezone"},
		{"no days", Schedule{Enabled: true, Timezone: "UTC", Start: "09:00", End: "17:00"}, "working day"},
		{"day out of range", Schedule{Enabled: true, Timezone: "UTC", Days: []int{7}, Start: "09:00", End: "17:00"}, "days"},
		{"duplicate day", Schedule{Enabled: true, Timezone: "UTC", Days: []int{1, 1}, Start: "09:00", End: "17:00"}, "days"},
		{"bad clock", weekdays("UTC", "9:00", "17:00"), "HH:MM"},
		{"bad minutes", weekdays("UTC", "09:75", "17:00"), "HH:MM"},
		{"25h", weekdays("UTC", "09:00", "25:00"), "HH:MM"},
		{"24:30", weekdays("UTC", "09:00", "24:30"), "HH:MM"},
		{"same start end", weekdays("UTC", "09:00", "09:00"), "differ"},
		{"start 24", weekdays("UTC", "24:00", "09:00"), "start"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if err := c.s.Validate(); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want error containing %q", err, c.want)
			}
		})
	}
}

func TestScheduleDisabledOrBrokenIsOpen(t *testing.T) {
	now := at(t, "2026-03-07T03:00:00Z") // a Saturday night
	if !(Schedule{}).IsOpen(now) {
		t.Fatal("no schedule = always available")
	}
	if !weekdays("Mars/Olympus", "09:00", "17:00").IsOpen(now) {
		t.Fatal("a broken schedule must not silently stop an agent")
	}
}

func TestScheduleWeekdayHours(t *testing.T) {
	s := weekdays("UTC", "09:00", "17:00")
	cases := []struct {
		when string
		open bool
	}{
		{"2026-03-09T08:59:00Z", false}, // Monday before
		{"2026-03-09T09:00:00Z", true},  // opens exactly at the start
		{"2026-03-09T16:59:59Z", true},
		{"2026-03-09T17:00:00Z", false}, // end is exclusive
		{"2026-03-07T12:00:00Z", false}, // Saturday
		{"2026-03-08T12:00:00Z", false}, // Sunday
	}
	for _, c := range cases {
		if got := s.IsOpen(at(t, c.when)); got != c.open {
			t.Errorf("%s: open=%v want %v", c.when, got, c.open)
		}
	}
}

func TestScheduleTimezonesAndDST(t *testing.T) {
	ny := weekdays("America/New_York", "09:00", "17:00")
	cases := []struct {
		name string
		when string
		open bool
	}{
		// US daylight saving starts Sunday 2026-03-08: New York is UTC-5 before, UTC-4 after.
		{"Friday before the change, 13:30Z is 08:30 EST", "2026-03-06T13:30:00Z", false},
		{"Friday before the change, 14:00Z is 09:00 EST", "2026-03-06T14:00:00Z", true},
		{"Monday after the change, 12:59Z is 08:59 EDT", "2026-03-09T12:59:00Z", false},
		{"Monday after the change, 13:00Z is 09:00 EDT", "2026-03-09T13:00:00Z", true},
		{"Monday after the change, 20:59Z is 16:59 EDT", "2026-03-09T20:59:00Z", true},
		{"Monday after the change, 21:00Z is 17:00 EDT", "2026-03-09T21:00:00Z", false},
		// daylight saving ends Sunday 2026-11-01
		{"Monday after fall back, 13:59Z is 08:59 EST", "2026-11-02T13:59:00Z", false},
		{"Monday after fall back, 14:00Z is 09:00 EST", "2026-11-02T14:00:00Z", true},
	}
	for _, c := range cases {
		if got := ny.IsOpen(at(t, c.when)); got != c.open {
			t.Errorf("%s: open=%v want %v", c.name, got, c.open)
		}
	}
	madrid := weekdays("Europe/Madrid", "09:00", "18:00") // CEST starts 2026-03-29
	for when, want := range map[string]bool{
		"2026-03-27T07:59:00Z": false, // CET: 08:59
		"2026-03-27T08:00:00Z": true,  // CET: 09:00
		"2026-03-30T06:59:00Z": false, // CEST: 08:59
		"2026-03-30T07:00:00Z": true,  // CEST: 09:00
	} {
		if got := madrid.IsOpen(at(t, when)); got != want {
			t.Errorf("madrid %s: open=%v want %v", when, got, want)
		}
	}
	// the same instant is a different local day in Sao Paulo (UTC-3) and in Auckland (UTC+13 in March)
	sp := Schedule{Enabled: true, Timezone: "America/Sao_Paulo", Days: []int{1}, Start: "00:00", End: "24:00"} // Mondays only
	if !sp.IsOpen(at(t, "2026-03-09T03:00:00Z")) {
		t.Error("03:00Z Monday is 00:00 Monday in Sao Paulo")
	}
	if sp.IsOpen(at(t, "2026-03-09T02:59:00Z")) {
		t.Error("02:59Z Monday is still Sunday in Sao Paulo")
	}
}

func TestScheduleOvernight(t *testing.T) {
	s := Schedule{Enabled: true, Timezone: "UTC", Days: []int{1, 2, 3, 4, 5}, Start: "22:00", End: "06:00"}
	cases := map[string]bool{
		"2026-03-09T21:59:00Z": false, // Monday
		"2026-03-09T22:00:00Z": true,
		"2026-03-10T03:00:00Z": true,  // Tuesday small hours belong to Monday's window
		"2026-03-10T06:00:00Z": false, // end is exclusive
		"2026-03-14T03:00:00Z": true,  // Saturday small hours belong to Friday's window
		"2026-03-14T23:00:00Z": false, // Saturday night is not a working day
		"2026-03-15T03:00:00Z": false, // Sunday small hours: Saturday did not start a window
		"2026-03-09T03:00:00Z": false, // Monday small hours: Sunday did not start a window
	}
	for when, want := range cases {
		if got := s.IsOpen(at(t, when)); got != want {
			t.Errorf("%s: open=%v want %v", when, got, want)
		}
	}
}

func TestScheduleAroundTheClock(t *testing.T) {
	s := Schedule{Enabled: true, Timezone: "Asia/Tokyo", Days: []int{0, 1, 2, 3, 4, 5, 6}, Start: "00:00", End: "24:00"}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"2026-03-08T00:00:00Z", "2026-03-08T15:59:00Z", "2026-07-01T23:59:00Z"} {
		if !s.IsOpen(at(t, w)) {
			t.Errorf("%s must be open", w)
		}
	}
}

func TestScheduleNextOpen(t *testing.T) {
	ny := weekdays("America/New_York", "09:00", "17:00")
	// open now: the answer is "now"
	now := at(t, "2026-03-09T15:00:00Z")
	if n, ok := ny.NextOpen(now); !ok || !n.Equal(now) {
		t.Fatalf("open now: %v %v", n, ok)
	}
	// Friday after closing -> Monday 09:00 EDT = 13:00Z, across the daylight saving change
	n, ok := ny.NextOpen(at(t, "2026-03-06T22:30:00Z"))
	if !ok || !n.Equal(at(t, "2026-03-09T13:00:00Z")) {
		t.Fatalf("Friday evening -> Monday morning across DST: %v %v", n, ok)
	}
	// before opening the same day
	n, ok = ny.NextOpen(at(t, "2026-03-10T11:00:00Z")) // 07:00 EDT Tuesday
	if !ok || !n.Equal(at(t, "2026-03-10T13:00:00Z")) {
		t.Fatalf("same day: %v %v", n, ok)
	}
	// overnight schedule: closed at 12:00 UTC, next opening is 22:00 the same day
	night := Schedule{Enabled: true, Timezone: "UTC", Days: []int{1, 2}, Start: "22:00", End: "06:00"}
	n, ok = night.NextOpen(at(t, "2026-03-09T12:00:00Z")) // Monday noon
	if !ok || !n.Equal(at(t, "2026-03-09T22:00:00Z")) {
		t.Fatalf("overnight: %v %v", n, ok)
	}
	// disabled: never held back
	if n, ok := (Schedule{}).NextOpen(now); !ok || !n.Equal(now) {
		t.Fatal("disabled schedule")
	}
}

func TestScheduleNextOpenAfterWeekWithOneDay(t *testing.T) {
	s := Schedule{Enabled: true, Timezone: "Europe/Madrid", Days: []int{3}, Start: "10:00", End: "12:00"} // Wednesdays
	n, ok := s.NextOpen(at(t, "2026-03-25T11:00:00Z"))                                                    // Wednesday 12:00 CET: just closed
	// next Wednesday is 2026-04-01, already in CEST (UTC+2): 10:00 local = 08:00Z
	if !ok || !n.Equal(at(t, "2026-04-01T08:00:00Z")) {
		t.Fatalf("got %v %v", n, ok)
	}
}

func TestAvailabilityAt(t *testing.T) {
	s := weekdays("UTC", "09:00", "17:00")
	a := s.AvailabilityAt(at(t, "2026-03-07T10:00:00Z")) // Saturday
	if a.Open || a.NextOpen == nil || !a.NextOpen.Equal(at(t, "2026-03-09T09:00:00Z")) || a.Timezone != "UTC" {
		t.Fatalf("%+v", a)
	}
	if b := s.AvailabilityAt(at(t, "2026-03-09T10:00:00Z")); !b.Open || b.NextOpen != nil {
		t.Fatalf("%+v", b)
	}
}
