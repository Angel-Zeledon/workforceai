package config

import (
	"strings"
	"testing"
)

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"AUTH_ENABLED", "JWT_SECRET", "ALLOWED_ORIGINS", "ENABLE_DEMO_RESET", "DATABASE_URL", "TRUSTED_PROXIES", "MIGRATE_DATABASE_URL", "DB_APP_ROLE"} {
		t.Setenv(k, "")
	}
}

func TestDefaultsAreDevFriendlyButSafe(t *testing.T) {
	clearEnv(t)
	c := Load()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.AuthEnabled || c.EnableDemoReset || len(c.AllowedOrigins) != 0 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if c.ReadTimeout <= 0 || c.IdleTimeout <= 0 || c.MaxBodyBytes <= 0 {
		t.Fatalf("timeouts/limits must have defaults: %+v", c)
	}
	if c.DBAppRole != "app_user" {
		t.Fatalf("DBAppRole = %q", c.DBAppRole)
	}
}

func TestAuthRequiresStrongSecret(t *testing.T) {
	clearEnv(t)
	t.Setenv("AUTH_ENABLED", "true")
	t.Setenv("DATABASE_URL", "postgres://x")

	if err := Load().Validate(); err == nil || !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Fatalf("missing secret must fail, got %v", err)
	}
	t.Setenv("JWT_SECRET", strings.Repeat("a", 31))
	if err := Load().Validate(); err == nil {
		t.Fatal("31-byte secret must fail")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("a", 32))
	if err := Load().Validate(); err != nil {
		t.Fatalf("32-byte secret must pass: %v", err)
	}
}

func TestAuthRequiresDatabaseAndRejectsWildcardOrigin(t *testing.T) {
	clearEnv(t)
	t.Setenv("AUTH_ENABLED", "1")
	t.Setenv("JWT_SECRET", strings.Repeat("s", 40))
	if err := Load().Validate(); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("auth without DB must fail, got %v", err)
	}
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("ALLOWED_ORIGINS", "https://app.example.com, *")
	if err := Load().Validate(); err == nil || !strings.Contains(err.Error(), "ALLOWED_ORIGINS") {
		t.Fatalf("wildcard origin with auth must fail, got %v", err)
	}
}

func TestParsing(t *testing.T) {
	clearEnv(t)
	t.Setenv("ALLOWED_ORIGINS", " https://a.com/ ,https://b.com,, ")
	t.Setenv("ENABLE_DEMO_RESET", "true")
	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8, 192.168.1.1")
	c := Load()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(c.AllowedOrigins) != 2 || c.AllowedOrigins[0] != "https://a.com" || c.AllowedOrigins[1] != "https://b.com" {
		t.Fatalf("origins = %v", c.AllowedOrigins)
	}
	if !c.EnableDemoReset || len(c.TrustedProxies) != 2 {
		t.Fatalf("cfg = %+v", c)
	}
}

func TestInvalidValuesFailBoot(t *testing.T) {
	clearEnv(t)
	t.Setenv("AUTH_ENABLED", "maybe")
	if err := Load().Validate(); err == nil {
		t.Fatal("invalid AUTH_ENABLED must fail")
	}
	clearEnv(t)
	t.Setenv("TRUSTED_PROXIES", "not-an-ip")
	if err := Load().Validate(); err == nil {
		t.Fatal("invalid TRUSTED_PROXIES must fail")
	}
}

func TestCostControlEnv(t *testing.T) {
	clearEnv(t)
	c := Load()
	if c.App.ConfirmThresholdUSD != 1.0 || c.App.RequestBudgetCapUSD != 0 || c.App.AgentBudgetCapUSD != 0 {
		t.Fatalf("defaults: %+v", c.App)
	}
	t.Setenv("REQUEST_BUDGET_CAP_USD", "2.5")
	t.Setenv("AGENT_BUDGET_USD", "10")
	t.Setenv("COST_CONFIRM_THRESHOLD_USD", "0") // 0 disables the confirmation
	t.Setenv("BUDGET_PAUSE_TIMEOUT", "5m")
	c = Load()
	if c.App.RequestBudgetCapUSD != 2.5 || c.App.AgentBudgetCapUSD != 10 || c.App.ConfirmThresholdUSD != 0 || c.App.PauseTimeout.Minutes() != 5 {
		t.Fatalf("env not applied: %+v", c.App)
	}
	t.Setenv("COST_CONFIRM_THRESHOLD_USD", "-3") // invalid: keep the default
	if got := Load().App.ConfirmThresholdUSD; got != 1.0 {
		t.Fatalf("invalid value must keep the default, got %v", got)
	}
}

func TestMaxParallelPerOrg(t *testing.T) {
	t.Setenv("MAX_PARALLEL_PER_ORG", "")
	if got := Load().App.MaxParallelPerOrg; got != 8 {
		t.Fatalf("default = %d, want 8", got)
	}
	t.Setenv("MAX_PARALLEL_PER_ORG", "3")
	if got := Load().App.MaxParallelPerOrg; got != 3 {
		t.Fatalf("env = %d, want 3", got)
	}
	t.Setenv("MAX_PARALLEL_PER_ORG", "0") // invalid values keep the default
	if got := Load().App.MaxParallelPerOrg; got != 8 {
		t.Fatalf("zero = %d, want 8", got)
	}
}
