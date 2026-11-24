package application

import (
	"context"
	"errors"
	"strings"

	"aiworkforce/backend/internal/domain"
)

// Office scope of a request (docs/superpowers/specs/2026-11-23-oficinas-agentes-habilidades-design.md §4, §5, §8).
//
// Every request is asked from ONE office. That office travels in the context (WithRequestOffice) and is the ONLY place
// the isolation rule lives: the office whose context an agent uses is OfficeScope.Effective(ctx, agent), which is the
// request's office when the agent works for it and the agent's base office otherwise, never another one. Everything
// that hands an agent stored content takes the office from there:
//
//	memory of the company + of THAT office (memory.Service, Config.AgentOffice), the notes (offices.RecallNotes), the
//	files of that office and the ones shared with it (drive.AgentFS through AgentOffices), the skills of an office
//	(skills.AgentSkills), the connections of an office (connections.Service), the agent's own key/value memory
//	(agentMemoryScope below), and the chat history (officeHistory below).
//
// Office "" means unknown (a schedule, an old client, history written before this existed): the agent keeps to its
// base office and nothing else. Without an OfficeScope installed (no offices) none of this changes anything.

type requestOfficeKey struct{}

// WithRequestOffice returns ctx carrying the office the work is asked from ("" = unknown; the office id as the
// offices model names it, General included).
func WithRequestOffice(ctx context.Context, office string) context.Context {
	office = strings.TrimSpace(office)
	if office == "" {
		return ctx
	}
	return context.WithValue(ctx, requestOfficeKey{}, office)
}

// WithoutRequestOffice returns ctx with no request office: what the agent's BASE office is, whatever it is asked for.
func WithoutRequestOffice(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestOfficeKey{}, "")
}

// RequestOfficeFrom is the office ctx's work is asked from ("" = unknown).
func RequestOfficeFrom(ctx context.Context) string {
	s, _ := ctx.Value(requestOfficeKey{}).(string)
	return s
}

// OfficeScope answers where an agent works. Implemented in cmd/server over the offices model.
type OfficeScope interface {
	// WorksFor reports whether the agent works for the office: its base office, or another one that accepted it
	// (only with MULTI_OFFICE_AGENTS on). Personal agents answer true: they belong to their person, not to an office.
	WorksFor(ctx context.Context, org, agentID, office string) (bool, error)
	// Effective is the office whose context the agent uses under ctx's request office, and its base office. An error
	// means unknown: callers fail closed.
	Effective(ctx context.Context, org, agentID string) (effective, base string, err error)
}

// SetOfficeScope installs the offices lookup (nil removes it: requests are not scoped).
func (o *Orchestrator) SetOfficeScope(s OfficeScope) { o.officeScope = s }

// OfficeOfRequest is the office a request was asked from ("" = General's old requests, unknown or a request that does
// not exist). An error means the store could not tell: callers fail closed.
func (o *Orchestrator) OfficeOfRequest(ctx context.Context, org, requestID string) (string, error) {
	r, err := o.store.GetRequest(ctx, org, requestID)
	if errors.Is(err, domain.ErrNotFound) {
		return "", nil
	}
	return r.OfficeID, err
}

// OfficeAgents is officeAgents for the services around the orchestrator (the team debates).
func (o *Orchestrator) OfficeAgents(ctx context.Context, office string, agents []domain.Agent) []domain.Agent {
	return o.officeAgents(ctx, office, agents)
}

// officeAgents keeps the agents that work for the office (plus the assistant). An agent whose office cannot be told
// is dropped (fail closed). office "" or no scope: everyone, as before.
func (o *Orchestrator) officeAgents(ctx context.Context, office string, agents []domain.Agent) []domain.Agent {
	if office == "" || o.officeScope == nil {
		return agents
	}
	out := make([]domain.Agent, 0, len(agents))
	for _, a := range agents {
		if a.ID == assistantID {
			out = append(out, a)
			continue
		}
		if ok, err := o.officeScope.WorksFor(ctx, o.org(ctx), a.ID, office); err == nil && ok {
			out = append(out, a)
		}
	}
	return out
}

// Scope of the agent's own key/value memory (domain.Memory). The base office keeps writing "agent" (what it has
// always been); work for another office is kept apart under "agent@<office>".
const memoryScopeAgent = "agent"

// agentMemoryScope is the Memory.Scope an agent writes under for ctx's request, and the one it reads.
func (o *Orchestrator) agentMemoryScope(ctx context.Context, agentID string) string {
	if o.officeScope == nil || RequestOfficeFrom(ctx) == "" {
		return memoryScopeAgent
	}
	eff, base, err := o.officeScope.Effective(ctx, o.org(ctx), agentID)
	if err != nil {
		return memoryScopeAgent + "@?" // unknown: a scope nobody reads
	}
	if eff == base {
		return memoryScopeAgent
	}
	return memoryScopeAgent + "@" + eff
}

// scopedMemory keeps the entries of the agent's memory that belong to the office of ctx's request: the base office's
// ("agent") or exactly "agent@<this office>"; every other scope (chat, conversation...) is not an office's.
func (o *Orchestrator) scopedMemory(ctx context.Context, agentID string, mem []domain.Memory) []domain.Memory {
	want := o.agentMemoryScope(ctx, agentID)
	out := mem[:0:0]
	for _, m := range mem {
		if m.Scope == memoryScopeAgent || strings.HasPrefix(m.Scope, memoryScopeAgent+"@") {
			if m.Scope != want {
				continue
			}
		}
		out = append(out, m)
	}
	return out
}

// officeHistory keeps the messages of the office the turn is asked from. An unknown office reads only what was
// written without one (General and everything earlier): never another office's conversation.
func officeHistory(ctx context.Context, msgs []domain.Message) []domain.Message {
	want := messageOffice(RequestOfficeFrom(ctx))
	out := msgs[:0:0]
	for _, m := range msgs {
		if messageOffice(m.OfficeID) == want {
			out = append(out, m)
		}
	}
	return out
}

// stampOffice sets the office of ctx's request on a message that has none yet.
func stampOffice(ctx context.Context, m *domain.Message) {
	if m.OfficeID == "" {
		m.OfficeID = messageOffice(RequestOfficeFrom(ctx))
	}
}

// generalOfficeID is how the offices model names General. Messages of General are stored without an office, like all
// the history written before offices scoped work, so one reading General reads both.
const generalOfficeID = "general"

func messageOffice(id string) string {
	if id == generalOfficeID {
		return ""
	}
	return id
}
