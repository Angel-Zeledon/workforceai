package application

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Chat history (docs/architecture/chat-history.md): the user comes back to the
// conversations with the office ("General") and with each agent, and the two
// views go hand in hand. There is ONE copy of every message: the General
// stream is every message of the office channel plus every 1:1 chat, and the
// thread of an agent is a VIEW over the same rows (its own 1:1 chat, every
// message it sent or received, and the user messages and system notices of the
// turns in which it took part). Nothing is duplicated, so both views always agree.

// ThreadGeneral is the key of the office-wide stream. Any other thread key is an agent id.
const ThreadGeneral = "general"

// Conversation kinds of the history list.
const (
	ConvKindOffice  = "office"
	ConvKindAgent   = "agent"
	ConvKindRequest = "request"
)

const (
	maxHistoryLimit     = 200
	defaultHistoryLimit = 500
)

// ChatMessage is a message of a thread: the stored message plus the agents it
// belongs to (the badge in the General view). For a user message these are the
// agents that took part in its turn, i.e. where it was routed.
type ChatMessage struct {
	domain.Message
	AgentIDs []string `json:"agent_ids"`
}

// ChatConversation is one row of the history list.
type ChatConversation struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Kind         string    `json:"kind"`
	AgentID      string    `json:"agent_id,omitempty"`
	Participants []string  `json:"participants"`
	RequestID    *string   `json:"request_id"`
	ProjectID    *string   `json:"project_id"`
	Preview      string    `json:"preview"`
	PreviewFrom  string    `json:"preview_from,omitempty"`
	MessageCount int       `json:"message_count"`
	Unread       int       `json:"unread"`
	Archived     bool      `json:"archived"`
	TitleCustom  bool      `json:"title_custom"`
	UpdatedAt    time.Time `json:"updated_at"`
	// LastMessageAt equals UpdatedAt (the field GET /conversations always had).
	LastMessageAt time.Time  `json:"last_message_at"`
	ArchivedAt    *time.Time `json:"archived_at,omitempty"`
	// SharedLegacy (E0): the row includes the 1:1 history written before private threads existed (shared
	// with the whole organization, read-only).
	SharedLegacy bool `json:"shared_legacy,omitempty"`
	// OwnerID is the person of a private thread ("" = shared). Internal: the API shows public ids only.
	OwnerID string `json:"-"`
}

// ChatThread is one entry of the left list: General or one agent.
type ChatThread struct {
	Key           string    `json:"key"`
	AgentID       string    `json:"agent_id,omitempty"`
	Preview       string    `json:"preview"`
	PreviewFrom   string    `json:"preview_from,omitempty"`
	LastMessageAt time.Time `json:"last_message_at"`
	MessageCount  int       `json:"message_count"`
	Unread        int       `json:"unread"`
}

// ChatConvQuery filters the history list. Archived: "" (hide archived), "only", "all".
type ChatConvQuery struct {
	// Viewer (E0) is the person reading: they see the shared conversations and their own private threads,
	// never another person's. Set by the service from the context, never by the client.
	Viewer   string
	AgentID  string
	Q        string
	Kind     string
	Archived string
	Limit    int // 0 = all (capped)
	Offset   int
}

// ChatThreadQuery pages a thread: the Limit messages older than BeforeID ("" = newest).
type ChatThreadQuery struct {
	Viewer   string // the person reading (E0, see ChatConvQuery.Viewer)
	Thread   string // ThreadGeneral or an agent id
	BeforeID string
	Limit    int
}

// ChatConvPatch changes a conversation; nil fields stay as they are.
type ChatConvPatch struct {
	Title    *string
	Archived *bool
}

