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

// debaterStub is the runtime capability of the team debates.
type debaterStub struct {
	calls atomic.Int32
	fail  bool
}

func (d *debaterStub) Debate(_ context.Context, in application.DebateRequest) (application.DebateResponse, error) {
	d.calls.Add(1)
	if d.fail {
		return application.DebateResponse{}, errors.New("model down: sk-live-secretsecretsecret1234")
	}
	var turns []application.DebateTurnOut
	for _, p := range in.Participants {
		turns = append(turns, application.DebateTurnOut{AgentID: p.ID, Kind: "position", Text: "postura de " + p.ID})
	}
	turns = append(turns, application.DebateTurnOut{AgentID: "assistant", Kind: "synthesis", Text: "Por fases."})
	return application.DebateResponse{Turns: turns, Synthesis: domain.DebateSynthesis{Choice: "Por fases", Rationale: "menos riesgo"},
		Usage: &application.Usage{Model: "sim", InputTokens: 500, OutputTokens: 200, CostUSD: 0.003}, Model: "sim", Provider: "simulation"}, nil
}

func TestDebatesAPIRunListAndRead(t *testing.T) {
	stub := &debaterStub{}
	e := newEnv(t, opts{debater: stub})
	resp, body := e.do("POST", "/api/v1/debates", "", map[string]any{"topic": "Precio 2027", "options": []string{"Subir", "Mantener"}, "agent_ids": []string{"sales", "accounting"}}, nil)
	var d domain.Debate
	_ = json.Unmarshal(body, &d)
	if resp.StatusCode != 201 || d.ID == "" || len(d.Turns) != 3 || !d.Advisory || !d.Untrusted || d.Synthesis.Choice != "Por fases" || len(d.Options) != 2 {
		t.Fatalf("POST = %d %s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), `"approved"`) || strings.Contains(string(body), `"decision"`) {
		t.Fatalf("a debate must not carry a decision: %s", body)
	}
	resp, body = e.do("GET", "/api/v1/debates", "", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), d.ID) {
		t.Fatalf("list = %d %s", resp.StatusCode, body)
	}
	resp, body = e.do("GET", "/api/v1/debates/"+d.ID, "", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "postura de sales") {
		t.Fatalf("get = %d %s", resp.StatusCode, body)
	}
	if c := e.status("GET", "/api/v1/debates/nope", "", nil); c != 404 {
		t.Errorf("unknown = %d", c)
	}
	if c := e.status("POST", "/api/v1/debates", "", map[string]any{"topic": ""}); c != 400 {
		t.Errorf("empty topic = %d", c)
	}
	if c := e.status("POST", "/api/v1/debates", "", map[string]any{"topic": "x", "agent_ids": []string{"sales"}}); c != 400 {
		t.Errorf("one participant = %d", c)
	}
}

func TestDebatesAPIDegradesWithoutLeakingTheCause(t *testing.T) {
	e := newEnv(t, opts{debater: &debaterStub{fail: true}})
	resp, body := e.do("POST", "/api/v1/debates", "", map[string]any{"topic": "Precio 2027"}, nil)
	if resp.StatusCode != 503 || !strings.Contains(string(body), `"unavailable"`) || strings.Contains(string(body), "sk-live") ||
		!strings.Contains(string(body), "debate_id") {
		t.Fatalf("POST = %d %s", resp.StatusCode, body)
	}
}

func TestDebatesRoutesAreOffWithoutTheService(t *testing.T) {
	e := newEnv(t, opts{})
	if c := e.status("GET", "/api/v1/debates", "", nil); c != 404 && c != 405 {
		t.Errorf("unmounted route = %d", c)
	}
}
