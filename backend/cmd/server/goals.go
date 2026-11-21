package main

import (
	"context"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/goals"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/memory"
	"aiworkforce/backend/internal/offices"
	"aiworkforce/backend/internal/projects"
	"aiworkforce/backend/internal/sanitize"
)

// wireGoals builds the company goals (F1, docs/architecture/goals.md) once the departments exist (their D3 seal
// decides who sees a department's goal and which agents receive it) and before orch.Recover, so a resumed request
// keeps handing its goal to its agents. The texts reach the agents as untrusted recalled data, cleaned like the
// memory notes people write (memory.Clean with the known secrets); a text that reads like instructions is refused
// when it is written.
func wireGoals(pg *postgres.Store, rec *application.Recorder, orch *application.Orchestrator, psvc *projects.Service,
	officesSvc *offices.Service, sus *sanitize.Suspects) *goals.Service {
	var st goals.Store = goals.NewMemStore()
	if pg != nil {
		st = &postgres.GoalStore{S: pg}
	}
	cfg := goals.Config{Store: st,
		Audit: func(ctx context.Context, org, actor, action, id string, d map[string]any) {
			rec.Audit(application.WithOrg(ctx, org), domain.AuditLog{Actor: actor, Action: action, Entity: "goal", EntityID: id, Details: d})
		},
		Emit: func(ctx context.Context, org, typ string, g goals.Goal, aud []string) {
			rec.Emit(application.WithOrg(ctx, org), application.Action{Type: typ, Entity: "goal", EntityID: g.ID, Payload: map[string]any{"goal": g},
				Audience: aud, SkipAudit: true})
		},
		Clean: func(s string) string { return memory.Clean(s, sus) },
		Accept: func(s string) bool {
			score, _ := sanitize.ScoreInjection(s)
			return score < sanitize.InjectionThreshold
		}}
	if officesSvc != nil {
		cfg.Offices = officesSvc.Model
	}
	svc := goals.New(cfg)
	orch.SetGoals(svc)
	if psvc != nil {
		psvc.SetGoalCheck(svc.CheckLink)
	}
	return svc
}