// ChatHistoryStore is the storage behind the history. Postgres and the memory
// store implement it with the same semantics (tested against one suite).
//
// E0: every read takes the viewer (the person reading). Messages and conversations whose owner is another
// person are invisible to them; read markers and unread counts are per viewer (a marker written before
// per-person markers existed, user ”, still counts as a baseline). Ids given to the store are STORED ids;
// rows come back with stored ids too (the service turns them into public ids).
type ChatHistoryStore interface {
	ChatConversations(ctx context.Context, org string, q ChatConvQuery) ([]ChatConversation, bool, error)
	// ChatGetConversation returns a conversation as viewer sees it (not found when it belongs to another person).
	ChatGetConversation(ctx context.Context, org, viewer, id string) (ChatConversation, error)
	ChatThreadMessages(ctx context.Context, org string, q ChatThreadQuery) ([]ChatMessage, bool, error)
	// ChatThreads returns General first, then one entry per given agent id (in that order), as viewer sees them.
	ChatThreads(ctx context.Context, org, viewer string, agentIDs []string) ([]ChatThread, error)
	// ChatMarkRead stores viewer's read marker of a thread ("thread:<key>") or a conversation ("conv:<stored id>").
	ChatMarkRead(ctx context.Context, org, viewer, key string, at time.Time) error
	ChatPatchConversation(ctx context.Context, org, id string, p ChatConvPatch) error
	// ChatDeleteConversation removes a conversation and its messages; it returns how many messages.
	ChatDeleteConversation(ctx context.Context, org, id string) (int, error)
}

// ChatHistory is the service behind GET /conversations (enriched) and /chats/*.
type ChatHistory struct {
	Store ChatHistoryStore
	Core  Store
	Rec   *Recorder
	Cfg   Config
	// ProjectOf resolves the project launched by a request ("" when none). Optional.
	ProjectOf func(ctx context.Context, org, requestID string) string
}

func (h *ChatHistory) org(ctx context.Context) string { return OrgFrom(ctx, h.Cfg.OrgID) }

// Conversations lists the history (title, preview, unread...). It reports whether more rows exist.
func (h *ChatHistory) Conversations(ctx context.Context, q ChatConvQuery) ([]ChatConversation, bool, error) {
	q.Q = strings.TrimSpace(q.Q)
	switch q.Archived {
	case "", "only", "all":
	default:
		return nil, false, fmt.Errorf("%w: archived must be only or all", domain.ErrInvalid)
	}
	switch q.Kind {
	case "", ConvKindOffice, ConvKindAgent, ConvKindRequest:
	default:
		return nil, false, fmt.Errorf("%w: kind must be office, agent or request", domain.ErrInvalid)
	}
	if q.Limit <= 0 || q.Limit > defaultHistoryLimit {
		q.Limit = defaultHistoryLimit
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	if r := []rune(q.Q); len(r) > 100 {
		q.Q = string(r[:100])
	}
	org := h.org(ctx)
	q.Viewer = PersonFrom(ctx)
	rows, more, err := h.Store.ChatConversations(ctx, org, q)
	if err != nil {
		return nil, false, err
	}
	rows = publicRows(q.Viewer, rows)
	rows = slices.DeleteFunc(rows, func(c ChatConversation) bool { return !convVisible(ctx, c) }) // D3: private work
	h.link(ctx, org, rows)
	return rows, more, nil
}

// publicRows turns stored rows into what the viewer sees: public ids, and one row per agent chat (their
// private thread and the earlier shared history are the same chat for them: counts add up, the newest wins).
func publicRows(viewer string, rows []ChatConversation) []ChatConversation {
	out := make([]ChatConversation, 0, len(rows))
	at := map[string]int{}
	for _, r := range rows {
		r = publicRow(viewer, r)
		i, dup := at[r.ID]
		if !dup {
			at[r.ID] = len(out)
			out = append(out, r)
			continue
		}
		c := &out[i]
		c.MessageCount += r.MessageCount
		c.Unread += r.Unread
		c.SharedLegacy = c.SharedLegacy || r.SharedLegacy
		if r.UpdatedAt.After(c.UpdatedAt) {
			c.Preview, c.PreviewFrom, c.UpdatedAt, c.LastMessageAt = r.Preview, r.PreviewFrom, r.UpdatedAt, r.LastMessageAt
		}
		if c.OwnerID == "" && r.OwnerID != "" { // the private row owns title and archive state
			c.Title, c.TitleCustom, c.Archived, c.ArchivedAt, c.OwnerID = r.Title, r.TitleCustom, r.Archived, r.ArchivedAt, r.OwnerID
		}
	}
	return out
}

func publicRow(viewer string, r ChatConversation) ChatConversation {
	pub := domain.PublicChatConv(r.ID)
	if viewer != "" && r.OwnerID == "" && pub == r.ID && strings.HasPrefix(r.ID, domain.ChatAgentPrefix) {
		r.SharedLegacy = true
	}
	r.ID = pub
	r.Kind, r.AgentID = ChatKind(pub)
	return r
}

func (h *ChatHistory) link(ctx context.Context, org string, rows []ChatConversation) {
	if h.ProjectOf == nil {
		return
	}
	for i := range rows {
		if rows[i].RequestID == nil || *rows[i].RequestID == "" {
			continue
		}
		if p := h.ProjectOf(ctx, org, *rows[i].RequestID); p != "" {
			rows[i].ProjectID = &p
		}
	}
}

// Threads returns General and one thread per agent (the left list).
func (h *ChatHistory) Threads(ctx context.Context) ([]ChatThread, error) {
	org := h.org(ctx)
	agents, err := h.Core.ListAgents(ctx, org)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(agents))
	for _, a := range agents {
		if ChatVisible(ctx, a.ID) { // D3: no thread with an agent the person neither uses nor sees the work of
			ids = append(ids, a.ID)
		}
	}
	return h.Store.ChatThreads(ctx, org, PersonFrom(ctx), ids)
}

