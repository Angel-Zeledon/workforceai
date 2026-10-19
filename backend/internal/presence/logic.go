// Package presence tracks the PEOPLE who are online in an organization's virtual office: where they stand,
// what they are doing (status) and who is near whom. The state is in memory (it is live by nature, it dies with the
// socket); only the events travel through the events hub, always tagged with the organization, so one tenant never
// sees another. See docs/architecture/presence.md.
package presence

import (
	"math"
	"strings"
	"time"
	"unicode"
)

// Status is what a person is doing, shown on their avatar and in the presence panel.
type Status string

const (
	StatusAvailable Status = "available"
	StatusFocus     Status = "focus"   // do not disturb: no conversation offers
	StatusMeeting   Status = "meeting" // in a meeting: no conversation offers
	StatusAway      Status = "away"    // set by hand or automatically after being idle
)

// ValidStatus reports whether s is one of the four statuses.
func ValidStatus(s Status) bool {
	switch s {
	case StatusAvailable, StatusFocus, StatusMeeting, StatusAway:
		return true
	}
	return false
}

// Approachable says whether a person with this status may be offered a conversation.
func Approachable(s Status) bool { return s == StatusAvailable }

// Bounds is the walkable floor (world meters); positions outside are clamped inside.
type Bounds struct{ X0, X1, Z0, Z1 float64 }

// DefaultBounds matches FLOOR in frontend/src/lib/officeGeometry.ts.
var DefaultBounds = Bounds{X0: -13.9, X1: 13.9, Z0: -9.7, Z1: 9.7}

// Clamp keeps (x, z) inside b; ok is false for NaN or infinite input.
func (b Bounds) Clamp(x, z float64) (cx, cz float64, ok bool) {
	if math.IsNaN(x) || math.IsNaN(z) || math.IsInf(x, 0) || math.IsInf(z, 0) {
		return 0, 0, false
	}
	return math.Min(b.X1, math.Max(b.X0, x)), math.Min(b.Z1, math.Max(b.Z0, z)), true
}

// Dist is the distance on the floor plane.
func Dist(ax, az, bx, bz float64) float64 { return math.Hypot(ax-bx, az-bz) }

// NextNear decides whether two people are near with hysteresis: they become near inside enter and stop being near
// beyond exit (exit > enter), so someone standing on the border does not flicker the offer on and off.
func NextNear(wasNear bool, dist, enter, exit float64) bool {
	if wasNear {
		return dist <= exit
	}
	return dist <= enter
}

// ShouldSend is the throttle of position updates: send when nothing was sent yet, or when at least minInterval passed
// AND the person moved at least minDelta since the last sent position. A pure function so it is testable with a fake clock.
func ShouldSend(last time.Time, lastX, lastZ float64, now time.Time, x, z float64, minInterval time.Duration, minDelta float64) bool {
	if last.IsZero() {
		return true
	}
	if now.Sub(last) < minInterval {
		return false
	}
	return Dist(lastX, lastZ, x, z) >= minDelta
}

// AutoAway says whether an idle person should be marked away: only an available person is moved automatically
// (focus and meeting are deliberate and never overridden).
func AutoAway(s Status, lastActive, now time.Time, after time.Duration) bool {
	return s == StatusAvailable && after > 0 && now.Sub(lastActive) >= after
}

// CleanText trims, drops control characters and cuts to max runes (zone keys, names that reach other people's screens).
func CleanText(s string, max int) string {
	s = strings.TrimSpace(s)
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if unicode.IsControl(r) {
			continue
		}
		out = append(out, r)
		if len(out) >= max {
			break
		}
	}
	return string(out)
}
