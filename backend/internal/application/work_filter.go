package application

import "context"

// WorkFilter (D3, equipos-oficinas.md §4.1) says which work the person reading may see: the work of an agent of a
// private office is only its members'. Agent and Request may be nil (everything visible). The API sets it on the
// context of the reads that list work (the chat history); without one every read behaves as before.
type WorkFilter struct {
	Agent   func(agentID string) bool   // the agent's work (its chats, tasks, memory)
	Request func(requestID string) bool // a request (and its conversation, report...)
}

type workFilterKey struct{}

// WithWorkFilter returns ctx carrying the work filter of the person reading.
func WithWorkFilter(ctx context.Context, f WorkFilter) context.Context {
	return context.WithValue(ctx, workFilterKey{}, f)
}

// WorkVisible reports whether the person reading may see the work of an agent (true without a filter).
func WorkVisible(ctx context.Context, agentID string) bool {
	f, ok := ctx.Value(workFilterKey{}).(WorkFilter)
	return !ok || f.Agent == nil || f.Agent(agentID)
}

// RequestVisible reports whether the person reading may see a request (true without a filter).
func RequestVisible(ctx context.Context, requestID string) bool {
	f, ok := ctx.Value(workFilterKey{}).(WorkFilter)
	return !ok || f.Request == nil || f.Request(requestID)
}

// convVisible reports whether the person reading may see a history row: its agent, its other participants and its
// request must all be visible.
func convVisible(ctx context.Context, c ChatConversation) bool {
	if c.AgentID != "" && !WorkVisible(ctx, c.AgentID) {
		return false
	}
	for _, p := range c.Participants {
		if !WorkVisible(ctx, p) {
			return false
		}
	}
	return c.RequestID == nil || *c.RequestID == "" || RequestVisible(ctx, *c.RequestID)
}
