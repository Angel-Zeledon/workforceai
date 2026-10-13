package api_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/vault"
)

const fakeKey = "sk-proj-TESTONLY-abcdefghijklmnop-1234567890"

// fakeLLMTester stands in for the runtime: it records what it was given and answers by key prefix.
type fakeLLMTester struct {
	mu    sync.Mutex
	calls []application.RuntimeCredential
}

func (f *fakeLLMTester) TestProvider(_ context.Context, c application.RuntimeCredential) (application.LLMTestResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	res := application.LLMTestResult{Provider: c.Provider, Model: c.Model, LatencyMS: 3}
	switch {
	case strings.HasPrefix(c.APIKey, "sim-ok"):
		res.OK, res.Code = true, "ok"
	case strings.HasPrefix(c.APIKey, "sim-quota"):
		res.Code = "quota"
	default:
		res.Code, res.Message = "invalid_key", "provider rejected the key "+c.APIKey // an echo the service must scrub
	}
	return res, nil
}

type llmEnv struct {
	svc    *application.LLMService
	tester *fakeLLMTester
	store  *memory.Store
	policy *application.ModelPolicyService
}

func attachLLM(d *api.Deps, mem *memory.Store, rec *application.Recorder, cfg application.Config, noKEK bool) *llmEnv {
	var kw vault.KeyWrapper
	if !noKEK {
		w, err := vault.NewEnvKeyWrapper(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
		if err != nil {
			panic(err)
		}
		kw = w
	}
	v := vault.New(vault.NewMemRepo(), kw)
	pol := &application.ModelPolicyService{Store: mem, Rec: rec, Cfg: cfg}
	t := &fakeLLMTester{}
	svc := &application.LLMService{Store: mem, Sealer: v, Tester: t, Policy: pol, Rec: rec, Cfg: cfg}
	d.LLM, d.ModelPolicy = svc, pol
	return &llmEnv{svc: svc, tester: t, store: mem, policy: pol}
}

func TestLLMDefaultIsThePlatformModel(t *testing.T) {
	e := newEnv(t, opts{llm: true})
	resp, body := e.do("GET", "/api/v1/settings/llm", "", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("get = %d %s", resp.StatusCode, body)
	}
	var v application.LLMView
	_ = json.Unmarshal(body, &v)
	if v.ActiveProvider != "deepseek" || !v.UsingPlatformModel || !v.EncryptionOn || len(v.Providers) != 5 {
		t.Fatalf("view = %s", body)
	}
	for _, p := range v.Providers {
		if p.Configured {
			t.Errorf("%s configured by default", p.ID)
		}
	}
}

func TestLLMKeyIsNeverReturnedAndAlwaysEncrypted(t *testing.T) {
	e := newEnv(t, opts{llm: true})
	resp, body := e.do("PUT", "/api/v1/settings/llm/providers/openai", "", map[string]any{
		"api_key": fakeKey, "model": "gpt-4.1-mini", "org_id": "org-abc", "project_id": "proj_xyz"}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("put = %d %s", resp.StatusCode, body)
	}
	var v application.LLMView
	_ = json.Unmarshal(body, &v)
	var oa application.LLMProviderView
	for _, p := range v.Providers {
		if p.ID == "openai" {
			oa = p
		}
	}
	if !oa.Configured || oa.KeyLast4 != "7890" || oa.Model != "gpt-4.1-mini" || oa.OrgID != "org-abc" || oa.UpdatedAt == nil {
		t.Fatalf("openai view = %+v", oa)
	}
	// No response of any endpoint, no stored byte and no audit entry may contain the key.
	for _, path := range []string{"/api/v1/settings/llm", "/api/v1/settings/model-policy"} {
		_, b := e.do("GET", path, "", nil, nil)
		if strings.Contains(string(b), fakeKey) || strings.Contains(string(b), "TESTONLY") {
			t.Errorf("%s leaks the key: %s", path, b)
		}
	}
	if strings.Contains(string(body), "TESTONLY") {
		t.Errorf("put response leaks the key")
	}
	recs, _ := e.llm.store.ListLLMCredentials(context.Background(), domain.DemoOrgID)
	if len(recs) != 1 || len(recs[0].Sealed) == 0 || strings.Contains(string(recs[0].Sealed), "TESTONLY") {
		t.Fatalf("stored record must be ciphertext: %+v", recs)
	}
	raw, _ := json.Marshal(recs[0])
	if strings.Contains(string(raw), "TESTONLY") || strings.Contains(string(raw), "sealed") {
		t.Errorf("a record serialized to JSON must carry no key material: %s", raw)
	}
	for _, a := range e.store.Audit() {
		b, _ := json.Marshal(a)
		if strings.Contains(string(b), "TESTONLY") {
			t.Errorf("audit entry leaks the key: %s", b)
		}
	}
	if !hasAuditAction(e.store, "settings.llm_credential_set") {
		t.Error("set was not audited")
	}
}

func hasAuditAction(s *memory.Store, action string) bool {
	for _, a := range s.Audit() {
		if a.Action == action {
			return true
		}
	}
	return false
}

func TestLLMSelectRotateDeleteAreAudited(t *testing.T) {
	e := newEnv(t, opts{llm: true})
	put := func(key string) int {
		return e.status("PUT", "/api/v1/settings/llm/providers/openai", "", map[string]any{"api_key": key, "model": "gpt-4.1"})
	}
	if c := put(fakeKey); c != 200 {
		t.Fatalf("put = %d", c)
	}
	if c := e.status("PUT", "/api/v1/settings/llm/active", "", map[string]any{"provider": "openai"}); c != 200 {
		t.Fatalf("select = %d", c)
	}
	_, body := e.do("GET", "/api/v1/settings/llm", "", nil, nil)
	var v application.LLMView
	_ = json.Unmarshal(body, &v)
	if v.ActiveProvider != "openai" || v.UsingPlatformModel {
		t.Fatalf("active = %+v", v)
	}
	if c := put("sk-proj-ROTATED-abcdefghijklmnop-0000"); c != 200 {
		t.Fatalf("rotate = %d", c)
	}
	// Changing only the model keeps the key.
	if c := e.status("PUT", "/api/v1/settings/llm/providers/openai", "", map[string]any{"model": "gpt-4o"}); c != 200 {
		t.Fatalf("model-only update = %d", c)
	}
	// What the runtime would receive: the rotated key, the new model, strict BYO.
	p := e.llm.svc.Apply(context.Background(), application.RuntimePolicy{})
	if !p.ByoOnly || len(p.ProviderCredentials) != 1 || p.ProviderCredentials[0].APIKey != "sk-proj-ROTATED-abcdefghijklmnop-0000" ||
		p.ProviderCredentials[0].Model != "gpt-4o" || p.PreferredProviders[0] != "openai" {
		t.Fatalf("runtime policy = %+v", p)
	}
	// Deleting the active credential returns the organization to the platform default.
	if c := e.status("DELETE", "/api/v1/settings/llm/providers/openai", "", nil); c != 200 {
		t.Fatalf("delete = %d", c)
	}
	_, body = e.do("GET", "/api/v1/settings/llm", "", nil, nil)
	v = application.LLMView{}
	_ = json.Unmarshal(body, &v)
	if v.ActiveProvider != "deepseek" || !v.UsingPlatformModel {
		t.Fatalf("after delete: %s", body)
	}
	if p := e.llm.svc.Apply(context.Background(), application.RuntimePolicy{}); !p.Empty() {
		t.Fatalf("default org must send nothing, got %+v", p)
	}
	for _, a := range []string{"settings.llm_credential_set", "settings.llm_credential_rotated", "settings.llm_credential_updated",
		"settings.llm_active_changed", "settings.llm_credential_deleted"} {
		if !hasAuditAction(e.store, a) {
			t.Errorf("missing audit %s", a)
		}
	}
}

func TestLLMCannotSelectAProviderWithoutAKey(t *testing.T) {
	e := newEnv(t, opts{llm: true})
	if c := e.status("PUT", "/api/v1/settings/llm/active", "", map[string]any{"provider": "kimi"}); c != 400 {
		t.Errorf("select without key = %d, want 400", c)
	}
	if c := e.status("PUT", "/api/v1/settings/llm/active", "", map[string]any{"provider": "mistral"}); c != 400 {
		t.Errorf("select unknown = %d, want 400", c)
	}
	if c := e.status("PUT", "/api/v1/settings/llm/active", "", map[string]any{"provider": "deepseek"}); c != 200 {
		t.Errorf("select platform default = %d", c)
	}
}

func TestLLMValidation(t *testing.T) {
	e := newEnv(t, opts{llm: true})
	for name, c := range map[string]struct {
		provider string
		body     map[string]any
	}{
		"missing key":            {"openai", map[string]any{"model": "gpt-4.1"}},
		"short key":              {"openai", map[string]any{"api_key": "abc"}},
		"key with space":         {"openai", map[string]any{"api_key": "sk-abc def-0123456789"}},
		"unknown provider":       {"mistral", map[string]any{"api_key": fakeKey}},
		"bad model":              {"openai", map[string]any{"api_key": fakeKey, "model": "gpt 4; drop"}},
		"http base for openai":   {"openai", map[string]any{"api_key": fakeKey, "base_url": "http://x.example.com/v1"}},
		"base url on anthropic":  {"anthropic", map[string]any{"api_key": fakeKey, "base_url": "https://x.example.com"}},
		"custom without base":    {"custom", map[string]any{"api_key": fakeKey, "model": "llama3"}},
		"metadata service":       {"custom", map[string]any{"api_key": fakeKey, "model": "m", "base_url": "http://169.254.169.254/v1"}},
		"credentials in url":     {"kimi", map[string]any{"api_key": fakeKey, "base_url": "https://user:pw@x.example.com/v1"}},
		"org id on kimi":         {"kimi", map[string]any{"api_key": fakeKey, "org_id": "org-1"}},
		"org id with bad chars":  {"openai", map[string]any{"api_key": fakeKey, "org_id": "org 1;"}},
		"custom without a model": {"custom", map[string]any{"api_key": fakeKey, "base_url": "https://x.example.com/v1"}},
	} {
		if code := e.status("PUT", "/api/v1/settings/llm/providers/"+c.provider, "", c.body); code != 400 {
			t.Errorf("%s: status %d, want 400", name, code)
		}
	}
	// Valid ones.
	for name, c := range map[string]struct {
		provider string
		body     map[string]any
	}{
		"kimi default url": {"kimi", map[string]any{"api_key": fakeKey, "model": "kimi-latest"}},
		"kimi custom url":  {"kimi", map[string]any{"api_key": fakeKey, "model": "kimi-k2-0905-preview", "base_url": "https://api.moonshot.cn/v1/"}},
		"custom local":     {"custom", map[string]any{"api_key": fakeKey, "model": "llama3", "base_url": "http://llm.internal:11434/v1"}},
		"anthropic":        {"anthropic", map[string]any{"api_key": fakeKey}},
	} {
		if code := e.status("PUT", "/api/v1/settings/llm/providers/"+c.provider, "", c.body); code != 200 {
			t.Errorf("%s: status %d, want 200", name, code)
		}
	}
	// Two of the cases above write the same provider (kimi) and map iteration order is random, so the last write
	// would be arbitrary: write the custom-URL one again, last, to make the check below deterministic.
	if code := e.status("PUT", "/api/v1/settings/llm/providers/kimi", "", map[string]any{"api_key": fakeKey, "model": "kimi-k2-0905-preview", "base_url": "https://api.moonshot.cn/v1/"}); code != 200 {
		t.Errorf("kimi custom url (final write): status %d, want 200", code)
	}
	_, body := e.do("GET", "/api/v1/settings/llm", "", nil, nil)
	var v application.LLMView
	_ = json.Unmarshal(body, &v)
	for _, p := range v.Providers {
		if p.ID == "kimi" && p.BaseURL != "https://api.moonshot.cn/v1" {
			t.Errorf("kimi base url = %q (trailing slash must be trimmed)", p.BaseURL)
		}
	}
}

func TestLLMConnectionTestCodesAndNoStorage(t *testing.T) {
	e := newEnv(t, opts{llm: true})
	test := func(key string) application.LLMTestResult {
		resp, body := e.do("POST", "/api/v1/settings/llm/providers/openai/test", "", map[string]any{"api_key": key, "model": "gpt-4.1"}, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("test = %d %s", resp.StatusCode, body)
		}
		var r application.LLMTestResult
		_ = json.Unmarshal(body, &r)
		if strings.Contains(string(body), key) {
			t.Errorf("test response echoes the key: %s", body)
		}
		return r
	}
	if r := test(fakeKey); r.OK || r.Code != "invalid_key" {
		t.Errorf("fake key = %+v, want invalid_key", r)
	}
	if r := test("sim-quota-0123456789"); r.Code != "quota" {
		t.Errorf("quota = %+v", r)
	}
	if r := test("sim-ok-0123456789"); !r.OK || r.Code != "ok" {
		t.Errorf("ok = %+v", r)
	}
	// A test with a typed key stores nothing.
	recs, _ := e.llm.store.ListLLMCredentials(context.Background(), domain.DemoOrgID)
	if len(recs) != 0 {
		t.Errorf("a test must not store the key: %+v", recs)
	}
	// Without a typed key and without a stored one there is nothing to test.
	if c := e.status("POST", "/api/v1/settings/llm/providers/openai/test", "", nil); c != 400 {
		t.Errorf("test without key = %d, want 400", c)
	}
	// With a stored key the test uses it (and records the outcome).
	e.status("PUT", "/api/v1/settings/llm/providers/openai", "", map[string]any{"api_key": "sim-ok-STORED-0123456789", "model": "gpt-4.1"})
	resp, body := e.do("POST", "/api/v1/settings/llm/providers/openai/test", "", nil, nil)
	var r application.LLMTestResult
	_ = json.Unmarshal(body, &r)
	if resp.StatusCode != 200 || !r.OK {
		t.Fatalf("stored test = %d %s", resp.StatusCode, body)
	}
	last := e.llm.tester.calls[len(e.llm.tester.calls)-1]
	if last.APIKey != "sim-ok-STORED-0123456789" || last.Model != "gpt-4.1" {
		t.Errorf("stored key was not used: %+v", last)
	}
	_, gb := e.do("GET", "/api/v1/settings/llm", "", nil, nil)
	if !strings.Contains(string(gb), `"last_test_code":"ok"`) {
		t.Errorf("outcome not recorded: %s", gb)
	}
	if !hasAuditAction(e.store, "settings.llm_credential_tested") {
		t.Error("test was not audited")
	}
}

func TestLLMEncryptionOffFailsClosed(t *testing.T) {
	e := newEnv(t, opts{llm: true, llmNoKEK: true})
	resp, body := e.do("PUT", "/api/v1/settings/llm/providers/openai", "", map[string]any{"api_key": fakeKey}, nil)
	if resp.StatusCode != 503 || !strings.Contains(string(body), "encryption_unavailable") {
		t.Fatalf("put without KEK = %d %s, want 503", resp.StatusCode, body)
	}
	recs, _ := e.llm.store.ListLLMCredentials(context.Background(), domain.DemoOrgID)
	if len(recs) != 0 {
		t.Fatalf("nothing may be stored without encryption: %+v", recs)
	}
	_, gb := e.do("GET", "/api/v1/settings/llm", "", nil, nil)
	var v application.LLMView
	_ = json.Unmarshal(gb, &v)
	if v.EncryptionOn {
		t.Errorf("view must say encryption is unavailable")
	}
}

func TestLLMOnlyOwnersAndAdminsAndOrgsAreIsolated(t *testing.T) {
	e := newEnv(t, opts{llm: true, auth: true})
	owner := e.register("owner-llm@example.com", "LlmCo")
	admin := e.member(owner, "admin-llm@example.com", auth.RoleAdmin)
	member := e.member(owner, "member-llm@example.com", auth.RoleMember)
	viewer := e.member(owner, "viewer-llm@example.com", auth.RoleViewer)
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/v1/settings/llm"},
		{"PUT", "/api/v1/settings/llm/providers/openai"},
		{"DELETE", "/api/v1/settings/llm/providers/openai"},
		{"POST", "/api/v1/settings/llm/providers/openai/test"},
		{"PUT", "/api/v1/settings/llm/active"},
	} {
		for tok, want := range map[string]int{"": 401, viewer: 403, member: 403} {
			if c := e.status(route.method, route.path, tok, map[string]any{"api_key": fakeKey}); c != want {
				t.Errorf("%s %s with role token %q... = %d, want %d", route.method, route.path, tok[:min(len(tok), 4)], c, want)
			}
		}
	}
	if c := e.status("PUT", "/api/v1/settings/llm/providers/openai", admin, map[string]any{"api_key": fakeKey, "model": "gpt-4.1"}); c != 200 {
		t.Fatalf("admin put = %d", c)
	}
	// A second organization never sees the first one's credential nor its key.
	other := e.register("other-llm@example.com", "OtherCo")
	_, body := e.do("GET", "/api/v1/settings/llm", other.AccessToken, nil, nil)
	var v application.LLMView
	_ = json.Unmarshal(body, &v)
	for _, p := range v.Providers {
		if p.Configured {
			t.Errorf("another organization sees %s as configured", p.ID)
		}
	}
	_, body = e.do("GET", "/api/v1/settings/llm", owner.AccessToken, nil, nil)
	_ = json.Unmarshal(body, &v)
	var seen bool
	for _, p := range v.Providers {
		seen = seen || (p.ID == "openai" && p.Configured)
	}
	if !seen {
		t.Errorf("owner does not see its own credential: %s", body)
	}
}

