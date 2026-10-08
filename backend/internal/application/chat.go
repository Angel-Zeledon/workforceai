package application

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"aiworkforce/backend/internal/domain"
)

// This file is the conversational layer (docs/architecture/chat-routing.md):
// a message in the office channel or in a 1:1 chat is first ROUTED (smalltalk,
// question or task and who should answer), and only a task becomes a request
// with a plan. Everything that talks goes through the same guards as the rest
// of the orchestrator: kill switch / agent pause, budget caps, audit.

// ---- optional runtime port (type-asserted, like Estimator) ----

// ChatRuntime is the runtime capability behind POST /v1/route and
// POST /v1/chat-reply. Runtimes without it keep working: the backend then
// routes with its own local rules and answers with short canned lines.
type ChatRuntime interface {
	Route(ctx context.Context, in RouteRequest) (RouteResponse, error)
	ChatReply(ctx context.Context, in ChatReplyRequest) (ChatReplyResponse, error)
}

// ---- runtime DTOs ----

type RouteAgent struct {
	ID    string `json:"id"`
	Role  string `json:"role"`
	Title string `json:"title"`
	Name  string `json:"name"`
	// Profile of the role template (absent for roles without one): the runtime
	// routes and plans with it instead of a fixed list of roles.
	Topic    string   `json:"topic,omitempty"`
	Keywords []string `json:"keywords,omitempty"`
	Related  []string `json:"related,omitempty"`
	Area     string   `json:"area,omitempty"`
}

// ChatHistoryItem is one earlier message; From is "user" or an agent id.
type ChatHistoryItem struct {
	From string `json:"from"`
	Text string `json:"text"`
}

type RouteRequest struct {
	Text         string            `json:"text"`
	Conversation string            `json:"conversation"`
	Agents       []RouteAgent      `json:"agents"`
	History      []ChatHistoryItem `json:"history"`
	Locale       string            `json:"locale,omitempty"`
	Tone         string            `json:"tone,omitempty"`
}

type RouteResponder struct {
	AgentID string `json:"agent_id"`
	Role    string `json:"role"` // primary | contributor
	Reason  string `json:"reason"`
}

// RouteConsult (1:1 chats only): the topic belongs to another area.
type RouteConsult struct {
	AgentID string `json:"agent_id"`
	Reason  string `json:"reason"`
}

type RouteResponse struct {
	Intent     string           `json:"intent"`
	Topic      string           `json:"topic"`
	Responders []RouteResponder `json:"responders"`
	Consult    *RouteConsult    `json:"consult,omitempty"`
	Source     string           `json:"source,omitempty"` // rules | llm (local when decided by the backend)
	Usage      *Usage           `json:"usage,omitempty"`
}

type ChatAgent struct {
	ID      string `json:"id"`
	Role    string `json:"role"`
	Title   string `json:"title"`
	Name    string `json:"name"`
	Persona string `json:"persona"`
	Area    string `json:"area,omitempty"`
}

type ChatConsultIn struct {
	FromAgentID string `json:"from_agent_id"`
	Question    string `json:"question"`
}

type ChatReplyRequest struct {
	Agent         ChatAgent         `json:"agent"`
	Text          string            `json:"text"`
	Conversation  string            `json:"conversation"`
	Intent        string            `json:"intent"`
	Topic         string            `json:"topic"`
	ResponderRole string            `json:"responder_role"`
	Reason        string            `json:"reason,omitempty"`
	Agents        []RouteAgent      `json:"agents"`
	History       []ChatHistoryItem `json:"history"`
	PriorReplies  []ChatHistoryItem `json:"prior_replies"`
	Consult       *ChatConsultIn    `json:"consult,omitempty"`
	Slot          int               `json:"slot"`
	ConsultTo     string            `json:"consult_to,omitempty"`
	Limit         string            `json:"limit,omitempty"`   // kill_switch | paused | budget | read_only | no_connection
	Handoff       bool              `json:"handoff,omitempty"` // the user accepted a redirect
	Locale        string            `json:"locale,omitempty"`
	Tone          string            `json:"tone,omitempty"`
}

// ChatReplyConsult asks the backend to show a visible consult message and to
// have the colleague answer.
type ChatReplyConsult struct {
	ToAgentID string `json:"to_agent_id"`
	Question  string `json:"question"`
}

type ChatReplyResponse struct {
	Text    string            `json:"text"`
	Kind    string            `json:"kind,omitempty"`
	Consult *ChatReplyConsult `json:"consult,omitempty"`
	Usage   Usage             `json:"usage"`
}

// MessagePager is the optional store capability behind GET
// /conversations/{id}/messages?limit=&before=. Stores without it are paged in
// memory by Queries.ChatMessages.
type MessagePager interface {
	// ListMessagesPage returns up to limit messages older than beforeID ("" =
	// the newest ones), oldest first, and whether older ones exist.
	ListMessagesPage(ctx context.Context, orgID, conversationID, beforeID string, limit int) ([]domain.Message, bool, error)
}

const (
	maxChatTextRunes = 4000
	chatHistoryLimit = 8
	defaultChatLimit = 50
	maxChatLimit     = 200
)

// ChatTurn is what POST /messages answers: the stored user message and the id
// that groups every event of the turn.
type ChatTurn struct {
	MessageID string `json:"message_id"`
	TurnID    string `json:"turn_id"`
}

