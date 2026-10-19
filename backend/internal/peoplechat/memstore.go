package peoplechat

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore is the in-memory Store: used by the tests and by a server without a database (demo mode).
type MemoryStore struct {
	mu       sync.Mutex
	channels map[string]map[string]*Channel  // org -> id
	members  map[string]map[string][]Member  // org -> channel -> members
	messages map[string]map[string][]Message // org -> channel -> messages (ascending seq)
	people   map[string][]Person
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{channels: map[string]map[string]*Channel{}, members: map[string]map[string][]Member{}, messages: map[string]map[string][]Message{}, people: map[string][]Person{}}
}

// SetPeople defines the directory of an organization.
func (m *MemoryStore) SetPeople(org string, p []Person) {
	m.mu.Lock()
	m.people[org] = append([]Person(nil), p...)
	m.mu.Unlock()
}

func (m *MemoryStore) ensureOrg(org string) {
	if m.channels[org] == nil {
		m.channels[org], m.members[org], m.messages[org] = map[string]*Channel{}, map[string][]Member{}, map[string][]Message{}
	}
}

func (m *MemoryStore) byScope(org, kind, scope string) *Channel {
	for _, c := range m.channels[org] {
		if c.Kind == kind && c.ScopeKey == scope && scope != "" {
			return c
		}
	}
	return nil
}

func (m *MemoryStore) EnsureOrgChannel(_ context.Context, org, createdBy string) (Channel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureOrg(org)
	if c := m.byScope(org, KindOrg, "general"); c != nil {
		return *c, nil
	}
	c := &Channel{ID: uuid.NewString(), Kind: KindOrg, Name: "general", ScopeKey: "general", CreatedBy: createdBy, CreatedAt: time.Now().UTC()}
	m.channels[org][c.ID] = c
	return *c, nil
}

func (m *MemoryStore) CreateChannel(_ context.Context, org string, ch Channel, members []Member) (Channel, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureOrg(org)
	if ch.ScopeKey != "" && ch.Kind != KindGroup {
		if c := m.byScope(org, ch.Kind, ch.ScopeKey); c != nil {
			return *c, false, nil
		}
	}
	c := ch
	m.channels[org][c.ID] = &c
	m.members[org][c.ID] = append([]Member(nil), members...)
	return c, true, nil
}

func (m *MemoryStore) Channel(_ context.Context, org, id string) (Channel, []Member, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.channels[org][id]
	if c == nil {
		return Channel{}, nil, ErrNotFound
	}
	return *c, append([]Member(nil), m.members[org][id]...), nil
}

func (m *MemoryStore) ChannelsOf(_ context.Context, org, userID string, shared bool) ([]Channel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Channel
	for id, c := range m.channels[org] {
		_, in := isMember(m.members[org][id], MemberUser, userID)
		if in || c.Kind == KindOrg || (shared && (c.Kind == KindDepartment || c.Kind == KindProject)) {
			out = append(out, *c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *MemoryStore) Members(_ context.Context, org string, ids []string) (map[string][]Member, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string][]Member{}
	for _, id := range ids {
		out[id] = append([]Member(nil), m.members[org][id]...)
	}
	return out, nil
}

func (m *MemoryStore) AddMember(_ context.Context, org, channelID string, mem Member) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.channels[org][channelID] == nil {
		return ErrNotFound
	}
	if _, in := isMember(m.members[org][channelID], mem.Kind, mem.ID); in {
		return nil
	}
	if mem.Role == "" {
		mem.Role = "member"
	}
	m.members[org][channelID] = append(m.members[org][channelID], mem)
	return nil
}

func (m *MemoryStore) RemoveMember(_ context.Context, org, channelID, kind, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.members[org][channelID]
	out := cur[:0:0]
	for _, x := range cur {
		if !(x.Kind == kind && x.ID == id) {
			out = append(out, x)
		}
	}
	m.members[org][channelID] = out
	return nil
}

func (m *MemoryStore) Append(_ context.Context, org string, msg Message) (Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.channels[org][msg.ChannelID]
	if c == nil {
		return Message{}, ErrNotFound
	}
	c.LastSeq++
	msg.Seq = c.LastSeq
	t := msg.CreatedAt
	c.LastMessageAt = &t
	m.messages[org][c.ID] = append(m.messages[org][c.ID], msg)
	return msg, nil
}

func (m *MemoryStore) Messages(_ context.Context, org, channelID string, before int64, limit int) ([]Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	all := m.messages[org][channelID]
	end := len(all)
	if before > 0 {
		end = sort.Search(len(all), func(i int) bool { return all[i].Seq >= before })
	}
	start := end - limit
	if start < 0 {
		start = 0
	}
	return append([]Message(nil), all[start:end]...), nil
}

func (m *MemoryStore) MarkRead(_ context.Context, org, channelID, userID string, seq int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.channels[org][channelID]
	if c == nil {
		return ErrNotFound
	}
	ms := m.members[org][channelID]
	for i := range ms {
		if ms[i].Kind == MemberUser && ms[i].ID == userID {
			if seq > ms[i].LastReadSeq {
				ms[i].LastReadSeq = seq
			}
			return nil
		}
	}
	if c.Kind == KindOrg { // implicit membership of the all-hands channel
		m.members[org][channelID] = append(ms, Member{Kind: MemberUser, ID: userID, Role: "member", LastReadSeq: seq})
	}
	return nil
}

func (m *MemoryStore) MentionsUnread(_ context.Context, org, channelID, userID string, afterSeq int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, msg := range m.messages[org][channelID] {
		if msg.Seq <= afterSeq || (msg.AuthorKind == MemberUser && msg.AuthorID == userID) {
			continue
		}
		for _, mn := range msg.Mentions {
			if mn.Kind == MemberUser && mn.ID == userID {
				n++
				break
			}
		}
	}
	return n, nil
}

func (m *MemoryStore) Directory(_ context.Context, org string) ([]Person, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Person(nil), m.people[org]...), nil
}
