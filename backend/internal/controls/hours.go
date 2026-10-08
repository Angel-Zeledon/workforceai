package controls

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // zone database embedded: the container may not ship one
)

// CodeOutsideHours is the deny code of Admit while the organization is closed.
const CodeOutsideHours = "outside_operating_hours"

// DayHours is one open interval of a weekday. Day is 0=Sunday..6=Saturday (the
// same convention as application.Schedule.Weekdays). Open and Close are local
// "HH:MM" times of the organization time zone; Close may be "24:00". Intervals
// do not cross midnight: write 22:00-24:00 on one day and 00:00-06:00 on the next.
type DayHours struct {
	Day   int    `json:"day"`
	Open  string `json:"open"`
	Close string `json:"close"`
}

// OperatingHours is the weekly schedule in which agents may START new work.
// The zero value (Enabled=false) means "always open": no behaviour change.
// It never blocks approvals, reads of the UI, or tool calls of work already
// admitted: only Admit (new tasks, scheduler claims) consults it.
type OperatingHours struct {
	Enabled  bool       `json:"enabled"`
	Timezone string     `json:"timezone"`
	Weekly   []DayHours `json:"weekly"`
}

const maxHourIntervals = 28

func parseHM(s string) (int, error) {
	h, m, ok := strings.Cut(s, ":")
	if !ok || len(h) != 2 || len(m) != 2 {
		return 0, fmt.Errorf("time %q must be HH:MM", s)
	}
	hh, e1 := strconv.Atoi(h)
	mm, e2 := strconv.Atoi(m)
	if e1 != nil || e2 != nil || hh < 0 || hh > 24 || mm < 0 || mm > 59 || (hh == 24 && mm != 0) {
		return 0, fmt.Errorf("time %q is not a valid HH:MM", s)
	}
	return hh*60 + mm, nil
}

// Validate checks the schedule. A disabled schedule is always valid.
func (h OperatingHours) Validate() error {
	if !h.Enabled {
		return nil
	}
	if _, err := time.LoadLocation(h.Timezone); err != nil || strings.TrimSpace(h.Timezone) == "" {
		return fmt.Errorf("%w: operating_hours.timezone must be an IANA zone such as America/Mexico_City", ErrInvalid)
	}
	if len(h.Weekly) == 0 {
		return fmt.Errorf("%w: operating_hours needs at least one open interval (or disable it)", ErrInvalid)
	}
	if len(h.Weekly) > maxHourIntervals {
		return fmt.Errorf("%w: too many operating_hours intervals (max %d)", ErrInvalid, maxHourIntervals)
	}
	for _, d := range h.Weekly {
		if d.Day < 0 || d.Day > 6 {
			return fmt.Errorf("%w: operating_hours.day must be 0 (Sunday) to 6 (Saturday)", ErrInvalid)
		}
		o, err := parseHM(d.Open)
		if err != nil {
			return fmt.Errorf("%w: operating_hours.open: %v", ErrInvalid, err)
		}
		c, err := parseHM(d.Close)
		if err != nil {
			return fmt.Errorf("%w: operating_hours.close: %v", ErrInvalid, err)
		}
		if o >= c {
			return fmt.Errorf("%w: operating_hours: open must be before close (intervals do not cross midnight)", ErrInvalid)
		}
	}
	return nil
}

func (h OperatingHours) loc() *time.Location {
	l, err := time.LoadLocation(h.Timezone)
	if err != nil {
		return time.UTC
	}
	return l
}

// IsOpen reports whether work may start at t. A disabled schedule is always open.
func (h OperatingHours) IsOpen(t time.Time) bool {
	if !h.Enabled {
		return true
	}
	lt := t.In(h.loc())
	now := lt.Hour()*60 + lt.Minute()
	for _, d := range h.Weekly {
		if d.Day != int(lt.Weekday()) {
			continue
		}
		o, e1 := parseHM(d.Open)
		c, e2 := parseHM(d.Close)
		if e1 == nil && e2 == nil && now >= o && now < c {
			return true
		}
	}
	return false
}

// NextOpen returns the next instant (within 8 days) at which the schedule
// opens, or nil when open now / never.
func (h OperatingHours) NextOpen(t time.Time) *time.Time {
	if !h.Enabled || h.IsOpen(t) {
		return nil
	}
	loc := h.loc()
	lt := t.In(loc)
	var best *time.Time
	for add := 0; add <= 7; add++ {
		day := lt.AddDate(0, 0, add)
		for _, d := range h.Weekly {
			if d.Day != int(day.Weekday()) {
				continue
			}
			o, err := parseHM(d.Open)
			if err != nil {
				continue
			}
			at := time.Date(day.Year(), day.Month(), day.Day(), o/60, o%60, 0, 0, loc)
			if at.After(t) && (best == nil || at.Before(*best)) {
				u := at.UTC()
				best = &u
			}
		}
		if best != nil {
			return best
		}
	}
	return best
}

// HoursView is the answer of GET /org/operating-hours.
type HoursView struct {
	OperatingHours
	OpenNow  bool       `json:"open_now"`
	NextOpen *time.Time `json:"next_open_at"`
}

// OperatingHours returns the schedule and whether the org is open now.
func (s *Service) OperatingHours(ctx context.Context, org string) (HoursView, error) {
	st, err := s.State(ctx, org)
	if err != nil {
		return HoursView{}, err
	}
	h := normHours(st.Settings.OperatingHours)
	now := s.now()
	return HoursView{OperatingHours: h, OpenNow: h.IsOpen(now), NextOpen: h.NextOpen(now)}, nil
}

func normHours(h *OperatingHours) OperatingHours {
	if h == nil {
		return OperatingHours{Weekly: []DayHours{}, Timezone: "UTC"}
	}
	out := *h
	if out.Weekly == nil {
		out.Weekly = []DayHours{}
	}
	if out.Timezone == "" {
		out.Timezone = "UTC"
	}
	return out
}

// SetOperatingHours stores the weekly schedule (audited, control.changed).
// Callers need org:manage (owner/admin).
func (s *Service) SetOperatingHours(ctx context.Context, org string, h OperatingHours, actor string) (HoursView, error) {
	if err := h.Validate(); err != nil {
		return HoursView{}, err
	}
	s.mu.Lock()
	st, err := s.Store.GetOrg(ctx, org)
	if err != nil {
		s.mu.Unlock()
		return HoursView{}, err
	}
	norm(&st)
	if h.Weekly == nil {
		h.Weekly = []DayHours{}
	}
	st.Settings.OperatingHours = &h
	if err := s.Store.PutOrg(ctx, org, st); err != nil {
		s.mu.Unlock()
		return HoursView{}, err
	}
	s.mu.Unlock()
	s.changed(ctx, org, actor, "control.operating_hours", st, map[string]any{"operating_hours": h})
	return s.OperatingHours(ctx, org)
}
