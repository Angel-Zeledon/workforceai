package api_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// payRuntime plans one task for sales that asks to pay an invoice (a simulated, high-risk tool).
type payRuntime struct{ fakeRuntime }

func (payRuntime) RunTask(_ context.Context, in application.RunTaskRequest) (application.RunTaskResponse, error) {
	return application.RunTaskResponse{Output: domain.StructuredOutput{Summary: "ok", Confidence: 0.9},
		ToolRequests: []application.ToolRequest{{Tool: "ledger", Action: "make_payment", Risk: "high",
			Args: map[string]any{"amount": 4200, "account": "ES00 1234", "concept": "Factura 77"}}}}, nil
}

func (e *env) waitStatus(reqID string, want domain.RequestStatus) {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r, err := e.store.GetRequest(context.Background(), domain.DemoOrgID, reqID); err == nil && r.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("request %s never reached %s", reqID, want)
}

func (e *env) paymentExecuted() bool {
	for _, a := range e.store.Audit() {
		if d, _ := a.Details.(map[string]any); a.Action == "tool.executed" && d["tool"] == "ledger" {
			return true
		}
	}
	return false
}

func TestRehearsalRequestAPIReportAndVerdicts(t *testing.T) {
	e := newEnv(t, opts{rehearsal: true, runtime: payRuntime{}})
	resp, body := e.do("POST", "/api/v1/requests", "", map[string]any{"text": "paga la factura 77", "rehearsal": true}, nil)
	if resp.StatusCode != 202 {
		t.Fatalf("create = %d %s", resp.StatusCode, body)
	}
	var created struct {
		RequestID string `json:"request_id"`
	}
	_ = json.Unmarshal(body, &created)
	e.waitStatus(created.RequestID, domain.RequestDone)
	if ps, _ := e.store.ListApprovals(context.Background(), domain.DemoOrgID, "pending"); len(ps) != 0 {
		t.Fatalf("a rehearsal asked for approvals: %+v", ps)
	}
	if e.paymentExecuted() {
		t.Fatal("the rehearsed payment was recorded as executed")
	}

	resp, body = e.do("GET", "/api/v1/requests/"+created.RequestID+"/rehearsal", "", nil, nil)
	var rep application.RequestReport
	_ = json.Unmarshal(body, &rep)
	if resp.StatusCode != 200 || !rep.Rehearsal || len(rep.Actions) != 1 || rep.Summary.Undecided != 1 {
		t.Fatalf("report = %d %s", resp.StatusCode, body)
	}
	a := rep.Actions[0]
	if a.Tool != "ledger" || a.Action != "make_payment" || a.WouldDecision != domain.WouldApprove || a.Amount == nil || *a.Amount != 4200 ||
		a.Preview["concept"] != "Factura 77" || a.Scope != application.RehearsalScopeRequest {
		t.Fatalf("action = %+v", a)
	}
	if c := e.status("POST", "/api/v1/rehearsal/actions/"+a.ID+"/verdict", "", map[string]string{"verdict": "maybe"}); c != 400 {
		t.Fatalf("bad verdict = %d", c)
	}
	if c := e.status("POST", "/api/v1/rehearsal/actions/nope/verdict", "", map[string]string{"verdict": "would_approve"}); c != 404 {
		t.Fatalf("unknown id = %d", c)
	}
	resp, body = e.do("POST", "/api/v1/rehearsal/actions/"+a.ID+"/verdict", "", map[string]string{"verdict": "would_reject"}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("verdict = %d %s", resp.StatusCode, body)
	}
	resp, body = e.do("GET", "/api/v1/rehearsal/agents", "", nil, nil)
	var ags struct {
		Agents []application.AgentSummary `json:"agents"`
	}
	_ = json.Unmarshal(body, &ags)
	if resp.StatusCode != 200 || len(ags.Agents) != 1 || ags.Agents[0].AgentID != "sales" || ags.Agents[0].WouldReject != 1 {
		t.Fatalf("agents = %d %s", resp.StatusCode, body)
	}
	resp, body = e.do("GET", "/api/v1/rehearsal/actions?agent_id=sales&limit=10", "", nil, nil)
	var list struct {
		Items []domain.RehearsalAction `json:"items"`
	}
	_ = json.Unmarshal(body, &list)
	if resp.StatusCode != 200 || len(list.Items) != 1 || list.Items[0].Verdict != domain.VerdictWouldReject {
		t.Fatalf("list = %d %s", resp.StatusCode, body)
	}
	if c := e.status("GET", "/api/v1/rehearsal/actions?limit=0", "", nil); c != 400 {
		t.Fatalf("bad limit = %d", c)
	}
	if c := e.status("GET", "/api/v1/requests/nope/rehearsal", "", nil); c != 404 {
		t.Fatalf("unknown request = %d", c)
	}
}

