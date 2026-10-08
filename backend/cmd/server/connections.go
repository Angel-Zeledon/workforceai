package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/config"
	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/connections/calendar"
	"aiworkforce/backend/internal/connections/gmail"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/gateway"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/sanitize"
	"aiworkforce/backend/internal/vault"
)

type connWiring struct {
	conns *connections.Service
	ctl   *controls.Service
	gw    *gateway.Gateway
}

// wireConnections builds the vault, connections service, controls, Tool
// Gateway and plugs them into the orchestrator. Without CONNECTIONS_KEK the
// vault stays disabled (fail closed for live credentials) while simulated
// connections keep working.
func wireConnections(ctx context.Context, cfg config.Config, log *slog.Logger, pg *postgres.Store, authStore auth.Store,
	rec *application.Recorder, orch *application.Orchestrator) (connWiring, error) {

	var cstore connections.Store
	var kstore controls.Store
	var vrepo vault.Repo
	var orgIDs func(context.Context) []string
	if pg != nil {
		cs := &postgres.ConnStore{S: pg, ExtraOrgs: []string{cfg.App.OrgID}}
		cstore, kstore, vrepo = cs, cs, &postgres.VaultRepo{S: pg}
		orgIDs = func(ctx context.Context) []string {
			ids, _ := pg.ListOrgIDs(ctx)
			return append(ids, cfg.App.OrgID)
		}
	} else {
		cstore, kstore, vrepo = connections.NewMemStore(), controls.NewMemStore(), vault.NewMemRepo()
		orgIDs = func(context.Context) []string { return []string{cfg.App.OrgID} }
	}

	var kw vault.KeyWrapper
	if cfg.ConnectionsKEK != "" {
		w, err := vault.NewEnvKeyWrapper(cfg.ConnectionsKEK, cfg.ConnectionsKEKPrevious)
		if err != nil {
			return connWiring{}, err
		}
		kw = w
		if cfg.AppEnv == "production" {
			log.Warn("CONNECTIONS_KEK is read from the environment in production: prefer a KMS-backed KeyWrapper and back the key up separately from the database")
		}
	} else {
		log.Warn("CONNECTIONS_KEK not set: live connections are disabled (simulated connections still work)")
	}
	sus := sanitize.NewSuspects()
	sus.Add(cfg.GoogleClientSecret)
	v := vault.New(vrepo, kw)
	v.Suspects = sus

	audit := func(ctx context.Context, org, actor, action, entity, id string, details map[string]any) {
		rec.Audit(application.WithOrg(ctx, org), domain.AuditLog{Actor: actor, Action: action, Entity: entity, EntityID: id, Details: details})
	}
	emit := func(ctx context.Context, org, typ string, payload map[string]any) {
		rec.Emit(application.WithOrg(ctx, org), application.Action{Type: typ, Entity: "connection", Payload: payload, SkipAudit: true})
	}

	gm, err := gmail.New(gmail.Config{})
	if err != nil {
		return connWiring{}, err
	}
	cal, err := calendar.New(calendar.Config{})
	if err != nil {
		return connWiring{}, err
	}
	providers := map[string]connections.Provider{"google_gmail": gm, "google_calendar": cal}
	apps := map[string]connections.OAuthApp{}
	if cfg.GoogleClientID != "" && cfg.GoogleClientSecret != "" {
		// One Google OAuth client serves every Google adapter; each connection
		// still asks only for the scopes of its own capabilities.
		google := connections.OAuthApp{ClientID: cfg.GoogleClientID, ClientSecret: cfg.GoogleClientSecret, RedirectURL: cfg.OAuthRedirectURL}
		for _, id := range []string{"google_gmail", "google_calendar"} {
			apps[id] = google
		}
	} else {
		log.Info("GOOGLE_OAUTH_CLIENT_ID/SECRET not set: Google OAuth (Gmail, Calendar) is unavailable (bring your own OAuth app); simulated connections work")
	}
	adminCount := func(ctx context.Context, org string) int {
		if authStore == nil {
			return 1
		}
		ms, err := authStore.ListMembers(ctx, org)
		if err != nil {
			return 2 // cannot tell: assume more than one admin (the safer side)
		}
		n := 0
		for _, m := range ms {
			if m.Role == auth.RoleOwner || m.Role == auth.RoleAdmin {
				n++
			}
		}
		return n
	}
	cs, err := connections.NewService(connections.Config{Store: cstore, Vault: v, Apps: apps, Suspects: sus,
		Providers: providers, Audit: audit, Emit: emit, AdminCount: adminCount, UIBase: cfg.UIBaseURL, RedirectURL: cfg.OAuthRedirectURL})
	if err != nil {
		return connWiring{}, err
	}
	ctl := controls.New(kstore, controls.AuditFunc(audit), controls.EmitFunc(emit))
	gw := gateway.New(cs, ctl, sus, log)
	gw.Audit, gw.Emit, gw.HoldSeconds, gw.OrgIDs = audit, emit, cfg.EmailHoldSeconds, orgIDs
	cs.OnRevoke = func(ctx context.Context, org, id, reason string) { gw.CancelHoldsFor(ctx, org, id, reason) }
	ctl.Hooks = controls.Hooks{OnKillSwitch: gw.OnKillSwitch, OnRelease: gw.OnRelease}
	orch.SetConnections(gateway.Guard{G: gw}, gw, gw)
	go gw.Run(ctx, 5*time.Second)

	// SIGHUP re-reads KILL_SWITCH from the environment (redundant path when the UI/API do not answer).
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-hup:
				log.Warn("SIGHUP: KILL_SWITCH reloaded", "level", ctl.ReloadEnv())
			}
		}
	}()
	if lvl := ctl.ReloadEnv(); lvl != controls.LevelNone {
		log.Warn("KILL_SWITCH is set in the environment: nothing will run until it is unset and the process is reloaded", "level", lvl)
	}
	return connWiring{conns: cs, ctl: ctl, gw: gw}, nil
}
