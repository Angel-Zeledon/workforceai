package application

import (
	"context"
	"strings"

	"aiworkforce/backend/internal/domain"
)

// WorkFilter (D3, equipos-oficinas.md §4.1) says which work the person reading may see: the work of an agent of a
// private office is only its members'. Whoever may use such an agent (shared with them, or in the directory) talks
// with it in their own thread, and sees nothing else of the office. Any field may be nil (everything allowed). The
// API sets it on the context of the reads that list work (the chat history, the office chat's routing); without one
// everything behaves as before.
type WorkFilter struct {
	Agent   func(agentID string) bool   // the agent's work (its tasks, memory, the shared history of its chat)
	Use     func(agentID string) bool   // the person may use the agent (talk with it in their own thread)
	Request func(requestID string) bool // a request (and its conversation, report...)
}

type workFilterKey struct{}

// WithWorkFilter returns ctx carrying the work filter of the person reading.
func WithWorkFilter(ctx context.Context, f WorkFilter) context.Context {
	return context.WithValue(ctx, workFilterKey{}, f)
}

func workFilter(ctx context.Context) WorkFilter {
	f, _ := ctx.Value(workFilterKey{}).(WorkFilter)
	return f
}

// WorkVisible reports whether the person reading may see the work of an agent (true without a filter).
func WorkVisible(ctx context.Context, agentID string) bool {
	f := workFilter(ctx)
	return f.Agent == nil || f.Agent(agentID)
}

// AgentUsable reports whether the person may use an agent (true without a filter).
func AgentUsable(ctx context.Context, agentID string) bool {
	f := workFilter(ctx)
	return f.Use == nil || f.Use(agentID)
}

// ChatVisible reports whether the person may open their own chat with an agent: they see its work or may use it.
func ChatVisible(ctx context.Context, agentID string) bool {
	return WorkVisible(ctx, agentID) || AgentUsable(ctx, agentID)
}

// HasOwnChat reports whether the person has their own (E0 private) thread with an agent. D3: it stays readable to
// them even when they can no longer use the agent (read only: sending is refused by the API).
func HasOwnChat(ctx context.Context, store Store, org, agentID string) bool {
	me := PersonFrom(ctx)
	if me == "" || agentID == "" || store == nil {
		return false
	}
	_, err := store.GetConversation(ctx, org, domain.ChatPrivateConv(agentID, me))
	return err == nil
}

// RequestVisible reports whether the person reading may see a request (true without a filter).
func RequestVisible(ctx context.Context, requestID string) bool {
	f := workFilter(ctx)
	return f.Request == nil || f.Request(requestID)
}

// SharedMessageVisible reports whether the person reading may see a message of a STORED chat conversation: always in
// a private thread (theirs: the store only returns their own); in a shared one (the office chat, the history shared
// with an agent) not what an agent whose work they do not see said or was told, which is that office's work and is
// sealed live as well (internal/officeaudience).
func SharedMessageVisible(ctx context.Context, m domain.Message) bool {
	if domain.ChatConvOwner(m.ConversationID) != "" {
		return true
	}
	return !NamesHiddenAgent(ctx, m)
}

// NamesHiddenAgent reports whether a message was written by or to an agent whose work the person reading does not see.
func NamesHiddenAgent(ctx context.Context, m domain.Message) bool {
	for _, a := range []string{m.From, m.To} {
		if IsAgentActor(a) && !WorkVisible(ctx, a) {
			return true
		}
	}
	return false
}

// storedRowVisible reports whether the person reading may see a STORED history row (before the rows of an agent
// chat are merged): their own thread with an agent always (D3: read only once they cannot use it), the history shared
// before private threads existed only with the agent's work, a request's conversation with the request, else every
// participant's work.
func storedRowVisible(ctx context.Context, c ChatConversation) bool {
	if agent, ok := strings.CutPrefix(domain.PublicChatConv(c.ID), domain.ChatAgentPrefix); ok {
		return c.OwnerID != "" || WorkVisible(ctx, agent)
	}
	return convVisible(ctx, c)
}

// convVisible reports whether the person reading may see a history row: their chat with an agent (ChatVisible),
// a request's conversation (the request), else every participant's work.
func convVisible(ctx context.Context, c ChatConversation) bool {
	if c.AgentID != "" {
		return ChatVisible(ctx, c.AgentID)
	}
	if c.RequestID != nil && *c.RequestID != "" {
		return RequestVisible(ctx, *c.RequestID)
	}
	for _, p := range c.Participants {
		if !WorkVisible(ctx, p) {
			return false
		}
	}
	return true
}
