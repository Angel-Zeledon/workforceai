package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/scim"
	"aiworkforce/backend/internal/sso"
	"aiworkforce/backend/internal/vault"
	"github.com/go-chi/chi/v5"
)

// identityWiring is the enterprise identity stack (docs/architecture/identity.md): OIDC SSO, verified domains, the
// sign-in policy and SCIM. SSO is available on every plan.
type identityWiring struct {
	sso  *sso.Service
	scim *scim.Service
	// base URLs shown to administrators and registered at the identity provider.
	ssoBase, scimBase string
	// janitor purges abandoned login records and re-checks verified domains; run it with startJanitor.
	janitor *sso.Janitor
}

// wireIdentity builds the services. Call it before auth.NewService (the auth service takes the sso service as its
// policy provider) and then identityRoutes once the auth service exists.
func wireIdentity(pg *postgres.Store, authStore auth.Store, v *vault.Vault, rec *application.Recorder, uiBase string) (*identityWiring, error) {
	var ss sso.Store = sso.NewMemStore()
	var cs scim.Store = scim.NewMemStore()
	if pg != nil {
		ss, cs = sso.NewPGStore(pg.Pool()), scim.NewPGStore(pg.Pool())
	}
	api := strings.TrimRight(os.Getenv("PUBLIC_API_URL"), "/")
	if api == "" {
		api = strings.TrimRight(uiBase, "/") + "/api/v1"
	}
	if strings.HasPrefix(api, "/") { // no UI_BASE_URL: local development
		api = "http://localhost:8080/api/v1"
	}
	redirect := strings.TrimSpace(os.Getenv("SSO_REDIRECT_URL"))
	if redirect == "" {
		redirect = api + "/sso/callback"
	}
	appURL := strings.TrimRight(uiBase, "/")
	if appURL == "" {
		appURL = "http://localhost:3000"
	}
	audit := func(ctx context.Context, org, actor, action string, details map[string]any) {
		rec.Audit(application.WithOrg(ctx, org), domain.AuditLog{Actor: actor, Action: action, Entity: "identity", EntityID: org, Details: details})
	}
	sc, err := scim.New(scim.Options{Store: cs, Auth: authStore, Audit: audit,
		DomainVerified: func(ctx context.Context, org, email string) bool {
			i := strings.LastIndexByte(email, '@')
			if i < 0 {
				return false
			}
			o, err := ss.OrgForVerifiedDomain(ctx, email[i+1:])
			return err == nil && o == org
		}})
	if err != nil {
		return nil, err
	}
	so, err := sso.New(sso.Options{Store: ss, Sealer: v, RedirectURL: redirect, AppURL: appURL, Audit: audit, Gate: sc.Deactivated})
	if err != nil {
		return nil, err
	}
	jan := &sso.Janitor{Store: ss, Audit: audit}
	return &identityWiring{sso: so, scim: sc, ssoBase: redirect, scimBase: api + "/scim/v2", janitor: jan}, nil
}

// routes returns the three handlers mounted by the router's "enterprise identity" block.
func (w *identityWiring) routes(svc *auth.Service, hc sso.HandlerConfig, sc scim.HandlerConfig) (public, admin, scimRoutes http.Handler) {
	sc.BaseURL = w.scimBase
	sh := scim.NewHandler(w.scim, sc)
	hc.Auth = svc
	hc.Extra = func(r chi.Router) { sh.AdminRoutes(r) }
	h := sso.NewHandler(w.sso, hc)
	return h.PublicRoutes(), h.AdminRoutes(), sh.Routes()
}

// startJanitor sweeps once an hour until ctx ends (each domain is re-checked at most once per Janitor.Recheck).
func (w *identityWiring) startJanitor(ctx context.Context, log *slog.Logger) {
	w.janitor.Log = log
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			if rep := w.janitor.RunOnce(ctx); rep.Lapsed+rep.Revoked+rep.Errors > 0 {
				log.Info("sso janitor", "checked", rep.Checked, "lapsed", rep.Lapsed, "recovered", rep.Recovered, "revoked", rep.Revoked, "errors", rep.Errors)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}
