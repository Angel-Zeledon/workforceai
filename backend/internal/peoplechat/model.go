// Package peoplechat is the person-to-person and group chat of an organization: the all-hands channel, department
// and project channels, private groups and 1:1 DMs between people. Agents can be members of a channel and be
// mentioned (the agent chats of chat-routing.md are a separate, older layer and are not touched). Unread counts are
// kept per member with a per-channel sequence. See docs/architecture/presence.md.
package peoplechat

import (
	"errors"
	"time"
)

// Channel kinds.
const (
	KindOrg        = "org"        // everyone in the organization (one per org, scope "general")
	KindDepartment = "department" // tied to a department name
	KindProject    = "project"    // tied to a project id
	KindGroup      = "group"      // private group of chosen people (and agents)
	KindDM         = "dm"         // exactly two people
)

// Member kinds.
const (
	MemberUser  = "user"
	MemberAgent = "agent"
)

// Limits.
const (
	MaxBody    = 4000
	MaxName    = 80
	MaxMembers = 200
	PageSize   = 50
)

var (
	ErrForbidden = errors.New("peoplechat: forbidden")
	ErrNotFound  = errors.New("peoplechat: not found")
	ErrInvalid   = errors.New("peoplechat: invalid")
)

// Channel is one conversation space.
type Channel struct {
	ID            string     `json:"id"`
	Kind          string     `json:"kind"`
	Name          string     `json:"name"`
	ScopeKey      string     `json:"scope_key,omitempty"`
	CreatedBy     string     `json:"created_by"`
	LastSeq       int64      `json:"last_seq"`
	LastMessageAt *time.Time `json:"last_message_at,omitempty"`
	Archived      bool       `json:"archived"`
	CreatedAt     time.Time  `json:"created_at"`
}

// Member belongs to a channel; LastReadSeq is its read marker.
type Member struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Role        string `json:"role"` // owner | member
	LastReadSeq int64  `json:"last_read_seq"`
}

// Mention points at a person or an agent named in a message.
type Mention struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// Message is one chat line.
type Message struct {
	ID         string    `json:"id"`
	ChannelID  string    `json:"channel_id"`
	Seq        int64     `json:"seq"`
	AuthorKind string    `json:"author_kind"`
	AuthorID   string    `json:"author_id"`
	Body       string    `json:"body"`
	Mentions   []Mention `json:"mentions"`
	CreatedAt  time.Time `json:"created_at"`
}

// ChannelView is a channel as one person sees it in their list.
type ChannelView struct {
	Channel
	Members []Member `json:"members"`
	// Unread: messages after the person's read marker that they did not write.
	Unread int64 `json:"unread"`
	// MentionsUnread: unread messages that mention the person.
	MentionsUnread int64  `json:"mentions_unread"`
	Preview        string `json:"preview,omitempty"`
}

// Person is an entry of the organization directory (for DMs and mentions).
type Person struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// AgentRef is an agent of the organization.
type AgentRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Unread is the pure unread count: messages after the read marker. A marker beyond the last sequence counts 0.
func Unread(lastSeq, lastRead int64) int64 {
	if lastRead < 0 {
		lastRead = 0
	}
	if n := lastSeq - lastRead; n > 0 {
		return n
	}
	return 0
}

// DMKey is the stable key of a DM between two users, independent of the order.
func DMKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}
