package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"aiworkforce/backend/internal/domain"
)

type fakeWorkspace struct{ calls int }

func (f *fakeWorkspace) Workspace(_ context.Context, org, agentID, requestID string) *RunWorkspace {
	f.calls++
	return &RunWorkspace{Desk: "/Agents/" + agentID, Project: "/Projects/P (" + requestID + ")", Shared: "/Shared", CanWrite: true}
}

func TestWorkspaceOnlyForDriveAgents(t *testing.T) {
	src := &fakeWorkspace{}
	o := &Orchestrator{cfg: Config{OrgID: "org1"}, wsSrc: src}
	rs := &run{req: domain.Request{ID: "r1"}}

	full := o.workspaceFor(context.Background(), rs, domain.Agent{ID: "sales", Tools: []string{"fs", "crm"}})
	if full == nil || full.Desk != "/Agents/sales" || full.Project != "/Projects/P (r1)" || !full.CanWrite {
		t.Fatalf("fs agent: %+v", full)
	}
	ro := o.workspaceFor(context.Background(), rs, domain.Agent{ID: "aud", Tools: []string{"fs_read"}})
	if ro == nil || ro.CanWrite {
		t.Fatalf("fs_read agent must not be told it can write: %+v", ro)
	}
	if none := o.workspaceFor(context.Background(), rs, domain.Agent{ID: "x", Tools: []string{"crm"}}); none != nil {
		t.Fatalf("an agent without a drive label gets no workspace: %+v", none)
	}
	if src.calls != 2 {
		t.Fatalf("source asked %d times, want 2", src.calls)
	}
	// Without a source nothing changes; a nil workspace is not serialized.
	o.wsSrc = nil
	if o.workspaceFor(context.Background(), rs, domain.Agent{ID: "sales", Tools: []string{"fs"}}) != nil {
		t.Fatal("no source: no workspace")
	}
	b, _ := json.Marshal(RunTaskRequest{})
	if strings.Contains(string(b), "workspace") {
		t.Fatalf("old payload shape changed: %s", b)
	}
}