// chatTurn is the in-memory state of one turn.
type chatTurn struct {
	conv, id, text, userMsgID string
	loc                       string
	style                     RunStyle
	agents                    []domain.Agent
	byID                      map[string]domain.Agent
	direct                    string // agent id of a 1:1 chat, "" for the office
	history                   []ChatHistoryItem
	noticed                   bool // a blocking notice (pause, budget) was already posted
	handoff                   *handoff
}

// handoff is a redirect the user may accept ("yes, pass it on"): the colleague
// then takes the turn. It is remembered per conversation (agent memory).
type handoff struct {
	From, To, Intent, Text string
}

func chatLocale(s RunStyle) string {
	if strings.HasPrefix(strings.ToLower(s.Locale), "en") {
		return "en"
	}
	return "es"
}

// SanitizeChatText cleans what a user typed before it is stored, shown to the
// team or put in a prompt: invalid UTF-8, control characters (NUL, escape, ...),
// bidirectional overrides and zero-width characters (they hide or reorder text)
// are dropped (the zero-width joiner stays: emoji need it); newlines and tabs stay. Surrounding whitespace is trimmed.
func SanitizeChatText(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r == '\r':
			return -1
		case unicode.IsControl(r): // C0, DEL and C1
			return -1
		case r == 0x200B, r == 0x200C, r == 0x200E, r == 0x200F, r >= 0x202A && r <= 0x202E, r >= 0x2060 && r <= 0x2064, r >= 0x2066 && r <= 0x2069, r == 0xFEFF:
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// IsChatConversation reports whether id is a logical chat conversation.
func IsChatConversation(id string) bool {
	return id == domain.ChatOffice || strings.HasPrefix(id, domain.ChatAgentPrefix)
}

func (o *Orchestrator) routeAgents(agents []domain.Agent, loc string) []RouteAgent {
	out := make([]RouteAgent, 0, len(agents))
	for _, a := range agents {
		ra := RouteAgent{ID: a.ID, Role: a.Role, Title: a.Title, Name: a.Name}
		ra.Topic, ra.Keywords, ra.Related, ra.Area = profileOf(a.Role, loc)
		out = append(out, ra)
	}
	return out
}

// PostChat stores the user's message of a chat conversation and starts the
// turn asynchronously: route, then answers (or a request for a task). It
// returns as soon as the message is stored.
func (o *Orchestrator) PostChat(ctx context.Context, conversation, text string) (ChatTurn, error) {
	turn, _, err := o.PostChatKeyed(ctx, conversation, text, "")
	return turn, err
}

// idemTTL is how long a client-chosen key remembers its turn.
const (
	idemTTL = 10 * time.Minute
	idemMax = 5000
)

// idemCache makes POST /messages safe to retry: the same Idempotency-Key in the
// same organization and conversation returns the original turn instead of
// posting (and answering) the message twice. The zero value is ready to use.
type idemCache struct {
	mu sync.Mutex
	m  map[string]idemEntry
}

type idemEntry struct {
	turn ChatTurn
	at   time.Time
}

func (c *idemCache) get(key string) (ChatTurn, bool) {
	e, ok := c.m[key]
	if !ok || time.Since(e.at) > idemTTL {
		return ChatTurn{}, false
	}
	return e.turn, true
}

func (c *idemCache) put(key string, t ChatTurn) {
	if c.m == nil {
		c.m = map[string]idemEntry{}
	}
	if len(c.m) >= idemMax {
		for k, e := range c.m {
			if time.Since(e.at) > idemTTL {
				delete(c.m, k)
			}
		}
		if len(c.m) >= idemMax { // still full: drop everything older than the newest half
			c.m = map[string]idemEntry{}
		}
	}
	c.m[key] = idemEntry{turn: t, at: time.Now()}
}

// PostChatKeyed is PostChat with an optional idempotency key (a retry or a
// double click with the same key returns the first turn; replayed is true then).
func (o *Orchestrator) PostChatKeyed(ctx context.Context, conversation, text, key string) (turn ChatTurn, replayed bool, err error) {
	key = strings.TrimSpace(key)
	if key == "" {
		turn, err = o.postChat(ctx, conversation, text)
		return turn, false, err
	}
	if len(key) > 128 {
		return ChatTurn{}, false, fmt.Errorf("%w: idempotency key is too long (max 128 characters)", domain.ErrInvalid)
	}
	full := o.org(ctx) + "|" + strings.TrimSpace(conversation) + "|" + key
	o.chatIdem.mu.Lock()
	defer o.chatIdem.mu.Unlock()
	if t, ok := o.chatIdem.get(full); ok {
		return t, true, nil
	}
	turn, err = o.postChat(ctx, conversation, text)
	if err == nil {
		o.chatIdem.put(full, turn)
	}
	return turn, false, err
}

func (o *Orchestrator) postChat(ctx context.Context, conversation, text string) (ChatTurn, error) {
	text = SanitizeChatText(text)
	if text == "" {
		return ChatTurn{}, fmt.Errorf("%w: text is required", domain.ErrInvalid)
	}
	if len([]rune(text)) > maxChatTextRunes {
		return ChatTurn{}, fmt.Errorf("%w: text is too long (max %d characters)", domain.ErrInvalid, maxChatTextRunes)
	}
	conversation = strings.TrimSpace(conversation)
	if !IsChatConversation(conversation) || conversation == domain.ChatAgentPrefix {
		return ChatTurn{}, fmt.Errorf("%w: conversation must be %q or %q", domain.ErrInvalid, domain.ChatOffice, domain.ChatAgentPrefix+"<agent id>")
	}
	org := o.org(ctx)
	agents, err := o.store.ListAgents(ctx, org)
	if err != nil {
		return ChatTurn{}, err
	}
	t := &chatTurn{conv: conversation, id: newID(), text: text, userMsgID: newID(), agents: agents,
		byID: map[string]domain.Agent{}, style: o.loadStyle(ctx)}
	t.loc = chatLocale(t.style)
	for _, a := range agents {
		t.byID[a.ID] = a
	}
	to := "all"
	if id, ok := strings.CutPrefix(conversation, domain.ChatAgentPrefix); ok {
		if _, known := t.byID[id]; !known {
			return ChatTurn{}, fmt.Errorf("%w: agent %q", domain.ErrNotFound, id)
		}
		t.direct, to = id, id
	}
	if err := o.ensureChatConv(ctx, t); err != nil {
		return ChatTurn{}, err
	}
	m := domain.Message{ID: t.userMsgID, ConversationID: conversation, From: "user", To: to, Kind: domain.MsgChat, Text: text,
		TS: time.Now().UTC(), TurnID: t.id}
	if err := o.store.AddMessage(ctx, org, m); err != nil {
		return ChatTurn{}, err
	}
	o.emitChat(ctx, m, "")

	o.mu.Lock()
	defer o.mu.Unlock()
	rctx := WithOrg(o.base, org)
	rctx = WithActor(rctx, ActorFrom(ctx, ""))
	rctx = WithActorRole(rctx, ActorRoleFrom(ctx, ""))
	// Turns of one conversation run one after the other, in the order the messages arrived: answers never
	// interleave between turns and a follow-up always sees what the previous turn said.
	key := org + "|" + conversation
	prev, done := o.chatQ.enqueue(key)
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		defer o.chatQ.finish(key, done)
		select {
		case <-prev:
		case <-rctx.Done():
			return
		}
		t.history = o.chatHistory(rctx, t)
		o.runTurn(rctx, t)
	}()
	return ChatTurn{MessageID: m.ID, TurnID: t.id}, nil
}

