package api_test

import (
	"encoding/json"
	"testing"
	"time"
)

// getJSON GETs path and decodes the JSON object.
func (e *env) getJSON(path string) map[string]any {
	e.t.Helper()
	resp, raw := e.do("GET", path, "", nil, nil)
	if resp.StatusCode != 200 {
		e.t.Fatalf("GET %s = %d %s", path, resp.StatusCode, raw)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		e.t.Fatalf("GET %s: %v (%s)", path, err, raw)
	}
	return m
}

func (e *env) waitRequest(id string, cond func(map[string]any) bool) map[string]any {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m := e.getJSON("/api/v1/requests/" + id)
		if cond(m) {
			return m
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("timeout waiting on request %s", id)
	return nil
}

func TestCostControlEndpoints(t *testing.T) {
	e := newEnv(t, opts{})
	// The fake runtime has no /v1/estimate (fallback estimate, no gate): a tiny
	// cap pauses the request on its first call.
	resp, raw := e.do("POST", "/api/v1/requests", "", map[string]any{"text": "hola", "budget_cap_usd": 0.01}, nil)
	if resp.StatusCode != 202 {
		t.Fatalf("create = %d %s", resp.StatusCode, raw)
	}
	var created struct {
		RequestID string `json:"request_id"`
	}
	_ = json.Unmarshal(raw, &created)
	id := created.RequestID

	m := e.waitRequest(id, func(m map[string]any) bool { return m["status"] == "paused" })
	if m["budget_cap_usd"].(float64) != 0.01 {
		t.Fatalf("budget_cap_usd = %v", m["budget_cap_usd"])
	}
	est, ok := m["estimate"].(map[string]any)
	if !ok || est["basis"] != "fallback" {
		t.Fatalf("estimate = %v", m["estimate"])
	}
	tot := est["total"].(map[string]any)
	if !(tot["min_usd"].(float64) < tot["max_usd"].(float64)) {
		t.Fatalf("the estimate must be a range: %v", tot)
	}
	if got := e.getJSON("/api/v1/requests/" + id + "/estimate"); got["request_id"] != id {
		t.Fatalf("estimate endpoint: %v", got)
	}

	st := e.getJSON("/api/v1/budget")
	pauses := st["pauses"].([]any)
	if len(pauses) != 1 || pauses[0].(map[string]any)["scope"] != "request" {
		t.Fatalf("budget pauses: %v", st["pauses"])
	}

	// Validation.
	if c := e.status("PUT", "/api/v1/requests/"+id+"/budget", "", map[string]any{"budget_cap_usd": -1}); c != 400 {
		t.Fatalf("negative cap = %d", c)
	}
	if c := e.status("PUT", "/api/v1/requests/nope/budget", "", map[string]any{"budget_cap_usd": 5}); c != 404 {
		t.Fatalf("unknown request = %d", c)
	}
	if c := e.status("POST", "/api/v1/requests/"+id+"/confirm", "", map[string]any{"decision": "maybe"}); c != 400 {
		t.Fatalf("bad decision = %d", c)
	}
	if c := e.status("POST", "/api/v1/requests/"+id+"/confirm", "", map[string]any{"decision": "proceed"}); c != 409 {
		t.Fatalf("confirm when not waiting = %d, want 409", c)
	}

	// Raise the cap and continue.
	if c := e.status("PUT", "/api/v1/requests/"+id+"/budget", "", map[string]any{"budget_cap_usd": 5}); c != 200 {
		t.Fatalf("raise = %d", c)
	}
	e.waitRequest(id, func(m map[string]any) bool { return m["status"] == "done" })

	if c := e.status("PUT", "/api/v1/agents/sales/budget", "", map[string]any{"monthly_budget_usd": 3}); c != 200 {
		t.Fatalf("agent budget = %d", c)
	}
	if c := e.status("PUT", "/api/v1/agents/ghost/budget", "", map[string]any{"monthly_budget_usd": 3}); c != 404 {
		t.Fatalf("unknown agent = %d", c)
	}
	st = e.getJSON("/api/v1/budget")
	var found bool
	for _, a := range st["agents"].([]any) {
		if am := a.(map[string]any); am["agent_id"] == "sales" && am["cap_usd"].(float64) == 3 {
			found = true
		}
	}
	if !found {
		t.Fatalf("agent cap not reported: %v", st["agents"])
	}

	for _, p := range []string{"/api/v1/costs/breakdown", "/api/v1/requests/" + id + "/cost"} {
		b := e.getJSON(p)
		for _, k := range []string{"total_usd", "by_agent", "by_task", "by_tool", "by_operation", "by_model", "untracked_usd"} {
			if _, ok := b[k]; !ok {
				t.Fatalf("%s lacks %s: %v", p, k, b)
			}
		}
	}
	if c := e.status("GET", "/api/v1/requests/nope/cost", "", nil); c != 404 {
		t.Fatalf("cost of unknown request = %d", c)
	}
}
