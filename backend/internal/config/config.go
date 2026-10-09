// Package config reads the process configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/projects"
)

// MinJWTSecretBytes is the minimum accepted length of JWT_SECRET.
const MinJWTSecretBytes = 32

type Config struct {
	Port        string
	DatabaseURL string
	// MigrateURL (MIGRATE_DATABASE_URL) is the owner/DDL connection used only to
	// apply migrations. Empty means DatabaseURL is used for both.
	MigrateURL string
	// DBAppRole (DB_APP_ROLE, default "app_user") is the non-owner, NOBYPASSRLS
	// role every application connection switches to (SET ROLE). Empty disables
	// the switch (use it when DATABASE_URL already logs in as a plain role).
	DBAppRole  string
	RedisURL   string
	RuntimeURL string
	App        application.Config
	// Projects holds the size limits of projects and the planner bounds (env PROJECT_*, PLANNER_*).
	Projects projects.Limits

	// AuthEnabled turns on JWT authentication, RBAC and per-tenant isolation
	// (org_id comes from the token). Default false (demo mode, fixed org).
	AuthEnabled bool
	// JWTSecret signs access tokens; >= 32 bytes and required with AuthEnabled.
	JWTSecret string
	// AllowedOrigins is the CORS / WebSocket origin allow-list. Empty with
	// AuthEnabled=false means "*" (dev); empty with auth means same-origin only.
	AllowedOrigins []string
	// EnableDemoReset registers POST /api/v1/demo/reset. Default false.
	EnableDemoReset bool
	// TrustedProxies are CIDRs/IPs whose X-Forwarded-For is honoured (rate limiting).
	TrustedProxies []netip.Prefix

	// Connections (docs/architecture/integrations-credentials.md). Without
	// ConnectionsKEK no live credential can be stored (fail closed); simulated
	// connections keep working.
	//
	// ConnectionsKEK is base64 of 32 random bytes (CONNECTIONS_KEK);
	// ConnectionsKEKPrevious (CONNECTIONS_KEK_PREVIOUS) lets old data keys be
	// unwrapped while the KEK is rotated.
	ConnectionsKEK         string
	ConnectionsKEKPrevious string
	// Bring-your-own OAuth app (self-hosted): GOOGLE_OAUTH_CLIENT_ID/SECRET and
	// OAUTH_REDIRECT_URL (the public URL of GET /api/v1/connections/oauth/callback).
	GoogleClientID     string
	GoogleClientSecret string
	OAuthRedirectURL   string
	// UIBaseURL is where the OAuth callback sends the browser afterwards.
	UIBaseURL string
	// EmailHoldSeconds is the cancellation window of an email send. The
	// mandatory minimum is 60 s; smaller values are raised, never honoured.
	EmailHoldSeconds int
	// AppEnv ("production" warns when the KEK comes from the environment).
	AppEnv string

	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	IdleTimeout       time.Duration
	// WriteTimeout is applied per request by the router to normal REST
	// handlers (never to the long-lived WebSocket), see api.Deps.WriteTimeout.
	WriteTimeout time.Duration
	MaxBodyBytes int64

	// errs collects invalid values found while loading; reported by Validate.
	errs []error
}

func getenv(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func getInt(k string, def int) int {
	if n, err := strconv.Atoi(getenv(k, "")); err == nil && n > 0 {
		return n
	}
	return def
}

// getFloat reads a non-negative float; 0 is valid (it disables the limit).
func getFloat(k string, def float64) float64 {
	if v, err := strconv.ParseFloat(getenv(k, ""), 64); err == nil && v >= 0 {
		return v
	}
	return def
}

func getDuration(k string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(getenv(k, "")); err == nil && d > 0 {
		return d
	}
	return def
}

func getBool(k string, def bool) (bool, error) {
	v := strings.ToLower(getenv(k, ""))
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def, fmt.Errorf("%s: invalid boolean %q", k, v)
	}
	return b, nil
}

