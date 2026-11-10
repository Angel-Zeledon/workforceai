package application

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/audit"
	"aiworkforce/backend/internal/domain"
)

// Recorder persists and publishes events, audit logs and activity items.
// It is the single place where "every relevant action writes audit + event".
type Recorder struct {
	OrgID string
	Store Store
	Pub   Publisher
	Log   *slog.Logger

	// audienceFor (D3) scopes the live events that name no audience of their own (SetAudienceFor).
	audienceFor atomic.Pointer[AudienceFunc]
}

// Action describes something that happened.
type Action struct {
	Type      string // event type (WS contract)
	AgentID   string // envelope agent_id and audit actor (default "system")
	Entity    string // audit entity kind
	EntityID  string
	Payload   any
	Text      string // when set, an activity item is logged as well
	SkipAudit bool
	// Audience (E0) limits the live delivery to these user ids (a private chat thread). Such an event is
	// NOT kept in the organization's event log (it would be readable by everyone) and logs no activity.
	// Without one, the recorder's AudienceFor (D3) may scope the event to the members of a private office: kept
	// out of the event log too, but its activity line is kept (GET /activity filters it per person).
	Audience []string
}

func newID() string { return uuid.NewString() }

// Emit saves + publishes the event, writes the audit log and optional activity.
func (r *Recorder) Emit(ctx context.Context, a Action) {
	// Persist even if the caller's context was cancelled (e.g. demo reset).
	ctx = context.WithoutCancel(ctx)
	now := time.Now().UTC()
	ev := domain.Event{ID: newID(), Type: a.Type, TS: now, OrgID: r.org(ctx), AgentID: a.AgentID, Payload: a.Payload, Audience: a.Audience}
	private := len(a.Audience) > 0 // E0: the caller named who receives it
	var office []string            // D3: who may see the work of the private office this event is about
	scoped := false
	if !private {
		office, scoped = r.scope(ctx, ev)
		ev.Audience = office
	}
	if !private && !scoped {
		if err := r.Store.SaveEvent(ctx, r.org(ctx), ev); err != nil {
			r.Log.Warn("save event", "type", a.Type, "err", err)
		}
	}
	if err := r.Pub.Publish(ctx, ev); err != nil {
		r.Log.Warn("publish event", "type", a.Type, "err", err)
	}
	if !a.SkipAudit {
		actor := a.AgentID
		if actor == "" {
			actor = "system"
		}
		r.Audit(ctx, domain.AuditLog{Actor: actor, Action: a.Type, Entity: a.Entity, EntityID: a.EntityID, Details: a.Payload})
	}
	if a.Text != "" && !private {
		r.activity(ctx, a.AgentID, a.Type, a.Text, office)
	}
}

// Audit writes an audit log entry only. The trail holds metadata, never
// content or secrets: details are scrubbed here, the single entry point, and
// the entry is tagged with the request it belongs to.
func (r *Recorder) Audit(ctx context.Context, a domain.AuditLog) {
	a.ID, a.TS = newID(), time.Now().UTC()
	a.Details = audit.Scrub(a.Details)
	a.Actor, a.EntityID = audit.CleanField(a.Actor), audit.CleanField(a.EntityID)
	if a.RequestID == "" {
		if a.RequestID = RequestIDFrom(ctx); a.RequestID == "" && a.Entity == "request" {
			a.RequestID = a.EntityID
		}
	}
	if err := r.Store.AddAudit(context.WithoutCancel(ctx), r.org(ctx), a); err != nil {
		r.Log.Warn("audit", "action", a.Action, "err", err)
	}
}

// Activity stores an activity item and emits activity.logged (D3: only to the members of the agent's private
// office).
func (r *Recorder) Activity(ctx context.Context, agentID, kind, text string) {
	ctx = context.WithoutCancel(ctx)
	aud, _ := r.scope(ctx, domain.Event{Type: domain.EvActivityLogged, OrgID: r.org(ctx), AgentID: agentID})
	r.activity(ctx, agentID, kind, text, aud)
}

// activity stores an activity item and emits activity.logged to aud (nil: the whole organization).
func (r *Recorder) activity(ctx context.Context, agentID, kind, text string, aud []string) {
	item := domain.ActivityItem{ID: newID(), TS: time.Now().UTC(), Kind: kind, Text: text}
	if agentID != "" {
		item.AgentID = &agentID
	}
	if err := r.Store.AddActivity(ctx, r.org(ctx), item); err != nil {
		r.Log.Warn("activity", "err", err)
		return
	}
	ev := domain.Event{ID: newID(), Type: domain.EvActivityLogged, TS: item.TS, OrgID: r.org(ctx), AgentID: agentID,
		Payload: map[string]any{"item": item}, Audience: aud}
	if err := r.Pub.Publish(ctx, ev); err != nil {
		r.Log.Warn("publish activity", "err", err)
	}
}
