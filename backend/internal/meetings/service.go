package meetings

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/calendar"
)

// Config wires the service. Only Store is required; every other port is optional and the
// features behind it answer ErrUnavailable (or degrade, for the summarizer) when it is nil.
type Config struct {
	Store      Store
	Calendars  Calendars
	Summarizer Summarizer
	Tasks      TaskMaker
	Files      FileSaver
	Agents     Agents
	// Audit records who did what (ids and counts only, never notes or minutes text).
	Audit func(ctx context.Context, org, actor, action, entity, id string, details map[string]any)
	// Emit tells the UI something changed.
	Emit func(ctx context.Context, org, typ string, payload map[string]any)
	Now  func() time.Time
	// JitsiBase is the platform default Jitsi server (the organization can override it).
	JitsiBase string
	// DefaultAgentID receives action items that have no agent owner (default "assistant").
	DefaultAgentID string
}

// Service implements the meeting use cases.
type Service struct{ Config }

// New builds the service.
func New(c Config) *Service {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Audit == nil {
		c.Audit = func(context.Context, string, string, string, string, string, map[string]any) {}
	}
	if c.Emit == nil {
		c.Emit = func(context.Context, string, string, map[string]any) {}
	}
	if c.DefaultAgentID == "" {
		c.DefaultAgentID = "assistant"
	}
	return &Service{c}
}

