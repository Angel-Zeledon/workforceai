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

// askingRuntime: every task asks once ("which channel?") and finishes once it has an answer.
type askingRuntime struct{ fakeRuntime }

func (askingRuntime) Plan(context.Context, application.PlanRequest) (application.PlanResponse, error) {
	return application.PlanResponse{Tasks: []application.PlannedTask{
		{Key: "a", Title: "Llamar", AgentID: "sales"},
		{Key: "b", Title: "Registrar", AgentID: "analyst", DependsOn: []string{"a"}},
	}}, nil
}

func (askingRuntime) RunTask(_ context.Context, in application.RunTaskRequest) (application.RunTaskResponse, error) {
	r := application.RunTaskResponse{Output: domain.StructuredOutput{Summary: "ok", Confidence: 0.9}}
	if in.HelpAllowed && len(in.HelpAnswers) == 0 && in.Task.Title == "Llamar" {
		r.HelpRequest = &application.HelpRequest{Kind: "decision", WhatHappened: "No hay teléfono", Need: "¿Por qué canal?", Options: []string{"Correo", "WhatsApp"}}
	}
	return r, nil
}

func (e *env) openQuestions(n int) []map[string]any {
	e.t.Helper()
	var items []map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, body := e.do("GET", "/api/v1/questions", "", nil, nil)
		if resp.StatusCode != 200 {
			e.t.Fatalf("list = %d %s", resp.StatusCode, body)
		}
		var out struct {
			Items []map[string]any `json:"items"`
		}
		_ = json.Unmarshal(body, &out)
		if items = out.Items; len(items) >= n {
			return items
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("open questions = %d, want %d", len(items), n)
	return nil
}

func TestQuestionsAPIListAndAnswer(t *testing.T) {
	e := newEnv(t, opts{questions: true, runtime: askingRuntime{}})
	if resp, body := e.do("POST", "/api/v1/requests", "", map[string]any{"text": "contactar"}, nil); resp.StatusCode >= 300 {
		t.Fatalf("submit = %d %s", resp.StatusCode, body)
	}
	items := e.openQuestions(1)
	q := items[0]
	if q["need"] != "¿Por qué canal?" || q["task_title"] != "Llamar" || q["can_answer"] != true {
		t.Fatalf("item = %+v", q)
	}
	id := q["id"].(string)
	resp, body := e.do("GET", "/api/v1/questions/"+id, "", nil, nil)
	var det struct {
		Impact     application.SkipImpact  `json:"impact"`
		Candidates []application.Candidate `json:"reassign_candidates"`
	}
	_ = json.Unmarshal(body, &det)
	if resp.StatusCode != 200 || det.Impact.Allowed || len(det.Impact.Skipped) != 1 || len(det.Candidates) == 0 {
		t.Fatalf("detail = %d %s", resp.StatusCode, body)
	}
	// skipping the only path to the result is refused with the explanation
	resp, body = e.do("POST", "/api/v1/questions/"+id+"/answer", "", map[string]any{"kind": "skip", "confirm": true}, nil)
	var refused struct {
		Code   string                 `json:"code"`
		Impact application.SkipImpact `json:"impact"`
	}
	_ = json.Unmarshal(body, &refused)
	if resp.StatusCode != http.StatusConflict || refused.Code != "skip_blocks_result" || !refused.Impact.AllLost {
		t.Fatalf("skip = %d %s", resp.StatusCode, body)
	}
	if resp, body := e.do("POST", "/api/v1/questions/"+id+"/answer", "", map[string]any{"kind": "option", "option": "Correo"}, nil); resp.StatusCode != 200 {
		t.Fatalf("answer = %d %s", resp.StatusCode, body)
	}
	if resp, _ := e.do("POST", "/api/v1/questions/"+id+"/answer", "", map[string]any{"kind": "option", "option": "Correo"}, nil); resp.StatusCode != http.StatusConflict {
		t.Fatalf("second answer = %d, want 409", resp.StatusCode)
	}
}

func TestAnswerBatch(t *testing.T) {
	e := newEnv(t, opts{questions: true, runtime: askingRuntime{}})
	for i := 0; i < 3; i++ {
		e.do("POST", "/api/v1/requests", "", map[string]any{"text": "contactar"}, nil)
	}
	items := e.openQuestions(3)
	ids := []string{}
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	resp, body := e.do("POST", "/api/v1/questions/answer-batch", "", map[string]any{"ids": ids, "kind": "text", "text": "usa el correo"}, nil)
	var out struct {
		Answered []string          `json:"answered"`
		Failed   map[string]string `json:"failed"`
	}
	_ = json.Unmarshal(body, &out)
	if resp.StatusCode != 200 || len(out.Answered) != 3 || len(out.Failed) != 0 {
		t.Fatalf("batch = %d %s", resp.StatusCode, body)
	}
	if resp, _ := e.do("POST", "/api/v1/questions/answer-batch", "", map[string]any{"ids": ids, "kind": "skip"}, nil); resp.StatusCode != 400 {
		t.Fatalf("batch skip = %d, want 400", resp.StatusCode)
	}
}
