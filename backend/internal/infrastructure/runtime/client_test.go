package runtime

import (
	"errors"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// B8: the devil's advocate call speaks the Python contract and carries no secret-looking field.
func TestClientCounterargumentSpeaksTheContract(t *testing.T) {
	var path string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"counterargument":"c","risks":["r"],"checks":["k"],"usage":{"model":"m","input_tokens":1,"output_tokens":1,"cost_usd":0.1,"duration_ms":1}}`))
	}))
	defer srv.Close()
	out, err := New(srv.URL).Counterargument(context.Background(), application.CounterargumentRequest{Locale: "en",
		Approval: application.CounterargumentBrief{Action: "email.send", Title: "t", Details: "d", Risk: "high", RequiredApprovals: 2}})
	if err != nil || out.Counterargument != "c" || len(out.Risks) != 1 || out.Usage == nil || out.Usage.CostUSD != 0.1 {
		t.Fatalf("response = %+v %v", out, err)
	}
	ap, _ := body["approval"].(map[string]any)
	if path != "/v1/counterargument" || body["locale"] != "en" || ap["risk"] != "high" || ap["required_approvals"] != float64(2) {
		t.Fatalf("request = %s %v", path, body)
	}
	for k := range ap {
		for _, bad := range []string{"token", "secret", "password", "credential", "key"} {
			if strings.Contains(k, bad) {
				t.Errorf("approval brief field %q looks like a credential", k)
			}
		}
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

func TestParseRuntimeErrorUsesTheMachineCode(t *testing.T) {
	e := parseRuntimeError("/x", 402, []byte(`{"detail":{"code":"call_budget_exceeded","estimated_usd":2.5,"allowance_usd":1}}`))
	if !errors.Is(e, application.ErrCallBudgetExceeded) || !errors.Is(e, application.ErrNonRetryable) {
		t.Fatalf("budget: %v", e)
	}
	var re *application.RuntimeError
	if !errors.As(e, &re) || re.EstimatedUSD != 2.5 || re.AllowanceUSD != 1 {
		t.Fatalf("numbers: %+v", re)
	}
	nb := parseRuntimeError("/x", 402, []byte(`{"detail":{"code":"provider_no_balance"}}`))
	if !errors.Is(nb, application.ErrProviderNoBalance) {
		t.Fatalf("no balance: %v", nb)
	}
	// Message text alone must not classify the error.
	if errors.Is(parseRuntimeError("/x", 500, []byte(`provider_no_balance call_budget_exceeded`)), application.ErrProviderNoBalance) {
		t.Fatal("text must not match")
	}
	if errors.Is(parseRuntimeError("/x", 429, []byte(`{}`)), application.ErrNonRetryable) {
		t.Fatal("429 is retryable")
	}
}
