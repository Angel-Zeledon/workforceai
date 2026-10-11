package application

import (
	"context"
	"slices"

	"aiworkforce/backend/internal/domain"
)

// RunWorkspace tells an agent where it works in the drive (F1,
// docs/architecture/drive.md): its desk, the folder of the project the task
// belongs to and whether it may write. Paths only: the runtime never touches
// storage; the agent asks for fs.* tool requests and the backend executes them.
type RunWorkspace struct {
	Desk     string `json:"desk"`
	Project  string `json:"project,omitempty"`
	Shared   string `json:"shared"`
	CanWrite bool   `json:"can_write"`
}

// WorkspaceSource resolves the drive folders of an agent for a request
// (installed by the server when the drive is configured).
type WorkspaceSource interface {
	Workspace(ctx context.Context, org, agentID, requestID string) *RunWorkspace
}

// SetWorkspaceSource installs the drive folder resolver (nil removes it).
func (o *Orchestrator) SetWorkspaceSource(s WorkspaceSource) { o.wsSrc = s }

// workspaceFor is the workspace of an agent that has a drive tool label
// ("fs" or "fs_read"); nil otherwise, so payloads of agents without the drive
// do not change.
func (o *Orchestrator) workspaceFor(ctx context.Context, rs *run, agent domain.Agent) *RunWorkspace {
	if o.wsSrc == nil {
		return nil
	}
	full, ro := slices.Contains(agent.Tools, "fs"), slices.Contains(agent.Tools, "fs_read")
	if !full && !ro {
		return nil
	}
	ws := o.wsSrc.Workspace(ctx, o.org(ctx), agent.ID, rs.req.ID)
	if ws != nil {
		ws.CanWrite = full
	}
	return ws
}