// chatQueue chains the turns of each conversation (FIFO). The zero value is ready to use.
type chatQueue struct {
	mu    sync.Mutex
	tails map[string]chan struct{}
}

// enqueue returns the channel that closes when the previous turn of key is over
// (already closed when there is none) and the one this turn must close.
func (q *chatQueue) enqueue(key string) (prev <-chan struct{}, done chan struct{}) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.tails == nil {
		q.tails = map[string]chan struct{}{}
	}
	done = make(chan struct{})
	if last, ok := q.tails[key]; ok {
		prev = last
	} else {
		closed := make(chan struct{})
		close(closed)
		prev = closed
	}
	q.tails[key] = done
	return prev, done
}

func (q *chatQueue) finish(key string, done chan struct{}) {
	close(done)
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.tails[key] == done {
		delete(q.tails, key)
	}
}

func (o *Orchestrator) ensureChatConv(ctx context.Context, t *chatTurn) error {
	org := o.org(ctx)
	if _, err := o.store.GetConversation(ctx, org, t.conv); err == nil {
		return nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	c := domain.Conversation{ID: t.conv, LastMessageAt: time.Now().UTC()}
	if t.direct == "" {
		c.Title = chatText(t.loc, "office_title")
		c.Participants = []string{"user"}
		for _, a := range t.agents {
			c.Participants = append(c.Participants, a.ID)
		}
	} else {
		c.Title = fmt.Sprintf(chatText(t.loc, "direct_title"), t.byID[t.direct].Name)
		c.Participants = []string{"user", t.direct}
	}
	if err := o.store.CreateConversation(ctx, org, c); err != nil {
		if _, e2 := o.store.GetConversation(ctx, org, t.conv); e2 == nil {
			return nil // another turn created it first
		}
		return err
	}
	return nil
}

// chatHistory returns the most recent spoken messages of the conversation that
// came BEFORE this turn: not the user's message of this turn nor messages the
// user wrote later while this turn was waiting in the queue.
func (o *Orchestrator) chatHistory(ctx context.Context, t *chatTurn) []ChatHistoryItem {
	const window = chatHistoryLimit + 8 // room for the messages that are filtered out below
	var msgs []domain.Message
	var err error
	if p, ok := o.store.(MessagePager); ok {
		msgs, _, err = p.ListMessagesPage(ctx, o.org(ctx), t.conv, "", window)
	} else {
		msgs, err = o.store.ListMessages(ctx, o.org(ctx), t.conv)
		if len(msgs) > window {
			msgs = msgs[len(msgs)-window:]
		}
	}
	if err != nil {
		return nil
	}
	var mine time.Time
	for _, m := range msgs {
		if m.ID == t.userMsgID {
			mine = m.TS
		}
	}
	out := make([]ChatHistoryItem, 0, len(msgs))
	for _, m := range msgs {
		if m.From == "system" || (m.Kind != domain.MsgChat && m.Kind != domain.MsgAnswer) {
			continue
		}
		if m.ID == t.userMsgID || (m.From == "user" && !mine.IsZero() && m.TS.After(mine)) {
			continue
		}
		out = append(out, ChatHistoryItem{From: m.From, Text: truncate(m.Text, 400)})
	}
	if len(out) > chatHistoryLimit {
		out = out[len(out)-chatHistoryLimit:]
	}
	return out
}

// ---- the turn ----

func (o *Orchestrator) runTurn(ctx context.Context, t *chatTurn) {
	var route RouteResponse
	if h := o.takeHandoff(ctx, t); h != nil && isHandoffAccepted(t.text) {
		t.handoff = h
		route = RouteResponse{Intent: h.Intent, Topic: "general", Source: "handoff", Responders: []RouteResponder{
			{AgentID: h.To, Role: domain.RolePrimary, Reason: chatText(t.loc, "reason_handoff")}}}
	} else {
		route = o.decideRoute(ctx, t)
	}
	ids := make([]string, 0, len(route.Responders))
	for _, r := range route.Responders {
		ids = append(ids, r.AgentID)
	}
	payload := map[string]any{"turn_id": t.id, "conversation": t.conv, "intent": route.Intent, "topic": route.Topic,
		"responders": route.Responders, "source": route.Source}
	if route.Consult != nil {
		payload["consult"] = route.Consult
	}
	o.rec.Emit(ctx, Action{Type: domain.EvRouteDecided, Entity: "conversation", EntityID: t.conv, Payload: payload, SkipAudit: true})
	// Metadata only: never the text of the message.
	o.rec.Audit(ctx, domain.AuditLog{Actor: ActorFrom(ctx, "user"), Action: "chat.turn", Entity: "conversation", EntityID: t.conv,
		Details: map[string]any{"turn_id": t.id, "intent": route.Intent, "topic": route.Topic, "responders": ids, "source": route.Source}})

	if t.handoff != nil {
		o.chatHandoff(ctx, t, route)
		return
	}
	if route.Intent == domain.IntentTask {
		o.chatTask(ctx, t, route)
		return
	}
	var prior []ChatHistoryItem
	for i, r := range route.Responders {
		delay := time.Duration(0)
		if i > 0 {
			delay = o.cfg.ChatStagger
		}
		agent := t.byID[r.AgentID]
		req := o.chatReplyRequest(t, agent, route, r, i, prior)
		resp, res := o.chatSpeak(ctx, t, agent, req, domain.MsgChat, "user", delay)
		if res == speakStop {
			return
		}
		if res != speakDone {
			continue
		}
		prior = append(prior, ChatHistoryItem{From: agent.ID, Text: resp.Text})
		if resp.Consult != nil && i == 0 && route.Intent == domain.IntentQuestion && route.Consult != nil {
			o.chatConsult(ctx, t, agent, route, resp.Consult)
			o.rememberHandoff(ctx, t, handoff{From: agent.ID, To: route.Consult.AgentID, Intent: domain.IntentQuestion, Text: t.text})
		}
	}
}

func (o *Orchestrator) chatReplyRequest(t *chatTurn, agent domain.Agent, route RouteResponse, r RouteResponder, slot int, prior []ChatHistoryItem) ChatReplyRequest {
	req := ChatReplyRequest{
		Agent: chatAgent(agent, t.style.Locale),
		Text:  t.text, Conversation: t.conv, Intent: route.Intent, Topic: route.Topic, ResponderRole: r.Role, Reason: r.Reason,
		Agents: o.routeAgents(t.agents, t.style.Locale), History: append([]ChatHistoryItem{}, t.history...), PriorReplies: append([]ChatHistoryItem{}, prior...), Slot: slot,
		Locale: t.style.Locale, Tone: t.style.ToneFor(agent.ID),
	}
	if route.Consult != nil && r.Role == domain.RolePrimary {
		req.ConsultTo = route.Consult.AgentID
	}
	return req
}

// ---- routing ----

// decideRoute asks the runtime and falls back to the local rules on any
// problem (runtime down, timeout, invalid answer). It never fails.
func (o *Orchestrator) decideRoute(ctx context.Context, t *chatTurn) RouteResponse {
	in := RouteRequest{Text: t.text, Conversation: t.conv, Agents: o.routeAgents(t.agents, t.style.Locale), History: append([]ChatHistoryItem{}, t.history...),
		Locale: t.style.Locale, Tone: t.style.Tone}
	if cr, ok := o.rt.(ChatRuntime); ok {
		cctx, cancel := context.WithTimeout(ctx, o.chatTimeout())
		resp, err := cr.Route(cctx, in)
		cancel()
		if err == nil {
			if clean, ok := sanitizeRoute(resp, t); ok {
				if resp.Usage != nil {
					o.chatRecordCost(ctx, "assistant", *resp.Usage)
				}
				return clean
			}
			err = errors.New("invalid routing answer")
		}
		if ctx.Err() == nil {
			o.log.Warn("route failed, using local rules", "err", err)
			o.rec.Audit(ctx, domain.AuditLog{Actor: "system", Action: "chat.route_fallback", Entity: "conversation", EntityID: t.conv,
				Details: map[string]any{"turn_id": t.id, "error": truncate(err.Error(), 200)}})
		}
	}
	clean, _ := sanitizeRoute(localRoute(in), t)
	clean.Source = "local"
	return clean
}

// sanitizeRoute enforces the hard rules whatever the router said: only known
// agents, one primary, no crowd (greeting: assistant + 2, topic: owner + 1),
// a task is coordinated by the assistant and a 1:1 chat is answered ONLY by
// its agent.
func sanitizeRoute(r RouteResponse, t *chatTurn) (RouteResponse, bool) {
	switch r.Intent {
	case domain.IntentSmalltalk, domain.IntentQuestion, domain.IntentTask:
	default:
		return r, false
	}
	if r.Topic == "" {
		r.Topic = "general"
	}
	seen := map[string]bool{}
	var rs []RouteResponder
	for _, x := range r.Responders {
		if _, ok := t.byID[x.AgentID]; !ok || seen[x.AgentID] {
			continue
		}
		seen[x.AgentID] = true
		rs = append(rs, x)
	}
	switch {
	case r.Source == "handoff":
		// the user accepted a redirect: the colleague is the single responder
	case t.direct != "":
		reason := ""
		for _, x := range rs {
			if x.AgentID == t.direct {
				reason = x.Reason
			}
		}
		rs = []RouteResponder{{AgentID: t.direct, Role: domain.RolePrimary, Reason: reason}}
		if r.Consult != nil {
			if _, ok := t.byID[r.Consult.AgentID]; !ok || r.Consult.AgentID == t.direct || r.Intent == domain.IntentSmalltalk {
				r.Consult = nil
			}
		}
	default:
		// office: a redirect only exists when ONE colleague (not the assistant) was addressed
		if r.Consult != nil {
			_, known := t.byID[r.Consult.AgentID]
			if !known || len(rs) != 1 || rs[0].AgentID == assistantID || rs[0].AgentID == r.Consult.AgentID || r.Intent == domain.IntentSmalltalk {
				r.Consult = nil
			}
		}
		if r.Intent == domain.IntentTask && len(rs) > 0 {
			rs = rs[:1] // one person confirms; the plan decides who works
		}
	}
	if len(rs) == 0 {
		return r, false
	}
	limit := 1 + 1 // question: owner + one contributor
	switch r.Intent {
	case domain.IntentSmalltalk:
		limit = 1 + 2
	case domain.IntentTask:
		limit = 1
	}
	if len(rs) > limit {
		rs = rs[:limit]
	}
	for i := range rs {
		rs[i].Role = domain.RoleContributor
		if i == 0 {
			rs[i].Role = domain.RolePrimary
		}
	}
	r.Responders = rs
	return r, true
}

func firstReason(rs []RouteResponder, id string) string {
	for _, r := range rs {
		if r.AgentID == id {
			return r.Reason
		}
	}
	return ""
}

func (o *Orchestrator) chatTimeout() time.Duration {
	if o.cfg.ChatTimeout > 0 {
		return o.cfg.ChatTimeout
	}
	return 30 * time.Second
}

// ---- speaking ----

type speakResult int

const (
	speakDone    speakResult = iota // a message was posted
	speakSkipped                    // this agent did not speak (paused, runtime failed): others may
	speakStop                       // the turn must end (budget)
)

// chatSpeak makes ONE agent say one thing: guard, typing, budget reservation,
// runtime call (with a local canned fallback), usage record, message.
func (o *Orchestrator) chatSpeak(ctx context.Context, t *chatTurn, agent domain.Agent, req ChatReplyRequest, kind, to string, delay time.Duration) (ChatReplyResponse, speakResult) {
	if ctx.Err() != nil {
		return ChatReplyResponse{}, speakStop
	}
	if v := o.chatAdmit(ctx, agent.ID); !v.Allowed {
		// A paused colleague that was only a side voice is skipped silently; the user hears about it
		// when the agent they asked is paused or the whole team is stopped.
		o.chatBlocked(ctx, t, agent, v.Code, req.ResponderRole != domain.RoleContributor || v.Code != "agent_paused")
		return ChatReplyResponse{}, speakSkipped
	}
	o.emitTyping(ctx, t, agent.ID, true)
	defer o.emitTyping(ctx, t, agent.ID, false)
	restore := o.chatTalking(ctx, agent.ID)
	defer restore()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ChatReplyResponse{}, speakStop
		}
	}

	res, ex := o.chatReserve(ctx, agent.ID)
	if ex != nil {
		o.chatBudgetNotice(ctx, t, ex)
		return ChatReplyResponse{}, speakStop
	}
	var resp ChatReplyResponse
	var err error
	if cr, ok := o.rt.(ChatRuntime); ok {
		cctx, cancel := context.WithTimeout(ctx, o.chatTimeout())
		resp, err = cr.ChatReply(cctx, req)
		cancel()
		if err == nil && strings.TrimSpace(resp.Text) == "" {
			err = errors.New("empty reply")
		}
	} else {
		err = errors.New("runtime without chat support")
	}
	if err != nil {
		res.Release()
		if ctx.Err() != nil {
			return ChatReplyResponse{}, speakStop
		}
		o.log.Warn("chat reply failed, using canned reply", "agent", agent.ID, "err", err)
		o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: "chat.reply_fallback", Entity: "conversation", EntityID: t.conv,
			Details: map[string]any{"turn_id": t.id, "error": truncate(err.Error(), 200)}})
		text := fallbackReply(t.loc, req, agent)
		if text == "" {
			return ChatReplyResponse{}, speakSkipped
		}
		resp = ChatReplyResponse{Text: text}
	} else {
		o.chatRecordUsage(ctx, agent.ID, resp.Usage, res)
	}
	resp.Text = strings.TrimSpace(resp.Text)
	m := domain.Message{ID: newID(), ConversationID: t.conv, From: agent.ID, To: to, Kind: kind, Text: resp.Text,
		TS: time.Now().UTC(), TurnID: t.id}
	if kind != domain.MsgAnswer { // an answer replies to a colleague, not to the user
		reply := t.userMsgID
		m.ReplyTo = &reply
	}
	o.chatStore(ctx, m, "")
	return resp, speakDone
}

