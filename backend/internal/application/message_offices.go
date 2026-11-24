package application

import (
	"context"
	"slices"

	"aiworkforce/backend/internal/domain"
)

// MessageOffices says which messages a READ of the history may return, decided by the store in the query (so a page is
// full of what the person may read and "has more" is true to it): the messages of one office (the one asked with
// ?office_id=) and never the ones written in an office the person cannot see (a private office they are not in).
// The zero value allows everything. It travels in the context (WithMessageOffices) like the work filter.
type MessageOffices struct {
	Asked  bool     // only the messages of Office
	Office string   // the office asked ("" = General, which keeps what was written without one)
	Hide   []string // offices whose messages are never returned (never General: "" is not listed)
}

type messageOfficesKey struct{}

// WithMessageOffices returns ctx carrying the filter of the messages a read may return.
func WithMessageOffices(ctx context.Context, f MessageOffices) context.Context {
	return context.WithValue(ctx, messageOfficesKey{}, f)
}

// MessageOfficesFrom is the filter set by WithMessageOffices (zero = everything).
func MessageOfficesFrom(ctx context.Context) MessageOffices {
	f, _ := ctx.Value(messageOfficesKey{}).(MessageOffices)
	return f
}

// Active reports whether the filter leaves something out.
func (f MessageOffices) Active() bool { return f.Asked || len(f.Hide) > 0 }

// Allows reports whether a message written in office (the id as stored; "" and "general" are General) may be returned.
func (f MessageOffices) Allows(office string) bool {
	office = messageOffice(office)
	if f.Asked && office != messageOffice(f.Office) {
		return false
	}
	return office == "" || !slices.Contains(f.Hide, office)
}

// HideIDs is Hide as a non-nil list (a SQL array parameter).
func (f MessageOffices) HideIDs() []string {
	if f.Hide == nil {
		return []string{}
	}
	return f.Hide
}

// WithHide returns f also hiding the messages of these offices.
func (f MessageOffices) WithHide(offices []string) MessageOffices {
	f.Hide = append(slices.Clone(f.Hide), offices...)
	return f
}

// WithOfficeAsked returns f also restricted to the messages of one office.
func (f MessageOffices) WithOfficeAsked(office string) MessageOffices {
	f.Asked, f.Office = true, messageOffice(office)
	return f
}

// FilterMessages keeps the messages the filter allows (the same slice when nothing is left out).
func (f MessageOffices) FilterMessages(msgs []domain.Message) []domain.Message {
	if !f.Active() {
		return msgs
	}
	return slices.DeleteFunc(slices.Clone(msgs), func(m domain.Message) bool { return !f.Allows(m.OfficeID) })
}
