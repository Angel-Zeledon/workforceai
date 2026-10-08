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
	"syscall"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/config"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/events"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/infrastructure/postgres"
	redisinfra "aiworkforce/backend/internal/infrastructure/redis"
	"aiworkforce/backend/internal/infrastructure/runtime"
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
	if err := store.Seed(ctx, domain.SeedOrg(cfg.App.BudgetUSD), domain.SeedAgents()); err != nil {
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

	// Approval rules and the policy engine (organization rules as data, applied
	// to every tool request) and the tamper-evident audit trail.
	if cs, ok := store.(application.ConfigStore); ok {
		orch.SetPolicy(&application.PolicyService{Store: cs, Cfg: cfg.App})
	}
	var auditSvc *application.AuditService
	if as, ok := store.(application.AuditStore); ok {
		auditSvc = &application.AuditService{Store: as, Rec: rec, Cfg: cfg.App}
	}

	// Projects (whole workflows with parallel work) and agent workspaces (artifacts).
	ws := wireWorkspaces(ctx, cfg, log, pg, store, rt, rec, approvals, orch, orgCfg, cw)

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
	if n, err := orch.Recover(ctx, orgIDs); err != nil {
		log.Warn("recover in-progress requests", "err", err)
	} else if n > 0 {
		log.Info("resumed in-progress requests", "count", n)
	}

	deps := api.Deps{Cfg: cfg.App, Audit: auditSvc, Conns: cw.conns, Controls: cw.ctl, Gateway: cw.gw, Queries: queries, Orch: orch, Approvals: approvals,
		Projects: ws.projects, Artifacts: ws.artifacts,
		Store: store, Runtime: rt, Hub: hub, Log: log, OrgConfig: orgCfg, ModelPolicy: modelPolicy,
		AuthEnabled: cfg.AuthEnabled, AllowedOrigins: cfg.AllowedOrigins,
		EnableDemoReset: cfg.EnableDemoReset, MaxBodyBytes: cfg.MaxBodyBytes}
	if cfg.AuthEnabled {
		svc, err := auth.NewService(auth.Config{
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
		ah := auth.NewHandler(svc, auth.HandlerConfig{Limiter: limiter, TrustedProxies: cfg.TrustedProxies, FailOpen: true})
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
