package memory

import (
	"context"
	"slices"
	"strings"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Chat history for the in-memory store (application.ChatHistoryStore). The
// semantics are the same as the Postgres store's; one shared suite checks both.

type convMeta struct {
	archivedAt  *time.Time
	titleCustom bool
}

type chatHistState struct {
	meta  map[string]convMeta
	reads map[string]time.Time
}

var _ application.ChatHistoryStore = (*Store)(nil)

func (s *Store) sortedMessages() []domain.Message {
	out := slices.Clone(s.messages)
	slices.SortStableFunc(out, func(a, b domain.Message) int {
		if c := a.TS.Compare(b.TS); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

func (s *Store) readAt(key string) time.Time { return s.hist.reads[key] }

// convRow builds the list row of c from the messages (already sorted).
func (s *Store) convRow(c domain.Conversation, msgs []domain.Message) application.ChatConversation {
	kind, agent := application.ChatKind(c.ID)
	row := application.ChatConversation{ID: c.ID, Title: c.Title, Kind: kind, AgentID: agent, Participants: slices.Clone(c.Participants),
		RequestID: c.RequestID, UpdatedAt: c.LastMessageAt, LastMessageAt: c.LastMessageAt}
	if row.Participants == nil {
		row.Participants = []string{}
	}
	m := s.hist.meta[c.ID]
	row.Archived, row.ArchivedAt, row.TitleCustom = m.archivedAt != nil, m.archivedAt, m.titleCustom
	read := s.readAt("conv:" + c.ID)
	firstUser := ""
	for _, x := range msgs {
		if x.ConversationID != c.ID {
			continue
		}
		row.MessageCount++
		row.Preview, row.PreviewFrom = application.Preview(x.Text), x.From
		if firstUser == "" && x.From == "user" {
			firstUser = x.Text
		}
		if x.From != "user" && x.TS.After(read) {
			row.Unread++
		}
	}
	if strings.TrimSpace(row.Title) == "" {
		row.Title = application.Preview(firstUser)
		if r := []rune(row.Title); len(r) > 80 {
			row.Title = string(r[:80])
		}
	}
	return row
}

func (s *Store) ChatConversations(_ context.Context, _ string, q application.ChatConvQuery) ([]application.ChatConversation, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs := s.sortedMessages()
	var rows []application.ChatConversation
	for _, id := range s.convOrder {
		c := s.convs[id]
		row := s.convRow(c, msgs)
		switch q.Archived {
		case "":
			if row.Archived {
				continue
			}
		case "only":
			if !row.Archived {
				continue
			}
		}
		if q.Kind != "" && row.Kind != q.Kind {
			continue
		}
		if q.AgentID != "" && c.ID != domain.ChatAgentConv(q.AgentID) {
			ok := false
			for _, m := range msgs {
				if m.ConversationID == c.ID && (m.From == q.AgentID || m.To == q.AgentID) {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		if q.Q != "" && !application.SearchMatch(row.Title, q.Q) {
			ok := false
			for _, m := range msgs {
				if m.ConversationID == c.ID && application.SearchMatch(m.Text, q.Q) {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		rows = append(rows, row)
	}
	slices.SortStableFunc(rows, func(a, b application.ChatConversation) int {
		if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	more := false
	if q.Offset > len(rows) {
		rows = nil
	} else {
		rows = rows[q.Offset:]
	}
	if q.Limit > 0 && len(rows) > q.Limit {
		rows, more = rows[:q.Limit], true
	}
	if rows == nil {
		rows = []application.ChatConversation{}
	}
	return rows, more, nil
}

func (s *Store) ChatGetConversation(_ context.Context, _, id string) (application.ChatConversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.convs[id]
	if !ok {
		return application.ChatConversation{}, domain.ErrNotFound
	}
	return s.convRow(c, s.sortedMessages()), nil
}

func (s *Store) threadMessages(msgs []domain.Message, thread string) []domain.Message {
	agent := ""
	var turns map[string]bool
	if thread != application.ThreadGeneral {
		agent = thread
		turns = application.AgentTurns(msgs, agent)
	}
	var out []domain.Message
	for _, m := range msgs {
		if application.InThread(m, agent, turns) {
			out = append(out, m)
		}
	}
	return out
}

func (s *Store) ChatThreadMessages(_ context.Context, _ string, q application.ChatThreadQuery) ([]application.ChatMessage, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := s.sortedMessages()
	in := s.threadMessages(all, q.Thread)
	page, more, err := application.PageMessages(in, q.BeforeID, q.Limit)
	if err != nil {
		return nil, false, err
	}
	turns := application.TurnAgents(all)
	out := make([]application.ChatMessage, 0, len(page))
	for _, m := range page {
		out = append(out, application.WithAgentIDs(m, turns))
	}
	return out, more, nil
}

func (s *Store) ChatThreads(_ context.Context, _ string, agentIDs []string) ([]application.ChatThread, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := s.sortedMessages()
	mk := func(key, agent string) application.ChatThread {
		t := application.ChatThread{Key: key, AgentID: agent}
		read := s.readAt("thread:" + key)
		for _, m := range s.threadMessages(all, key) {
			t.MessageCount++
			t.Preview, t.PreviewFrom, t.LastMessageAt = application.Preview(m.Text), m.From, m.TS
			if m.From != "user" && m.TS.After(read) {
				t.Unread++
			}
		}
		return t
	}
	out := []application.ChatThread{mk(application.ThreadGeneral, "")}
	for _, id := range agentIDs {
		out = append(out, mk(id, id))
	}
	return out, nil
}

func (s *Store) ChatMarkRead(_ context.Context, _, key string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hist.reads == nil {
		s.hist.reads = map[string]time.Time{}
	}
	set := func(k string) {
		if at.After(s.hist.reads[k]) {
			s.hist.reads[k] = at
		}
	}
	set(key)
	// reading a thread reads the 1:1 chats it contains
	if t, ok := strings.CutPrefix(key, "thread:"); ok {
		if t == application.ThreadGeneral {
			set("conv:" + domain.ChatOffice)
			for id := range s.convs {
				if strings.HasPrefix(id, domain.ChatAgentPrefix) {
					set("conv:" + id)
				}
			}
		} else {
			set("conv:" + domain.ChatAgentConv(t))
		}
	}
	return nil
}

func (s *Store) ChatPatchConversation(_ context.Context, _, id string, p application.ChatConvPatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.convs[id]
	if !ok {
		return domain.ErrNotFound
	}
	if s.hist.meta == nil {
		s.hist.meta = map[string]convMeta{}
	}
	m := s.hist.meta[id]
	if p.Title != nil {
		c.Title, m.titleCustom = *p.Title, true
		s.convs[id] = c
	}
	if p.Archived != nil {
		if *p.Archived {
			if m.archivedAt == nil {
				now := time.Now().UTC()
				m.archivedAt = &now
			}
		} else {
			m.archivedAt = nil
		}
	}
	s.hist.meta[id] = m
	return nil
}

func (s *Store) ChatDeleteConversation(_ context.Context, _, id string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.convs[id]; !ok {
		return 0, domain.ErrNotFound
	}
	kept, n := s.messages[:0:0], 0
	for _, m := range s.messages {
		if m.ConversationID == id {
			n++
			continue
		}
		kept = append(kept, m)
	}
	s.messages = kept
	delete(s.convs, id)
	delete(s.hist.meta, id)
	delete(s.hist.reads, "conv:"+id)
	s.convOrder = slices.DeleteFunc(s.convOrder, func(x string) bool { return x == id })
	return n, nil
}
