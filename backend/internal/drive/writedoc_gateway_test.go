package drive_test

import (
	"context"
	"strings"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/drive"
)

func TestGatewayToolWriteDocument(t *testing.T) {
	e := newAgentEnv(t)
	var noted []string
	tool := &drive.GatewayTool{FS: e.fs,
		AgentTools: func(_ context.Context, _, agent string) ([]string, error) {
			if agent == "ro" {
				return []string{"fs_read"}, nil
			}
			return []string{"fs"}, nil
		},
		Notify: func(_ context.Context, _, _, _, action, path string) { noted = append(noted, action+" "+path) }}
	if side, ok := tool.SideEffect("write_document"); !ok || !side {
		t.Fatal("write_document has side effects")
	}
	call := application.GatewayCall{Org: "o1", AgentID: "sales", TaskID: "t1", Tool: "fs", Action: "write_document",
		Args: map[string]any{"path": "/Agents/sales/p.xlsx", "document": prodDoc()}}
	if as := tool.Assess(e.ctx, call); as.DenyCode != "" || as.NeedsApproval {
		t.Fatalf("assess: %+v", as)
	}
	res := tool.Run(e.ctx, call, false)
	if res.Status != "succeeded" || !strings.Contains(res.Summary, "wrote /Agents/sales/p.xlsx") {
		t.Fatalf("run: %+v", res)
	}
	if len(noted) != 1 || noted[0] != "write /Agents/sales/p.xlsx" { // file.activity and the chat message know "write"
		t.Fatalf("notified = %v", noted)
	}
	// outside the desk and the projects: approval, with the path in the title
	call.Args = map[string]any{"path": "/Shared/p.xlsx", "document": prodDoc()}
	if as := tool.Assess(e.ctx, call); !as.NeedsApproval || !strings.Contains(as.Pending.Title, "write_document /Shared/p.xlsx") {
		t.Fatalf("assess outside: %+v", as)
	}
	// a read-only agent cannot
	call.AgentID = "ro"
	if as := tool.Assess(e.ctx, call); as.DenyCode != "tool_not_granted" {
		t.Fatalf("read-only agent: %+v", as)
	}
	// a document that cannot be built is reported to the agent (not a bare denial)
	call.AgentID = "sales"
	call.Args = map[string]any{"path": "/Agents/sales/mal.xlsx", "document": map[string]any{"sheets": []any{}}}
	res = tool.Run(e.ctx, call, false)
	if res.Status != "failed" || res.Code != "invalid_args" || !strings.Contains(res.Summary, "needs sheets") {
		t.Fatalf("bad document: %+v", res)
	}
}

// A write in a free zone needs no approval from the drive, but the gateway may
// still hold it (an approve_each agent): the change must be described anyway, or
// that approval has no title and cannot become a standing approval.
func TestFreeZoneWriteIsStillDescribed(t *testing.T) {
	e := newAgentEnv(t)
	tool := &drive.GatewayTool{FS: e.fs, AgentTools: func(context.Context, string, string) ([]string, error) { return []string{"fs"}, nil }}
	call := application.GatewayCall{Org: "o1", AgentID: "sales", TaskID: "t1", Tool: "fs", Action: "write",
		Args: map[string]any{"path": "/Agents/sales/nota.md", "content": "hola"}}
	as := tool.Assess(e.ctx, call)
	if as.DenyCode != "" || as.NeedsApproval {
		t.Fatalf("a desk write is free: %+v", as)
	}
	if as.Pending.Reversibility != "versioned" || !strings.Contains(as.Pending.Title, "/Agents/sales/nota.md") || as.Pending.Risk == "" {
		t.Fatalf("the write must be described: %+v", as.Pending)
	}
}
