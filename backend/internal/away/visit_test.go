package away

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)

func TestTouchStartsAVisitOnlyAfterTheGap(t *testing.T) {
	gap := 30 * time.Minute
	m := Touch(Mark{}, false, t0, gap)
	if !m.LastSeenAt.Equal(t0) || !m.VisitStartedAt.Equal(t0) || m.AwaySince != nil {
		t.Fatalf("first visit = %+v", m)
	}
	// Heartbeats inside the gap only move last_seen.
	m = Touch(m, true, t0.Add(10*time.Minute), gap)
	m = Touch(m, true, t0.Add(35*time.Minute), gap)
	if !m.VisitStartedAt.Equal(t0) || !m.LastSeenAt.Equal(t0.Add(35*time.Minute)) || m.AwaySince != nil {
		t.Fatalf("same visit = %+v", m)
	}
	// A heartbeat after the gap starts a new visit and remembers when the previous one ended.
	back := t0.Add(35*time.Minute + 9*time.Hour)
	m2 := Touch(m, true, back, gap)
	if !m2.VisitStartedAt.Equal(back) || m2.AwaySince == nil || !m2.AwaySince.Equal(t0.Add(35*time.Minute)) {
		t.Fatalf("new visit = %+v", m2)
	}
	// A clock that goes backwards changes nothing.
	if m3 := Touch(m2, true, back.Add(-time.Hour), gap); m3 != m2 {
		t.Fatalf("backwards clock = %+v", m3)
	}
}

func TestAbsenceIsTheSameBeforeAndAfterTheHeartbeat(t *testing.T) {
	gap := 30 * time.Minute
	left := t0
	m := Mark{LastSeenAt: left, VisitStartedAt: left.Add(-2 * time.Hour)}
	if _, _, ok := Absence(Mark{}, false, t0, gap); ok {
		t.Fatal("first visit ever has no absence")
	}
	if _, _, ok := Absence(m, true, left.Add(5*time.Minute), gap); ok {
		t.Fatal("inside a visit with no previous one: no absence")
	}
	back := left.Add(10 * time.Hour)
	from, to, ok := Absence(m, true, back, gap) // the summary is asked before the heartbeat arrives
	if !ok || !from.Equal(left) || !to.Equal(back) {
		t.Fatalf("before heartbeat = %v %v %v", from, to, ok)
	}
	touched := Touch(m, true, back, gap)
	from2, to2, ok := Absence(touched, true, back.Add(3*time.Minute), gap) // a reload a bit later
	if !ok || !from2.Equal(from) || !to2.Equal(to) {
		t.Fatalf("after heartbeat = %v %v %v", from2, to2, ok)
	}
}

func TestDismissedLastsOneVisit(t *testing.T) {
	gap := 30 * time.Minute
	back := t0.Add(10 * time.Hour)
	m := Touch(Mark{LastSeenAt: t0, VisitStartedAt: t0}, true, back, gap)
	if Dismissed(m, true, back, gap) {
		t.Fatal("not dismissed yet")
	}
	d := back.Add(time.Minute)
	m = Touch(m, true, d, gap)
	m.DismissedAt = &d
	if !Dismissed(m, true, d.Add(5*time.Minute), gap) {
		t.Fatal("dismissed in this visit")
	}
	// The next absence opens it again.
	later := d.Add(9 * time.Hour)
	if Dismissed(m, true, later, gap) {
		t.Fatal("an old dismissal must not hide the next absence")
	}
	m = Touch(m, true, later, gap)
	if Dismissed(m, true, later.Add(time.Minute), gap) {
		t.Fatal("dismissal from the previous visit leaked into the new one")
	}
}
