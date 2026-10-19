package api

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/calendar"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/meetings"
)

// Meetings API (docs/architecture/meetings.md). Reading needs requests:read (viewers see the
// calendar of the office); creating, running and writing minutes needs requests:create (members);
// settings need org:manage. Calendar free/busy needs requests:create too, because it reveals the
// availability of colleagues. The only public route is the ICS subscription feed, whose URL is a
// secret token.

// The public feed endpoint is rate limited per client address (the token is the secret; this only
// protects the database from a flood of guesses).
var (
	feedLimiter = auth.NewMemoryLimiter()
	feedLimit   = auth.Limit{Max: 600, Window: time.Minute}
)

func remoteHost(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

func (s *server) mountMeetingsPublic(r chi.Router) {
	if s.Meetings == nil {
		return
	}
	r.Get("/calendar/feed/{file}", s.calendarFeed)
}

func (s *server) mountMeetings(r chi.Router) {
	if s.Meetings == nil {
		return
	}
	read := s.can(auth.PermRequestsRead)
	write := s.can(auth.PermRequestsCreate)
	r.With(read).Get("/meetings", s.listMeetings)
	r.With(write).Post("/meetings", s.createMeeting)
	r.With(read).Get("/meetings/rooms", s.meetingRooms)
	r.With(read).Get("/meetings/settings", s.getMeetingSettings)
	r.With(s.can(auth.PermOrgManage)).Put("/meetings/settings", s.putMeetingSettings)
	r.With(s.can(auth.PermConnectionsRead)).Get("/meetings/calendars", s.meetingCalendars)
	r.With(write).Get("/meetings/calendar-events", s.meetingCalendarEvents)
	r.With(write).Post("/meetings/free-busy", s.meetingFreeBusy)
	r.With(read).Get("/meetings/feeds", s.listMeetingFeeds)
	r.With(write).Post("/meetings/feeds", s.createMeetingFeed)
	r.With(write).Delete("/meetings/feeds/{fid}", s.revokeMeetingFeed)
	r.With(read).Get("/meetings/{id}", s.getMeeting)
	r.With(write).Patch("/meetings/{id}", s.patchMeeting)
	r.With(write).Post("/meetings/{id}/start", s.startMeeting)
	r.With(write).Post("/meetings/{id}/end", s.endMeeting)
	r.With(write).Post("/meetings/{id}/cancel", s.cancelMeeting)
	r.With(write).Post("/meetings/{id}/brief", s.briefMeeting)
	r.With(write).Post("/meetings/{id}/sync", s.syncMeeting)
	r.With(read).Get("/meetings/{id}/ics", s.meetingICS)
	r.With(read).Get("/meetings/{id}/minutes", s.getMinutes)
	r.With(write).Post("/meetings/{id}/minutes", s.postMinutes)
	r.With(write).Put("/meetings/{id}/minutes", s.putMinutes)
	r.With(write).Post("/meetings/{id}/minutes/tasks", s.minutesTasks)
}

// meetingFail maps service errors to a stable code and status. Messages never carry provider text.
func (s *server) meetingFail(w http.ResponseWriter, err error) {
	code, status := "internal", http.StatusInternalServerError
	switch {
	case errors.Is(err, meetings.ErrNotFound), errors.Is(err, domain.ErrNotFound):
		code, status = "not_found", http.StatusNotFound
	case errors.Is(err, meetings.ErrRoomBusy):
		code, status = "room_busy", http.StatusConflict
	case errors.Is(err, meetings.ErrState):
		code, status = "invalid_state", http.StatusConflict
	case errors.Is(err, meetings.ErrBudget):
		code, status = "budget_exceeded", http.StatusConflict
	case errors.Is(err, meetings.ErrNothingToDo):
		code, status = "nothing_to_do", http.StatusUnprocessableEntity
	case errors.Is(err, meetings.ErrUnavailable):
		code, status = "not_available", http.StatusNotImplemented
	case errors.Is(err, meetings.ErrInvalid), errors.Is(err, calendar.ErrInvalid):
		code, status = "invalid", http.StatusBadRequest
	case errors.Is(err, calendar.ErrReauth):
		code, status = "reauth_required", http.StatusConflict
	case errors.Is(err, calendar.ErrReadOnly):
		code, status = "read_only", http.StatusConflict
	case errors.Is(err, calendar.ErrBlocked):
		code, status = "blocked", http.StatusConflict
	case errors.Is(err, calendar.ErrProvider), errors.Is(err, calendar.ErrNotFound):
		code, status = "provider_error", http.StatusBadGateway
	case errors.Is(err, domain.ErrInvalid):
		code, status = "invalid", http.StatusBadRequest
	}
	if status == http.StatusInternalServerError {
		s.Log.Error("meetings request failed", "err", err)
		writeJSON(w, status, map[string]string{"error": "internal error", "code": code})
		return
	}
	writeJSON(w, status, map[string]string{"error": err.Error(), "code": code})
}

func meetingTime(w http.ResponseWriter, r *http.Request, key string) (time.Time, bool) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return time.Time{}, true
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": key + " must be RFC 3339", "code": "invalid"})
		return time.Time{}, false
	}
	return t, true
}

