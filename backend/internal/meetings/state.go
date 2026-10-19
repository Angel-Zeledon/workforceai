package meetings

import "fmt"

// The meeting state machine:
//
//	scheduled --start--> live --end--> ended
//	scheduled --cancel--> cancelled
//
// ended and cancelled are final. A live meeting cannot be cancelled (it is
// ended: it happened), an ended one cannot be restarted, and only a scheduled
// meeting can be edited or rescheduled. Minutes can be written while live or
// after the end.

// Action is a transition trigger.
type Action string

const (
	ActStart  Action = "start"
	ActEnd    Action = "end"
	ActCancel Action = "cancel"
)

var transitions = map[Status]map[Action]Status{
	StatusScheduled: {ActStart: StatusLive, ActCancel: StatusCancelled},
	StatusLive:      {ActEnd: StatusEnded},
}

// Next returns the status after applying an action, or ErrState.
func Next(from Status, a Action) (Status, error) {
	if to, ok := transitions[from][a]; ok {
		return to, nil
	}
	return from, fmt.Errorf("%w: cannot %s a %s meeting", ErrState, a, from)
}

// Final reports whether a status has no outgoing transition.
func (s Status) Final() bool { return len(transitions[s]) == 0 }

// Editable reports whether title, time, room, attendees and agenda can change.
func (s Status) Editable() bool { return s == StatusScheduled }

// CanWriteMinutes reports whether minutes can be generated.
func (s Status) CanWriteMinutes() bool { return s == StatusLive || s == StatusEnded }

// Occupies reports whether the meeting holds its room (cancelled and ended ones do not).
func (s Status) Occupies() bool { return s == StatusScheduled || s == StatusLive }
