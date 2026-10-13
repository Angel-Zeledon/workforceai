package api_test

import (
	"testing"

	"aiworkforce/backend/internal/auth"
)

// The web tool setting over HTTP: owner/admin only, OFF by default, validated, per organization, audited.
func TestWebSettings(t *testing.T) {
	e := newEnv(t, opts{auth: true, web: true})
	owner := e.register("owner-web@example.com", "OwnerWeb")
	member := e.member(owner, "member-web@example.com", auth.RoleMember)
	rival := e.register("rival-web@example.com", "RivalWeb")

	s := e.json("GET", api1+"/settings/web", owner.AccessToken, nil, 200)
	if s["enabled"] != false || s["mode"] != "allowlist" || s["search_available"] != false || s["calls_today"].(float64) != 0 {
		t.Fatalf("default settings: %v", s)
	}
	e.json("GET", api1+"/settings/web", member, nil, 403)
	e.json("PUT", api1+"/settings/web", member, map[string]any{"enabled": true}, 403)
	e.json("PUT", api1+"/settings/web", owner.AccessToken, map[string]any{"enabled": true, "mode": "wide"}, 400)
	e.json("PUT", api1+"/settings/web", owner.AccessToken, map[string]any{"enabled": true, "allowed_domains": []string{"127.0.0.1"}}, 400)
	e.json("PUT", api1+"/settings/web", owner.AccessToken, map[string]any{"enabled": true, "max_calls_per_task": 9999}, 400)
	s = e.json("PUT", api1+"/settings/web", owner.AccessToken, map[string]any{"enabled": true, "mode": "allowlist",
		"allowed_domains": []string{"https://Example.com/x", "*.docs.org"}, "blocked_domains": []string{"bad.com"}, "max_calls_per_day": 20}, 200)
	if s["enabled"] != true || len(s["allowed_domains"].([]any)) != 2 || s["allowed_domains"].([]any)[0] != "docs.org" || s["max_calls_per_day"].(float64) != 20 {
		t.Fatalf("put: %v", s)
	}
	if r := e.json("GET", api1+"/settings/web", rival.AccessToken, nil, 200); r["enabled"] != false {
		t.Fatalf("org isolation: %v", r)
	}
	found := false
	for _, a := range e.store.Audit() {
		found = found || a.Action == "web.settings.updated"
	}
	if !found {
		t.Fatal("web.settings.updated audit missing")
	}
}
