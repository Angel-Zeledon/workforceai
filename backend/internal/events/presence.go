package events

import (
	"time"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/domain"
)

// Event types of the human-presence layer (docs/architecture/presence.md). All additive: a client that
// does not know them ignores them. Presence frames go to the whole organization (they carry no private
// content); chat frames are addressed (Event.Audience) to the members of the channel.
const (
	EvPresenceJoined    = "presence.joined"    // a person came online
	EvPresenceLeft      = "presence.left"      // the last socket of a person closed
	EvPresenceMoved     = "presence.moved"     // throttled position update
	EvPresenceStatus    = "presence.status"    // available | focus | meeting | away
	EvPresenceProximity = "presence.proximity" // two people came near / went apart (hook point of the call stream)

	EvPeopleChatMessage = "pchat.message" // a message in a people channel (addressed to its members)
	EvPeopleChatChannel = "pchat.channel" // a channel was created or its members changed
	EvPeopleChatRead    = "pchat.read"    // a person read a channel (addressed to that person: syncs their other tabs)
)

// NewEvent builds a frame for an organization; audience (optional) restricts it to those users.
func NewEvent(org, typ string, payload any, audience ...string) domain.Event {
	return domain.Event{ID: uuid.NewString(), Type: typ, TS: time.Now().UTC(), OrgID: org, Payload: payload, Audience: audience}
}
