package drive_test

import (
	"context"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/drive"
)

// A described fs change names its normalized path and its action as data (not only inside the title), so a screen
// can tell which file waits for a decision.
func TestFsApprovalCarriesPath(t *testing.T) {
	e := newOfficeEnv(t)
	tool := &drive.GatewayTool{FS: e.fs, AgentTools: func(context.Context, string, string) ([]string, error) { return []string{"fs"}, nil }}
	// a write in the agent's own office is free, but still described (an approve_each agent is held on it)
	call := application.GatewayCall{Org: "o1", AgentID: "legal", TaskID: "t1", Tool: "fs", Action: "write",
		Args: map[string]any{"path": "/Offices/Legal/Casos/x.md/", "content": "hola"}}
	if as := tool.Assess(e.ctx, call); as.DenyCode != "" || as.Pending.Path != "/Offices/Legal/Casos/x.md" || as.Pending.Action != "write" {
		t.Fatalf("own office write: %+v", as)
	}
	// a write outside the agent's scope asks a human
	call.Args = map[string]any{"path": "/Shared/nota.md", "content": "hola"}
	if as := tool.Assess(e.ctx, call); !as.NeedsApproval || as.Pending.Path != "/Shared/nota.md" || as.Pending.Action != "write" {
		t.Fatalf("shared write: %+v", as)
	}
	// a delete always asks
	call.Action, call.Args = "delete", map[string]any{"path": "/Offices/Legal/Casos/demanda.md"}
	if as := tool.Assess(e.ctx, call); !as.NeedsApproval || as.Pending.Path != "/Offices/Legal/Casos/demanda.md" || as.Pending.Action != "delete" {
		t.Fatalf("delete: %+v", as)
	}
	// a read is never held: no path is reported for it
	call.Action, call.Args = "read", map[string]any{"path": "/Offices/Legal/Casos/demanda.md"}
	if as := tool.Assess(e.ctx, call); as.NeedsApproval || as.Pending.Path != "" || as.Pending.Action != "" {
		t.Fatalf("read: %+v", as)
	}
}
