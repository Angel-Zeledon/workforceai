package main

import (
	"context"
	"log/slog"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/config"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/orgchart"
	"aiworkforce/backend/internal/projects"
	"aiworkforce/backend/internal/trust"
)

// wirePermissions builds the approval permissions per agent (docs/architecture/06-permisos-autonomia.md section 11)
// and binds the observer that gives newly hired agents the factory rules. The level is written through the trust
// ladder (ceiling, controls, audit); without it, straight to agents.autonomy (the service already checks the
// ceiling). The person responsible for the agent's team comes from the organization model: the head of the agent's
// department (or of a department above it) or the person the agent reports to.
func wirePermissions(cfg config.Config, log *slog.Logger, store application.Store, rec *application.Recorder, trustSvc *trust.Service,
	ctl *controls.Service, chart *orgchart.Service, obs *application.PermissionsObserver, policySvc *application.PolicyService,
	proj *projects.Service) *application.PermissionsService {
	cs, ok := store.(application.ConfigStore)
	if !ok {
		return nil
	}
	svc := &application.PermissionsService{Store: cs, Agents: store, Approvals: store, Rec: rec, OrgID: cfg.App.OrgID, Log: log}
	if proj != nil {
		// "In this project / batch do not ask for <category>": the project and batch of a request or an approval.
		svc.ProjectScope = proj.ScopeOfProject
		if policySvc != nil {
			policySvc.SetScopeResolver(proj.ScopeOfRequest)
		}
	}
	if trustSvc != nil {
		svc.SetLevel = trustSvc.ChooseLevel
	} else {
		svc.SetLevel = func(ctx context.Context, agentID, autonomy, _ string) error {
			return cs.SetAgentAutonomy(ctx, application.OrgFrom(ctx, cfg.App.OrgID), agentID, autonomy)
		}
	}
	if ctl != nil {
		svc.Blocked = func(ctx context.Context, org, agentID string) string {
			if v := ctl.Check(ctx, org, agentID, true); !v.Allowed {
				return v.Code
			}
			return ""
		}
	}
	if chart != nil {
		svc.IsTeamLead = func(ctx context.Context, org, userID, agentID string) bool {
			return teamLead(application.WithOrg(ctx, org), chart, org, userID, agentID)
		}
	}
	if obs != nil {
		obs.Bind(svc)
	}
	return svc
}

func teamLead(ctx context.Context, chart *orgchart.Service, org, userID, agentID string) bool {
	me := orgchart.Ref{Kind: orgchart.KindPerson, ID: userID}
	job, err := chart.Job(ctx, agentID)
	if err != nil {
		return false
	}
	if job.ReportsTo == me {
		return true
	}
	deps, err := chart.Store.ListDepartments(ctx, org)
	if err != nil {
		return false
	}
	byID := make(map[string]orgchart.Department, len(deps))
	for _, d := range deps {
		byID[d.ID] = d
	}
	for id, hops := job.DepartmentID, 0; id != "" && hops < 32; hops++ {
		d, ok := byID[id]
		if !ok {
			return false
		}
		if d.Head == me {
			return true
		}
		id = d.ParentID
	}
	return false
}