func (h *ChatHistory) checkThread(ctx context.Context, key string) error {
	if key == ThreadGeneral {
		return nil
	}
	if !ChatVisible(ctx, key) { // D3: answered like an agent that does not exist
		return fmt.Errorf("%w: agent %s", domain.ErrNotFound, key)
	}
	_, err := h.Core.GetAgent(ctx, h.org(ctx), key)
	return err
}

// Thread pages the messages of General or of one agent, oldest first.
func (h *ChatHistory) Thread(ctx context.Context, q ChatThreadQuery) ([]ChatMessage, bool, error) {
	if err := h.checkThread(ctx, q.Thread); err != nil {
		return nil, false, err
	}
	if q.Limit <= 0 {
		q.Limit = 50
	}
	q.Limit = min(q.Limit, maxHistoryLimit)
	q.Viewer = PersonFrom(ctx)
	msgs, more, err := h.Store.ChatThreadMessages(ctx, h.org(ctx), q)
	if err != nil {
		return nil, false, err
	}
	plain := make([]domain.Message, len(msgs))
	for i := range msgs {
		plain[i] = msgs[i].Message
	}
	plain = PublicMessages(ctx, plain)
	for i := range msgs {
		msgs[i].Message = plain[i]
	}
	if q.Thread != ThreadGeneral && !WorkVisible(ctx, q.Thread) {
		// D3: someone who uses a private office's agent without being in it sees their own thread only, not the
		// history the organization shared with the agent before private threads existed
		msgs = slices.DeleteFunc(msgs, func(m ChatMessage) bool { return m.SharedLegacy })
	}
	return msgs, more, nil
}

// MarkThreadRead says the person has seen everything in a thread (their own marker).
func (h *ChatHistory) MarkThreadRead(ctx context.Context, key string) error {
	if err := h.checkThread(ctx, key); err != nil {
		return err
	}
	return h.Store.ChatMarkRead(ctx, h.org(ctx), PersonFrom(ctx), "thread:"+key, time.Now().UTC())
}

