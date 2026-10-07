package application

import (
	"context"
	"log/slog"
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
}

func newID() string { return uuid.NewString() }

// Emit saves + publishes the event, writes the audit log and optional activity.
func (r *Recorder) Emit(ctx context.Context, a Action) {
	// Persist even if the caller's context was cancelled (e.g. demo reset).
	ctx = context.WithoutCancel(ctx)
	now := time.Now().UTC()
	ev := domain.Event{ID: newID(), Type: a.Type, TS: now, OrgID: r.org(ctx), AgentID: a.AgentID, Payload: a.Payload}
	if err := r.Store.SaveEvent(ctx, r.org(ctx), ev); err != nil {
		r.Log.Warn("save event", "type", a.Type, "err", err)
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
	if a.Text != "" {
		r.Activity(ctx, a.AgentID, a.Type, a.Text)
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

// Activity stores an activity item and emits activity.logged.
func (r *Recorder) Activity(ctx context.Context, agentID, kind, text string) {
	ctx = context.WithoutCancel(ctx)
	item := domain.ActivityItem{ID: newID(), TS: time.Now().UTC(), Kind: kind, Text: text}
	if agentID != "" {
		item.AgentID = &agentID
	}
	if err := r.Store.AddActivity(ctx, r.org(ctx), item); err != nil {
		r.Log.Warn("activity", "err", err)
		return
	}
	ev := domain.Event{ID: newID(), Type: domain.EvActivityLogged, TS: item.TS, OrgID: r.org(ctx), AgentID: agentID, Payload: map[string]any{"item": item}}
	if err := r.Pub.Publish(ctx, ev); err != nil {
		r.Log.Warn("publish activity", "err", err)
	}
}