// ---- meetings ----

func (s *server) listMeetings(w http.ResponseWriter, r *http.Request) {
	from, ok := meetingTime(w, r, "from")
	if !ok {
		return
	}
	to, ok := meetingTime(w, r, "to")
	if !ok {
		return
	}
	if err := meetings.Window(from, to); err != nil {
		s.meetingFail(w, err)
		return
	}
	f := meetings.Filter{From: from, To: to, Room: r.URL.Query().Get("room"), Limit: queryInt(r, "limit")}
	for _, st := range strings.Split(r.URL.Query().Get("status"), ",") {
		if st = strings.TrimSpace(st); st != "" {
			f.Status = append(f.Status, meetings.Status(st))
		}
	}
	if r.URL.Query().Get("mine") == "1" {
		f.Person = s.actor(r)
	}
	out, err := s.Meetings.List(r.Context(), s.org(r), f)
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type meetingBody struct {
	Title        string                `json:"title"`
	Description  string                `json:"description"`
	Room         string                `json:"room"`
	Start        time.Time             `json:"start"`
	End          time.Time             `json:"end"`
	Provider     string                `json:"provider"`
	JoinURL      string                `json:"join_url"`
	Attendees    []meetings.Attendee   `json:"attendees"`
	Agenda       []meetings.AgendaItem `json:"agenda"`
	SyncCalendar bool                  `json:"sync_calendar"`
	// StartNow creates the meeting and opens it at once (an instant meeting in a room).
	StartNow    bool   `json:"start_now"`
	BriefAgents bool   `json:"brief_agents"`
	Lang        string `json:"lang"`
}

func (s *server) createMeeting(w http.ResponseWriter, r *http.Request) {
	var b meetingBody
	if err := s.decode(w, r, &b); err != nil {
		s.fail(w, err)
		return
	}
	if b.StartNow {
		now := time.Now().UTC()
		if b.Start.IsZero() {
			b.Start = now
		}
		if b.End.IsZero() {
			b.End = b.Start.Add(time.Hour)
		}
	}
	org, actor := s.org(r), s.actor(r)
	m, err := s.Meetings.Create(r.Context(), org, actor, meetings.CreateInput{Title: b.Title, Description: b.Description, Room: b.Room, Start: b.Start, End: b.End,
		Provider: b.Provider, JoinURL: b.JoinURL, Attendees: b.Attendees, Agenda: b.Agenda, SyncCalendar: b.SyncCalendar})
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	if b.StartNow {
		if m, err = s.Meetings.Start(r.Context(), org, actor, m.ID, b.BriefAgents, b.Lang); err != nil {
			s.meetingFail(w, err)
			return
		}
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *server) getMeeting(w http.ResponseWriter, r *http.Request) {
	m, err := s.Meetings.Get(r.Context(), s.org(r), chi.URLParam(r, "id"))
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *server) patchMeeting(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Title       *string                `json:"title"`
		Description *string                `json:"description"`
		Room        *string                `json:"room"`
		Start       *time.Time             `json:"start"`
		End         *time.Time             `json:"end"`
		Provider    *string                `json:"provider"`
		JoinURL     *string                `json:"join_url"`
		Attendees   *[]meetings.Attendee   `json:"attendees"`
		Agenda      *[]meetings.AgendaItem `json:"agenda"`
	}
	if err := s.decode(w, r, &b); err != nil {
		s.fail(w, err)
		return
	}
	m, err := s.Meetings.Update(r.Context(), s.org(r), s.actor(r), chi.URLParam(r, "id"), meetings.UpdateInput{Title: b.Title, Description: b.Description,
		Room: b.Room, Start: b.Start, End: b.End, Provider: b.Provider, JoinURL: b.JoinURL, Attendees: b.Attendees, Agenda: b.Agenda})
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

type langBody struct {
	Lang        string `json:"lang"`
	BriefAgents bool   `json:"brief_agents"`
}

func (s *server) startMeeting(w http.ResponseWriter, r *http.Request) {
	var b langBody
	if r.ContentLength != 0 {
		if err := s.decode(w, r, &b); err != nil {
			s.fail(w, err)
			return
		}
	}
	m, err := s.Meetings.Start(r.Context(), s.org(r), s.actor(r), chi.URLParam(r, "id"), b.BriefAgents, b.Lang)
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *server) endMeeting(w http.ResponseWriter, r *http.Request) {
	m, err := s.Meetings.End(r.Context(), s.org(r), s.actor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *server) cancelMeeting(w http.ResponseWriter, r *http.Request) {
	m, err := s.Meetings.Cancel(r.Context(), s.org(r), s.actor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *server) briefMeeting(w http.ResponseWriter, r *http.Request) {
	var b langBody
	if r.ContentLength != 0 {
		if err := s.decode(w, r, &b); err != nil {
			s.fail(w, err)
			return
		}
	}
	id := chi.URLParam(r, "id")
	req, err := s.Meetings.Brief(r.Context(), s.org(r), s.actor(r), id, b.Lang)
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"request_id": req})
}

func (s *server) syncMeeting(w http.ResponseWriter, r *http.Request) {
	m, err := s.Meetings.SyncNow(r.Context(), s.org(r), s.actor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *server) meetingICS(w http.ResponseWriter, r *http.Request) {
	body, name, err := s.Meetings.ExportICS(r.Context(), s.org(r), chi.URLParam(r, "id"))
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(body))
}

// ---- rooms, settings, calendars ----

type roomView struct {
	meetings.Room
	// Current is the meeting holding the room right now (live, or scheduled and in its window).
	Current *roomMeeting `json:"current"`
	Next    *roomMeeting `json:"next"`
}

type roomMeeting struct {
	ID     string          `json:"id"`
	Title  string          `json:"title"`
	Status meetings.Status `json:"status"`
	Start  time.Time       `json:"start"`
	End    time.Time       `json:"end"`
}

// meetingRooms: GET /meetings/rooms: the rooms of the office and who has them now / next.
func (s *server) meetingRooms(w http.ResponseWriter, r *http.Request) {
	org := s.org(r)
	set, err := s.Meetings.EffectiveSettings(r.Context(), org)
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	now := time.Now().UTC()
	ms, err := s.Meetings.List(r.Context(), org, meetings.Filter{From: now.Add(-12 * time.Hour), To: now.Add(7 * 24 * time.Hour),
		Status: []meetings.Status{meetings.StatusScheduled, meetings.StatusLive}, Limit: 500})
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	out := make([]roomView, 0, len(set.Rooms))
	for _, room := range set.Rooms {
		v := roomView{Room: room}
		for _, m := range ms {
			if m.Room != room.ID {
				continue
			}
			rm := &roomMeeting{ID: m.ID, Title: m.Title, Status: m.Status, Start: m.Start, End: m.End}
			switch {
			case m.Status == meetings.StatusLive || (!m.Start.After(now) && m.End.After(now)):
				if v.Current == nil {
					v.Current = rm
				}
			case m.Start.After(now):
				if v.Next == nil || m.Start.Before(v.Next.Start) {
					v.Next = rm
				}
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getMeetingSettings(w http.ResponseWriter, r *http.Request) {
	set, err := s.Meetings.EffectiveSettings(r.Context(), s.org(r))
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, set)
}

func (s *server) putMeetingSettings(w http.ResponseWriter, r *http.Request) {
	var in meetings.Settings
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	set, err := s.Meetings.PutSettings(r.Context(), s.org(r), s.actor(r), in)
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, set)
}

func (s *server) meetingCalendars(w http.ResponseWriter, r *http.Request) {
	l, err := s.Meetings.CalendarConnections(r.Context(), s.org(r))
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

type calEventView struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	JoinURL   string    `json:"join_url,omitempty"`
	Attendees int       `json:"attendees"`
	AllDay    bool      `json:"all_day"`
}

// meetingCalendarEvents: GET /meetings/calendar-events?from=&to=: the events of the connected
// calendar, shown next to the meetings. Text is displayed, never fed to a model.
func (s *server) meetingCalendarEvents(w http.ResponseWriter, r *http.Request) {
	from, ok := meetingTime(w, r, "from")
	if !ok {
		return
	}
	to, ok := meetingTime(w, r, "to")
	if !ok {
		return
	}
	if from.IsZero() {
		from = time.Now().UTC().Truncate(24 * time.Hour)
	}
	if to.IsZero() {
		to = from.Add(7 * 24 * time.Hour)
	}
	evs, err := s.Meetings.CalendarEvents(r.Context(), s.org(r), from, to)
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	out := make([]calEventView, 0, len(evs))
	for _, e := range evs {
		out = append(out, calEventView{ID: e.ID, Title: e.Title, Start: e.Start, End: e.End, JoinURL: e.JoinURL, Attendees: len(e.Attendees), AllDay: e.AllDay})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) meetingFreeBusy(w http.ResponseWriter, r *http.Request) {
	var b struct {
		From          time.Time `json:"from"`
		To            time.Time `json:"to"`
		Emails        []string  `json:"emails"`
		DurationMin   int       `json:"duration_min"`
		WorkStartHour int       `json:"work_start_hour"`
		WorkEndHour   int       `json:"work_end_hour"`
		TZOffsetMin   int       `json:"tz_offset_min"`
	}
	if err := s.decode(w, r, &b); err != nil {
		s.fail(w, err)
		return
	}
	out, err := s.Meetings.FreeBusy(r.Context(), s.org(r), meetings.FreeBusyIn{From: b.From, To: b.To, Emails: b.Emails, DurationMin: b.DurationMin,
		WorkStartHour: b.WorkStartHour, WorkEndHour: b.WorkEndHour, TZOffsetMin: b.TZOffsetMin})
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- minutes ----

func (s *server) getMinutes(w http.ResponseWriter, r *http.Request) {
	m, err := s.Meetings.Minutes(r.Context(), s.org(r), chi.URLParam(r, "id"))
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *server) postMinutes(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Notes  string `json:"notes"`
		Source string `json:"source"`
		Lang   string `json:"lang"`
		// FilePath reads the notes from a drive file (text, markdown, docx, pdf ...) the person may read.
		FilePath string `json:"file_path"`
	}
	if err := s.decode(w, r, &b); err != nil {
		s.fail(w, err)
		return
	}
	if !s.has(r, auth.PermFilesWrite) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "minutes are saved in the drive: your role cannot write files", "code": "forbidden"})
		return
	}
	if b.FilePath != "" {
		if s.Drive == nil || !s.has(r, auth.PermFilesRead) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "cannot read drive files", "code": "forbidden"})
			return
		}
		p, err := drive.CleanPath(b.FilePath)
		if err != nil {
			s.driveFail(w, err)
			return
		}
		f, err := s.Drive.FileByPath(r.Context(), p)
		if err != nil {
			s.driveFail(w, err)
			return
		}
		txt, err := s.Drive.ReadText(r.Context(), f, 0, 0, meetings.MaxNotesChars)
		if err != nil {
			s.driveFail(w, err)
			return
		}
		b.Notes, b.Source = txt.Text, "upload"
	}
	m, err := s.Meetings.GenerateMinutes(r.Context(), s.org(r), s.actor(r), chi.URLParam(r, "id"), meetings.MinutesInput{Notes: b.Notes, Source: b.Source, Lang: b.Lang})
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *server) putMinutes(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Summary     *string                `json:"summary"`
		Decisions   *[]string              `json:"decisions"`
		ActionItems *[]meetings.ActionItem `json:"action_items"`
	}
	if err := s.decode(w, r, &b); err != nil {
		s.fail(w, err)
		return
	}
	m, err := s.Meetings.EditMinutes(r.Context(), s.org(r), s.actor(r), chi.URLParam(r, "id"), meetings.MinutesEdit{Summary: b.Summary, Decisions: b.Decisions, ActionItems: b.ActionItems})
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *server) minutesTasks(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Indexes []int  `json:"indexes"`
		Lang    string `json:"lang"`
	}
	if r.ContentLength != 0 {
		if err := s.decode(w, r, &b); err != nil {
			s.fail(w, err)
			return
		}
	}
	m, req, err := s.Meetings.CreateTasks(r.Context(), s.org(r), s.actor(r), chi.URLParam(r, "id"), b.Indexes, b.Lang)
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"minutes": m, "request_id": req})
}