// splitList parses a comma-separated list, trimming blanks and trailing slashes.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimRight(strings.TrimSpace(p), "/"); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseProxies(list []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, s := range list {
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p)
			continue
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("TRUSTED_PROXIES: invalid CIDR or IP %q", s)
		}
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// Load builds the configuration from the environment. DATABASE_URL is optional
// so the server can run fully in memory for demos; call Validate before use.
func Load() Config {
	app := application.DefaultConfig()
	if v, err := strconv.ParseFloat(getenv("BUDGET_USD", ""), 64); err == nil && v > 0 {
		app.BudgetUSD = v
	}
	actions := getenv("APPROVAL_ACTIONS", "send_proposal,send_contract")
	app.ApprovalActions = map[string]bool{}
	for _, a := range strings.Split(actions, ",") {
		if a = strings.TrimSpace(a); a != "" {
			app.ApprovalActions[a] = true
		}
	}
	app.MaxParallel = getInt("MAX_PARALLEL", app.MaxParallel)
	app.MaxParallelPerOrg = getInt("MAX_PARALLEL_PER_ORG", app.MaxParallelPerOrg)
	app.MaxPlanDepth = getInt("MAX_PLAN_DEPTH", app.MaxPlanDepth)
	app.TaskTimeout = getDuration("TASK_TIMEOUT", app.TaskTimeout)
	app.MaxRetries = getInt("TASK_RETRIES", app.MaxRetries)
	app.ApprovalTimeout = getDuration("APPROVAL_TIMEOUT", app.ApprovalTimeout)
	app.RequestBudgetCapUSD = getFloat("REQUEST_BUDGET_CAP_USD", app.RequestBudgetCapUSD)
	app.AgentBudgetCapUSD = getFloat("AGENT_BUDGET_USD", app.AgentBudgetCapUSD)
	app.ConfirmThresholdUSD = getFloat("COST_CONFIRM_THRESHOLD_USD", app.ConfirmThresholdUSD)
	app.PauseTimeout = getDuration("BUDGET_PAUSE_TIMEOUT", app.PauseTimeout)
	app.TaskMaxAttempts = getInt("TASK_MAX_ATTEMPTS", app.TaskMaxAttempts)
	app.TaskRetryBackoff = getDuration("TASK_RETRY_BACKOFF", app.TaskRetryBackoff)
	app.ProjectReminderEvery = getDuration("PROJECT_APPROVAL_REMINDER_EVERY", app.ProjectReminderEvery)
	app.ProjectApprovalTimeout = getDuration("PROJECT_APPROVAL_TIMEOUT", app.ProjectApprovalTimeout)
	app.ProjectBudgetPauseTimeout = getDuration("PROJECT_BUDGET_PAUSE_TIMEOUT", app.ProjectBudgetPauseTimeout)
	app.ChatStagger = getDuration("CHAT_STAGGER", app.ChatStagger)
	app.ChatTimeout = getDuration("CHAT_TIMEOUT", app.ChatTimeout)
	app.DepContextTokenBudget = getInt("DEP_CONTEXT_TOKEN_BUDGET", app.DepContextTokenBudget)
	app.ProjectContextTokenBudget = getInt("PROJECT_CONTEXT_TOKEN_BUDGET", app.ProjectContextTokenBudget)
	app.SynthTokenBudget = getInt("SYNTH_TOKEN_BUDGET", app.SynthTokenBudget)
	app.SynthMaxGroups = getInt("SYNTH_MAX_GROUPS", app.SynthMaxGroups)
	app.QualityMaxRework = getInt("QUALITY_MAX_REWORK", app.QualityMaxRework)
	app.QualityLowConfidence = getFloat("QUALITY_LOW_CONFIDENCE", app.QualityLowConfidence)

	c := Config{
		Port:              getenv("PORT", "8080"),
		DatabaseURL:       getenv("DATABASE_URL", ""),
		MigrateURL:        getenv("MIGRATE_DATABASE_URL", ""),
		DBAppRole:         getenv("DB_APP_ROLE", "app_user"),
		RedisURL:          getenv("REDIS_URL", ""),
		RuntimeURL:        getenv("RUNTIME_URL", "http://localhost:8000"),
		App:               app,
		Projects:          projectLimits(),
		JWTSecret:         os.Getenv("JWT_SECRET"), // not trimmed: bytes count
		AllowedOrigins:    splitList(getenv("ALLOWED_ORIGINS", "")),
		ReadHeaderTimeout: getDuration("READ_HEADER_TIMEOUT", 10*time.Second),
		ReadTimeout:       getDuration("READ_TIMEOUT", 30*time.Second),
		IdleTimeout:       getDuration("IDLE_TIMEOUT", 120*time.Second),
		WriteTimeout:      getDuration("WRITE_TIMEOUT", 60*time.Second),
		MaxBodyBytes:      int64(getInt("MAX_BODY_BYTES", 1<<20)),

		ConnectionsKEK:         strings.TrimSpace(os.Getenv("CONNECTIONS_KEK")),
		ConnectionsKEKPrevious: strings.TrimSpace(os.Getenv("CONNECTIONS_KEK_PREVIOUS")),
		GoogleClientID:         getenv("GOOGLE_OAUTH_CLIENT_ID", ""),
		GoogleClientSecret:     strings.TrimSpace(os.Getenv("GOOGLE_OAUTH_CLIENT_SECRET")),
		OAuthRedirectURL:       getenv("OAUTH_REDIRECT_URL", "http://localhost:8080/api/v1/connections/oauth/callback"),
		UIBaseURL:              getenv("UI_BASE_URL", ""),
		EmailHoldSeconds:       getInt("EMAIL_HOLD_SECONDS", 60),
		AppEnv:                 strings.ToLower(getenv("APP_ENV", "")),
	}
	var err error
	if c.AuthEnabled, err = getBool("AUTH_ENABLED", false); err != nil {
		c.errs = append(c.errs, err)
	}
	if c.EnableDemoReset, err = getBool("ENABLE_DEMO_RESET", false); err != nil {
		c.errs = append(c.errs, err)
	}
	if c.TrustedProxies, err = parseProxies(splitList(getenv("TRUSTED_PROXIES", ""))); err != nil {
		c.errs = append(c.errs, err)
	}
	return c
}

