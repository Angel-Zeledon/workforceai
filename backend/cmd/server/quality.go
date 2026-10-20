package main

import (
	"context"
	"log/slog"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/config"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/quality"
)

// wireQuality builds the advisory quality score (docs/architecture/quality-score.md). With Postgres the
// signals are SQL over approvals, tasks and events and the golden cases live in migration 789; without it
// the signals come from the application store (no retry count) and the cases are kept in memory.
func wireQuality(cfg config.Config, log *slog.Logger, pg *postgres.Store, store application.Store, rec *application.Recorder) *quality.Service {
	qcfg := quality.Config{OrgID: cfg.App.OrgID, Log: log,
		Agents: func(ctx context.Context, org string) ([]domain.Agent, error) { return store.ListAgents(ctx, org) },
		Audit: func(ctx context.Context, org, actor, action, id string, d map[string]any) {
			rec.Audit(application.WithOrg(ctx, org), domain.AuditLog{Actor: actor, Action: action, Entity: "quality_case", EntityID: id, Details: d})
		}}
	if pg != nil {
		qs := &postgres.QualityStore{S: pg}
		qcfg.Store, qcfg.Signals = qs, qs
	} else {
		qcfg.Store, qcfg.Signals = quality.NewMemStore(), quality.StoreSignals{Store: store}
	}
	return quality.New(qcfg)
}
