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
