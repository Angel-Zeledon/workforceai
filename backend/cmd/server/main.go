// Command server runs the AI Workforce OS backend.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/config"
	"aiworkforce/backend/internal/counters"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/events"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/infrastructure/postgres"
	redisinfra "aiworkforce/backend/internal/infrastructure/redis"
	"aiworkforce/backend/internal/infrastructure/runtime"
	"aiworkforce/backend/internal/mailer"
	"aiworkforce/backend/internal/projects"
	"aiworkforce/backend/internal/push"
	"aiworkforce/backend/internal/rewards"
	"aiworkforce/backend/internal/roles"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	if len(os.Args) > 1 && os.Args[1] == "ctl" {
		return runCtl(log, os.Args[2:])
	}
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Store: Postgres is required in real deployments; without DATABASE_URL we
	// fall back to memory (demo only, data is lost on restart).
	var store application.Store
	var pg *postgres.Store
	if cfg.DatabaseURL != "" {
		var err error
		pg, err = connectPostgres(ctx, cfg, log)
		if err != nil {
			return err
		}
		defer pg.Close()
		store = pg
	} else {
		log.Warn("DATABASE_URL not set: using in-memory store (data is not persisted)")
		store = memory.New()
	}
	if err := store.Seed(ctx, domain.SeedOrg(cfg.App.BudgetUSD), roles.SeedAgents()); err != nil {
		return err
	}

	// Events: Redis pub/sub -> hub; degrade to local delivery when Redis is absent.
	hub := events.NewHub(log)
	local := events.LocalBus{Hub: hub}
	var pub application.Publisher = local
	var locker application.Locker = memory.NewLocker()
	var limiter auth.Limiter = auth.NewMemoryLimiter()
	if cfg.RedisURL != "" {
		rc, err := redisinfra.New(ctx, cfg.RedisURL)
		if err != nil {
			log.Warn("redis unavailable, using in-process events and locks", "err", err)
		} else {
			defer rc.Close()
			pub = events.FallbackBus{Primary: rc, Fallback: local, Log: log}
			locker = rc
			limiter = auth.NewRedisLimiter(rc.Scripter(), "aiworkforce:rl:")
			go hub.Run(ctx, rc.Subscribe(ctx))
		}
	} else {
		log.Warn("REDIS_URL not set: using in-process events and locks")
	}

	// Web Push (optional): approval.requested also notifies subscribed browsers.
	pushSvc := wirePush(log, pg)
	if pushSvc != nil {
		pub = push.Notifier{Inner: pub, Svc: pushSvc}
	}

	// Coins: the observer sees task.completed / project.status_changed after
	// they were published and pays them in the background (internal/rewards).
	rewardsObs := &rewards.Observer{Inner: pub}
	pub = rewardsObs

	rt := runtime.New(cfg.RuntimeURL).WithToken(os.Getenv("RUNTIME_TOKEN"))
	if os.Getenv("RUNTIME_TOKEN") == "" {
		log.Warn("RUNTIME_TOKEN not set: calls to the agent runtime are not authenticated (set it in both services)")
	}
	queries := &application.Queries{Store: store, Cfg: cfg.App}
	rec := &application.Recorder{OrgID: cfg.App.OrgID, Store: store, Pub: pub, Log: log}

	// Organization model policy (allowed/preferred providers, model per role),
	// within the operator ceiling ALLOWED_PROVIDERS; sent on every runtime call.
	var modelPolicy *application.ModelPolicyService
	if ps, ok := store.(application.ModelPolicyStore); ok {
		modelPolicy = &application.ModelPolicyService{Store: ps, Rec: rec, Cfg: cfg.App,
			Ceiling: application.ParseProviderList(os.Getenv("ALLOWED_PROVIDERS"))}
		modelPolicy.Providers = rt.Providers
		rt.WithPolicy(modelPolicy.Runtime)
	}
	approvals := application.NewApprovals(cfg.App, store, rec)
	orch := application.NewOrchestrator(ctx, cfg.App, store, rt, locker, rec, approvals, queries, log)

	// Organization configuration: onboarding packs, workflow templates, regional
	// tone and schedules (daily briefing). Both stores implement ConfigStore.
	var orgCfg *application.OrgConfig
	if cs, ok := store.(application.ConfigStore); ok {
		orgCfg = &application.OrgConfig{Cfg: cfg.App, Store: cs, Core: store, Orch: orch, Rec: rec, Log: log}
		orch.SetStyle(orgCfg)
		var lister application.OrgLister
		if l, ok := store.(application.OrgLister); ok {
			lister = l
		}
		orgCfg.StartScheduler(ctx, 30*time.Second, lister)
	}

	// Connections, vault, Tool Gateway and controls (kill switch, read-only
	// mode, agent pause, plan review). Simulated connections need no key.
	var authStore auth.Store
	if cfg.AuthEnabled {
		authStore = auth.NewPGStore(pg.Pool())
	}
	cw, err := wireConnections(ctx, cfg, log, pg, authStore, rec, orch)
	if err != nil {
		return err
	}

	// Bring-your-own-model: the organization's own provider key (sealed with its data key) is merged
	// into every runtime call; without a choice the platform default (DeepSeek) applies.
	var llmSvc *application.LLMService
	if ls, ok := store.(application.LLMStore); ok && modelPolicy != nil {
		llmSvc = &application.LLMService{Store: ls, Sealer: cw.vault, Tester: rt, Policy: modelPolicy, Rec: rec, Cfg: cfg.App, Suspects: cw.sus}
		rt.WithPolicy(func(c context.Context) application.RuntimePolicy { return llmSvc.Apply(c, modelPolicy.Runtime(c)) })
	}

	approvals.Observer = cw.ctl
	orch.SetAnomalyObserver(cw.ctl)

	// Approval rules and the policy engine (organization rules as data, applied
	// to every tool request) and the tamper-evident audit trail.
	if cs, ok := store.(application.ConfigStore); ok {
		orch.SetPolicy(&application.PolicyService{Store: cs, Cfg: cfg.App, Counters: counterStore(pg), Log: log})
	}
	var auditSvc *application.AuditService
	if as, ok := store.(application.AuditStore); ok {
		auditSvc = &application.AuditService{Store: as, Rec: rec, Cfg: cfg.App}
	}

	// V2: agent work profiles, thought audit (traces, retention) and task intervention.
	var v2Store application.V2Store
	var profiles *application.ProfileService
	var tracer *application.Tracer
	if vs, ok := store.(application.V2Store); ok {
		v2Store = vs
		var tones application.ConfigStore
		if cs, ok := store.(application.ConfigStore); ok {
			tones = cs
		}
		profiles = &application.ProfileService{Store: vs, Core: store, Tones: tones, Rec: rec, Config: cfg.App}
		if orgCfg != nil {
			profiles.Locale = func(c context.Context) string { return orgCfg.Locale(c, "") }
		}
		tracer = &application.Tracer{Store: vs, Log: log, Retention: traceRetention()}
		orch.SetV2(vs, profiles, tracer)
		go purgeTracesLoop(ctx, log, tracer, store)
	}

	// Projects (whole workflows with parallel work) and agent workspaces (artifacts).
	ws := wireWorkspaces(ctx, cfg, log, pg, store, rt, rec, approvals, orch, orgCfg, cw, modelPolicy)
	skillsSvc := wireSkills(cfg, log, pg, store, rec, orch, orgCfg, cw, ws.drive)

	// Standing per-project approvals: "always allow this internal, reversible action in this project".
	var standingSvc *application.StandingService
	if ss, ok := store.(application.StandingStore); ok && ws.projects != nil {
		standingSvc = &application.StandingService{Store: ss, Core: store, Projects: ws.projects, Rec: rec, Cfg: cfg.App, Days: cfg.StandingApprovalDays}
		orch.SetStanding(standingSvc)
	}
	rewardsSvc := wireRewards(log, pg, store, rec, ws.projects)
	chats := wireChatHistory(cfg.App, store, rec, ws.projects)
	rewardsObs.Bind(rewardsSvc)
	// Slash commands: /clear and /compact keep their state in the conversation context store.
	if cc, ok := store.(application.ConvContextStore); ok {
		orch.SetConvContext(cc)
	}

	// Durable execution: resume the requests a previous process left in progress
	// (after the project gate is installed, so project requests are recognized).
	var orgIDs []string
	if l, ok := store.(application.OrgLister); ok {
		if ids, err := l.ListOrgIDs(ctx); err == nil {
			orgIDs = ids
		} else {
			log.Warn("recover: list organizations", "err", err)
		}
	}
	memorySvc := wireMemory(ctx, log, pg, store, rec, orch, cw, ws, orgIDs, cfg.App.OrgID)
	if n, err := orch.Recover(ctx, orgIDs); err != nil {
		log.Warn("recover in-progress requests", "err", err)
	} else if n > 0 {
		log.Info("resumed in-progress requests", "count", n)
	}
	// Launched projects whose request was resumed are already live; the others
	// (their request finished while the process was down) settle their status.
	if ws.projects != nil {
		if n := ws.projects.Recover(ctx, orgIDs); n > 0 {
			log.Info("re-attached launched projects", "count", n)
		}
	}

	deps := api.Deps{Cfg: cfg.App, Audit: auditSvc, Conns: cw.conns, Controls: cw.ctl, Gateway: cw.gw, Queries: queries, Orch: orch, Approvals: approvals,
		Standing: standingSvc,
		Projects: ws.projects, Artifacts: ws.artifacts, Drive: ws.drive, Sandbox: ws.sandbox.svc, SandboxAvailable: ws.sandbox.available, Skills: skillsSvc, Memory: memorySvc, Rewards: rewardsSvc, Chats: chats, Commands: &api.CommandsDeps{LongMemory: longMemoryFor(memorySvc)}, Push: pushSvc, Rec: rec, Profiles: profiles, V2: v2Store, Tracer: tracer,
		Store: store, Runtime: rt, Hub: hub, Log: log, OrgConfig: orgCfg, ModelPolicy: modelPolicy, LLM: llmSvc,
		AuthEnabled: cfg.AuthEnabled, AllowedOrigins: cfg.AllowedOrigins,
		EnableDemoReset: cfg.EnableDemoReset, MaxBodyBytes: cfg.MaxBodyBytes}
	// GDPR export / deletion of the organization and the purge janitor (internal/tenantdata).
	deps.TenantData = wireTenantData(ctx, cfg, log, pg, authStore, limiter, ws.objects)
	if cfg.AuthEnabled {
		if strings.HasPrefix(cfg.JWTSecret, "dev-only-insecure") {
			log.Warn("JWT_SECRET is the local development default from docker-compose.yml: set your own secret before exposing this server")
		}
		var authMailer mailer.Mailer
		if cfg.EmailVerification || cfg.EmailDevLog || cfg.SMTP.Host != "" {
			if cfg.EmailDevLog {
				authMailer = mailer.Log{Log: log}
			} else {
				sm, merr := mailer.NewSMTP(cfg.SMTP)
				if merr != nil {
					return merr
				}
				authMailer = sm
			}
			if cfg.EmailVerification {
				log.Info("email verification is ON for new accounts")
			} else {
				log.Warn("EMAIL_VERIFICATION is off: anyone can register without proving they own the address")
			}
			log.Info("password reset by e-mail is available")
		} else {
			log.Warn("no SMTP configured: new accounts are not verified and passwords cannot be reset by e-mail")
		}
		svc, err := auth.NewService(auth.Config{
			RequireVerification: cfg.EmailVerification, Mailer: authMailer, AppURL: cfg.UIBaseURL,
			TrialEnabled: cfg.TrialEnabled, TrialTTL: time.Duration(cfg.TrialTTLHours) * time.Hour, TrialGlobalPerDay: cfg.TrialGlobalPerDay, TrialEmailDomain: cfg.TrialEmailDomain,
			Logf:                func(f string, a ...any) { log.Warn(fmt.Sprintf(f, a...)) },
			Store:               authStore,
			Secret:              []byte(cfg.JWTSecret),
			DefaultOrgBudgetUSD: cfg.App.BudgetUSD,
			LoginLimiter:        limiter,
			Audit: func(actx context.Context, e auth.AuditEvent) {
				rec.Audit(application.WithOrg(actx, e.OrgID), domain.AuditLog{Actor: e.Actor, Action: e.Action, Entity: e.Entity, EntityID: e.EntityID, Details: e.Details})
			},
		})
		if err != nil {
			return err
		}
		deps.Auth = svc
		ah := auth.NewHandler(svc, auth.HandlerConfig{Limiter: limiter, TrustedProxies: cfg.TrustedProxies, FailOpen: true,
			CookieName: cfg.AuthCookieName, CookieSecure: cfg.AuthCookieSecure, AllowedOrigins: cfg.AllowedOrigins,
			TrialLimit: auth.Limit{Max: cfg.TrialPerIPPerDay, Window: 24 * time.Hour}})
		deps.AuthRoutes, deps.InvitationRoutes = ah.Routes(), ah.InvitationRoutes()
		deps.WSMaxAge = 15 * time.Minute // = default access token lifetime
	} else {
		log.Warn("AUTH_ENABLED=false: the API is open and uses the fixed demo organization (development only)")
	}
	if cfg.EnableDemoReset {
		log.Warn("ENABLE_DEMO_RESET=true: POST /api/v1/demo/reset is exposed")
	}
	srv := api.NewHTTPServer(":"+cfg.Port, api.NewRouter(deps), api.Timeouts{
		ReadHeader: cfg.ReadHeaderTimeout, Read: cfg.ReadTimeout, Write: cfg.WriteTimeout, Idle: cfg.IdleTimeout})

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", srv.Addr, "runtime", cfg.RuntimeURL)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down")
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
		orch.Wait()
	}
	return nil
}