// storedIDs are the stored conversations behind a public id, as the viewer sees them: their private thread
// and the earlier shared history for an agent chat, the id itself otherwise. Only the existing ones.
func (h *ChatHistory) storedIDs(ctx context.Context, id string) ([]string, error) {
	stored, err := ResolveChatConv(ctx, id)
	if err != nil {
		return nil, err
	}
	ids := []string{stored}
	if pub := domain.PublicChatConv(stored); pub != stored {
		ids = append(ids, pub)
	}
	var out []string
	for _, cid := range ids {
		if _, err := h.Store.ChatGetConversation(ctx, h.org(ctx), PersonFrom(ctx), cid); err == nil {
			out = append(out, cid)
		} else if !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: conversation %q", domain.ErrNotFound, id)
	}
	return out, nil
}

// MarkRead marks one conversation as read (for the person reading).
func (h *ChatHistory) MarkRead(ctx context.Context, id string) error {
	ids, err := h.storedIDs(ctx, id)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, cid := range ids {
		if err := h.Store.ChatMarkRead(ctx, h.org(ctx), PersonFrom(ctx), "conv:"+cid, now); err != nil {
			return err
		}
	}
	return nil
}

const maxTitleRunes = 120

// Update renames and/or archives a conversation. Audited (metadata only).
func (h *ChatHistory) Update(ctx context.Context, id string, p ChatConvPatch) (ChatConversation, error) {
	if p.Title == nil && p.Archived == nil {
		return ChatConversation{}, fmt.Errorf("%w: nothing to change", domain.ErrInvalid)
	}
	if p.Title != nil {
		t := strings.Join(strings.Fields(SanitizeChatText(*p.Title)), " ")
		if t == "" {
			return ChatConversation{}, fmt.Errorf("%w: title is required", domain.ErrInvalid)
		}
		if r := []rune(t); len(r) > maxTitleRunes {
			t = string(r[:maxTitleRunes])
		}
		p.Title = &t
	}
	org := h.org(ctx)
	// E0: the caller renames or archives their own thread; the earlier shared history is read-only.
	stored, err := ResolveChatConv(ctx, id)
	if err != nil {
		return ChatConversation{}, err
	}
	if _, err := h.Store.ChatGetConversation(ctx, org, PersonFrom(ctx), stored); err != nil {
		return ChatConversation{}, err
	}
	if err := h.Store.ChatPatchConversation(ctx, org, stored, p); err != nil {
		return ChatConversation{}, err
	}
	if h.Rec != nil {
		det := map[string]any{}
		if p.Title != nil {
			det["renamed"] = true
		}
		if p.Archived != nil {
			det["archived"] = *p.Archived
		}
		h.Rec.Audit(ctx, domain.AuditLog{Actor: ActorFrom(ctx, "user"), Action: "conversation.updated", Entity: "conversation", EntityID: id, Details: det})
	}
	c, err := h.Store.ChatGetConversation(ctx, org, PersonFrom(ctx), stored)
	if err == nil {
		rows := []ChatConversation{publicRow(PersonFrom(ctx), c)}
		h.link(ctx, org, rows)
		c = rows[0]
	}
	return c, err
}

// Delete removes a conversation and its messages for good. Audited. Deleting a
// chat stream (office or an agent's 1:1 chat) clears that history in both views.
//
// E0: for an agent chat it removes the caller's own private thread and the earlier shared history (the route
// needs conversations:delete, an administrator's permission, as before); another person's private thread is
// never touched (an administrator does not read nor delete it).
func (h *ChatHistory) Delete(ctx context.Context, id string) error {
	ids, err := h.storedIDs(ctx, id)
	if err != nil {
		return err
	}
	n := 0
	for _, cid := range ids {
		k, err := h.Store.ChatDeleteConversation(ctx, h.org(ctx), cid)
		if err != nil {
			return err
		}
		n += k
	}
	if h.Rec != nil {
		h.Rec.Audit(ctx, domain.AuditLog{Actor: ActorFrom(ctx, "user"), Action: "conversation.deleted", Entity: "conversation", EntityID: id,
			Details: map[string]any{"messages": n}})
	}
	return nil
}

