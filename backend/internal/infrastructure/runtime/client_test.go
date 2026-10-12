package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"aiworkforce/backend/internal/application"
)

func TestClientSendsTokenAndMergesPolicy(t *testing.T) {
	var auth string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"objectives":[],"tasks":[]}`))
	}))
	defer srv.Close()
	c := New(srv.URL).WithToken(" tok ").WithPolicy(func(context.Context) application.RuntimePolicy {
		return application.RuntimePolicy{AllowedProviders: []string{"anthropic"}, RoleModels: map[string]string{"legal": "anthropic/claude-opus-5-5"}}
	})
	if _, err := c.Plan(context.Background(), application.PlanRequest{RequestText: "hola"}); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer tok" {
		t.Fatalf("authorization = %q", auth)
	}
	if body["request_text"] != "hola" {
		t.Fatalf("original fields lost: %v", body)
	}
	if a, _ := body["allowed_providers"].([]any); len(a) != 1 || a[0] != "anthropic" {
		t.Fatalf("allowed_providers not merged: %v", body)
	}
	if m, _ := body["role_models"].(map[string]any); m["legal"] != "anthropic/claude-opus-5-5" {
		t.Fatalf("role_models not merged: %v", body)
	}
}

// K1: the slash commands' two runtime calls speak the Python contract (paths and field names).
func TestClientSummarizeAndContextSizes(t *testing.T) {
	var paths []string
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		paths, bodies = append(paths, r.URL.Path), append(bodies, b)
		if r.URL.Path == "/v1/summarize" {
			_, _ = w.Write([]byte(`{"text":"resumen","usage":{"model":"m","input_tokens":10,"output_tokens":3,"cost_usd":0.001,"duration_ms":5}}`))
			return
		}
		_, _ = w.Write([]byte(`{"segments":[{"key":"profile","label":"p","tokens":7}],"total_tokens":7}`))
	}))
	defer srv.Close()
	c := New(srv.URL)
	var _ application.SummaryRuntime = c
	var _ application.ContextSizeRuntime = c
	s, err := c.Summarize(context.Background(), application.SummarizeRequest{Mode: "compact", Locale: "en", PreviousSummary: "antes",
		Messages: []application.ChatHistoryItem{{From: "user", Text: "hola"}}})
	if err != nil || s.Text != "resumen" || s.Usage.InputTokens != 10 || s.Usage.CostUSD != 0.001 {
		t.Fatalf("summarize: %+v %v", s, err)
	}
	sizes, err := c.ContextSizes(context.Background(), application.RunTaskRequest{})
	if err != nil || sizes.TotalTokens != 7 || sizes.Segments[0].Key != "profile" {
		t.Fatalf("context sizes: %+v %v", sizes, err)
	}
	if paths[0] != "/v1/summarize" || paths[1] != "/v1/context-sizes" {
		t.Fatalf("paths: %v", paths)
	}
	m := bodies[0]
	msgs, _ := m["messages"].([]any)
	if m["mode"] != "compact" || m["locale"] != "en" || m["previous_summary"] != "antes" || len(msgs) != 1 || msgs[0].(map[string]any)["from"] != "user" {
		t.Fatalf("summarize body: %v", m)
	}
}

func TestClientWithoutTokenOrPolicySendsPlainBody(t *testing.T) {
	var auth string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"objectives":[],"tasks":[]}`))
	}))
	defer srv.Close()
	c := New(srv.URL).WithPolicy(func(context.Context) application.RuntimePolicy { return application.RuntimePolicy{} })
	if _, err := c.Plan(context.Background(), application.PlanRequest{RequestText: "x"}); err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		t.Fatalf("unexpected authorization %q", auth)
	}
	if _, ok := body["allowed_providers"]; ok {
		t.Fatalf("empty policy must add nothing: %v", body)
	}
}
