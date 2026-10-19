package meetings

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// Filter selects meetings.
type Filter struct {
	From, To time.Time // meetings overlapping [From, To); zero = open
	Status   []Status
	Room     string
	// Person, when set, keeps the meetings that person organizes or attends.
	Person string
	Limit  int
}

// Store is the persistence port. Every method is scoped to one organization.
type Store interface {
	CreateMeeting(ctx context.Context, m Meeting) error
	GetMeeting(ctx context.Context, org, id string) (Meeting, error)
	ListMeetings(ctx context.Context, org string, f Filter) ([]Meeting, error)
	// UpdateMeeting writes the meeting only if its stored status is `want` (ErrState otherwise):
	// two people pressing "start" or "cancel" at once cannot both win.
	UpdateMeeting(ctx context.Context, m Meeting, want Status) error

	PutMinutes(ctx context.Context, m Minutes) error
	GetMinutes(ctx context.Context, org, meetingID string) (Minutes, error)
	// MinutesExist reports, for the given meeting ids, which have minutes.
	MinutesExist(ctx context.Context, org string, ids []string) (map[string]bool, error)

	GetSettings(ctx context.Context, org string) (Settings, error)
	PutSettings(ctx context.Context, org string, s Settings, by string) error

	CreateFeed(ctx context.Context, f Feed) error
	ListFeeds(ctx context.Context, org, user string) ([]Feed, error)
	RevokeFeed(ctx context.Context, org, user, id string, at time.Time) error
	// FeedByHash returns an active feed.
	FeedByHash(ctx context.Context, org, hash string) (Feed, error)
}

// Matches reports whether a meeting passes the filter.
func (f Filter) Matches(m Meeting) bool {
	if !f.From.IsZero() && !m.End.After(f.From) {
		return false
	}
	if !f.To.IsZero() && !m.Start.Before(f.To) {
		return false
	}
	if f.Room != "" && m.Room != f.Room {
		return false
	}
	if len(f.Status) > 0 {
		ok := false
		for _, s := range f.Status {
			if s == m.Status {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	if f.Person != "" && !m.Involves(f.Person) {
		return false
	}
	return true
}

// Involves reports whether a person (id or e-mail) organizes or attends the meeting.
func (m Meeting) Involves(person string) bool {
	if person == "" {
		return false
	}
	if m.Organizer == person {
		return true
	}
	for _, a := range m.Attendees {
		if a.Kind != KindAgent && (a.ID == person || (a.Email != "" && strings.EqualFold(a.Email, person))) {
			return true
		}
	}
	return false
}

// MemStore is the in-memory Store (tests, demos without a database).
type MemStore struct {
	mu       sync.Mutex
	meetings map[string]Meeting // org|id
	minutes  map[string]Minutes
	settings map[string]Settings
	feeds    map[string]Feed // org|id
}

// NewMemStore returns an empty store.
func NewMemStore() *MemStore {
	return &MemStore{meetings: map[string]Meeting{}, minutes: map[string]Minutes{}, settings: map[string]Settings{}, feeds: map[string]Feed{}}
}

func key(org, id string) string { return org + "|" + id }

func clone(m Meeting) Meeting {
	m.Attendees = append([]Attendee(nil), m.Attendees...)
	m.Agenda = append([]AgendaItem(nil), m.Agenda...)
	return m
}

func (s *MemStore) CreateMeeting(_ context.Context, m Meeting) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.meetings[key(m.OrgID, m.ID)]; ok {
		return ErrInvalid
	}
	s.meetings[key(m.OrgID, m.ID)] = clone(m)
	return nil
}

func (s *MemStore) GetMeeting(_ context.Context, org, id string) (Meeting, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.meetings[key(org, id)]
	if !ok {
		return Meeting{}, ErrNotFound
	}
	return clone(m), nil
}

func (s *MemStore) ListMeetings(_ context.Context, org string, f Filter) ([]Meeting, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Meeting
	for _, m := range s.meetings {
		if m.OrgID == org && f.Matches(m) {
			out = append(out, clone(m))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		return out[i].ID < out[j].ID
	})
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func (s *MemStore) UpdateMeeting(_ context.Context, m Meeting, want Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.meetings[key(m.OrgID, m.ID)]
	if !ok {
		return ErrNotFound
	}
	if cur.Status != want {
		return ErrState
	}
	s.meetings[key(m.OrgID, m.ID)] = clone(m)
	return nil
}

func (s *MemStore) PutMinutes(_ context.Context, m Minutes) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.meetings[key(m.OrgID, m.MeetingID)]; !ok {
		return ErrNotFound
	}
	m.Decisions = append([]string(nil), m.Decisions...)
	m.ActionItems = append([]ActionItem(nil), m.ActionItems...)
	s.minutes[key(m.OrgID, m.MeetingID)] = m
	return nil
}

func (s *MemStore) GetMinutes(_ context.Context, org, meetingID string) (Minutes, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.minutes[key(org, meetingID)]
	if !ok {
		return Minutes{}, ErrNotFound
	}
	m.Decisions = append([]string(nil), m.Decisions...)
	m.ActionItems = append([]ActionItem(nil), m.ActionItems...)
	return m, nil
}

func (s *MemStore) MinutesExist(_ context.Context, org string, ids []string) (map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]bool{}
	for _, id := range ids {
		if _, ok := s.minutes[key(org, id)]; ok {
			out[id] = true
		}
	}
	return out, nil
}

func (s *MemStore) GetSettings(_ context.Context, org string) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings[org], nil
}

func (s *MemStore) PutSettings(_ context.Context, org string, st Settings, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings[org] = st
	return nil
}

func (s *MemStore) CreateFeed(_ context.Context, f Feed) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.feeds[key(f.OrgID, f.ID)] = f
	return nil
}

func (s *MemStore) ListFeeds(_ context.Context, org, user string) ([]Feed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Feed
	for _, f := range s.feeds {
		if f.OrgID == org && f.UserID == user && f.RevokedAt == nil {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *MemStore) RevokeFeed(_ context.Context, org, user, id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.feeds[key(org, id)]
	if !ok || f.UserID != user || f.RevokedAt != nil {
		return ErrNotFound
	}
	f.RevokedAt = &at
	s.feeds[key(org, id)] = f
	return nil
}

func (s *MemStore) FeedByHash(_ context.Context, org, hash string) (Feed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.feeds {
		if f.OrgID == org && f.TokenHash == hash && f.RevokedAt == nil {
			return f, nil
		}
	}
	return Feed{}, ErrNotFound
}
