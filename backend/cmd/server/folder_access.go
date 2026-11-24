package main

import (
	"context"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
)

// agentFolders evaluates the folder access of agents for the API ("Carpetas que puede ver", "Qué agentes la ven"):
// the same policy as the fs.* tools, with the project directory and the customer district. nil without a drive.
func agentFolders(ws workspaceWiring) *drive.AgentFS {
	if ws.drive == nil {
		return nil
	}
	return &drive.AgentFS{Svc: ws.drive, Projects: projectDirectory{store: ws.pstore}, Customers: customerDirectory(ws.customers)}
}

// helperFileCheck is the rule "may this agent read this file" for temporary helpers (Orchestrator.SetHelperFileCheck):
// exactly the zone rule of fs.read (its desk, /Shared, its office, its projects, the rules people set). Without a
// drive nothing is readable, so a part with files is refused (fail closed).
func helperFileCheck(af *drive.AgentFS) func(ctx context.Context, org, agentID string, f domain.FileRef) bool {
	return func(ctx context.Context, org, agentID string, f domain.FileRef) bool {
		if af == nil {
			return false
		}
		may, err := af.ReadableFilter(application.WithOrg(ctx, org), agentID)
		return err == nil && may(f.Path)
	}
}
