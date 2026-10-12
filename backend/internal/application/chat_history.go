package application

import (
	"context"
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
	AgentID  string
	Q        string
	Kind     string
	Archived string
	Limit    int // 0 = all (capped)
	Offset   int
}

// ChatThreadQuery pages a thread: the Limit messages older than BeforeID ("" = newest).
type ChatThreadQuery struct {
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
type ChatHistoryStore interface {
	ChatConversations(ctx context.Context, org string, q ChatConvQuery) ([]ChatConversation, bool, error)
	ChatGetConversation(ctx context.Context, org, id string) (ChatConversation, error)
	ChatThreadMessages(ctx context.Context, org string, q ChatThreadQuery) ([]ChatMessage, bool, error)
	// ChatThreads returns General first, then one entry per given agent id (in that order).
	ChatThreads(ctx context.Context, org string, agentIDs []string) ([]ChatThread, error)
	// ChatMarkRead stores the read marker of a thread ("thread:<key>") or a conversation ("conv:<id>").
	ChatMarkRead(ctx context.Context, org, key string, at time.Time) error
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
	rows, more, err := h.Store.ChatConversations(ctx, org, q)
	if err != nil {
		return nil, false, err
	}
	h.link(ctx, org, rows)
	return rows, more, nil
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
		ids = append(ids, a.ID)
	}
	return h.Store.ChatThreads(ctx, org, ids)
}

func (h *ChatHistory) checkThread(ctx context.Context, key string) error {
	if key == ThreadGeneral {
		return nil
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
	return h.Store.ChatThreadMessages(ctx, h.org(ctx), q)
}

// MarkThreadRead says the user has seen everything in a thread.
func (h *ChatHistory) MarkThreadRead(ctx context.Context, key string) error {
	if err := h.checkThread(ctx, key); err != nil {
		return err
	}
	return h.Store.ChatMarkRead(ctx, h.org(ctx), "thread:"+key, time.Now().UTC())
}

// MarkRead marks one conversation as read.
func (h *ChatHistory) MarkRead(ctx context.Context, id string) error {
	if _, err := h.Store.ChatGetConversation(ctx, h.org(ctx), id); err != nil {
		return err
	}
	return h.Store.ChatMarkRead(ctx, h.org(ctx), "conv:"+id, time.Now().UTC())
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
	if err := h.Store.ChatPatchConversation(ctx, org, id, p); err != nil {
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
	c, err := h.Store.ChatGetConversation(ctx, org, id)
	if err == nil {
		rows := []ChatConversation{c}
		h.link(ctx, org, rows)
		c = rows[0]
	}
	return c, err
}

// Delete removes a conversation and its messages for good. Audited. Deleting a
// chat stream (office or an agent's 1:1 chat) clears that history in both views.
func (h *ChatHistory) Delete(ctx context.Context, id string) error {
	n, err := h.Store.ChatDeleteConversation(ctx, h.org(ctx), id)
	if err != nil {
		return err
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
	if m.ConversationID == domain.ChatAgentConv(agent) || m.From == agent || m.To == agent {
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
	if a, ok := strings.CutPrefix(m.ConversationID, domain.ChatAgentPrefix); ok {
		add(a)
	}
	return ChatMessage{Message: m, AgentIDs: ids}
}

// Preview is the one-line text shown in the lists.
func Preview(s string) string {
	return truncate(strings.Join(strings.Fields(s), " "), 140)
}

// SearchMatch is the case-insensitive substring match used by the history search.
func SearchMatch(hay, needle string) bool {
	return strings.Contains(strings.ToLower(hay), strings.ToLower(needle))
}
