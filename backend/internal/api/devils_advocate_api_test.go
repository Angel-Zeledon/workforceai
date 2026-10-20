package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// advocateStub is the runtime capability of the devil's advocate review.
type advocateStub struct {
	calls atomic.Int32
	fail  bool
}

func (a *advocateStub) Counterargument(context.Context, application.CounterargumentRequest) (application.CounterargumentResponse, error) {
	a.calls.Add(1)
	if a.fail {
		return application.CounterargumentResponse{}, errors.New("model down: sk-live-secretsecretsecret1234")
	}
	return application.CounterargumentResponse{Counterargument: "Mejor esperar.", Risks: []string{"importe"}, Checks: []string{"verifica"},
		Usage: &application.Usage{Model: "sim", InputTokens: 500, OutputTokens: 100, CostUSD: 0.002}, Model: "sim", Provider: "simulation"}, nil
}

func (e *env) pendingRisk(risk string) domain.Approval {
	e.t.Helper()
	ap, _, err := e.appr.Request(context.Background(), domain.Approval{TaskID: "t-" + risk, AgentID: "sales", Action: "email.send", Risk: risk, Title: "Enviar propuesta"})
	if err != nil {
		e.t.Fatal(err)
	}
	return ap
}

func TestDevilsAdvocateAPIGenerateCacheAndRead(t *testing.T) {
	stub := &advocateStub{}
	e := newEnv(t, opts{advocate: stub})
	ap := e.pendingRisk("high")
	path := "/api/v1/approvals/" + ap.ID + "/counterargument"

	if resp, body := e.do("GET", path, "", nil, nil); resp.StatusCode != 404 || !strings.Contains(string(body), "not_generated") {
		t.Fatalf("GET before generating = %d %s", resp.StatusCode, body)
	}
	resp, body := e.do("POST", path, "", nil, nil) // an empty body is fine
	var c domain.Counterargument
	_ = json.Unmarshal(body, &c)
	if resp.StatusCode != 200 || c.Counterargument != "Mejor esperar." || !c.Advisory || c.Cached || c.ApprovalID != ap.ID {
		t.Fatalf("POST = %d %s", resp.StatusCode, body)
	}
	resp, body = e.do("POST", path, "", map[string]any{"locale": "en"}, nil)
	_ = json.Unmarshal(body, &c)
	if resp.StatusCode != 200 || !c.Cached {
		t.Fatalf("second POST = %d %s", resp.StatusCode, body)
	}
	resp, body = e.do("GET", path, "", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Mejor esperar.") {
		t.Fatalf("GET = %d %s", resp.StatusCode, body)
	}
	if n := stub.calls.Load(); n != 1 {
		t.Fatalf("runtime called %d times", n)
	}
	// The approval is untouched: still pending and decidable.
	resp, body = e.do("POST", "/api/v1/approvals/"+ap.ID+"/decision", "", map[string]any{"decision": "approve"}, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"approved"`) {
		t.Fatalf("decision = %d %s", resp.StatusCode, body)
	}
}

func TestDevilsAdvocateAPIDegradesWithoutLeakingTheCause(t *testing.T) {
	stub := &advocateStub{fail: true}
	e := newEnv(t, opts{advocate: stub})
	ap := e.pendingRisk("high")
	resp, body := e.do("POST", "/api/v1/approvals/"+ap.ID+"/counterargument", "", nil, nil)
	if resp.StatusCode != 503 || !strings.Contains(string(body), `"unavailable"`) || strings.Contains(string(body), "sk-live") {
		t.Fatalf("POST = %d %s", resp.StatusCode, body)
	}
	if c := e.status("POST", "/api/v1/approvals/"+ap.ID+"/decision", "", map[string]any{"decision": "reject"}); c != 200 {
		t.Fatalf("the approval must still be decidable, got %d", c)
	}
}

func TestDevilsAdvocateAPIRefusesLowRiskAndUnknownApprovals(t *testing.T) {
	stub := &advocateStub{}
	e := newEnv(t, opts{advocate: stub})
	low := e.pendingRisk("low")
	if c := e.status("POST", "/api/v1/approvals/"+low.ID+"/counterargument", "", nil); c != 400 {
		t.Errorf("low risk = %d", c)
	}
	if c := e.status("POST", "/api/v1/approvals/nope/counterargument", "", nil); c != 404 {
		t.Errorf("unknown = %d", c)
	}
	if c := e.status("GET", "/api/v1/approvals/nope/counterargument", "", nil); c != 404 {
		t.Errorf("unknown GET = %d", c)
	}
	if stub.calls.Load() != 0 {
		t.Fatal("the model was called")
	}
}

func TestDevilsAdvocateRoutesAreOffWithoutTheService(t *testing.T) {
	e := newEnv(t, opts{})
	ap := e.pendingRisk("high")
	if c := e.status("POST", "/api/v1/approvals/"+ap.ID+"/counterargument", "", nil); c != 404 && c != 405 {
		t.Errorf("unmounted route = %d", c)
	}
}