// wireRewards builds the coin service: Postgres when configured, memory
// otherwise. REWARDS_ENABLED=false switches earning off; REWARDS_RULES (JSON)
// overrides the earning rules (docs/architecture/rewards.md).
func wireRewards(log *slog.Logger, pg *postgres.Store, store application.Store, rec *application.Recorder, proj *projects.Service) *rewards.Service {
	cat, err := rewards.LoadCatalog()
	if err != nil {
		log.Error("rewards: catalog", "err", err)
		return nil
	}
	rules, err := rewards.ParseRules(os.Getenv("REWARDS_RULES"))
	if err != nil {
		log.Warn("rewards: using the default rules", "err", err)
		rules = rewards.DefaultRules()
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("REWARDS_ENABLED"))); v == "false" || v == "0" || v == "off" {
		rules.Enabled = false
	}
	var rs rewards.Store = rewards.NewMemStore()
	if pg != nil {
		rs = pg
	}
	svc := &rewards.Service{Store: rs, Catalog: cat, Rules: rules, Rec: rec, Log: log,
		Agents: func(ctx context.Context, org, id string) bool {
			_, err := store.GetAgent(ctx, org, id)
			return err == nil
		},
		// A task with an approval a human rejected earns nothing.
		Rejected: func(ctx context.Context, org, taskID, requestID string) bool {
			var list []domain.Approval
			var err error
			if l, ok := store.(application.RequestApprovalLister); ok && requestID != "" {
				list, err = l.ListApprovalsByRequest(ctx, org, requestID, nil)
			} else {
				list, err = store.ListApprovals(ctx, org, string(domain.ApprovalRejected))
			}
			if err != nil {
				return false
			}
			for _, a := range list {
				if a.TaskID == taskID && a.Status == domain.ApprovalRejected {
					return true
				}
			}
			return false
		},
	}
	if proj != nil {
		svc.Classifier = proj
	}
	// Every organization starts with coins to spend (REWARDS_WELCOME_COINS, default 1000; 0 turns it off).
	// The grant happens lazily, the first time the organization opens the shop or the office.
	svc.WelcomeCoins = 1000
	if v := strings.TrimSpace(os.Getenv("REWARDS_WELCOME_COINS")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			svc.WelcomeCoins = n
		}
	}
	return svc
}

