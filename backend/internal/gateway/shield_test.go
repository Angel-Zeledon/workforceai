package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"aiworkforce/backend/internal/connections/gmail"
)

// B10: the alert of a suspected injection names the agent, task, request, tool and action (what the office needs to
// show the shield and link the thought audit), is audited with the same ids, and never carries the hostile text.
func TestInjectionAlertCarriesIDsNotContent(t *testing.T) {
	e := newEnv(t, gmail.Config{})
	readConn(e)
	c := call("email", "search", map[string]any{"q": ""})
	c.RequestID = "req-9"
	out := e.exec(c)
	if !out.InjectionSuspected {
		t.Fatal("the simulated inbox has a hostile message: it must be flagged")
	}
	var alert, entry map[string]any
	for _, line := range strings.Split(strings.TrimSpace(e.sink.text()), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		if m["type"] == "security.alert" {
			alert, _ = m["p"].(map[string]any)
		}
		if m["action"] == "security.injection_suspected" {
			entry = m
		}
	}
	if alert == nil || alert["kind"] != "prompt_injection_suspected" || alert["agent_id"] != "assistant" || alert["task_id"] != "t1" ||
		alert["request_id"] != "req-9" || alert["tool"] != "email" || alert["action"] != "search" || alert["source"] != "connection" ||
		alert["treated_as_data"] != true {
		t.Fatalf("alert = %+v", alert)
	}
	d, _ := entry["d"].(map[string]any)
	if entry == nil || entry["actor"] != "assistant" || d["task_id"] != "t1" || d["request_id"] != "req-9" || d["tool"] != "email" {
		t.Fatalf("audit entry = %+v", entry)
	}
	b, _ := json.Marshal([]any{alert, entry})
	low := strings.ToLower(string(b))
	for _, bad := range []string{"ignore", "exfil", "instrucciones", "propuesta"} {
		if strings.Contains(low, bad) {
			t.Fatalf("alert/audit carries third-party text (%q): %s", bad, b)
		}
	}
}