// chatConsult shows the consult of a 1:1 agent to a colleague and the answer.
func (o *Orchestrator) chatConsult(ctx context.Context, t *chatTurn, from domain.Agent, route RouteResponse, c *ChatReplyConsult) {
	to, ok := t.byID[c.ToAgentID]
	question := strings.TrimSpace(c.Question)
	if !ok || to.ID == from.ID || question == "" {
		return
	}
	if v := o.chatAdmit(ctx, to.ID); !v.Allowed {
		return // the colleague is paused: nothing is asked
	}
	reply := t.userMsgID
	m := domain.Message{ID: newID(), ConversationID: t.conv, From: from.ID, To: to.ID, Kind: domain.MsgConsult, Text: truncate(question, 400),
		TS: time.Now().UTC(), TurnID: t.id, ReplyTo: &reply}
	o.chatStore(ctx, m, "")
	req := o.chatReplyRequest(t, to, route, RouteResponder{AgentID: to.ID, Role: domain.RoleContributor}, 1, nil)
	req.Consult = &ChatConsultIn{FromAgentID: from.ID, Question: question}
	req.ConsultTo = ""
	o.chatSpeak(ctx, t, to, req, domain.MsgAnswer, from.ID, o.cfg.ChatStagger)
}

// chatTask answers a task request: the agent confirms, then the normal
// request flow (plan, human reviews, approvals) takes over. The assistant
// explains who got what and why once the plan exists (announceAssignments).
func (o *Orchestrator) chatTask(ctx context.Context, t *chatTurn, route RouteResponse) {
	r := route.Responders[0]
	agent := t.byID[r.AgentID]
	req := o.chatReplyRequest(t, agent, route, r, 0, nil)
	_, res := o.chatSpeak(ctx, t, agent, req, domain.MsgChat, "user", 0)
	if res == speakStop {
		return
	}
	if res == speakSkipped && !o.chatAdmit(ctx, agent.ID).Allowed {
		return // paused or kill switch: no work is created
	}
	link := &chatLink{conv: t.conv, turnID: t.id, replyTo: t.userMsgID, speaker: agent.ID, loc: t.loc}
	if route.Consult != nil && agent.Role != "assistant" {
		link.reassignFrom, link.reassignTo = agent.ID, route.Consult.AgentID
	}
	if g := o.conn.guard; g != nil && g.SideEffectsBlocked(ctx, o.org(ctx), agent.ID) {
		o.chatLimitSay(ctx, t, agent, "read_only") // the work goes on, but the user hears what it cannot do
	}
	id, err := o.Submit(withChatLink(ctx, link), t.text)
	if err != nil {
		if ctx.Err() == nil {
			o.chatSystem(ctx, t, fmt.Sprintf(chatText(t.loc, "task_failed"), chatSafeCause(t.loc, err)))
		}
		return
	}
	o.log.Info("chat task submitted", "request", id, "turn", t.id)
}

