package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// standingProjects: proj1 runs, everything else does not exist.
type standingProjects struct{}

func (standingProjects) ProjectOfRequest(context.Context, string, string) (string, bool) {
	return "proj1", true
}
func (standingProjects) ProjectActive(_ context.Context, _, id string) bool { return id == "proj1" }

func (e *env) pendingStanding(tool, action, risk string, eligible bool) domain.Approval {
	e.t.Helper()
	ap, _, err := e.appr.Request(context.Background(), domain.Approval{TaskID: "t-" + action, AgentID: "sales", Action: action, Tool: tool, ProjectID: "proj1",
		StandingEligible: eligible, Risk: risk, Title: action, PolicyRule: "baseline.autonomy:approve_each"})
	if err != nil {
		e.t.Fatal(err)
	}
	return ap
}

func TestStandingApprovalAPIAlwaysAllowListRevoke(t *testing.T) {
	e := newEnv(t, opts{standing: true})
	ap := e.pendingStanding("spreadsheet", "update", "medium", true)

	resp, body := e.do("POST", "/api/v1/approvals/"+ap.ID+"/decision", "", map[string]any{"decision": "approve", "always_allow": true}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("decision = %d %s", resp.StatusCode, body)
	}
	var out struct {
		ID       string                        `json:"id"`
		Status   string                        `json:"status"`
		Standing *application.StandingApproval `json:"standing"`
		Err      string                        `json:"standing_error"`
	}
	_ = json.Unmarshal(body, &out)
	if out.ID != ap.ID || out.Status != "approved" || out.Standing == nil || out.Standing.Status != "active" || out.Standing.Tool != "spreadsheet" || out.Standing.AgentID != "sales" {
		t.Fatalf("decision body = %s", body)
	}

	resp, body = e.do("GET", "/api/v1/projects/proj1/standing-approvals", "", nil, nil)
	var list []application.StandingApproval
	_ = json.Unmarshal(body, &list)
	if resp.StatusCode != 200 || len(list) != 1 || list[0].ID != out.Standing.ID {
		t.Fatalf("list = %d %s", resp.StatusCode, body)
	}
	if resp, _ := e.do("GET", "/api/v1/projects/other/standing-approvals", "", nil, nil); resp.StatusCode != 200 {
		t.Fatalf("other project list = %d", resp.StatusCode)
	}

	// Revoking from the wrong project finds nothing; from the right one it ends the grant.
	if c := e.status("DELETE", "/api/v1/projects/other/standing-approvals/"+out.Standing.ID, "", nil); c != 404 {
		t.Errorf("revoke from another project = %d", c)
	}
	resp, body = e.do("DELETE", "/api/v1/projects/proj1/standing-approvals/"+out.Standing.ID, "", nil, nil)
	var rv application.StandingApproval
	_ = json.Unmarshal(body, &rv)
	if resp.StatusCode != 200 || rv.Status != "revoked" || rv.RevokedBy == "" {
		t.Fatalf("revoke = %d %s", resp.StatusCode, body)
	}
	if c := e.status("DELETE", "/api/v1/projects/proj1/standing-approvals/"+out.Standing.ID, "", nil); c != 200 {
		t.Errorf("second revoke = %d (idempotent)", c)
	}
}

