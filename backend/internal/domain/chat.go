package domain

import "strings"

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

// ChatOwnerSep separates the agent and the person in the STORED id of a private 1:1 thread
// (E0, docs/architecture/chat-history.md): each person has their own thread with each agent, stored as
// "agent:<agent>@<user>". The API and the WebSocket keep speaking "agent:<agent>" (the caller's own thread);
// the stored id never leaves the backend. "agent:<agent>" without owner is the history written before
// private threads existed: shared and read-only.
const ChatOwnerSep = "@"

// ChatPrivateConv is the stored id of the private thread of owner with agentID ("agent:<agent>" when
// owner is empty: no identity, e.g. a deployment without authentication).
func ChatPrivateConv(agentID, owner string) string {
	if owner == "" {
		return ChatAgentConv(agentID)
	}
	return ChatAgentPrefix + agentID + ChatOwnerSep + owner
}

// ChatConvOwner is the person a stored chat conversation belongs to ("" = shared).
func ChatConvOwner(id string) string {
	rest, ok := strings.CutPrefix(id, ChatAgentPrefix)
	if !ok {
		return ""
	}
	if i := strings.Index(rest, ChatOwnerSep); i >= 0 {
		return rest[i+len(ChatOwnerSep):]
	}
	return ""
}

// PublicChatConv is the id the API shows for a stored conversation id: the owner is dropped
// ("agent:<agent>@<user>" -> "agent:<agent>"); any other id is unchanged.
func PublicChatConv(id string) string {
	rest, ok := strings.CutPrefix(id, ChatAgentPrefix)
	if !ok {
		return id
	}
	if i := strings.Index(rest, ChatOwnerSep); i >= 0 {
		return ChatAgentPrefix + rest[:i]
	}
	return id
}

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

// FileRef points to a drive file from a chat message (never carries content).
// Action is "write" (produced by the agent), "attach" (attached by the user).
type FileRef struct {
	FileID  string `json:"file_id,omitempty"`
	Path    string `json:"path"`
	Name    string `json:"name"`
	Mime    string `json:"mime,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Version int    `json:"version,omitempty"`
	Action  string `json:"action,omitempty"`
	// Set when Action is "edit" (fs.edit): the version before the edit and the
	// lines added and removed, so the chat can show the change with Keep / Undo.
	FromVersion int `json:"from_version,omitempty"`
	Added       int `json:"added,omitempty"`
	Removed     int `json:"removed,omitempty"`
}