// ---- ICS feeds ----

func (s *server) listMeetingFeeds(w http.ResponseWriter, r *http.Request) {
	fs, err := s.Meetings.ListFeeds(r.Context(), s.org(r), s.actor(r))
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fs)
}

// createMeetingFeed: POST /meetings/feeds {label} -> {feed, token, path}. The token is shown once; path is
// the public URL path to subscribe to (the UI prefixes the API origin).
func (s *server) createMeetingFeed(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Label string `json:"label"`
	}
	if r.ContentLength != 0 {
		if err := s.decode(w, r, &b); err != nil {
			s.fail(w, err)
			return
		}
	}
	token, f, err := s.Meetings.CreateFeed(r.Context(), s.org(r), s.actor(r), b.Label)
	if err != nil {
		s.meetingFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"feed": f, "token": token, "path": "/api/v1/calendar/feed/" + token + ".ics"})
}

func (s *server) revokeMeetingFeed(w http.ResponseWriter, r *http.Request) {
	if err := s.Meetings.RevokeFeed(r.Context(), s.org(r), s.actor(r), chi.URLParam(r, "fid")); err != nil {
		s.meetingFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// calendarFeed is the PUBLIC subscription endpoint (calendar apps cannot send a bearer token):
// GET /calendar/feed/<token>.ics. An unknown, revoked or malformed token is a plain 404.
func (s *server) calendarFeed(w http.ResponseWriter, r *http.Request) {
	if res, err := feedLimiter.Allow(r.Context(), "feed:"+remoteHost(r), feedLimit); err == nil && !res.Allowed {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	file := chi.URLParam(r, "file")
	token, ok := strings.CutSuffix(file, ".ics")
	if !ok {
		http.NotFound(w, r)
		return
	}
	body, err := s.Meetings.FeedICS(r.Context(), token)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_, _ = w.Write([]byte(body))
}
