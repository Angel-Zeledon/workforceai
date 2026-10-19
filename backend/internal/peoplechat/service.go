package peoplechat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/events"
)

// Store persists channels, members and messages. Every method is scoped to one organization.
type Store interface {
	// EnsureOrgChannel returns the all-hands channel, creating it on first use.
	EnsureOrgChannel(ctx context.Context, org, createdBy string) (Channel, error)
	// CreateChannel inserts the channel with its members; when a channel with the same (kind, scope_key) exists
	// (DMs, department, project) it returns that one with created=false.
	CreateChannel(ctx context.Context, org string, ch Channel, members []Member) (out Channel, created bool, err error)
	Channel(ctx context.Context, org, id string) (Channel, []Member, error)
	// ChannelsOf lists the channels the user belongs to; shared adds every department/project channel (admins).
	ChannelsOf(ctx context.Context, org, userID string, shared bool) ([]Channel, error)
	Members(ctx context.Context, org string, channelIDs []string) (map[string][]Member, error)
	AddMember(ctx context.Context, org, channelID string, m Member) error
	RemoveMember(ctx context.Context, org, channelID, kind, id string) error
	// Append stores the message with the next sequence of its channel and returns it.
	Append(ctx context.Context, org string, m Message) (Message, error)
	// Messages returns up to limit messages with seq < before (before 0 = the latest), oldest first.
	Messages(ctx context.Context, org, channelID string, before int64, limit int) ([]Message, error)
	// MarkRead moves the read marker forward (never back), creating the membership row of the all-hands channel.
	MarkRead(ctx context.Context, org, channelID, userID string, seq int64) error
	// MentionsUnread counts messages after afterSeq, written by others, that mention the user.
	MentionsUnread(ctx context.Context, org, channelID, userID string, afterSeq int64) (int64, error)
	Directory(ctx context.Context, org string) ([]Person, error)
}

// Service holds the rules; the HTTP layer only authenticates and maps errors.
type Service struct {
	Store Store
	Pub   events.Publisher
	// Agents lists the agents of an organization (membership checks, mention candidates, directory).
	Agents func(ctx context.Context, org string) ([]AgentRef, error)
	// OnAgentMention is the hook point for agents that are members and get mentioned: the integration with the
	// orchestrator (answering in the channel through PostAsAgent) plugs in here. Optional; called synchronously after
	// the message is stored, so implementations must not block.
	OnAgentMention func(ctx context.Context, org string, ch Channel, m Message, agentID string)
	Now            func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s *Service) publish(org, typ string, payload any, audience []string) {
	if s.Pub == nil {
		return
	}
	// an empty, non-nil audience is a real (empty) restriction: nobody receives it
	if audience != nil && len(audience) == 0 {
		return
	}
	_ = s.Pub.Publish(context.Background(), events.NewEvent(org, typ, payload, audience...))
}

func (s *Service) agents(ctx context.Context, org string) map[string]AgentRef {
	out := map[string]AgentRef{}
	if s.Agents == nil {
		return out
	}
	list, err := s.Agents(ctx, org)
	if err != nil {
		return out
	}
	for _, a := range list {
		out[a.ID] = a
	}
	return out
}

func (s *Service) load(ctx context.Context, org string, a Actor, id string) (Channel, []Member, error) {
	ch, ms, err := s.Store.Channel(ctx, org, id)
	if err != nil {
		return Channel{}, nil, err
	}
	if !CanRead(ch, ms, a) {
		// do not reveal that a private channel exists
		return Channel{}, nil, ErrNotFound
	}
	return ch, ms, nil
}

// Channels lists what the actor can open, each with unread and mention counts, most recently active first.
func (s *Service) Channels(ctx context.Context, org string, a Actor) ([]ChannelView, error) {
	if _, err := s.Store.EnsureOrgChannel(ctx, org, a.UserID); err != nil {
		return nil, err
	}
	chs, err := s.Store.ChannelsOf(ctx, org, a.UserID, a.isAdmin())
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(chs))
	for i, c := range chs {
		ids[i] = c.ID
	}
	members, err := s.Store.Members(ctx, org, ids)
	if err != nil {
		return nil, err
	}
	out := make([]ChannelView, 0, len(chs))
	for _, c := range chs {
		ms := members[c.ID]
		if !CanRead(c, ms, a) {
			continue
		}
		v := ChannelView{Channel: c, Members: ms}
		if me, in := isMember(ms, MemberUser, a.UserID); in {
			v.Unread = Unread(c.LastSeq, me.LastReadSeq)
			if v.Unread > 0 {
				v.MentionsUnread, _ = s.Store.MentionsUnread(ctx, org, c.ID, a.UserID, me.LastReadSeq)
			}
		} else if c.Kind == KindOrg {
			v.Unread = Unread(c.LastSeq, 0)
			v.MentionsUnread, _ = s.Store.MentionsUnread(ctx, org, c.ID, a.UserID, 0)
		}
		if c.LastSeq > 0 {
			if last, err := s.Store.Messages(ctx, org, c.ID, 0, 1); err == nil && len(last) == 1 {
				v.Preview = preview(last[0].Body)
			}
		}
		out = append(out, v)
	}
	sortViews(out)
	return out, nil
}