// counterStore is where windowed counters (policy limits, anomaly detection)
// persist: Postgres when configured, otherwise process memory (lost on restart).
func counterStore(pg *postgres.Store) counters.Store {
	if pg != nil {
		return pg
	}
	return counters.NewMemStore()
}

// connectPostgres retries so that the backend survives starting before the DB.
func connectPostgres(ctx context.Context, cfg config.Config, log *slog.Logger) (*postgres.Store, error) {
	var lastErr error
	for i := 0; i < 30; i++ {
		pg, err := postgres.Open(ctx, postgres.Options{URL: cfg.DatabaseURL, MigrateURL: cfg.MigrateURL, AppRole: cfg.DBAppRole})
		if err == nil {
			return pg, nil
		}
		lastErr = err
		log.Warn("waiting for postgres", "attempt", i+1, "err", err)
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

// traceRetention is TRACE_RETENTION_DAYS (default 30): how long thought-audit traces are kept.
func traceRetention() time.Duration {
	days := application.DefaultTraceRetention
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("TRACE_RETENTION_DAYS"))); err == nil && n > 0 {
		days = n
	}
	return time.Duration(days) * 24 * time.Hour
}

// purgeTracesLoop deletes expired traces once an hour, organization by organization (RLS applies).
func purgeTracesLoop(ctx context.Context, log *slog.Logger, tr *application.Tracer, store application.Store) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		orgs := []string{domain.DemoOrgID}
		if l, ok := store.(application.OrgLister); ok {
			if ids, err := l.ListOrgIDs(ctx); err == nil {
				orgs = application.WithDefaultOrg(ids, domain.DemoOrgID)
			}
		}
		for _, org := range orgs {
			if n, err := tr.Purge(ctx, org); err != nil {
				log.Warn("trace purge", "org", org, "err", err)
			} else if n > 0 {
				log.Info("expired traces purged", "org", org, "count", n)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// wireChatHistory builds the chat history service (docs/architecture/chat-history.md)
// over the store, which implements application.ChatHistoryStore in Postgres and in memory.
func wireChatHistory(cfg application.Config, store application.Store, rec *application.Recorder, proj *projects.Service) *application.ChatHistory {
	hs, ok := store.(application.ChatHistoryStore)
	if !ok {
		return nil
	}
	h := &application.ChatHistory{Store: hs, Core: store, Rec: rec, Cfg: cfg}
	if proj != nil {
		h.ProjectOf = proj.ProjectIDForRequest
	}
	return h
}
