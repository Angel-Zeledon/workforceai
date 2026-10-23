package api_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// awayTestGap is the visit gap of the test server: short, so the test can be "away" for a moment.
const awayTestGap = 500 * time.Millisecond

// B3: "while you were away" over HTTP: heartbeat, summary since the last visit, approvals only for who may decide
// them, dismiss, permissions and validation.
func TestAwaySummaryOverHTTP(t *testing.T) {
	e := newEnv(t, opts{auth: true, away: true})
	owner := e.register("owner-away@example.com", "OwnerAway")
	viewer := e.member(owner, "viewer-away@example.com", auth.RoleViewer)
	org := owner.OrgID

	// First visit ever: nothing to compare against.
	first := e.json("GET", api1+"/away-summary", owner.AccessToken, nil, 200)
	if first["first_visit"] != true || first["show"] != false || first["method"] != "deterministic" {
		t.Fatalf("first visit: %v", first)
	}
	hb := e.json("POST", api1+"/away-summary/heartbeat", owner.AccessToken, nil, 200)
	if hb["last_seen_at"] == nil || hb["visit_started_at"] == nil {
		t.Fatalf("heartbeat: %v", hb)
	}
	e.json("POST", api1+"/away-summary/heartbeat", viewer, nil, 200)

	// While they are away: a request completes, a task fails, an approval waits.
	ctx := context.Background()
	now := time.Now().UTC()
	save := func(id, typ, agent string, p any) {
		b, _ := json.Marshal(p)
		if err := e.store.SaveEvent(ctx, org, domain.Event{ID: id, Type: typ, TS: time.Now().UTC(), OrgID: org, AgentID: agent, Payload: json.RawMessage(b)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.store.CreateRequest(ctx, org, domain.Request{ID: "aw-r1", Text: "Resumen semanal", Status: domain.RequestDone, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	save("aw-1", domain.EvRequestCompleted, "", map[string]any{"request_id": "aw-r1", "report_id": "aw-rep1"})
	save("aw-2", domain.EvTaskFailed, "sales", map[string]any{"task": domain.Task{ID: "aw-t1", AgentID: "sales", Title: "Llamar al cliente"}, "error": "timeout"})
	if err := e.store.CreateApproval(ctx, org, domain.Approval{ID: "aw-ap1", TaskID: "aw-t2", AgentID: "sales", Action: "email.send", Title: "Enviar propuesta",
		Risk: "high", Status: domain.ApprovalPending, CreatedAt: time.Now().UTC(), RequiredApprovals: 1}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(awayTestGap + 100*time.Millisecond)

	s := e.json("GET", api1+"/away-summary?min_hours=0", owner.AccessToken, nil, 200)
	counts := s["counts"].(map[string]any)
	if s["show"] != true || counts["requests_completed"].(float64) != 1 || counts["tasks_failed"].(float64) != 1 || counts["approvals_waiting"].(float64) != 1 {
		t.Fatalf("owner summary: %v", s)
	}
	act := s["act_now"].([]any)
	if len(act) != 1 || act[0].(map[string]any)["ref_id"] != "aw-ap1" || act[0].(map[string]any)["new"] != true {
		t.Fatalf("act now: %v", act)
	}
	// The default threshold (8 hours) is not reached by a short absence: computed, not opened on its own.
	if s8 := e.json("GET", api1+"/away-summary", owner.AccessToken, nil, 200); s8["show"] != false || s8["min_hours"].(float64) != 8 {
		t.Fatalf("default threshold: show %v min %v", s8["show"], s8["min_hours"])
	}

	// A viewer cannot decide: the approval waits for someone else.
	vs := e.json("GET", api1+"/away-summary?min_hours=0", viewer, nil, 200)
	vc := vs["counts"].(map[string]any)
	if len(vs["act_now"].([]any)) != 0 || vc["approvals_waiting"].(float64) != 0 || vc["approvals_waiting_other"].(float64) != 1 {
		t.Fatalf("viewer summary: %v", vs)
	}

	// Dismiss closes it for this visit.
	d := e.json("POST", api1+"/away-summary/dismiss", owner.AccessToken, nil, 200)
	if d["dismissed_at"] == nil {
		t.Fatalf("dismiss: %v", d)
	}
	if s2 := e.json("GET", api1+"/away-summary?min_hours=0", owner.AccessToken, nil, 200); s2["show"] != false || s2["dismissed"] != true {
		t.Fatalf("after dismiss: show %v dismissed %v", s2["show"], s2["dismissed"])
	}

	// Validation and authentication.
	e.json("GET", api1+"/away-summary?min_hours=abc", owner.AccessToken, nil, 400)
	if resp, _ := e.do("GET", api1+"/away-summary", "", nil, nil); resp.StatusCode != 401 {
		t.Fatalf("no token = %d", resp.StatusCode)
	}
}
