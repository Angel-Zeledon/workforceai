package orgchart

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // zone data embedded: schedules must not depend on the host having /usr/share/zoneinfo
)

// A schedule is evaluated on the WALL CLOCK of its timezone, never on elapsed hours: "09:00-17:00 in
// America/New_York" means 09:00-17:00 local time on the day of a daylight-saving change too.

func parseClock(v string) (int, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(v), ":")
	if !ok || len(h) != 2 || len(m) != 2 {
		return 0, fmt.Errorf("time %q must be HH:MM", v)
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || mm < 0 || mm > 59 || hh < 0 || hh > 24 || (hh == 24 && mm != 0) {
		return 0, fmt.Errorf("time %q must be HH:MM between 00:00 and 24:00", v)
	}
	return hh*60 + mm, nil
}

// Validate checks a schedule. A disabled schedule is always valid (it is ignored).
func (s Schedule) Validate() error {
	if !s.Enabled {
		return nil
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil || strings.TrimSpace(s.Timezone) == "" {
		return fmt.Errorf("unknown timezone %q", s.Timezone)
	}
	if len(s.Days) == 0 {
		return fmt.Errorf("a schedule needs at least one working day")
	}
	seen := map[int]bool{}
	for _, d := range s.Days {
		if d < 0 || d > 6 || seen[d] {
			return fmt.Errorf("days must be unique numbers from 0 (Sunday) to 6 (Saturday)")
		}
		seen[d] = true
	}
	st, err := parseClock(s.Start)
	if err != nil {
		return err
	}
	en, err := parseClock(s.End)
	if err != nil {
		return err
	}
	if st == en {
		return fmt.Errorf("start and end must differ (use 00:00-24:00 for the whole day)")
	}
	if st == 24*60 {
		return fmt.Errorf("start cannot be 24:00")
	}
	return nil
}

// Normalize sorts the days; it never changes their meaning.
func (s Schedule) Normalize() Schedule {
	s.Days = append([]int{}, s.Days...)
	sort.Ints(s.Days)
	s.Timezone = strings.TrimSpace(s.Timezone)
	return s
}

func (s Schedule) hasDay(d int) bool {
	for _, x := range s.Days {
		if x == d {
			return true
		}
	}
	return false
}

// IsOpen reports whether the agent may act at instant t. A disabled or invalid schedule is always
// open: a broken configuration must never silently stop an agent forever.
func (s Schedule) IsOpen(t time.Time) bool {
	if !s.Enabled || s.Validate() != nil {
		return true
	}
	loc, _ := time.LoadLocation(s.Timezone)
	st, _ := parseClock(s.Start)
	en, _ := parseClock(s.End)
	lt := t.In(loc)
	m := lt.Hour()*60 + lt.Minute()
	today := int(lt.Weekday())
	yesterday := (today + 6) % 7
	if st < en {
		return s.hasDay(today) && m >= st && m < en
	}
	// overnight window (end earlier than start): it belongs to the day it starts
	return (s.hasDay(today) && m >= st) || (s.hasDay(yesterday) && m < en)
}

// NextOpen returns the first instant at or after t at which the agent may act (t itself when it is
// already open). It scans at most eight local days; ok is false only for a schedule with no usable
// window, which Validate rejects.
func (s Schedule) NextOpen(t time.Time) (time.Time, bool) {
	if !s.Enabled || s.Validate() != nil || s.IsOpen(t) {
		return t, true
	}
	loc, _ := time.LoadLocation(s.Timezone)
	st, _ := parseClock(s.Start)
	lt := t.In(loc)
	for i := 0; i <= 8; i++ {
		d := lt.AddDate(0, 0, i)
		if !s.hasDay(int(d.Weekday())) {
			continue
		}
		// time.Date resolves a wall time that does not exist (spring-forward gap) to the instant after the gap.
		cand := time.Date(d.Year(), d.Month(), d.Day(), st/60, st%60, 0, 0, loc)
		if cand.After(t) {
			return cand, true
		}
	}
	return time.Time{}, false
}

// Availability is what the UI and the task gate need to know about an agent at an instant.
type Availability struct {
	Open     bool       `json:"open"`
	NextOpen *time.Time `json:"next_open,omitempty"`
	Timezone string     `json:"timezone,omitempty"`
}

// AvailabilityAt evaluates a schedule at an instant.
func (s Schedule) AvailabilityAt(t time.Time) Availability {
	a := Availability{Open: s.IsOpen(t)}
	if s.Enabled {
		a.Timezone = s.Timezone
	}
	if !a.Open {
		if n, ok := s.NextOpen(t); ok {
			n = n.UTC() // an instant: the client formats it in its own zone
			a.NextOpen = &n
		}
	}
	return a
}
