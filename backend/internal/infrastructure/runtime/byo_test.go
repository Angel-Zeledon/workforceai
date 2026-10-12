package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aiworkforce/backend/internal/application"
)

func TestClientSendsTheOrgKeyOnlyWhereItIsUsed(t *testing.T) {
	got := map[string]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		got[r.URL.Path] = b
		_, _ = w.Write([]byte(`{"ok":true,"code":"ok","provider":"openai"}`))
	}))
	defer srv.Close()
	cred := application.RuntimeCredential{Provider: "openai", APIKey: "sk-secret-0123456789", Model: "gpt-4.1"}
	c := New(srv.URL).WithPolicy(func(context.Context) application.RuntimePolicy {
		return application.RuntimePolicy{ProviderCredentials: []application.RuntimeCredential{cred}, ByoOnly: true, PreferredProviders: []string{"openai"}}
	})
	_, _ = c.Plan(context.Background(), application.PlanRequest{RequestText: "hola"})
	creds, _ := got["/v1/plan"]["provider_credentials"].([]any)
	if len(creds) != 1 || got["/v1/plan"]["byo_only"] != true {
		t.Fatalf("plan must carry the org key: %v", got["/v1/plan"])
	}
	if first, _ := creds[0].(map[string]any); first["api_key"] != "sk-secret-0123456789" || first["provider"] != "openai" {
		t.Fatalf("credential = %v", creds[0])
	}
	// The cost estimate makes no model call: it must not receive the key.
	_, _ = c.Estimate(context.Background(), application.EstimateRequest{})
	if _, has := got["/v1/estimate"]["provider_credentials"]; has {
		t.Fatalf("estimate received the org key: %v", got["/v1/estimate"])
	}
	// The connection test carries ONLY the credential being tested, never the policy's.
	res, err := c.TestProvider(context.Background(), application.RuntimeCredential{Provider: "openai", APIKey: "sk-typed-0123456789"})
	if err != nil || !res.OK {
		t.Fatalf("test = %+v %v", res, err)
	}
	if _, has := got["/v1/provider-test"]["provider_credentials"]; has {
		t.Fatalf("provider-test got the policy credentials: %v", got["/v1/provider-test"])
	}
	if cr, _ := got["/v1/provider-test"]["credential"].(map[string]any); cr["api_key"] != "sk-typed-0123456789" {
		t.Fatalf("provider-test body = %v", got["/v1/provider-test"])
	}
}

func TestRuntimeCredentialNeverFormatsItsKey(t *testing.T) {
	cred := application.RuntimeCredential{Provider: "openai", APIKey: "sk-secret-0123456789"}
	pol := application.RuntimePolicy{ProviderCredentials: []application.RuntimeCredential{cred}}
	for _, s := range []string{fmt.Sprint(cred), fmt.Sprintf("%v %+v %#v", cred, cred, cred), fmt.Sprint(pol), fmt.Sprintf("%+v", pol)} {
		if strings.Contains(s, "sk-secret") {
			t.Fatalf("formatting leaks the key: %s", s)
		}
	}
}