// "Rehearsal never executes a write, even if approved": the agent's switch goes on while its payment waits for
// approval; the human approves; the payment is recorded as rehearsed and never as executed.
func TestRehearsalSwitchBeatsAnApprovalAPI(t *testing.T) {
	e := newEnv(t, opts{rehearsal: true, runtime: payRuntime{}})
	_, body := e.do("POST", "/api/v1/requests", "", map[string]any{"text": "paga la factura 77"}, nil)
	var created struct {
		RequestID string `json:"request_id"`
	}
	_ = json.Unmarshal(body, &created)
	var ap domain.Approval
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ps, _ := e.store.ListApprovals(context.Background(), domain.DemoOrgID, "pending"); len(ps) == 1 {
			ap = ps[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ap.ID == "" {
		t.Fatal("the real (non-rehearsal) payment must wait for approval")
	}
	if c := e.status("PUT", "/api/v1/agents/sales/rehearsal", "", map[string]bool{"enabled": true}); c != 200 {
		t.Fatalf("switch on = %d", c)
	}
	if c := e.status("PUT", "/api/v1/agents/nobody/rehearsal", "", map[string]bool{"enabled": true}); c != 404 {
		t.Fatalf("unknown agent = %d", c)
	}
	if c := e.status("PUT", "/api/v1/agents/sales/rehearsal", "", map[string]any{}); c != 400 {
		t.Fatalf("missing enabled = %d", c)
	}
	if c := e.status("POST", "/api/v1/approvals/"+ap.ID+"/decision", "", map[string]string{"decision": "approve"}); c != 200 {
		t.Fatalf("approve = %d", c)
	}
	e.waitStatus(created.RequestID, domain.RequestDone)
	if e.paymentExecuted() {
		t.Fatal("an approved payment ran while the agent was in rehearsal")
	}
	_, body = e.do("GET", "/api/v1/requests/"+created.RequestID+"/rehearsal", "", nil, nil)
	var rep application.RequestReport
	_ = json.Unmarshal(body, &rep)
	if rep.Rehearsal || len(rep.Actions) != 1 || rep.Actions[0].Scope != application.RehearsalScopeAgent {
		t.Fatalf("report = %s", body)
	}
}

// Turning rehearsal on takes power away (admins may); turning it off gives real effects back (owner only).
func TestRehearsalSwitchPermissions(t *testing.T) {
	e := newEnv(t, opts{auth: true, rehearsal: true})
	owner := e.register("owner@example.com", "Acme")
	admin := e.member(owner, "admin@example.com", auth.RoleAdmin)
	viewer := e.member(owner, "viewer@example.com", auth.RoleViewer)
	id := firstAgent(t, e, owner.AccessToken)
	path := "/api/v1/agents/" + id + "/rehearsal"
	if c := e.status("PUT", path, viewer, map[string]bool{"enabled": true}); c != 403 {
		t.Fatalf("viewer switch on = %d", c)
	}
	if c := e.status("PUT", path, admin, map[string]bool{"enabled": true}); c != 200 {
		t.Fatalf("admin switch on = %d", c)
	}
	if c := e.status("PUT", path, admin, map[string]bool{"enabled": false}); c != 403 {
		t.Fatalf("admin switch off = %d", c)
	}
	if c := e.status("PUT", path, owner.AccessToken, map[string]bool{"enabled": false}); c != 200 {
		t.Fatalf("owner switch off = %d", c)
	}
	if c := e.status("POST", "/api/v1/rehearsal/actions/x/verdict", viewer, map[string]string{"verdict": "would_approve"}); c != 403 {
		t.Fatalf("viewer verdict = %d", c)
	}
}

func firstAgent(t *testing.T, e *env, token string) string {
	t.Helper()
	_, body := e.do("GET", "/api/v1/agents", token, nil, nil)
	var list []domain.Agent
	if err := json.Unmarshal(body, &list); err != nil || len(list) == 0 {
		var wrapped struct {
			Items []domain.Agent `json:"items"`
		}
		_ = json.Unmarshal(body, &wrapped)
		list = wrapped.Items
	}
	if len(list) == 0 {
		t.Fatalf("no agents: %s", body)
	}
	return list[0].ID
}
