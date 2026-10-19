package main

import (
	"context"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/orgchart"
	"aiworkforce/backend/internal/projects"
)

// wireOrgChart builds the corporate organization model (docs/architecture/organization.md): departments,
// job descriptions, delegation rules and industry templates. It also installs the schedule gate: work for
// an agent outside its working hours waits in the queue (the projects gate, when there is one, keeps
// deciding everything else).
func wireOrgChart(pg *postgres.Store, store application.Store, authStore auth.Store, rec *application.Recorder, orch *application.Orchestrator,
	proj *projects.Service, defaultOrg string) *orgchart.Service {
	var st orgchart.Store = orgchart.NewMemoryStore()
	if pg != nil {
		st = &postgres.OrgChartStore{S: pg}
	}
	svc := &orgchart.Service{Store: st, Agents: store, DefaultOrg: defaultOrg,
		Audit: func(ctx context.Context, a domain.AuditLog) { rec.Audit(ctx, a) }}
	if w, ok := store.(orgchart.AgentWriter); ok {
		svc.Writer = w
	}
	if c, ok := store.(orgchart.CapSetter); ok {
		svc.Caps = c
	}
	if proj != nil {
		svc.Templates = proj
	}
	if authStore != nil {
		svc.People = func(ctx context.Context, org string) ([]orgchart.Person, error) {
			ms, err := authStore.ListMembers(ctx, org)
			if err != nil {
				return nil, err
			}
			out := make([]orgchart.Person, 0, len(ms))
			for _, m := range ms {
				out = append(out, orgchart.Person{UserID: m.UserID, Name: m.Name, Email: m.Email, Role: string(m.Role)})
			}
			return out, nil
		}
	}
	var inner application.TaskGate
	if proj != nil {
		inner = proj
	}
	orch.SetDelegation(svc.Checker()) // delegation rules (organization.md, section 3)
	svc.OnChange = orch.WakeHeld      // an edited schedule or rule releases held work at once
	orch.SetHoldStore(holdStoreFor(pg))
	orch.SetTaskGate(orgchart.TaskGate{Inner: inner, Svc: svc})
	return svc
}

// holdStoreFor picks the store of schedule holds: Postgres (migration 760) when there is a database, memory
// otherwise (the holds then do not survive a restart).
func holdStoreFor(pg *postgres.Store) application.HoldStore {
	if pg != nil {
		return &postgres.HoldStore{S: pg}
	}
	return &application.MemoryHolds{}
}

// startHoldWatcher resumes, every 30 s, the held tasks whose opening passed with nobody running them (the
// process restarted across the opening). Organizations are listed on each pass, so new ones are included.
// The returned channel closes when the watcher stopped (ctx ended).
func startHoldWatcher(ctx context.Context, orch *application.Orchestrator, store application.Store, every time.Duration) <-chan struct{} {
	return orch.StartHoldWatcher(ctx, every, func() []string {
		if l, ok := store.(application.OrgLister); ok {
			if ids, err := l.ListOrgIDs(ctx); err == nil {
				return ids
			}
		}
		return nil
	})
}