// ---- guards, budget and cost ----

func (o *Orchestrator) chatAdmit(ctx context.Context, agentID string) GuardVerdict {
	if o.conn.guard == nil {
		return GuardVerdict{Allowed: true}
	}
	return o.conn.guard.Admit(ctx, o.org(ctx), agentID)
}

func (o *Orchestrator) chatBlocked(ctx context.Context, t *chatTurn, agent domain.Agent, code string, notify bool) {
	o.rec.Audit(ctx, domain.AuditLog{Actor: "system", Action: "chat.blocked", Entity: "conversation", EntityID: t.conv,
		Details: map[string]any{"turn_id": t.id, "agent_id": agent.ID, "reason": code}})
	if t.noticed || !notify {
		return
	}
	t.noticed = true
	key := "blocked_other"
	switch code {
	case "kill_switch_active", "controls_unavailable":
		key = "blocked_" + code
	case "agent_paused":
		key = "blocked_agent_paused"
	}
	if o.chatLimitSay(ctx, t, agent, limitOf(code)) {
		return
	}
	msg := chatText(t.loc, key)
	if strings.Contains(msg, "%s") {
		msg = fmt.Sprintf(msg, agent.Name)
	}
	o.chatSystem(ctx, t, msg)
}

// chatTalking shows the agent as "talking" while it answers (only if it was
// idle: a working agent keeps its state) and restores it afterwards.
func (o *Orchestrator) chatTalking(ctx context.Context, agentID string) func() {
	prev, err := o.store.GetAgent(ctx, o.org(ctx), agentID)
	if err != nil || (prev.State != domain.StateIdle && prev.State != domain.StateCompleted) {
		return func() {}
	}
	o.setState(ctx, agentID, domain.StateTalking, "Respondiendo en el chat", nil, 0)
	return func() {
		cur, err := o.store.GetAgent(context.WithoutCancel(ctx), o.org(ctx), agentID)
		if err == nil && cur.State == domain.StateTalking {
			o.setState(ctx, agentID, domain.StateIdle, "Disponible", nil, 0)
		}
	}
}

