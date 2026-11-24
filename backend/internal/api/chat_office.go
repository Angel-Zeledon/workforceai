package api

import (
	"net/http"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/offices"
)

// The office a message, request or listing is asked from (design §4, docs/superpowers/plans/2026-11-23-aislamiento-oficinas.md).

// enterOffice validates an office_id: the person must be able to enter it, else it answers like a missing office (404).
// Without an offices model (no authentication, nothing restricted) the office is ignored, as before. It returns the id
// to scope the work with ("" = unscoped).
func (s *server) enterOffice(r *http.Request, id string) (string, error) {
	if id == "" {
		return "", nil
	}
	m, v, err := s.agentModel(r)
	if err != nil {
		return "", err
	}
	if m == nil {
		return "", nil
	}
	if _, ok := m.Office(id); !ok || !m.CanEnter(v, id) {
		return "", notFound("office", id)
	}
	return id, nil
}

// sameOffice compares two office ids, General being the same as none.
func sameOffice(a, b string) bool {
	if a == offices.GeneralID {
		a = ""
	}
	if b == offices.GeneralID {
		b = ""
	}
	return a == b
}

// officeMessages is the filter of a message listing: with an office asked, only that office's messages (General reads
// those written without one); always, never the messages of a private office the person is not in.
func (s *server) officeMessages(r *http.Request, want string) (func(domain.Message) bool, error) {
	m, v, err := s.agentModel(r)
	if err != nil {
		return nil, err
	}
	return func(msg domain.Message) bool {
		if want != "" && !sameOffice(msg.OfficeID, want) {
			return false
		}
		return m == nil || msg.OfficeID == "" || m.CanSeeOfficeWork(v, msg.OfficeID)
	}, nil
}
