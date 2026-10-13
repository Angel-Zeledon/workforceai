package api_test

import (
	"testing"

	"aiworkforce/backend/internal/auth"
)

// Desk mode over HTTP: off by default, owner/admin only, needs the explicit
// acknowledgement, audited, per organization.
func TestCodeExecutionDeskModeSetting(t *testing.T) {
	e := newEnv(t, opts{auth: true, code: true})
	owner := e.register("owner-desk@example.com", "OwnerDesk")
	member := e.member(owner, "member-desk@example.com", auth.RoleMember)
	rival := e.register("rival-desk@example.com", "RivalDesk")
	url := api1 + "/settings/code-execution"

	if s := e.json("GET", url, owner.AccessToken, nil, 200); s["auto_run"] != "off" {
		t.Fatalf("default must be off: %v", s)
	}
	e.json("PUT", url, member, map[string]any{"enabled": true, "auto_run": "desk", "acknowledge_risk": true}, 403)
	// no acknowledgement, wrong value, caps above the ceilings: refused
	e.json("PUT", url, owner.AccessToken, map[string]any{"enabled": true, "auto_run": "desk"}, 400)
	e.json("PUT", url, owner.AccessToken, map[string]any{"enabled": true, "auto_run": "desk", "acknowledge_risk": false}, 400)
	e.json("PUT", url, owner.AccessToken, map[string]any{"enabled": true, "auto_run": "everything", "acknowledge_risk": true}, 400)
	e.json("PUT", url, owner.AccessToken, map[string]any{"enabled": true, "auto_run": "desk", "acknowledge_risk": true, "auto_max_runs_per_task": 1000}, 400)
	if s := e.json("GET", url, owner.AccessToken, nil, 200); s["auto_run"] != "off" {
		t.Fatalf("refused PUTs must change nothing: %v", s)
	}
	s := e.json("PUT", url, owner.AccessToken, map[string]any{"enabled": true, "auto_run": "desk", "acknowledge_risk": true}, 200)
	if s["auto_run"] != "desk" || s["auto_effective_runs_per_task"].(float64) != 5 {
		t.Fatalf("desk mode: %v", s)
	}
	if r := e.json("GET", url, rival.AccessToken, nil, 200); r["auto_run"] != "off" {
		t.Fatalf("per organization: %v", r)
	}
	// a PUT that does not mention auto_run turns it off (the safe direction)
	if s = e.json("PUT", url, owner.AccessToken, map[string]any{"enabled": true}, 200); s["auto_run"] != "off" {
		t.Fatalf("%v", s)
	}
	var acked bool
	for _, a := range e.store.Audit() {
		if d, ok := a.Details.(map[string]any); ok && a.Action == "code.settings.updated" && d["auto_run"] == "desk" && d["risk_acknowledged"] == true {
			acked = true
		}
	}
	if !acked {
		t.Fatal("turning on desk mode must be audited with the acknowledgement")
	}
}