func TestLLMActiveMustBeAllowedByThePolicy(t *testing.T) {
	e := newEnv(t, opts{llm: true})
	e.status("PUT", "/api/v1/settings/llm/providers/openai", "", map[string]any{"api_key": fakeKey})
	if c := e.status("PUT", "/api/v1/settings/model-policy", "", map[string]any{"allowed_providers": []string{"deepseek", "anthropic"}}); c != 200 {
		t.Fatalf("policy = %d", c)
	}
	if c := e.status("PUT", "/api/v1/settings/llm/active", "", map[string]any{"provider": "openai"}); c != 400 {
		t.Errorf("active outside the data policy = %d, want 400", c)
	}
	// The existing per-role override works with the new providers.
	if c := e.status("PUT", "/api/v1/settings/model-policy", "", map[string]any{"allowed_providers": []string{"deepseek", "openai"},
		"role_models": map[string]string{"legal": "openai/gpt-4o-mini"}}); c != 200 {
		t.Errorf("role override on openai = %d", c)
	}
	if c := e.status("PUT", "/api/v1/settings/model-policy", "", map[string]any{"role_models": map[string]string{"legal": "openai/kimi-k2-0905-preview"}}); c != 400 {
		t.Errorf("a kimi model under openai = %d, want 400", c)
	}
}