func newID() string { return "mt_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20] }

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// ---- settings ----

var reRoomID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,29}$`)

// EffectiveSettings returns the settings with every default filled in.
func (s *Service) EffectiveSettings(ctx context.Context, org string) (Settings, error) {
	st, err := s.Store.GetSettings(ctx, org)
	if err != nil {
		return st, err
	}
	if st.DefaultProvider == "" || !validProvider(st.DefaultProvider) {
		st.DefaultProvider = ProviderJitsi
	}
	if st.JitsiBaseURL == "" {
		st.JitsiBaseURL = s.JitsiBase
	}
	if b, err := NormalizeJitsiBase(st.JitsiBaseURL); err == nil {
		st.JitsiBaseURL = b
	} else {
		st.JitsiBaseURL = DefaultJitsiBase
	}
	if st.MinutesFolder == "" {
		st.MinutesFolder = DefaultMinutesFolder
	}
	if len(st.Rooms) == 0 {
		st.Rooms = DefaultRooms()
	}
	return st, nil
}

// PutSettings validates and stores the settings of an organization.
func (s *Service) PutSettings(ctx context.Context, org, actor string, in Settings) (Settings, error) {
	if in.DefaultProvider != "" && !validProvider(in.DefaultProvider) {
		return Settings{}, invalid("unknown default_provider")
	}
	if in.JitsiBaseURL != "" {
		b, err := NormalizeJitsiBase(in.JitsiBaseURL)
		if err != nil {
			return Settings{}, err
		}
		in.JitsiBaseURL = b
	}
	if in.MinutesFolder != "" {
		f := strings.TrimRight(strings.TrimSpace(in.MinutesFolder), "/")
		if !strings.HasPrefix(f, "/") || len(f) > 200 || strings.Contains(f, "..") || strings.ContainsAny(f, "\r\n\x00\\") {
			return Settings{}, invalid("minutes_folder must be an absolute drive folder")
		}
		in.MinutesFolder = f
	}
	if len(in.Rooms) > 30 {
		return Settings{}, invalid("at most 30 rooms")
	}
	seen := map[string]bool{}
	for i, r := range in.Rooms {
		r.ID = strings.TrimSpace(r.ID)
		r.Name = strings.TrimSpace(r.Name)
		if !reRoomID.MatchString(r.ID) || seen[r.ID] || len([]rune(r.Name)) > 60 || r.Capacity < 0 || r.Capacity > 500 {
			return Settings{}, invalid("invalid room %q", r.ID)
		}
		seen[r.ID] = true
		in.Rooms[i] = r
	}
	for _, id := range []string{in.CalendarConnectionID, in.FreeBusyConnectionID} {
		if id == "" {
			continue
		}
		ok := false
		if s.Calendars != nil {
			list, err := s.Calendars.List(ctx, org)
			if err != nil {
				return Settings{}, err
			}
			for _, c := range list {
				ok = ok || c.ConnectionID == id
			}
		}
		if !ok {
			return Settings{}, invalid("unknown calendar connection")
		}
	}
	if err := s.Store.PutSettings(ctx, org, in, actor); err != nil {
		return Settings{}, err
	}
	s.Audit(ctx, org, actor, "meetings.settings_updated", "org", org, map[string]any{"provider": in.DefaultProvider, "calendar_connection": in.CalendarConnectionID != "", "rooms": len(in.Rooms)})
	return s.EffectiveSettings(ctx, org)
}

// ---- validation helpers ----

func (s *Service) agentIndex(ctx context.Context, org string) (map[string]AgentInfo, error) {
	idx := map[string]AgentInfo{}
	if s.Agents == nil {
		return idx, nil
	}
	list, err := s.Agents.Agents(ctx, org)
	if err != nil {
		return nil, err
	}
	for _, a := range list {
		idx[a.ID] = a
	}
	return idx, nil
}

func cleanText(s string, max int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\x00", ""))
	r := []rune(s)
	if len(r) > max {
		s = string(r[:max])
	}
	return s
}

func oneLine(s string, max int) string {
	return cleanText(strings.Join(strings.Fields(s), " "), max)
}

func validEmail(e string) bool {
	if len(e) > 200 || strings.ContainsAny(e, " \r\n<>,;") {
		return false
	}
	a, err := mail.ParseAddress(e)
	return err == nil && a.Address == e
}

func (s *Service) normAttendees(ctx context.Context, org string, in []Attendee) ([]Attendee, error) {
	if len(in) > MaxAttendees {
		return nil, invalid("at most %d attendees", MaxAttendees)
	}
	agents, err := s.agentIndex(ctx, org)
	if err != nil {
		return nil, err
	}
	out := make([]Attendee, 0, len(in))
	seen := map[string]bool{}
	for _, a := range in {
		a.ID, a.Email = strings.TrimSpace(a.ID), strings.TrimSpace(a.Email)
		a.Name = oneLine(a.Name, 80)
		switch a.Kind {
		case KindAgent:
			info, ok := agents[a.ID]
			if s.Agents != nil && !ok {
				return nil, invalid("unknown agent %q", a.ID)
			}
			if a.ID == "" {
				return nil, invalid("agent id is required")
			}
			if ok {
				a.Name = info.Name
			}
			if a.Name == "" {
				a.Name = a.ID
			}
			a.Email = ""
		case KindPerson, KindExternal:
			if a.Email != "" && !validEmail(a.Email) {
				return nil, invalid("invalid e-mail %q", a.Email)
			}
			if a.Kind == KindExternal && a.Email == "" {
				return nil, invalid("a guest needs an e-mail")
			}
			if a.ID == "" && a.Email == "" && a.Name == "" {
				return nil, invalid("an attendee needs a name, id or e-mail")
			}
			if a.Name == "" {
				a.Name = a.Email
				if a.Name == "" {
					a.Name = a.ID
				}
			}
		default:
			return nil, invalid("attendee kind must be person, agent or external")
		}
		k := a.Kind + "|" + strings.ToLower(a.ID) + "|" + strings.ToLower(a.Email)
		if a.ID == "" {
			k = a.Kind + "||" + strings.ToLower(a.Email) + "|" + strings.ToLower(a.Name)
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, a)
	}
	return out, nil
}

func normAgenda(in []AgendaItem, att []Attendee) ([]AgendaItem, error) {
	if len(in) > MaxAgenda {
		return nil, invalid("at most %d agenda items", MaxAgenda)
	}
	ids := map[string]bool{}
	for _, a := range att {
		if a.ID != "" {
			ids[a.ID] = true
		}
	}
	out := make([]AgendaItem, 0, len(in))
	for i, it := range in {
		it.Title = oneLine(it.Title, 200)
		if it.Title == "" {
			return nil, invalid("agenda item %d has no title", i+1)
		}
		if it.Minutes < 0 || it.Minutes > 480 {
			return nil, invalid("agenda item minutes must be 0 to 480")
		}
		if it.Owner != "" && !ids[it.Owner] {
			return nil, invalid("agenda owner %q is not an attendee", it.Owner)
		}
		it.Notes = cleanText(it.Notes, 2000)
		it.ID = fmt.Sprintf("ag%d", i+1)
		out = append(out, it)
	}
	return out, nil
}

func (s *Service) checkRoom(ctx context.Context, org string, set Settings, room string, start, end time.Time, self string) error {
	if room == "" {
		return nil
	}
	ok := false
	for _, r := range set.Rooms {
		ok = ok || r.ID == room
	}
	if !ok {
		return invalid("unknown room %q", room)
	}
	others, err := s.Store.ListMeetings(ctx, org, Filter{From: start, To: end, Room: room})
	if err != nil {
		return err
	}
	for _, o := range others {
		if o.ID != self && o.Status.Occupies() {
			return ErrRoomBusy
		}
	}
	return nil
}

func checkTimes(start, end time.Time) error {
	if start.IsZero() || end.IsZero() || !end.After(start) {
		return invalid("end must be after start")
	}
	if end.Sub(start) > MaxDuration {
		return invalid("a meeting lasts at most 12 hours")
	}
	return nil
}

// conferenceFor says which provider link the calendar can create for a meeting provider.
func conferenceFor(meetingProvider, calProvider string) string {
	switch {
	case meetingProvider == ProviderMeet && calProvider == calendar.ProviderGoogle:
		return calendar.ConfMeet
	case meetingProvider == ProviderTeams && calProvider == calendar.ProviderMicrosoft:
		return calendar.ConfTeams
	}
	return calendar.ConfNone
}

func (s *Service) decorate(m Meeting, set Settings, hasMinutes bool) Meeting {
	m.HasMinutes = hasMinutes
	m.Embeddable = Embeddable(m.Provider, m.JoinURL, set.JitsiBaseURL)
	if m.Attendees == nil {
		m.Attendees = []Attendee{}
	}
	if m.Agenda == nil {
		m.Agenda = []AgendaItem{}
	}
	return m
}

func (s *Service) view(ctx context.Context, m Meeting) (Meeting, error) {
	set, err := s.EffectiveSettings(ctx, m.OrgID)
	if err != nil {
		return m, err
	}
	ex, err := s.Store.MinutesExist(ctx, m.OrgID, []string{m.ID})
	if err != nil {
		return m, err
	}
	return s.decorate(m, set, ex[m.ID]), nil
}

// ---- create / read / update ----

// CreateInput is the data of a new meeting.
type CreateInput struct {
	Title, Description, Room string
	Start, End               time.Time
	Provider, JoinURL        string
	Attendees                []Attendee
	Agenda                   []AgendaItem
	// SyncCalendar writes the meeting to the organization's calendar connection.
	SyncCalendar bool
}

// Create schedules a meeting.
func (s *Service) Create(ctx context.Context, org, actor string, in CreateInput) (Meeting, error) {
	title := oneLine(in.Title, 200)
	if title == "" {
		return Meeting{}, invalid("title is required")
	}
	if err := checkTimes(in.Start, in.End); err != nil {
		return Meeting{}, err
	}
	set, err := s.EffectiveSettings(ctx, org)
	if err != nil {
		return Meeting{}, err
	}
	if err := s.checkRoom(ctx, org, set, in.Room, in.Start, in.End, ""); err != nil {
		return Meeting{}, err
	}
	att, err := s.normAttendees(ctx, org, in.Attendees)
	if err != nil {
		return Meeting{}, err
	}
	agenda, err := normAgenda(in.Agenda, att)
	if err != nil {
		return Meeting{}, err
	}
	provider := in.Provider
	if provider == "" {
		provider = set.DefaultProvider
	}
	if !validProvider(provider) {
		return Meeting{}, invalid("unknown provider %q", provider)
	}
	var cal calendar.Client
	if in.SyncCalendar {
		cal, err = s.syncClient(ctx, org, set)
		if err != nil {
			return Meeting{}, err
		}
	}
	link, err := s.resolveLink(provider, in.JoinURL, title, set, cal)
	if err != nil {
		return Meeting{}, err
	}
	now := s.Now().UTC()
	m := Meeting{ID: newID(), OrgID: org, Title: title, Description: cleanText(in.Description, 8000), Room: in.Room, Status: StatusScheduled,
		Start: in.Start.UTC(), End: in.End.UTC(), Organizer: actor, Provider: provider, JoinURL: link, Attendees: att, Agenda: agenda,
		CreatedAt: now, UpdatedAt: now}
	if err := s.Store.CreateMeeting(ctx, m); err != nil {
		return Meeting{}, err
	}
	if cal != nil {
		s.syncCreate(ctx, &m, cal, set)
	}
	s.Audit(ctx, org, actor, "meeting.created", "meeting", m.ID, map[string]any{"provider": provider, "room": m.Room, "attendees": len(att), "agenda": len(agenda), "synced": m.External.EventID != ""})
	s.Emit(ctx, org, "meeting.created", map[string]any{"meeting_id": m.ID, "status": string(m.Status)})
	return s.view(ctx, m)
}

// resolveLink returns the join URL a meeting is created with ("" when the calendar will create it).
func (s *Service) resolveLink(provider, joinURL, title string, set Settings, cal calendar.Client) (string, error) {
	switch provider {
	case ProviderNone:
		return "", nil
	case ProviderJitsi:
		if strings.TrimSpace(joinURL) != "" {
			return ValidateLink(provider, joinURL, set.JitsiBaseURL)
		}
		return JitsiURL(set.JitsiBaseURL, title)
	case ProviderMeet, ProviderTeams:
		if strings.TrimSpace(joinURL) != "" {
			return ValidateLink(provider, joinURL, set.JitsiBaseURL)
		}
		if cal != nil && conferenceFor(provider, cal.Provider()) != calendar.ConfNone {
			return "", nil // created together with the calendar event
		}
		return "", invalid("join_url is required: paste the %s link or sync with the matching calendar", provider)
	default: // zoom, custom
		return ValidateLink(provider, joinURL, set.JitsiBaseURL)
	}
}

// Get returns one meeting.
func (s *Service) Get(ctx context.Context, org, id string) (Meeting, error) {
	m, err := s.Store.GetMeeting(ctx, org, id)
	if err != nil {
		return Meeting{}, err
	}
	return s.view(ctx, m)
}

// List returns meetings.
func (s *Service) List(ctx context.Context, org string, f Filter) ([]Meeting, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 200
	}
	ms, err := s.Store.ListMeetings(ctx, org, f)
	if err != nil {
		return nil, err
	}
	set, err := s.EffectiveSettings(ctx, org)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(ms))
	for i, m := range ms {
		ids[i] = m.ID
	}
	ex, err := s.Store.MinutesExist(ctx, org, ids)
	if err != nil {
		return nil, err
	}
	for i := range ms {
		ms[i] = s.decorate(ms[i], set, ex[ms[i].ID])
	}
	if ms == nil {
		ms = []Meeting{}
	}
	return ms, nil
}

// UpdateInput changes a scheduled meeting; nil fields stay as they are.
type UpdateInput struct {
	Title       *string
	Description *string
	Room        *string
	Start, End  *time.Time
	Provider    *string
	JoinURL     *string
	Attendees   *[]Attendee
	Agenda      *[]AgendaItem
}

// Update edits (or reschedules) a scheduled meeting.
func (s *Service) Update(ctx context.Context, org, actor, id string, in UpdateInput) (Meeting, error) {
	m, err := s.Store.GetMeeting(ctx, org, id)
	if err != nil {
		return Meeting{}, err
	}
	if !m.Status.Editable() {
		return Meeting{}, fmt.Errorf("%w: only a scheduled meeting can be edited", ErrState)
	}
	set, err := s.EffectiveSettings(ctx, org)
	if err != nil {
		return Meeting{}, err
	}
	if in.Title != nil {
		if m.Title = oneLine(*in.Title, 200); m.Title == "" {
			return Meeting{}, invalid("title is required")
		}
	}
	if in.Description != nil {
		m.Description = cleanText(*in.Description, 8000)
	}
	if in.Start != nil {
		d := m.End.Sub(m.Start)
		m.Start = in.Start.UTC()
		if in.End == nil {
			m.End = m.Start.Add(d) // moving the start keeps the duration
		}
	}
	if in.End != nil {
		m.End = in.End.UTC()
	}
	if err := checkTimes(m.Start, m.End); err != nil {
		return Meeting{}, err
	}
	if in.Room != nil {
		m.Room = *in.Room
	}
	if err := s.checkRoom(ctx, org, set, m.Room, m.Start, m.End, m.ID); err != nil {
		return Meeting{}, err
	}
	if in.Attendees != nil {
		if m.Attendees, err = s.normAttendees(ctx, org, *in.Attendees); err != nil {
			return Meeting{}, err
		}
	}
	agendaIn := m.Agenda
	if in.Agenda != nil {
		agendaIn = *in.Agenda
	}
	if m.Agenda, err = normAgenda(agendaIn, m.Attendees); err != nil {
		return Meeting{}, err
	}
	if in.Provider != nil || in.JoinURL != nil {
		provider, link := m.Provider, ""
		if in.Provider != nil {
			provider = *in.Provider
		}
		if in.JoinURL != nil {
			link = *in.JoinURL
		} else if provider == m.Provider {
			link = m.JoinURL
		}
		if !validProvider(provider) {
			return Meeting{}, invalid("unknown provider %q", provider)
		}
		if link, err = s.resolveLink(provider, link, m.Title, set, nil); err != nil {
			return Meeting{}, err
		}
		m.Provider, m.JoinURL = provider, link
	}
	m.UpdatedAt = s.Now().UTC()
	if m.External.EventID != "" {
		s.syncUpdate(ctx, &m)
	}
	if err := s.Store.UpdateMeeting(ctx, m, StatusScheduled); err != nil {
		return Meeting{}, err
	}
	s.Audit(ctx, org, actor, "meeting.updated", "meeting", m.ID, map[string]any{"attendees": len(m.Attendees), "agenda": len(m.Agenda)})
	s.Emit(ctx, org, "meeting.updated", map[string]any{"meeting_id": m.ID, "status": string(m.Status)})
	return s.view(ctx, m)
}

// ---- state transitions ----

func (s *Service) transition(ctx context.Context, org, actor, id string, a Action) (Meeting, error) {
	m, err := s.Store.GetMeeting(ctx, org, id)
	if err != nil {
		return Meeting{}, err
	}
	next, err := Next(m.Status, a)
	if err != nil {
		return Meeting{}, err
	}
	was := m.Status
	now := s.Now().UTC()
	m.Status, m.UpdatedAt = next, now
	switch a {
	case ActStart:
		m.StartedAt = &now
	case ActEnd:
		m.EndedAt = &now
	case ActCancel:
		m.CancelledAt = &now
	}
	if err := s.Store.UpdateMeeting(ctx, m, was); err != nil {
		return Meeting{}, err
	}
	s.Audit(ctx, org, actor, "meeting."+string(next), "meeting", m.ID, map[string]any{"from": string(was)})
	s.Emit(ctx, org, "meeting.updated", map[string]any{"meeting_id": m.ID, "status": string(m.Status)})
	return m, nil
}

// Start opens the meeting (scheduled -> live). With brief, the invited agents get their preparation
// tasks right away (an error there does not undo the start; the meeting reports it).
func (s *Service) Start(ctx context.Context, org, actor, id string, brief bool, lang string) (Meeting, error) {
	m, err := s.transition(ctx, org, actor, id, ActStart)
	if err != nil {
		return Meeting{}, err
	}
	if brief {
		if req, berr := s.Brief(ctx, org, actor, id, lang); berr == nil {
			m.External.BriefRequestID = req
		}
	}
	return s.view(ctx, m)
}

// End closes a live meeting (live -> ended).
func (s *Service) End(ctx context.Context, org, actor, id string) (Meeting, error) {
	m, err := s.transition(ctx, org, actor, id, ActEnd)
	if err != nil {
		return Meeting{}, err
	}
	return s.view(ctx, m)
}

// Cancel cancels a scheduled meeting and removes its calendar event.
func (s *Service) Cancel(ctx context.Context, org, actor, id string) (Meeting, error) {
	m, err := s.transition(ctx, org, actor, id, ActCancel)
	if err != nil {
		return Meeting{}, err
	}
	if m.External.EventID != "" {
		s.syncDelete(ctx, &m)
	}
	return s.view(ctx, m)
}

// ---- agents: invitation and agenda ----

func fmtAgenda(agenda []AgendaItem, mine string, lang string) string {
	var b strings.Builder
	for i, a := range agenda {
		fmt.Fprintf(&b, "%d. %s", i+1, a.Title)
		if a.Minutes > 0 {
			fmt.Fprintf(&b, " (%d min)", a.Minutes)
		}
		if a.Owner == mine && mine != "" {
			b.WriteString(map[string]string{"es": " <- a tu cargo", "en": " <- you own this", "pt": " <- a seu cargo"}[lang])
		}
		if a.Notes != "" {
			b.WriteString(": " + oneLine(a.Notes, 300))
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// Brief gives every invited agent a preparation task for the meeting: its agenda, the
// participants and the points it owns. It creates one request (the same path as /reunion
// and /assign), so budget, approvals and the Tool Gateway apply. Asking twice is refused.
func (s *Service) Brief(ctx context.Context, org, actor, id, lang string) (string, error) {
	if s.Tasks == nil {
		return "", ErrUnavailable
	}
	m, err := s.Store.GetMeeting(ctx, org, id)
	if err != nil {
		return "", err
	}
	if m.Status != StatusScheduled && m.Status != StatusLive {
		return "", fmt.Errorf("%w: a %s meeting cannot be briefed", ErrState, m.Status)
	}
	if m.External.BriefRequestID != "" {
		return "", fmt.Errorf("%w: the agents were already briefed", ErrState)
	}
	var agents []Attendee
	var people []string
	for _, a := range m.Attendees {
		if a.Kind == KindAgent {
			agents = append(agents, a)
		}
		people = append(people, a.Name)
	}
	if len(agents) == 0 {
		return "", fmt.Errorf("%w: no agent is invited", ErrNothingToDo)
	}
	l := NormLang(lang)
	tr := func(es, en, pt string) string { return map[string]string{"es": es, "en": en, "pt": pt}[l] }
	b := TaskBatch{Text: tr("Preparación de la reunión: ", "Meeting preparation: ", "Preparação da reunião: ") + m.Title,
		Objectives: []string{tr("Que cada agente invitado llegue preparado a: ", "Every invited agent arrives prepared for: ", "Que cada agente convidado chegue preparado para: ") + m.Title}}
	for i, a := range agents {
		desc := fmt.Sprintf(tr("Reunión «%s» el %s. Participantes: %s.\n\nAgenda:\n%s\n\nPrepara tu postura para tu área: datos, riesgos y propuestas, en un resumen de máximo 200 palabras que se pueda leer en la reunión. Los puntos marcados «a tu cargo» los presentas tú. No envíes nada fuera ni cambies archivos; solo prepara el resumen.",
			"Meeting “%s” on %s. Participants: %s.\n\nAgenda:\n%s\n\nPrepare your position for your area: facts, risks and proposals, as a summary of at most 200 words that can be read in the meeting. The points marked “you own this” are yours to present. Send nothing outside and change no files; only prepare the summary.",
			"Reunião «%s» em %s. Participantes: %s.\n\nPauta:\n%s\n\nPrepare sua posição para sua área: dados, riscos e propostas, em um resumo de no máximo 200 palavras que possa ser lido na reunião. Os pontos marcados «a seu cargo» são seus. Não envie nada para fora nem altere arquivos; apenas prepare o resumo."),
			m.Title, m.Start.UTC().Format("2006-01-02 15:04 UTC"), strings.Join(people, ", "), orNone(fmtAgenda(m.Agenda, a.ID, l), tr("(sin agenda definida)", "(no agenda set)", "(sem pauta definida)")))
		b.Tasks = append(b.Tasks, TaskSpec{Key: fmt.Sprintf("p%d", i+1), Title: tr("Preparar la reunión: ", "Prepare for the meeting: ", "Preparar a reunião: ") + oneLine(m.Title, 60), Description: desc, AgentID: a.ID})
	}
	req, err := s.Tasks.CreateTasks(ctx, org, b)
	if err != nil {
		return "", err
	}
	m.External.BriefRequestID, m.UpdatedAt = req, s.Now().UTC()
	if err := s.Store.UpdateMeeting(ctx, m, m.Status); err != nil {
		return req, err
	}
	s.Audit(ctx, org, actor, "meeting.briefed", "meeting", m.ID, map[string]any{"agents": len(agents), "request_id": req})
	s.Emit(ctx, org, "meeting.updated", map[string]any{"meeting_id": m.ID, "status": string(m.Status)})
	return req, nil
}

func orNone(s, none string) string {
	if strings.TrimSpace(s) == "" {
		return none
	}
	return s
}

// ---- calendar sync ----

func (s *Service) syncClient(ctx context.Context, org string, set Settings) (calendar.Client, error) {
	if s.Calendars == nil || set.CalendarConnectionID == "" {
		return nil, fmt.Errorf("%w: no calendar connection is selected for meetings", ErrUnavailable)
	}
	c, err := s.Calendars.Client(ctx, org, set.CalendarConnectionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return c, nil
}

func syncCode(err error) string {
	switch {
	case errors.Is(err, calendar.ErrReauth):
		return "reauth_required"
	case errors.Is(err, calendar.ErrInvalid):
		return "invalid"
	case errors.Is(err, calendar.ErrNotConfigured):
		return "not_configured"
	case errors.Is(err, calendar.ErrReadOnly):
		return "read_only"
	case errors.Is(err, calendar.ErrBlocked):
		return "blocked"
	}
	return "provider_error"
}

func calInput(m Meeting, conference string) calendar.Input {
	var emails []string
	var names []string
	for _, a := range m.Attendees {
		if a.Email != "" {
			emails = append(emails, a.Email)
		}
		names = append(names, a.Name)
	}
	desc := m.Description
	if len(m.Agenda) > 0 {
		desc = strings.TrimSpace(desc + "\n\n" + fmtAgenda(m.Agenda, "", "en"))
	}
	if len(names) > 0 {
		desc = strings.TrimSpace(desc + "\n\nParticipants: " + strings.Join(names, ", "))
	}
	return calendar.Input{Title: m.Title, Description: desc, Location: m.Room, Start: m.Start, End: m.End, Attendees: emails,
		Conference: conference, ExistingJoinURL: m.JoinURL}
}

// syncCreate writes the event. A failure never fails the meeting: it is recorded as sync_error.
func (s *Service) syncCreate(ctx context.Context, m *Meeting, cal calendar.Client, set Settings) {
	conf := ""
	if m.JoinURL == "" {
		conf = conferenceFor(m.Provider, cal.Provider())
	}
	ev, err := cal.Create(ctx, calInput(*m, conf))
	if err != nil {
		m.External = External{CalendarProvider: cal.Provider(), ConnectionID: set.CalendarConnectionID, SyncError: syncCode(err)}
		s.Audit(ctx, m.OrgID, m.Organizer, "meeting.sync_failed", "meeting", m.ID, map[string]any{"code": m.External.SyncError, "provider": cal.Provider()})
	} else {
		m.External = External{CalendarProvider: cal.Provider(), ConnectionID: set.CalendarConnectionID, EventID: ev.ID}
		if m.JoinURL == "" {
			m.JoinURL = ev.JoinURL
		}
	}
	m.UpdatedAt = s.Now().UTC()
	_ = s.Store.UpdateMeeting(ctx, *m, m.Status)
}

func (s *Service) eventClient(ctx context.Context, m *Meeting) (calendar.Client, bool) {
	if s.Calendars == nil || m.External.ConnectionID == "" {
		return nil, false
	}
	c, err := s.Calendars.Client(ctx, m.OrgID, m.External.ConnectionID)
	if err != nil {
		m.External.SyncError = syncCode(err)
		return nil, false
	}
	return c, true
}

func (s *Service) syncUpdate(ctx context.Context, m *Meeting) {
	cal, ok := s.eventClient(ctx, m)
	if !ok {
		return
	}
	if _, err := cal.Update(ctx, m.External.EventID, calInput(*m, calendar.ConfNone)); err != nil {
		m.External.SyncError = syncCode(err)
		return
	}
	m.External.SyncError = ""
}

func (s *Service) syncDelete(ctx context.Context, m *Meeting) {
	cal, ok := s.eventClient(ctx, m)
	if ok {
		if err := cal.Delete(ctx, m.External.EventID); err != nil {
			m.External.SyncError = syncCode(err)
		} else {
			m.External.EventID, m.External.SyncError = "", ""
		}
	}
	_ = s.Store.UpdateMeeting(ctx, *m, m.Status)
}

// SyncNow writes (or rewrites) an existing scheduled meeting to the calendar.
func (s *Service) SyncNow(ctx context.Context, org, actor, id string) (Meeting, error) {
	m, err := s.Store.GetMeeting(ctx, org, id)
	if err != nil {
		return Meeting{}, err
	}
	if m.Status.Final() {
		return Meeting{}, fmt.Errorf("%w: a %s meeting is not synced", ErrState, m.Status)
	}
	set, err := s.EffectiveSettings(ctx, org)
	if err != nil {
		return Meeting{}, err
	}
	if m.External.EventID != "" {
		s.syncUpdate(ctx, &m)
		_ = s.Store.UpdateMeeting(ctx, m, m.Status)
		return s.view(ctx, m)
	}
	cal, err := s.syncClient(ctx, org, set)
	if err != nil {
		return Meeting{}, err
	}
	s.syncCreate(ctx, &m, cal, set)
	s.Audit(ctx, org, actor, "meeting.synced", "meeting", m.ID, map[string]any{"ok": m.External.EventID != ""})
	return s.view(ctx, m)
}

// CalendarConnections lists the calendar connections meetings can use.
func (s *Service) CalendarConnections(ctx context.Context, org string) ([]CalendarInfo, error) {
	if s.Calendars == nil {
		return []CalendarInfo{}, nil
	}
	l, err := s.Calendars.List(ctx, org)
	if l == nil {
		l = []CalendarInfo{}
	}
	return l, err
}

// CalendarEvents reads the events of the meeting calendar (shown dimmed next to the meetings).
func (s *Service) CalendarEvents(ctx context.Context, org string, from, to time.Time) ([]calendar.Event, error) {
	set, err := s.EffectiveSettings(ctx, org)
	if err != nil {
		return nil, err
	}
	id := set.CalendarConnectionID
	if id == "" {
		id = set.FreeBusyConnectionID
	}
	if s.Calendars == nil || id == "" {
		return nil, ErrUnavailable
	}
	c, err := s.Calendars.Client(ctx, org, id)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	evs, err := c.Events(ctx, from, to)
	if evs == nil {
		evs = []calendar.Event{}
	}
	return evs, err
}

// ---- free / busy ----

// FreeBusyIn asks when people are free.
type FreeBusyIn struct {
	From, To time.Time
	// Emails are the people to check; the connected account ("me") is always included.
	Emails      []string
	DurationMin int
	// WorkStartHour/WorkEndHour bound the suggested slots in the caller's time zone (TZOffsetMin
	// minutes east of UTC). Defaults: 9 to 18, UTC. Weekends are skipped.
	WorkStartHour, WorkEndHour, TZOffsetMin int
}

// FreeBusyOut is the answer.
type FreeBusyOut struct {
	Busy     map[string][]calendar.Busy `json:"busy"`
	Slots    []calendar.Slot            `json:"slots"`
	Provider string                     `json:"provider"`
}

// FreeBusy returns the busy blocks of the people and the slots where everybody is free.
func (s *Service) FreeBusy(ctx context.Context, org string, in FreeBusyIn) (FreeBusyOut, error) {
	set, err := s.EffectiveSettings(ctx, org)
	if err != nil {
		return FreeBusyOut{}, err
	}
	id := set.FreeBusyConnectionID
	if id == "" {
		id = set.CalendarConnectionID
	}
	if s.Calendars == nil || id == "" {
		return FreeBusyOut{}, ErrUnavailable
	}
	if !in.To.After(in.From) || in.To.Sub(in.From) > 31*24*time.Hour {
		return FreeBusyOut{}, invalid("choose a window of at most 31 days")
	}
	who := []string{"me"}
	seen := map[string]bool{"me": true}
	for _, e := range in.Emails {
		e = strings.TrimSpace(e)
		if e == "" || seen[strings.ToLower(e)] {
			continue
		}
		if !validEmail(e) {
			return FreeBusyOut{}, invalid("invalid e-mail %q", e)
		}
		seen[strings.ToLower(e)] = true
		who = append(who, e)
	}
	if len(who) > 21 {
		return FreeBusyOut{}, invalid("at most 20 people")
	}
	c, err := s.Calendars.Client(ctx, org, id)
	if err != nil {
		return FreeBusyOut{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	busy, err := c.FreeBusy(ctx, in.From, in.To, who)
	if err != nil {
		return FreeBusyOut{}, err
	}
	dur := time.Duration(in.DurationMin) * time.Minute
	if dur <= 0 {
		dur = 30 * time.Minute
	}
	ws, we := in.WorkStartHour, in.WorkEndHour
	if we <= ws || we > 24 || ws < 0 {
		ws, we = 9, 18
	}
	off := time.Duration(in.TZOffsetMin) * time.Minute
	var slots []calendar.Slot
	for _, sl := range calendar.FreeSlots(busy, in.From, in.To, dur, 30*time.Minute) {
		ls, le := sl.Start.Add(off), sl.End.Add(off)
		if wd := ls.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		sm, em := ls.Hour()*60+ls.Minute(), le.Hour()*60+le.Minute()
		if le.YearDay() != ls.YearDay() || sm < ws*60 || em > we*60 {
			continue
		}
		slots = append(slots, sl)
		if len(slots) >= 12 {
			break
		}
	}
	return FreeBusyOut{Busy: busy, Slots: slots, Provider: c.Provider()}, nil
}
