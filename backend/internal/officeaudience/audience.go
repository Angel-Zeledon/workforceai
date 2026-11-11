// Package officeaudience is the recorder's audience rule of D3 (equipos-oficinas.md §4.1): the live events about the
// work of a private office reach its members only.
package officeaudience

import (
	"context"
	"log/slog"
	"slices"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/offices"
)

// unscopedEvents are about the organization or an agent's identity, not about an agent's work: they keep reaching
// everybody (an admin sees every agent, the directory lists them).
var unscopedEvents = map[string]bool{domain.EvAgentCreated: true, domain.EvMetricsUpdated: true, domain.EvHello: true}

// echoToActor are the events of a change a person makes on an agent (its autonomy or trust level): the person who
// made it is part of the audience, so an admin outside a private office still sees the effect of their own action.
var echoToActor = map[string]bool{domain.EvAutonomyChanged: true, domain.EvTrustProposal: true}

// New returns the recorder's AudienceFor: a live event about the work of an agent of a private office (the event's
// agent, or the agent of the task, request, approval or conversation it names) reaches only the members of that
// office, admins included only as members, plus whoever asked for the request it belongs to (they always see their
// request: the person in the event's context, or the request's run meta through requestedBy). An event about a resource
// of a private office (a connection or skill naming its office_id) is sealed the same way. The work of agents of
// several private offices needs membership of all of them (the rule of GET /requests). The audience's Agent is the
// private agent that sealed the event (the first in id order), so its activity line is filed under it. Everything else
// reaches the whole organization as before. An unreadable offices model fails closed: the event reaches nobody and
// keeps no activity line.
func New(svc *offices.Service, store application.Store, requestedBy func(ctx context.Context, org, requestID string) string,
	log *slog.Logger) application.AudienceFunc {
	return func(ctx context.Context, org string, ev domain.Event) (application.Audience, bool) {
		if svc == nil || unscopedEvents[ev.Type] {
			return application.Audience{}, false
		}
		m, err := svc.Model(ctx, org)
		if err != nil {
			log.Warn("event audience: offices model", "type", ev.Type, "err", err)
			return application.Audience{}, true
		}
		if !hasPrivateOffice(m) {
			return application.Audience{}, false
		}
		refs := application.RefsOf(ctx, ev)
		ctx = application.WithOrg(ctx, org)
		agents, requests, people := refs.Agents, refs.Requests, refs.People
		if echoToActor[ev.Type] {
			// the person who made the change receives its echo even when they are not in the office
			if p := application.PersonFrom(ctx); p != "" {
				people = append(people, p)
			}
		}
		for _, id := range refs.Tasks {
			if t, err := store.GetTask(ctx, org, id); err == nil {
				agents, requests = append(agents, t.AgentID), append(requests, t.RequestID)
			}
		}
		for _, id := range refs.Conversations {
			if c, err := store.GetConversation(ctx, org, id); err == nil && c.RequestID != nil {
				requests = append(requests, *c.RequestID)
			}
		}
		for _, id := range uniqueIDs(requests) {
			if tasks, err := store.ListTasksByRequest(ctx, org, id); err == nil {
				for _, t := range tasks {
					agents = append(agents, t.AgentID)
				}
			}
			if requestedBy != nil {
				if p := requestedBy(ctx, org, id); p != "" {
					people = append(people, p)
				}
			}
		}
		var aud application.Audience
		sealed := false
		seen := map[string]bool{}
		seal := func(officeID, agent string) {
			o, ok := m.Office(officeID)
			if !ok || o.General || o.Visibility != offices.Private || seen[o.ID] {
				return
			}
			seen[o.ID] = true
			if !sealed {
				aud, sealed = application.Audience{Users: slices.Clone(o.Members), Agent: agent}, true
				return
			}
			aud.Users = slices.DeleteFunc(aud.Users, func(u string) bool { return !slices.Contains(o.Members, u) })
			if aud.Agent == "" {
				aud.Agent = agent
			}
		}
		for _, a := range uniqueIDs(agents) {
			seal(m.Agent(a).OfficeID, a)
		}
		// A resource of a department (a connection, a skill: its office_id) follows the department's visibility.
		for _, id := range refs.Offices {
			seal(id, "")
		}
		if !sealed {
			return application.Audience{}, false
		}
		aud.Users = uniqueIDs(append(aud.Users, people...))
		return aud, true
	}
}

// hasPrivateOffice reports whether any office of the model is private (otherwise nothing is scoped: no reads).
func hasPrivateOffice(m *offices.Model) bool {
	for _, o := range m.Offices() {
		if !o.General && o.Visibility == offices.Private {
			return true
		}
	}
	return false
}

// uniqueIDs returns the non-empty ids, sorted and without repeats.
func uniqueIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