// Validate reports configuration errors that must stop the boot.
func (c Config) Validate() error {
	errs := append([]error(nil), c.errs...)
	if c.AuthEnabled {
		if len(c.JWTSecret) < MinJWTSecretBytes {
			errs = append(errs, fmt.Errorf("JWT_SECRET must be set and at least %d bytes when AUTH_ENABLED=true (got %d)", MinJWTSecretBytes, len(c.JWTSecret)))
		}
		if c.DatabaseURL == "" {
			errs = append(errs, errors.New("DATABASE_URL is required when AUTH_ENABLED=true (accounts and tenants need Postgres)"))
		}
		for _, o := range c.AllowedOrigins {
			if o == "*" {
				errs = append(errs, errors.New(`ALLOWED_ORIGINS must not contain "*" when AUTH_ENABLED=true`))
			}
		}
	}
	return errors.Join(errs...)
}

// projectLimits reads the size limits of projects and the planner bounds
// (docs/plans/large-workflows.md, W4). Unset or invalid values keep the defaults.
func projectLimits() projects.Limits {
	d := projects.DefaultLimits()
	return projects.Limits{
		MaxNodesPerProject:  getInt("PROJECT_MAX_NODES", d.MaxNodesPerProject),
		MaxChildrenPerGroup: getInt("PROJECT_MAX_CHILDREN_PER_GROUP", d.MaxChildrenPerGroup),
		MaxActiveProjects:   getInt("PROJECT_MAX_ACTIVE", d.MaxActiveProjects),
		MaxPhases:           getInt("PLANNER_MAX_PHASES", d.MaxPhases),
		MaxTasksPerPhase:    getInt("PLANNER_MAX_TASKS_PER_PHASE", d.MaxTasksPerPhase),
		PlannerConcurrency:  getInt("PLANNER_CONCURRENCY", d.PlannerConcurrency),
		PhasesTimeout:       getDuration("PLANNER_PHASES_TIMEOUT", d.PhasesTimeout),
		PhaseTimeout:        getDuration("PLANNER_PHASE_TIMEOUT", d.PhaseTimeout),
		SyncWait:            getDuration("PLANNER_SYNC_WAIT", d.SyncWait),
		NoAuditNodes:        strings.EqualFold(strings.TrimSpace(getenv("QUALITY_AUDIT_NODES", "true")), "false"),
	}
}