// chatReserveUSD is what one chat reply holds while it runs.
func chatReserveUSD() float64 { return recalcCost("", 900, 350) }

// chatReserve holds budget for one reply. Chat calls belong to no request, so
// the agent cap and the organization limit apply (the ledger is the source of
// truth: the organization total also counts chat spend).
func (o *Orchestrator) chatReserve(ctx context.Context, agentID string) (*Reservation, *Exceeded) {
	est := chatReserveUSD()
	org := o.org(ctx)
	if o.cfg.BudgetUSD > 0 {
		used, err := o.store.OrgCost(ctx, org)
		if err == nil {
			spent := o.chatSpend(ctx)
			if used+spent+est > o.cfg.BudgetUSD+1e-9 {
				return nil, &Exceeded{Scope: domain.ScopeOrg, ScopeID: org, AgentID: agentID, CapUSD: o.cfg.BudgetUSD, SpentUSD: used + spent, NeededUSD: used + spent + est}
			}
		}
	}
	res, ex, err := o.budget.Reserve(ctx, "", agentID, est)
	if err != nil {
		o.log.Warn("chat budget check failed", "err", err)
		return nil, nil // a broken ledger must not silence the chat; the call is tiny (Release is nil-safe)
	}
	return res, ex
}

// chatSpend is the spend of calls that belong to no request (chat).
func (o *Orchestrator) chatSpend(ctx context.Context) float64 {
	usage, err := o.store.ListUsage(ctx, o.org(ctx))
	if err != nil {
		return 0
	}
	var s float64
	for _, u := range usage {
		if u.RequestID == "" {
			s += u.CostUSD
		}
	}
	return s
}

