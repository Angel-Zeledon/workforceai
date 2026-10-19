package api

import "net/http"

// meetingDirectory: GET /meetings/directory: the people and agents of the organization that can be picked as
// attendees (the Meetings screen shows a picker instead of free text).
func (s *server) meetingDirectory(w http.ResponseWriter, r *http.Request) {
	list, err := s.Meetings.Directory(r.Context(), s.org(r))
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"attendees": list})
}
