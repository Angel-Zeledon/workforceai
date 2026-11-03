package main

import "aiworkforce/backend/internal/drive"

// agentFolders evaluates the folder access of agents for the API ("Carpetas que puede ver", "Qué agentes la ven"):
// the same policy as the fs.* tools, with the project directory and the customer district. nil without a drive.
func agentFolders(ws workspaceWiring) *drive.AgentFS {
	if ws.drive == nil {
		return nil
	}
	return &drive.AgentFS{Svc: ws.drive, Projects: projectDirectory{store: ws.pstore}, Customers: customerDirectory(ws.customers)}
}
