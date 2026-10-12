package main

import (
	"context"
	"log/slog"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/config"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/roles"
	"aiworkforce/backend/internal/skills"
)

// wireSkills builds the skill library (metadata in Postgres or memory, files in
// the drive under /Shared/Skills), serves skill.load through the Tool Gateway as
// a native READ tool and gives the orchestrator the name + description list of
// each agent's active skills.
func wireSkills(cfg config.Config, log *slog.Logger, pg *postgres.Store, store application.Store, rec *application.Recorder,
	orch *application.Orchestrator, orgCfg *application.OrgConfig, cw connWiring, drv *drive.Service) *skills.Service {
	if drv == nil {
		return nil
	}
	var st skills.Store = skills.NewMemStore()
	if pg != nil {
		st = &postgres.SkillStore{S: pg}
	}
	scfg := skills.Config{Store: st, Drive: drv, OrgID: cfg.App.OrgID, Log: log,
		Audit: func(ctx context.Context, org, actor, action, id string, d map[string]any) {
			rec.Audit(application.WithOrg(ctx, org), domain.AuditLog{Actor: actor, Action: action, Entity: "skill", EntityID: id, Details: d})
		},
		AgentRole: func(ctx context.Context, org, agentID string) (string, error) {
			a, err := store.GetAgent(application.WithOrg(ctx, org), org, agentID)
			return a.Role, err
		},
		RoleDefaults: func(role string) []string {
			if t, ok := roles.Get(role); ok {
				return t.DefaultSkills
			}
			return nil
		}}
	if orgCfg != nil {
		scfg.Locale = func(ctx context.Context) string { return orgCfg.Locale(ctx, "") }
	}
	svc := skills.New(scfg)
	if cw.gw != nil {
		cw.gw.AddNative(&skills.GatewayTool{Svc: svc})
	}
	orch.SetSkillSource(svc)
	log.Info("skill library ready", "tool", skills.ToolName, "store", map[bool]string{true: "postgres", false: "memory"}[pg != nil])
	return svc
}
