package api_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/events"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/roles"
)

func newPermEnv(t *testing.T) *govEnv {
	t.Helper()
	cfg := application.DefaultConfig()
	mem := memory.New()
	if err := mem.Seed(context.Background(), domain.SeedOrg(cfg.BudgetUSD), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := events.NewHub(log)
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: mem, Pub: events.LocalBus{Hub: hub}, Log: log}
	appr := application.NewApprovals(cfg, mem, rec)
	q := &application.Queries{Store: mem, Cfg: cfg}
	svc, err := auth.NewService(auth.Config{Store: auth.NewMemoryStore(), Secret: []byte("0123456789abcdef0123456789abcdef-test"),
		PasswordParams: auth.PasswordParams{Memory: 8, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}})
	if err != nil {
		t.Fatal(err)
	}
	perms := &application.PermissionsService{Store: mem, Agents: mem, Rec: rec, OrgID: cfg.OrgID, Log: log,
		SetLevel: func(ctx context.Context, agentID, autonomy, _ string) error {
			return mem.SetAgentAutonomy(ctx, application.OrgFrom(ctx, cfg.OrgID), agentID, autonomy)
		}}
	d := api.Deps{Cfg: cfg, Queries: q, Approvals: appr, Store: mem, Runtime: proposalRuntime{}, Hub: hub, Log: log, Permissions: perms,
		AuthEnabled: true, Auth: svc, AuthRoutes: auth.NewHandler(svc, auth.HandlerConfig{}).Routes()}
	ts := httptest.NewServer(api.NewRouter(d))
	t.Cleanup(ts.Close)
	return &govEnv{env: &env{t: t, srv: ts, svc: svc, store: mem, hub: hub, appr: appr}, mem: mem}
}

func TestPermissionsEndpoints(t *testing.T) {
	e := newPermEnv(t)
	owner := e.register("owner@example.com", "O")
	admin := e.member(owner, "admin@example.com", auth.RoleAdmin)
	viewer := e.member(owner, "viewer@example.com", auth.RoleViewer)
	tok := owner.AccessToken

	if c := e.status("GET", "/api/v1/agents/sales/permissions", viewer, nil); c != 200 {
		t.Fatalf("viewer reads = %d", c)
	}
	if c := e.status("PUT", "/api/v1/agents/sales/permissions/level", viewer, map[string]string{"level": "important"}); c != 403 {
		t.Fatalf("viewer changes the level = %d, want 403", c)
	}
	if c := e.status("PUT", "/api/v1/agents/sales/permissions/exceptions/messages", tok, map[string]string{"mode": "never"}); c != 409 {
		t.Fatalf("exception before choosing a level = %d, want 409", c)
	}
	if c := e.status("PUT", "/api/v1/agents/sales/permissions/level", admin, map[string]string{"level": "important"}); c != 200 {
		t.Fatalf("admin chooses a level = %d", c)
	}
	if c := e.status("PUT", "/api/v1/agents/sales/permissions/exceptions/messages", admin, map[string]string{"mode": "never"}); c != 200 {
		t.Fatalf("admin sets an exception = %d", c)
	}
	if c := e.status("POST", "/api/v1/agents/sales/permissions/floor/payments", admin, map[string]bool{"understood": true}); c != 403 {
		t.Fatalf("admin lifts the floor = %d, want 403", c)
	}
	_, raw := e.do("POST", "/api/v1/agents/sales/permissions/floor/payments", tok, map[string]bool{"understood": true}, nil)
	if !strings.Contains(string(raw), `"lifted":{"category":"payments"`) {
		t.Fatalf("owner lifts the floor: %s", raw)
	}
	if c := e.status("PUT", "/api/v1/permissions/default", viewer, map[string]string{"level": "alone"}); c != 403 {
		t.Fatalf("viewer changes the default = %d", c)
	}
	_, raw = e.do("GET", "/api/v1/permissions", tok, nil, nil)
	if !strings.Contains(string(raw), `"mode":"never"`) {
		t.Fatalf("the overview lists the exceptions: %s", raw)
	}
}
