package calendar

import (
	"strings"
	"time"
	"unicode/utf8"
)

// ICS (RFC 5545) generation for the export of one meeting and the per-person
// subscription feed. Times are written in UTC ("Z"), text is escaped and lines
// are folded at 75 octets, so any calendar app (Google, Outlook, Apple) reads it.

// ICSEvent is one VEVENT.
type ICSEvent struct {
	UID         string
	Title       string
	Description string
	Location    string
	URL         string // join link
	Start, End  time.Time
	Organizer   string // e-mail, optional
	Attendees   []ICSAttendee
	Cancelled   bool
	Sequence    int
	Updated     time.Time // DTSTAMP / LAST-MODIFIED; zero = now
}

// ICSAttendee is one ATTENDEE line.
type ICSAttendee struct{ Name, Email string }

const icsTime = "20060102T150405Z"

// ICSEscape escapes a TEXT value.
func ICSEscape(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	r := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\n", `\n`)
	return r.Replace(s)
}

// icsParam makes a safe quoted parameter value (no quotes, CR, LF).
func icsParam(s string) string {
	s = strings.NewReplacer(`"`, "'", "\r", " ", "\n", " ").Replace(s)
	return `"` + s + `"`
}

// foldLine folds one content line at 75 octets without splitting a UTF-8 rune.
func foldLine(line string) string {
	if len(line) <= 75 {
		return line
	}
	var b strings.Builder
	n := 0
	limit := 75
	for _, r := range line {
		w := utf8.RuneLen(r)
		if n+w > limit {
			b.WriteString("\r\n ")
			n = 1 // the leading space counts
			limit = 75
		}
		b.WriteRune(r)
		n += w
	}
	return b.String()
}

// cleanHeader strips CR/LF from values that go on one line unescaped (UID, email, URL).
func cleanHeader(s string) string {
	return strings.NewReplacer("\r", "", "\n", "", " ", "").Replace(s)
}

// BuildICS renders a VCALENDAR with the given events. name is X-WR-CALNAME.
func BuildICS(name string, events []ICSEvent, now time.Time) string {
	var b strings.Builder
	w := func(line string) {
		b.WriteString(foldLine(line))
		b.WriteString("\r\n")
	}
	w("BEGIN:VCALENDAR")
	w("VERSION:2.0")
	w("PRODID:-//AI Workforce OS//Meetings//EN")
	w("CALSCALE:GREGORIAN")
	w("METHOD:PUBLISH")
	if name != "" {
		w("X-WR-CALNAME:" + ICSEscape(name))
	}
	for _, e := range events {
		stamp := e.Updated
		if stamp.IsZero() {
			stamp = now
		}
		w("BEGIN:VEVENT")
		w("UID:" + cleanHeader(e.UID))
		w("DTSTAMP:" + stamp.UTC().Format(icsTime))
		w("LAST-MODIFIED:" + stamp.UTC().Format(icsTime))
		w("DTSTART:" + e.Start.UTC().Format(icsTime))
		w("DTEND:" + e.End.UTC().Format(icsTime))
		w("SUMMARY:" + ICSEscape(e.Title))
		desc := e.Description
		if e.URL != "" {
			desc = strings.TrimSpace(desc + "\n\n" + e.URL)
		}
		if desc != "" {
			w("DESCRIPTION:" + ICSEscape(desc))
		}
		if e.Location != "" {
			w("LOCATION:" + ICSEscape(e.Location))
		}
		if e.URL != "" {
			w("URL:" + cleanHeader(e.URL))
		}
		if e.Organizer != "" {
			w("ORGANIZER:mailto:" + cleanHeader(e.Organizer))
		}
		for _, a := range e.Attendees {
			if a.Email == "" {
				continue
			}
			p := ""
			if a.Name != "" {
				p = ";CN=" + icsParam(a.Name)
			}
			w("ATTENDEE" + p + ";ROLE=REQ-PARTICIPANT:mailto:" + cleanHeader(a.Email))
		}
		w("SEQUENCE:" + itoa(e.Sequence))
		if e.Cancelled {
			w("STATUS:CANCELLED")
		} else {
			w("STATUS:CONFIRMED")
		}
		w("TRANSP:OPAQUE")
		w("END:VEVENT")
	}
	w("END:VCALENDAR")
	return b.String()
}

func itoa(n int) string {
	if n <= 0 {
		return "0"
	}
	var d [20]byte
	i := len(d)
	for n > 0 {
		i--
		d[i] = byte('0' + n%10)
		n /= 10
	}
	return string(d[i:])
}
