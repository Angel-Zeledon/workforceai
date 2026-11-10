package main

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

// officeAudience (D3, equipos-oficinas.md §4.1) is the recorder's AudienceFor: a live event about the work of an
// agent of a private office (the event's agent, or the agent of the task, request, approval or conversation it
// names) reaches only the members of that office, admins included only as members, plus whoever asked for the
// request it belongs to (they always see their request). The work of agents of several private offices needs
// membership of all of them (the rule of GET /requests). Everything else reaches the whole organization as before.
// An unreadable offices model fails closed: the event reaches nobody.
func officeAudience(svc *offices.Service, store application.Store, requestedBy func(ctx context.Context, org, requestID string) string,
	log *slog.Logger) application.AudienceFunc {
	return func(ctx context.Context, org string, ev domain.Event) ([]string, bool) {
		if svc == nil || unscopedEvents[ev.Type] {
			return nil, false
		}
		m, err := svc.Model(ctx, org)
		if err != nil {
			log.Warn("event audience: offices model", "type", ev.Type, "err", err)
			return nil, true
		}
		if !hasPrivateOffice(m) {
			return nil, false
		}
		refs := application.RefsOf(ctx, ev)
		ctx = application.WithOrg(ctx, org)
		agents, requests, people := refs.Agents, refs.Requests, refs.People
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
		var members []string
		sealed := false
		seen := map[string]bool{}
		for _, a := range uniqueIDs(agents) {
			o, ok := m.Office(m.Agent(a).OfficeID)
			if !ok || o.General || o.Visibility != offices.Private || seen[o.ID] {
				continue
			}
			seen[o.ID] = true
			if !sealed {
				members, sealed = slices.Clone(o.Members), true
				continue
			}
			members = slices.DeleteFunc(members, func(u string) bool { return !slices.Contains(o.Members, u) })
		}
		if !sealed {
			return nil, false
		}
		return uniqueIDs(append(members, people...)), true
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