func TestStandingApprovalAPIRefusesWhatIsNotInternalAndReversible(t *testing.T) {
	e := newEnv(t, opts{standing: true})
	for _, c := range []struct {
		name, tool, action, risk string
		eligible                 bool
	}{
		{"not offered", "spreadsheet", "update", "medium", false},
		{"forged flag on email", "email", "send_proposal", "medium", true},
		{"forged flag on code.run", "code", "run", "low", true},
		{"forged flag on delete", "fs", "delete", "medium", true},
		{"forged flag on a payment", "spreadsheet", "make_payment", "low", true},
		{"high risk", "documents", "draft", "high", true},
	} {
		ap := e.pendingStanding(c.tool, c.action, c.risk, c.eligible)
		resp, body := e.do("POST", "/api/v1/approvals/"+ap.ID+"/decision", "", map[string]any{"decision": "approve", "always_allow": true}, nil)
		var out struct {
			Status   string                        `json:"status"`
			Standing *application.StandingApproval `json:"standing"`
			Err      string                        `json:"standing_error"`
		}
		_ = json.Unmarshal(body, &out)
		// The single approval still goes through; the permanent permission does not.
		if resp.StatusCode != 200 || out.Status != "approved" || out.Standing != nil || out.Err == "" {
			t.Errorf("%s: %d %s", c.name, resp.StatusCode, body)
		}
	}
	if resp, body := e.do("GET", "/api/v1/projects/proj1/standing-approvals", "", nil, nil); resp.StatusCode != 200 || string(body) != "[]" && !jsonEmptyList(body) {
		t.Errorf("nothing must have been stored: %s", body)
	}
	// Rejecting with always_allow, or on a server without the feature, is a client error.
	ap := e.pendingStanding("spreadsheet", "update", "medium", true)
	if c := e.status("POST", "/api/v1/approvals/"+ap.ID+"/decision", "", map[string]any{"decision": "reject", "always_allow": true}); c != http.StatusBadRequest {
		t.Errorf("reject + always_allow = %d", c)
	}
	off := newEnv(t, opts{})
	ap2 := off.pendingStanding("spreadsheet", "update", "medium", true)
	if c := off.status("POST", "/api/v1/approvals/"+ap2.ID+"/decision", "", map[string]any{"decision": "approve", "always_allow": true}); c != http.StatusBadRequest {
		t.Errorf("always_allow without the feature = %d", c)
	}
}

func jsonEmptyList(b []byte) bool {
	var l []any
	return json.Unmarshal(b, &l) == nil && len(l) == 0
}

func TestStandingApprovalAPIPermissions(t *testing.T) {
	e := newEnv(t, opts{auth: true, standing: true})
	owner := e.register("own@example.com", "Own")
	// A viewer can read the list but cannot revoke.
	if c := e.status("GET", "/api/v1/projects/proj1/standing-approvals", owner.AccessToken, nil); c != 200 {
		t.Fatalf("owner list = %d", c)
	}
	if c := e.status("GET", "/api/v1/projects/proj1/standing-approvals", "", nil); c != 401 {
		t.Errorf("anonymous list = %d", c)
	}
	if c := e.status("DELETE", "/api/v1/projects/proj1/standing-approvals/x", "", nil); c != 401 {
		t.Errorf("anonymous revoke = %d", c)
	}
}

// The three-way choice of the approval card (once / session / project) reuses the standing approvals.
func TestApprovalScopeOnceSessionProject(t *testing.T) {
	e := newEnv(t, opts{standing: true})
	decide := func(action, scope string) (code int, st *application.StandingApproval) {
		ap := e.pendingStanding("spreadsheet", action, "medium", true)
		resp, body := e.do("POST", "/api/v1/approvals/"+ap.ID+"/decision", "", map[string]any{"decision": "approve", "scope": scope}, nil)
		var out struct {
			Standing *application.StandingApproval `json:"standing"`
		}
		_ = json.Unmarshal(body, &out)
		return resp.StatusCode, out.Standing
	}
	if c, st := decide("update", "once"); c != 200 || st != nil {
		t.Fatalf("once: %d %+v", c, st)
	}
	c, sess := decide("write", "session")
	if c != 200 || sess == nil || sess.Status != "active" {
		t.Fatalf("session: %d %+v", c, sess)
	}
	if d := sess.ExpiresAt.Sub(sess.CreatedAt); d > 9*time.Hour || d < 7*time.Hour {
		t.Fatalf("session grant lasts %v, want about 8h", d)
	}
	c, proj := decide("append", "project")
	if c != 200 || proj == nil || proj.ExpiresAt.Sub(proj.CreatedAt) < 24*time.Hour {
		t.Fatalf("project: %d %+v", c, proj)
	}
	ap := e.pendingStanding("spreadsheet", "edit", "medium", true)
	if c := e.status("POST", "/api/v1/approvals/"+ap.ID+"/decision", "", map[string]any{"decision": "approve", "scope": "forever"}); c != http.StatusBadRequest {
		t.Errorf("unknown scope = %d", c)
	}
	// Rejecting with a remembered scope is a client error, like always_allow.
	if c := e.status("POST", "/api/v1/approvals/"+ap.ID+"/decision", "", map[string]any{"decision": "reject", "scope": "session"}); c != http.StatusBadRequest {
		t.Errorf("reject + session = %d", c)
	}
}