func preview(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 90 {
		return string(r[:90]) + "…"
	}
	return s
}

func sortViews(v []ChannelView) {
	// org channel first, then by last activity (newest first), then by name
	less := func(a, b ChannelView) bool {
		if (a.Kind == KindOrg) != (b.Kind == KindOrg) {
			return a.Kind == KindOrg
		}
		at, bt := time.Time{}, time.Time{}
		if a.LastMessageAt != nil {
			at = *a.LastMessageAt
		}
		if b.LastMessageAt != nil {
			bt = *b.LastMessageAt
		}
		if !at.Equal(bt) {
			return at.After(bt)
		}
		return a.Name < b.Name
	}
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && less(v[j], v[j-1]); j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

// CreateInput is the body of a channel creation. For a DM, Members holds only the other person.
type CreateInput struct {
	Kind     string
	Name     string
	ScopeKey string // department name or project id
	Members  []Member
}

// Create opens a channel. The creator becomes its owner. A DM between the same two people returns the existing one.
func (s *Service) Create(ctx context.Context, org string, a Actor, in CreateInput) (ChannelView, error) {
	if !CanCreate(in.Kind, a) {
		return ChannelView{}, ErrForbidden
	}
	name := strings.TrimSpace(in.Name)
	if len([]rune(name)) > MaxName {
		return ChannelView{}, fmt.Errorf("%w: name too long", ErrInvalid)
	}
	dir, err := s.Store.Directory(ctx, org)
	if err != nil {
		return ChannelView{}, err
	}
	people := map[string]Person{}
	for _, p := range dir {
		people[p.ID] = p
	}
	if _, ok := people[a.UserID]; !ok && len(people) > 0 {
		return ChannelView{}, ErrForbidden
	}
	agents := s.agents(ctx, org)
	members, err := s.validMembers(in.Members, people, agents, a.UserID)
	if err != nil {
		return ChannelView{}, err
	}
	ch := Channel{ID: uuid.NewString(), Kind: in.Kind, Name: name, CreatedBy: a.UserID, CreatedAt: s.now()}
	switch in.Kind {
	case KindDM:
		if len(members) != 1 || members[0].Kind != MemberUser || members[0].ID == a.UserID {
			return ChannelView{}, fmt.Errorf("%w: a DM needs exactly one other person", ErrInvalid)
		}
		ch.ScopeKey, ch.Name = DMKey(a.UserID, members[0].ID), ""
	case KindOrg:
		ch.ScopeKey = "general"
		if name == "" {
			ch.Name = "general"
		}
	case KindDepartment, KindProject:
		ch.ScopeKey = strings.TrimSpace(in.ScopeKey)
		if ch.ScopeKey == "" || name == "" {
			return ChannelView{}, fmt.Errorf("%w: scope and name are required", ErrInvalid)
		}
	case KindGroup:
		if name == "" {
			return ChannelView{}, fmt.Errorf("%w: name is required", ErrInvalid)
		}
	}
	all := append([]Member{{Kind: MemberUser, ID: a.UserID, Role: "owner"}}, members...)
	if len(all) > MaxMembers {
		return ChannelView{}, fmt.Errorf("%w: too many members", ErrInvalid)
	}
	out, created, err := s.Store.CreateChannel(ctx, org, ch, all)
	if err != nil {
		return ChannelView{}, err
	}
	_, ms, err := s.Store.Channel(ctx, org, out.ID)
	if err != nil {
		return ChannelView{}, err
	}
	v := ChannelView{Channel: out, Members: ms}
	if created {
		s.publish(org, events.EvPeopleChatChannel, map[string]any{"channel": v}, Audience(out, ms))
	}
	return v, nil
}

// validMembers checks and deduplicates the requested members: users must belong to the organization, agents must exist.
func (s *Service) validMembers(in []Member, people map[string]Person, agents map[string]AgentRef, self string) ([]Member, error) {
	seen := map[string]bool{MemberUser + ":" + self: true}
	var out []Member
	for _, m := range in {
		key := m.Kind + ":" + m.ID
		if seen[key] {
			continue
		}
		switch m.Kind {
		case MemberUser:
			if _, ok := people[m.ID]; !ok {
				return nil, fmt.Errorf("%w: unknown person", ErrInvalid)
			}
		case MemberAgent:
			if _, ok := agents[m.ID]; !ok {
				return nil, fmt.Errorf("%w: unknown agent", ErrInvalid)
			}
		default:
			return nil, fmt.Errorf("%w: unknown member kind", ErrInvalid)
		}
		seen[key] = true
		out = append(out, Member{Kind: m.Kind, ID: m.ID, Role: "member"})
	}
	return out, nil
}

// OpenDM returns (creating it when needed) the DM between the actor and another person.
func (s *Service) OpenDM(ctx context.Context, org string, a Actor, other string) (ChannelView, error) {
	return s.Create(ctx, org, a, CreateInput{Kind: KindDM, Members: []Member{{Kind: MemberUser, ID: other}}})
}

// Messages returns a page of history (oldest first) of a channel the actor can read.
func (s *Service) Messages(ctx context.Context, org string, a Actor, channelID string, before int64, limit int) ([]Message, error) {
	if _, _, err := s.load(ctx, org, a, channelID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > PageSize {
		limit = PageSize
	}
	return s.Store.Messages(ctx, org, channelID, before, limit)
}

// Post writes a message as a person. It resolves mentions among the channel's members (the whole directory for the
// all-hands channel), advances the author's read marker, notifies the channel's sockets and calls the agent hook.
func (s *Service) Post(ctx context.Context, org string, a Actor, channelID, body string) (Message, error) {
	ch, ms, err := s.load(ctx, org, a, channelID)
	if err != nil {
		return Message{}, err
	}
	if !CanPost(ch, ms, a) {
		return Message{}, ErrForbidden
	}
	text, err := CleanBody(body)
	if err != nil {
		return Message{}, err
	}
	return s.store(ctx, org, ch, ms, MemberUser, a.UserID, text)
}

// PostAsAgent lets an agent that is a member of the channel answer in it (the integration point of OnAgentMention).
func (s *Service) PostAsAgent(ctx context.Context, org, agentID, channelID, body string) (Message, error) {
	ch, ms, err := s.Store.Channel(ctx, org, channelID)
	if err != nil {
		return Message{}, err
	}
	if _, in := isMember(ms, MemberAgent, agentID); !in || ch.Archived {
		return Message{}, ErrForbidden
	}
	text, err := CleanBody(body)
	if err != nil {
		return Message{}, err
	}
	return s.store(ctx, org, ch, ms, MemberAgent, agentID, text)
}

func (s *Service) candidates(ctx context.Context, org string, ch Channel, ms []Member) []Candidate {
	var out []Candidate
	dir, _ := s.Store.Directory(ctx, org)
	names := map[string]string{}
	for _, p := range dir {
		names[p.ID] = p.Name
	}
	agents := s.agents(ctx, org)
	if ch.Kind == KindOrg {
		for _, p := range dir {
			out = append(out, Candidate{Kind: MemberUser, ID: p.ID, Name: p.Name})
		}
		for _, m := range ms { // agents that joined the all-hands channel
			if m.Kind == MemberAgent {
				out = append(out, Candidate{Kind: MemberAgent, ID: m.ID, Name: agents[m.ID].Name})
			}
		}
		return out
	}
	for _, m := range ms {
		if m.Kind == MemberUser {
			out = append(out, Candidate{Kind: MemberUser, ID: m.ID, Name: names[m.ID]})
		} else {
			out = append(out, Candidate{Kind: MemberAgent, ID: m.ID, Name: agents[m.ID].Name})
		}
	}
	return out
}

func (s *Service) store(ctx context.Context, org string, ch Channel, ms []Member, kind, id, text string) (Message, error) {
	mentions := ParseMentions(text, s.candidates(ctx, org, ch, ms))
	if mentions == nil {
		mentions = []Mention{}
	}
	m, err := s.Store.Append(ctx, org, Message{ID: uuid.NewString(), ChannelID: ch.ID, AuthorKind: kind, AuthorID: id, Body: text, Mentions: mentions, CreatedAt: s.now()})
	if err != nil {
		return Message{}, err
	}
	if kind == MemberUser {
		_ = s.Store.MarkRead(ctx, org, ch.ID, id, m.Seq) // writing implies having read the thread so far
	}
	s.publish(org, events.EvPeopleChatMessage, map[string]any{"channel_id": ch.ID, "channel_kind": ch.Kind, "message": m}, Audience(ch, ms))
	if s.OnAgentMention != nil {
		for _, mn := range mentions {
			if mn.Kind != MemberAgent || (kind == MemberAgent && mn.ID == id) {
				continue
			}
			if _, in := isMember(ms, MemberAgent, mn.ID); in {
				s.OnAgentMention(ctx, org, ch, m, mn.ID)
			}
		}
	}
	return m, nil
}

// MarkRead moves the actor's read marker (up to the channel's last sequence) and syncs their other tabs.
func (s *Service) MarkRead(ctx context.Context, org string, a Actor, channelID string, seq int64) error {
	ch, _, err := s.load(ctx, org, a, channelID)
	if err != nil {
		return err
	}
	if seq > ch.LastSeq || seq <= 0 {
		seq = ch.LastSeq
	}
	if err := s.Store.MarkRead(ctx, org, channelID, a.UserID, seq); err != nil {
		return err
	}
	s.publish(org, events.EvPeopleChatRead, map[string]any{"channel_id": channelID, "seq": seq}, []string{a.UserID})
	return nil
}

// AddMember adds a person or an agent to a group/department/project channel.
func (s *Service) AddMember(ctx context.Context, org string, a Actor, channelID string, m Member) error {
	ch, ms, err := s.load(ctx, org, a, channelID)
	if err != nil {
		return err
	}
	if !CanManage(ch, ms, a) {
		return ErrForbidden
	}
	if len(ms) >= MaxMembers {
		return fmt.Errorf("%w: too many members", ErrInvalid)
	}
	dir, err := s.Store.Directory(ctx, org)
	if err != nil {
		return err
	}
	people := map[string]Person{}
	for _, p := range dir {
		people[p.ID] = p
	}
	vm, err := s.validMembers([]Member{m}, people, s.agents(ctx, org), "")
	if err != nil || len(vm) == 0 {
		return fmt.Errorf("%w: cannot add this member", ErrInvalid)
	}
	if err := s.Store.AddMember(ctx, org, channelID, vm[0]); err != nil {
		return err
	}
	_, ms2, _ := s.Store.Channel(ctx, org, channelID)
	s.publish(org, events.EvPeopleChatChannel, map[string]any{"channel": ChannelView{Channel: ch, Members: ms2}}, Audience(ch, ms2))
	return nil
}

// RemoveMember removes a member; people may remove themselves from channels they can leave.
func (s *Service) RemoveMember(ctx context.Context, org string, a Actor, channelID, kind, id string) error {
	ch, ms, err := s.load(ctx, org, a, channelID)
	if err != nil {
		return err
	}
	self := kind == MemberUser && id == a.UserID
	if !(CanManage(ch, ms, a) || (self && CanLeave(ch))) {
		return ErrForbidden
	}
	if _, in := isMember(ms, kind, id); !in {
		return ErrNotFound
	}
	if err := s.Store.RemoveMember(ctx, org, channelID, kind, id); err != nil {
		return err
	}
	_, ms2, _ := s.Store.Channel(ctx, org, channelID)
	aud := Audience(ch, ms2)
	if kind == MemberUser { // the removed person must learn it too
		aud = append(aud, id)
	}
	s.publish(org, events.EvPeopleChatChannel, map[string]any{"channel": ChannelView{Channel: ch, Members: ms2}}, aud)
	return nil
}

// Directory lists the people and the agents of the organization (DM picker, mention autocomplete).
func (s *Service) Directory(ctx context.Context, org string, a Actor) (people []Person, agents []AgentRef, err error) {
	if !a.Role.Has(auth.PermMembersRead) {
		return nil, nil, ErrForbidden
	}
	people, err = s.Store.Directory(ctx, org)
	if err != nil {
		return nil, nil, err
	}
	for _, ag := range s.agents(ctx, org) {
		agents = append(agents, ag)
	}
	return people, agents, nil
}

// IsClientError reports whether err is one of the package's caller errors.
func IsClientError(err error) bool {
	return errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalid)
}
