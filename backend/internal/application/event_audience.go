package application

import (
	"context"
	"encoding/json"
	"slices"

	"aiworkforce/backend/internal/domain"
)

// AudienceFunc (D3, equipos-oficinas.md §4.1) says who may receive a live event that names no audience of its own.
// scoped=false: the whole organization (as before). scoped=true: only users (possibly none); such an event is kept
// out of the organization's event log, while its activity line is kept (GET /activity filters it per person).
type AudienceFunc func(ctx context.Context, org string, ev domain.Event) (users []string, scoped bool)

// nobody is the audience of a scoped event no one may receive: an id no socket has, so the hub delivers it to no
// one (an empty audience would mean the whole organization) while the observers of the bus still see the event.
const nobody = "\x00nobody"

// SetAudienceFor installs (nil removes) the rule that scopes the live events without an audience of their own.
// Safe to call while events are being emitted.
func (r *Recorder) SetAudienceFor(fn AudienceFunc) {
	if fn == nil {
		r.audienceFor.Store(nil)
		return
	}
	r.audienceFor.Store(&fn)
}

// scope applies the installed AudienceFunc to ev.
func (r *Recorder) scope(ctx context.Context, ev domain.Event) ([]string, bool) {
	fn := r.audienceFor.Load()
	if fn == nil {
		return nil, false
	}
	users, scoped := (*fn)(ctx, ev.OrgID, ev)
	if !scoped {
		return nil, false
	}
	if len(users) == 0 {
		return []string{nobody}, true
	}
	return users, true
}

type workAgentsKey struct{}

// WithWorkAgents marks the events emitted under ctx as work of these agents too (a request addressed to an agent
// before its plan exists: the chat that started it, a preset plan).
func WithWorkAgents(ctx context.Context, agents ...string) context.Context {
	if len(agents) == 0 {
		return ctx
	}
	return context.WithValue(ctx, workAgentsKey{}, append(slices.Clone(WorkAgentsFrom(ctx)), agents...))
}

// addressedAgents are the agents a new request is addressed to before its plan exists: the agent of the chat that
// started it (and whoever took the work over from it) and the agents of a preset plan.
func addressedAgents(rs *run) []string {
	var out []string
	if l := rs.chat; l != nil {
		out = append(out, l.speaker, l.reassignTo)
	}
	if rs.preset != nil {
		for _, t := range rs.preset.Tasks {
			out = append(out, t.AgentID)
		}
	}
	return slices.DeleteFunc(out, func(a string) bool { return a == "" || a == assistantID })
}

// WorkAgentsFrom returns the agents set by WithWorkAgents.
func WorkAgentsFrom(ctx context.Context) []string {
	a, _ := ctx.Value(workAgentsKey{}).([]string)
	return a
}

// EventRefs is what an event is about, read from its envelope, its payload and its context: the agents, requests,
// tasks and conversations it names, and the people it is for (who asked, who wrote).
type EventRefs struct {
	Agents, Requests, Tasks, Conversations, People []string
}

// Keys of a payload (at any depth) that name each kind of reference. Values that are not ids of that kind (a
// "from" that is "user") are harmless: an unknown agent belongs to General.
var (
	agentKeys   = map[string]bool{"agent_id": true, "from": true, "to": true, "from_agent_id": true, "to_agent_id": true}
	agentsKeys  = map[string]bool{"agent_ids": true, "contributors": true}
	requestKeys = map[string]bool{"request_id": true}
	taskKeys    = map[string]bool{"task_id": true, "parent_task_id": true}
	convKeys    = map[string]bool{"conversation_id": true}
	personKeys  = map[string]bool{"requested_by": true, "author_id": true}
)

// RefsOf reads the references of an event.
func RefsOf(ctx context.Context, ev domain.Event) EventRefs {
	var r EventRefs
	if ev.AgentID != "" {
		r.Agents = append(r.Agents, ev.AgentID)
	}
	r.Agents = append(r.Agents, WorkAgentsFrom(ctx)...)
	if ev.Payload != nil {
		if raw, err := json.Marshal(ev.Payload); err == nil {
			var v any
			if json.Unmarshal(raw, &v) == nil {
				r.walk(v, 0)
			}
		}
	}
	for _, s := range []*[]string{&r.Agents, &r.Requests, &r.Tasks, &r.Conversations, &r.People} {
		slices.Sort(*s)
		*s = slices.Compact(*s)
	}
	return r
}

func (r *EventRefs) walk(v any, depth int) {
	if depth > 6 {
		return
	}
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if s, ok := val.(string); ok && s != "" {
				switch {
				case agentKeys[k]:
					r.Agents = append(r.Agents, s)
				case requestKeys[k]:
					r.Requests = append(r.Requests, s)
				case taskKeys[k]:
					r.Tasks = append(r.Tasks, s)
				case convKeys[k]:
					r.Conversations = append(r.Conversations, s)
				case personKeys[k]:
					if p := PersonID(s); p != "" {
						r.People = append(r.People, p)
					}
				}
				continue
			}
			if list, ok := val.([]any); ok && agentsKeys[k] {
				for _, e := range list {
					if s, ok := e.(string); ok && s != "" {
						r.Agents = append(r.Agents, s)
					}
				}
				continue
			}
			r.walk(val, depth+1)
		}
	case []any:
		for _, e := range x {
			r.walk(e, depth+1)
		}
	}
}
