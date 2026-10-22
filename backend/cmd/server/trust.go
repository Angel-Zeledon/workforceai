package main

import (
	"context"
	"log/slog"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/config"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/quality"
	"aiworkforce/backend/internal/trust"
)

// wireTrust builds the trust ladder (docs/architecture/trust-ladder.md, B1). With Postgres the evidence is SQL over
// approvals, tasks and audit_logs and the ladder's state lives in migration 791; without it the evidence comes from
// the application store and its audit trail and the state is kept in memory. The store must be able to write
// agents.autonomy (application.ConfigStore); otherwise the ladder is off.
func wireTrust(cfg config.Config, log *slog.Logger, pg *postgres.Store, store application.Store, rec *application.Recorder,
	ctl *controls.Service, qualitySvc *quality.Service) *trust.Service {
	cs, ok := store.(application.ConfigStore)
	if !ok {
		return nil
	}
	tcfg := trust.Config{Agents: store, SetAutonomy: cs.SetAgentAutonomy, Rec: rec, OrgID: cfg.App.OrgID, Log: log}
	if pg != nil {
		ts := &postgres.TrustStore{S: pg}
		tcfg.Store, tcfg.Signals = ts, ts
	} else {
		audit, _ := store.(application.AuditStore)
		tcfg.Store, tcfg.Signals = trust.NewMemStore(), trust.StoreSignals{Store: store, Audit: audit}
	}
	if qualitySvc != nil {
		tcfg.Quality = func(ctx context.Context, org, agentID string) (*trust.Quality, error) {
			rep, err := qualitySvc.ForAgent(application.WithOrg(ctx, org), agentID, quality.DefaultWindowDays)
			if err != nil || len(rep.Agents) == 0 || rep.Agents[0].Score == nil {
				return nil, err
			}
			return &trust.Quality{Score: *rep.Agents[0].Score, Trend: rep.Agents[0].Trend}, nil
		}
	}
	if ctl != nil {
		// Raising autonomy waits while the kill switch is on, the organization or the agent is read-only, or the
		// agent is paused (the same check every side-effecting action passes).
		tcfg.Blocked = func(ctx context.Context, org, agentID string) string {
			if v := ctl.Check(ctx, org, agentID, true); !v.Allowed {
				return v.Code
			}
			return ""
		}
	}
	return trust.New(tcfg)
}
