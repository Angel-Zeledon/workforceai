// Package away builds the "while you were away" summary (B3, docs/architecture/away-summary.md): what happened in
// an organization while a person was not looking, computed on read from data the platform already records (the
// event log, pending approvals and the cost ledger). It is deterministic and free: no model is called.
//
// The only new state is one mark per person and organization: when they were last seen. The frontend sends a
// heartbeat while the tab is visible; a heartbeat after a gap starts a new visit, and the absence the summary
// covers is the time between the end of the previous visit and the start of the new one.
package away

import "time"

// DefaultGap is the silence after which the next heartbeat starts a new visit. The frontend beats every few
// minutes while the tab is visible, so a gap this long means the person was really gone.
const DefaultGap = 30 * time.Minute

// Mark is when a person was last seen in an organization.
type Mark struct {
	LastSeenAt     time.Time `json:"last_seen_at"`
	VisitStartedAt time.Time `json:"visit_started_at"`
	// AwaySince is the end of the previous visit (nil on the first visit ever).
	AwaySince *time.Time `json:"away_since,omitempty"`
	// DismissedAt is when the person closed the summary; it only counts for the visit it was closed in.
	DismissedAt *time.Time `json:"dismissed_at,omitempty"`
}

// Touch records a heartbeat at now. found is false when the person has no mark yet (first visit).
func Touch(prev Mark, found bool, now time.Time, gap time.Duration) Mark {
	if !found {
		return Mark{LastSeenAt: now, VisitStartedAt: now}
	}
	if now.Before(prev.LastSeenAt) {
		return prev // a clock that went backwards never shortens or reopens a visit
	}
	if now.Sub(prev.LastSeenAt) >= gap {
		last := prev.LastSeenAt
		return Mark{LastSeenAt: now, VisitStartedAt: now, AwaySince: &last}
	}
	prev.LastSeenAt = now
	return prev
}

// Absence is the period the summary covers at now, whether or not the heartbeat of the new visit already
// arrived: before it, the absence runs from the last heartbeat until now; after it, from the end of the previous
// visit until the start of this one. ok is false on the first visit ever (nothing to compare against).
func Absence(m Mark, found bool, now time.Time, gap time.Duration) (from, to time.Time, ok bool) {
	if !found {
		return time.Time{}, time.Time{}, false
	}
	if now.Sub(m.LastSeenAt) >= gap {
		return m.LastSeenAt, now, true
	}
	if m.AwaySince != nil {
		return *m.AwaySince, m.VisitStartedAt, true
	}
	return time.Time{}, time.Time{}, false
}

// Dismissed says whether the person already closed the summary of the visit in progress at now.
func Dismissed(m Mark, found bool, now time.Time, gap time.Duration) bool {
	if !found || m.DismissedAt == nil || now.Sub(m.LastSeenAt) >= gap {
		return false // a new visit is starting: an old dismissal belonged to the previous one
	}
	return !m.DismissedAt.Before(m.VisitStartedAt)
}
