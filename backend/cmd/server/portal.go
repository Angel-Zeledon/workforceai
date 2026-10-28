package main

import (
	"context"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/config"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/portal"
	"aiworkforce/backend/internal/projects"
)

// wirePortal builds the client portal (docs/architecture/client-portal.md, B13). With Postgres the branding
// and the links live in migration 798; without it they are kept in memory (the open demo). It needs the
// projects service; the deliverables come from the artifacts service when there is one.
func wirePortal(cfg config.Config, pg *postgres.Store, rec *application.Recorder, ps *projects.Service, as *artifacts.Service, authStore auth.Store) *portal.Service {
	if ps == nil {
		return nil
	}
	pc := portal.Config{OrgID: cfg.App.OrgID,
		Project: func(ctx context.Context, org, id string) (projects.Detail, error) {
			return ps.Get(application.WithOrg(ctx, org), id)
		},
		Audit: func(ctx context.Context, org, actor, action, id string, d map[string]any) {
			rec.Audit(application.WithOrg(ctx, org), domain.AuditLog{Actor: actor, Action: action, Entity: "portal_link", EntityID: id, Details: d})
		}}
	if as != nil {
		pc.Deliverables = func(ctx context.Context, org, projectID string) ([]artifacts.Meta, error) {
			return as.List(application.WithOrg(ctx, org), artifacts.Filter{ProjectID: projectID})
		}
	}
	if authStore != nil {
		pc.OrgName = authStore.OrgName
	}
	if pg != nil {
		pc.Store = &postgres.PortalStore{S: pg}
	} else {
		pc.Store = portal.NewMemStore()
	}
	return portal.New(pc)
}