func (o *Orchestrator) chatRecordUsage(ctx context.Context, agentID string, u Usage, res *Reservation) {
	defer res.Release()
	if u.CostUSD <= 0 && u.InputTokens == 0 && u.OutputTokens == 0 {
		return
	}
	o.chatLedger(ctx, agentID, u)
}

// chatRecordCost records a call that was not reserved (the LLM router).
func (o *Orchestrator) chatRecordCost(ctx context.Context, agentID string, u Usage) {
	if u.CostUSD <= 0 && u.InputTokens == 0 && u.OutputTokens == 0 {
		return
	}
	o.chatLedger(ctx, agentID, u)
}

func (o *Orchestrator) chatLedger(ctx context.Context, agentID string, u Usage) {
	entry := domain.UsageEntry{AgentID: agentID, Kind: domain.UsageChat, Model: u.Model, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens}
	cost, err := o.budget.Record(ctx, "", entry, u.CostUSD)
	if err != nil {
		o.log.Warn("record chat usage", "err", err)
	}
	u.CostUSD = cost
	o.rec.Audit(ctx, domain.AuditLog{Actor: agentID, Action: "runtime.usage", Entity: "conversation", EntityID: "chat", Details: u})
	o.emitMetrics(ctx)
}

func (o *Orchestrator) chatBudgetNotice(ctx context.Context, t *chatTurn, ex *Exceeded) {
	o.rec.Audit(ctx, domain.AuditLog{Actor: "system", Action: "chat.budget_exceeded", Entity: string(ex.Scope), EntityID: ex.ScopeID,
		Details: map[string]any{"turn_id": t.id, "scope": ex.Scope, "cap_usd": ex.CapUSD, "spent_usd": ex.SpentUSD, "agent_id": ex.AgentID}})
	if t.noticed {
		return
	}
	t.noticed = true
	if a, ok := t.byID[ex.AgentID]; ok && o.chatLimitSay(ctx, t, a, "budget") {
		return
	}
	o.chatSystem(ctx, t, fmt.Sprintf(chatText(t.loc, "budget_"+string(ex.Scope)), ex.SpentUSD, ex.CapUSD))
}

// ---- messages and events ----

// chatSystem posts a notice of the system (pauses, budget, failures).
func (o *Orchestrator) chatSystem(ctx context.Context, t *chatTurn, text string) {
	reply := t.userMsgID
	o.chatStore(ctx, domain.Message{ID: newID(), ConversationID: t.conv, From: "system", To: "user", Kind: domain.MsgChat, Text: text,
		TS: time.Now().UTC(), TurnID: t.id, ReplyTo: &reply}, "")
}

// chatStore persists a message and publishes chat.message.
func (o *Orchestrator) chatStore(ctx context.Context, m domain.Message, requestID string) {
	if requestID != "" {
		m.RequestID = &requestID
	}
	if err := o.store.AddMessage(context.WithoutCancel(ctx), o.org(ctx), m); err != nil {
		o.log.Warn("add chat message", "err", err)
		return
	}
	o.emitChat(ctx, m, requestID)
}

func (o *Orchestrator) emitChat(ctx context.Context, m domain.Message, requestID string) {
	payload := map[string]any{"id": m.ID, "conversation": m.ConversationID, "turn_id": m.TurnID, "from": m.From, "to": m.To,
		"kind": m.Kind, "text": m.Text, "reply_to": m.ReplyTo, "ts": m.TS}
	if requestID != "" {
		payload["request_id"] = requestID
	}
	act := Action{Type: domain.EvChatMessage, Entity: "message", EntityID: m.ID, Payload: payload, SkipAudit: true}
	if m.From != "user" && m.From != "system" {
		act.AgentID = m.From
	}
	if m.Kind == domain.MsgConsult {
		act.Text = fmt.Sprintf("%s → %s: %s", m.From, m.To, truncate(m.Text, 100))
	}
	o.rec.Emit(ctx, act)
}

func (o *Orchestrator) emitTyping(ctx context.Context, t *chatTurn, agentID string, on bool) {
	o.rec.Emit(ctx, Action{Type: domain.EvChatTyping, AgentID: agentID, SkipAudit: true,
		Payload: map[string]any{"conversation": t.conv, "turn_id": t.id, "agent_id": agentID, "on": on}})
}

// ---- link between a chat turn and the request it created ----

type chatLink struct {
	conv, turnID, replyTo, speaker, loc string
	// reassignFrom declined the work (outside their role): their tasks go to reassignTo.
	reassignFrom, reassignTo string
}

type chatLinkKey struct{}

func withChatLink(ctx context.Context, l *chatLink) context.Context {
	return context.WithValue(ctx, chatLinkKey{}, l)
}

func chatLinkFrom(ctx context.Context) *chatLink {
	l, _ := ctx.Value(chatLinkKey{}).(*chatLink)
	return l
}

