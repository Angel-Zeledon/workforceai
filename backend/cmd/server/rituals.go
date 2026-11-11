package main

import (
	"context"
	"log/slog"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/config"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/offices"
	"aiworkforce/backend/internal/quality"
	"aiworkforce/backend/internal/rituals"
)

// wireRituals builds the daily stand-up and weekly retro (docs/architecture/rituals.md) and starts their schedule.
// With Postgres the history lives in migration 793; without it, in memory. The rituals read tasks and approvals from
// the application store, ask the execution guard (kill switch, pause, operating hours), use the summarize path only
// when the organization turned the model summary on, post their note in the office chat (without the agents of a
// private department) and apply accepted
// instruction notes through the agent work profile.
func wireRituals(ctx context.Context, cfg config.Config, log *slog.Logger, pg *postgres.Store, store application.Store, rec *application.Recorder,
	orch *application.Orchestrator, orgCfg *application.OrgConfig, qualitySvc *quality.Service, profiles *application.ProfileService,
	officesSvc *offices.Service) *rituals.Service {
	var st rituals.Store = rituals.NewMemStore()
	if pg != nil {
		st = &postgres.RitualStore{S: pg}
	}
	rc := rituals.Config{Store: st, Core: store, OrgID: cfg.App.OrgID, Log: log,
		Guard: orch.AdmitRitual,
		Summarize: func(c context.Context, in application.SummarizeRequest) (application.SummaryResult, error) {
			return orch.Summarize(c, "assistant", in)
		},
		Notice: func(c context.Context, text string) error { return orch.PostSystemNotice(c, domain.ChatOffice, text) },
		Emit:   rec.Emit,
		Audit:  rec.Audit,
	}
	if officesSvc != nil { // D3: the office chat note leaves out a private department's agents (unreadable model: all left out)
		rc.Public = func(c context.Context, agentID string) bool {
			m, err := officesSvc.Model(c, application.OrgFrom(c, cfg.App.OrgID))
			return err == nil && m.CanSeeWork(offices.Viewer{}, agentID)
		}
	}
	if orgCfg != nil {
		rc.Locale = func(c context.Context) string { return orgCfg.Locale(c, "") }
	}
	if qualitySvc != nil {
		rc.Quality = func(c context.Context) (map[string]rituals.QualitySnapshot, error) {
			rep, err := qualitySvc.Overview(c, 30)
			if err != nil {
				return nil, err
			}
			out := make(map[string]rituals.QualitySnapshot, len(rep.Agents))
			for _, a := range rep.Agents {
				out[a.AgentID] = rituals.QualitySnapshot{Score: a.Score, Level: a.Level, Trend: a.Trend}
			}
			return out, nil
		}
	}
	if profiles != nil {
		rc.Profiles = profiles
	}
	svc := rituals.New(rc)
	lister, _ := store.(application.OrgLister)
	svc.Start(ctx, time.Minute, lister)
	return svc
}