// ---- helpers shared by the store implementations ----

// ChatKind classifies a conversation id.
func ChatKind(id string) (kind, agent string) {
	id = domain.PublicChatConv(id)
	if id == domain.ChatOffice {
		return ConvKindOffice, ""
	}
	if a, ok := strings.CutPrefix(id, domain.ChatAgentPrefix); ok {
		return ConvKindAgent, a
	}
	return ConvKindRequest, ""
}

// IsAgentActor reports whether a message endpoint is an agent (not the user, the system or "all").
func IsAgentActor(id string) bool {
	return id != "" && id != "user" && id != "system" && id != "all"
}

// InThread reports whether message m belongs to the thread of agent (General when agent is ""),
// given the turns the agent took part in. It is the reference definition of the views that the
// SQL of the Postgres store mirrors.
func InThread(m domain.Message, agent string, agentTurns map[string]bool) bool {
	if !IsChatConversation(m.ConversationID) {
		return false
	}
	if agent == "" {
		return true
	}
	if domain.PublicChatConv(m.ConversationID) == domain.ChatAgentConv(agent) || m.From == agent || m.To == agent {
		return true
	}
	return (m.From == "user" || m.From == "system") && m.TurnID != "" && agentTurns[m.TurnID]
}

// AgentTurns lists the turn ids in which the agent sent or received a chat message.
func AgentTurns(msgs []domain.Message, agent string) map[string]bool {
	out := map[string]bool{}
	for _, m := range msgs {
		if IsChatConversation(m.ConversationID) && m.TurnID != "" && (m.From == agent || m.To == agent) {
			out[m.TurnID] = true
		}
	}
	return out
}

// TurnAgents maps each turn id to the agents that took part in it.
func TurnAgents(msgs []domain.Message) map[string][]string {
	out := map[string][]string{}
	for _, m := range msgs {
		if m.TurnID == "" || !IsChatConversation(m.ConversationID) {
			continue
		}
		for _, p := range []string{m.From, m.To} {
			if IsAgentActor(p) && !slices.Contains(out[m.TurnID], p) {
				out[m.TurnID] = append(out[m.TurnID], p)
			}
		}
	}
	return out
}

// WithAgentIDs builds the ChatMessage of m from the agents of its turn.
func WithAgentIDs(m domain.Message, turns map[string][]string) ChatMessage {
	ids := []string{}
	add := func(p string) {
		if IsAgentActor(p) && !slices.Contains(ids, p) {
			ids = append(ids, p)
		}
	}
	add(m.From)
	for _, p := range turns[m.TurnID] {
		add(p)
	}
	add(m.To)
	if a, ok := strings.CutPrefix(domain.PublicChatConv(m.ConversationID), domain.ChatAgentPrefix); ok {
		add(a)
	}
	return ChatMessage{Message: m, AgentIDs: ids}
}

// Preview is the one-line text shown in the lists.
func Preview(s string) string {
	return truncate(strings.Join(strings.Fields(s), " "), 140)
}

// VisibleTo reports whether viewer may see a stored message or conversation id (E0): shared ones, and the
// private threads of viewer. The reference rule the SQL of the Postgres store mirrors (owner_id IN (”, viewer)).
func VisibleTo(storedConv, viewer string) bool {
	o := domain.ChatConvOwner(storedConv)
	return o == "" || o == viewer
}

// UnreadFor reports whether message m counts as unread for viewer (newer than the marker is checked by the
// caller): what the viewer wrote never does, nor do user messages without an author (written before E0).
func UnreadFor(m domain.Message, viewer string) bool {
	return !(m.From == "user" && (m.AuthorID == "" || m.AuthorID == viewer))
}

// SearchMatch is the case-insensitive substring match used by the history search.
func SearchMatch(hay, needle string) bool {
	return strings.Contains(strings.ToLower(hay), strings.ToLower(needle))
}
