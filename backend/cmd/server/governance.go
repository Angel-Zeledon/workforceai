package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/governance"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/webhooks"
)

// wireGovernance connects the company-level controls (docs/architecture/governance.md): API tokens, usage
// reports, department limits, the retention purge job and the webhook worker. Everything needs Postgres (RLS);
// without it the governance routes are not mounted. RETENTION_JOB=off disables the purge job (policies can be
// edited, previewed and run by hand, but nothing runs on its own).
func wireGovernance(ctx context.Context, log *slog.Logger, pg *postgres.Store, store application.Store, rec *application.Recorder,
	auditSvc *application.AuditService, orch *application.Orchestrator, sealer webhooks.Sealer, wh *webhooks.Service, deps *api.Deps) {
	if pg == nil {
		return
	}
	lister, _ := store.(application.OrgLister)
	gs := &governance.PgStore{DB: pg, Pool: pg.Pool()}

	wh.Store, wh.Sealer = &webhooks.PgStore{DB: pg}, sealer
	if lister != nil {
		wh.Start(ctx, lister)
	}

	purger := &governance.Purger{Store: gs, Orgs: lister, Log: log, Protected: map[string]bool{domain.DemoOrgID: true},
		Audit: func(c context.Context, org, action string, d map[string]any) {
			rec.Audit(application.WithOrg(c, org), domain.AuditLog{Actor: "system", Action: action, Entity: "org", EntityID: org, Details: d})
		}}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("RETENTION_JOB"))); v == "off" || v == "false" || v == "0" {
		log.Warn("RETENTION_JOB=off: retention policies are not applied automatically")
	} else if lister != nil {
		purger.Start(ctx, time.Hour)
	}

	g := &api.GovernanceDeps{Tokens: &governance.TokenService{Store: gs}, Admin: gs, Reports: gs, Retention: gs, Purger: purger, Webhooks: wh}
	if auditSvc != nil { // avoid a non-nil interface holding a nil pointer
		g.Audit = auditSvc
	}
	if orch != nil { // department monthly limits are enforced by the budget guard (budget_dept.go)
		orch.Budget().DepartmentLimitsFor = func(c context.Context, org string) map[string]float64 {
			m, err := gs.DepartmentLimits(c, org)
			if err != nil {
				return nil
			}
			return m
		}
		g.OnLimitChange = orch.Budget().Wake
	}
	deps.Governance = g
}
