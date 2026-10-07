package domain

// Chat layer (docs/architecture/chat-routing.md): conversation ids, message
// kinds, intents and the additive WebSocket events.

// Logical conversation ids. The stored conversation id is the same string, so
// the channel of the office is "office" and the 1:1 chat with an agent is
// "agent:<id>" (conversations are unique per organization).
const (
	ChatOffice      = "office"
	ChatAgentPrefix = "agent:"
)

// ChatAgentConv is the conversation id of the 1:1 chat with an agent.
func ChatAgentConv(agentID string) string { return ChatAgentPrefix + agentID }

// Message kinds used by the chat layer (the others are chat|delegation|consult|answer).
const (
	MsgChat    = "chat"
	MsgConsult = "consult"
	MsgAnswer  = "answer"
)

// Intents the router can decide.
const (
	IntentSmalltalk = "smalltalk"
	IntentQuestion  = "question"
	IntentTask      = "task"
)

// Responder roles inside a turn.
const (
	RolePrimary     = "primary"
	RoleContributor = "contributor"
)

// Event types added by the chat layer (additive WS contract).
const (
	EvChatMessage  = "chat.message"
	EvChatTyping   = "chat.typing"
	EvRouteDecided = "route.decided"
)

// UsageChat is the ledger kind of a chat call (reply or LLM routing). Chat
// calls belong to no request: their entries have an empty request id.
const UsageChat UsageKind = "chat"
