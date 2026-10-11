package api_test

import (
	"context"
	"testing"
	"time"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/sandbox"
)

// The code sandbox settings and run history over HTTP: owner/admin only,
// disabled by default, audited, limits validated.
func TestCodeExecutionSettingsAndRuns(t *testing.T) {
	e := newEnv(t, opts{auth: true, code: true})
	owner := e.register("owner-code@example.com", "OwnerCode")
	member := e.member(owner, "member-code@example.com", auth.RoleMember)
	rival := e.register("rival-code@example.com", "RivalCode")

	s := e.json("GET", api1+"/settings/code-execution", owner.AccessToken, nil, 200)
	if s["enabled"] != false || s["available"] != false || s["ceiling_runtime_seconds"].(float64) <= 0 {
		t.Fatalf("default settings: %v", s)
	}
	e.json("GET", api1+"/settings/code-execution", member, nil, 403)
	e.json("PUT", api1+"/settings/code-execution", member, map[string]any{"enabled": true}, 403)
	e.json("PUT", api1+"/settings/code-execution", owner.AccessToken, map[string]any{"enabled": true, "max_runtime_seconds": 100000}, 400)
	e.json("PUT", api1+"/settings/code-execution", owner.AccessToken, map[string]any{"enabled": true, "max_runs_per_day": -1}, 400)
	s = e.json("PUT", api1+"/settings/code-execution", owner.AccessToken, map[string]any{"enabled": true, "max_runs_per_day": 5, "max_runtime_seconds": 30}, 200)
	if s["enabled"] != true || s["max_runs_per_day"].(float64) != 5 {
		t.Fatalf("put: %v", s)
	}
	// Per organization: the rival still has it off.
	if r := e.json("GET", api1+"/settings/code-execution", rival.AccessToken, nil, 200); r["enabled"] != false {
		t.Fatalf("org isolation: %v", r)
	}
	// A run of the owner's organization is listed for admins only and never for another org.
	orgID := owner.OrgID
	run := sandbox.Run{ID: "run_x", OrgID: orgID, AgentID: "analyst", ApprovalID: "ap1", Language: "python", Code: "print(1)", Status: sandbox.StatusAwaiting,
		ExpectedOutputs: []string{}, Inputs: []sandbox.InputInfo{}, Outputs: []sandbox.OutputInfo{}, CreatedAt: time.Now().UTC()}
	if err := e.code.Store.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if l := e.list(api1+"/code-runs", owner.AccessToken); len(l) != 1 {
		t.Fatalf("runs: %v", l)
	}
	if l := e.list(api1+"/code-runs?approval_id=ap1", owner.AccessToken); len(l) != 1 || l[0]["code"] != "print(1)" {
		t.Fatalf("by approval: %v", l)
	}
	e.json("GET", api1+"/code-runs/run_x", owner.AccessToken, nil, 200)
	e.json("GET", api1+"/code-runs/run_x", member, nil, 403)
	e.json("GET", api1+"/code-runs/run_x", rival.AccessToken, nil, 404)
	if l := e.list(api1+"/code-runs", rival.AccessToken); len(l) != 0 {
		t.Fatalf("rival sees runs: %v", l)
	}
	// The change of the setting is in the audit trail.
	found := false
	for _, a := range e.store.Audit() {
		found = found || a.Action == "code.settings.updated"
	}
	if !found {
		t.Fatal("code.settings.updated audit missing")
	}
}