// say posts a message of the request's speaker into the chat that started it.
func (o *Orchestrator) chatSay(ctx context.Context, rs *run, from, text string) {
	l := rs.chat
	if l == nil {
		return
	}
	reply := l.replyTo
	o.chatStore(ctx, domain.Message{ID: newID(), ConversationID: l.conv, From: from, To: "user", Kind: domain.MsgChat, Text: text,
		TS: time.Now().UTC(), TurnID: l.turnID, ReplyTo: &reply}, rs.req.ID)
}

// announceAssignments tells the user, in plain language, who got which task and
// why. It runs once, right after the plan became tasks, only for requests that
// started in a chat.
func (o *Orchestrator) announceAssignments(ctx context.Context, rs *run, tasks []domain.Task) {
	l := rs.chat
	if l == nil || len(tasks) == 0 {
		return
	}
	var b strings.Builder
	intro := "plan_intro"
	if l.reassignFrom != "" {
		intro = "plan_intro_reassigned" // the speaker declined this work: no "I put together a plan"
	}
	fmt.Fprintf(&b, chatText(l.loc, intro), len(tasks))
	for _, t := range tasks {
		a := rs.agents[t.AgentID]
		fmt.Fprintf(&b, "\n• %s (%s): %s. %s %s.", a.Name, a.Title, strings.TrimRight(t.Title, ". "), chatText(l.loc, "why"),
			strings.TrimRight(t.AssignedReason, ". "))
	}
	b.WriteString("\n" + chatText(l.loc, "plan_outro"))
	from := l.speaker
	if _, ok := rs.agents[from]; !ok {
		from = assistantID
	}
	o.chatSay(ctx, rs, from, b.String())
}

// echoFailure and echoReport mirror the end of a chat-born request in the chat.
func (o *Orchestrator) chatEchoFailure(ctx context.Context, rs *run, what string, cause error) {
	if rs.chat == nil {
		return
	}
	o.chatStore(ctx, domain.Message{ID: newID(), ConversationID: rs.chat.conv, From: "system", To: "user", Kind: domain.MsgChat,
		Text: fmt.Sprintf("%s: %s", what, chatSafeCause(rs.chat.loc, cause)), TS: time.Now().UTC(), TurnID: rs.chat.turnID, ReplyTo: &rs.chat.replyTo}, rs.req.ID)
}

// chatSafeCause is what the user may read about a failure: network and runtime
// errors carry internal addresses, so they become one plain sentence (the full
// error stays in the log and the audit trail).
func chatSafeCause(loc string, err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	low := strings.ToLower(msg)
	for _, leak := range []string{"://", "agent-runtime", "dial tcp", "connection refused", "context deadline", "i/o timeout", "eof", "post \""} {
		if strings.Contains(low, leak) {
			return chatText(loc, "cause_unavailable")
		}
	}
	return truncate(strings.Join(strings.Fields(msg), " "), 200)
}

func (o *Orchestrator) chatEchoReport(ctx context.Context, rs *run, title string) {
	if rs.chat == nil {
		return
	}
	o.chatSay(ctx, rs, assistantID, fmt.Sprintf(chatText(rs.chat.loc, "report_ready"), title))
}

// cleanReason trims a reason coming from the runtime.
func cleanReason(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 240 {
		s = string(r[:240])
	}
	return s
}

// ---- queries ----

// ChatMessages pages the messages of a conversation. limit <= 0 returns all of
// them (legacy behaviour). beforeID returns the messages older than that one.
// A chat conversation that has no messages yet is simply empty.
func (q *Queries) ChatMessages(ctx context.Context, id, beforeID string, limit int) ([]domain.Message, bool, error) {
	org := q.org(ctx)
	if IsChatConversation(id) {
		if agent, ok := strings.CutPrefix(id, domain.ChatAgentPrefix); ok {
			if agent == "" {
				return nil, false, fmt.Errorf("%w: conversation %q", domain.ErrNotFound, id)
			}
			if _, err := q.Store.GetAgent(ctx, org, agent); err != nil {
				return nil, false, err
			}
		}
		if _, err := q.Store.GetConversation(ctx, org, id); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return []domain.Message{}, false, nil
			}
			return nil, false, err
		}
	} else if _, err := q.Store.GetConversation(ctx, org, id); err != nil {
		return nil, false, err
	}
	if limit > maxChatLimit {
		limit = maxChatLimit
	}
	if limit > 0 {
		if p, ok := q.Store.(MessagePager); ok {
			return p.ListMessagesPage(ctx, org, id, beforeID, limit)
		}
	}
	all, err := q.Store.ListMessages(ctx, org, id)
	if err != nil {
		return nil, false, err
	}
	return PageMessages(all, beforeID, limit)
}

// PageMessages slices an oldest-first list: the `limit` messages before
// beforeID (or the newest ones), and whether older messages remain.
func PageMessages(all []domain.Message, beforeID string, limit int) ([]domain.Message, bool, error) {
	end := len(all)
	if beforeID != "" {
		end = slices.IndexFunc(all, func(m domain.Message) bool { return m.ID == beforeID })
		if end < 0 {
			return nil, false, fmt.Errorf("%w: message %q", domain.ErrNotFound, beforeID)
		}
	}
	start := 0
	if limit > 0 && end-limit > 0 {
		start = end - limit
	}
	out := slices.Clone(all[start:end])
	if out == nil {
		out = []domain.Message{}
	}
	return out, start > 0, nil
}
