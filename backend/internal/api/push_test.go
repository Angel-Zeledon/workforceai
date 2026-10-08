package api_test

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
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
	"aiworkforce/backend/internal/push"
)

func newPushEnv(t *testing.T, withPush bool) (*env, *memory.Store, *push.MemoryStore) {
	t.Helper()
	cfg := application.DefaultConfig()
	mem := memory.New()
	if err := mem.Seed(context.Background(), domain.SeedOrg(cfg.BudgetUSD), domain.SeedAgents()); err != nil {
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
	ps := push.NewMemoryStore()
	d := api.Deps{Cfg: cfg, Queries: q, Approvals: appr, Store: mem, Hub: hub, Log: log, Rec: rec,
		Audit: &application.AuditService{Store: mem, Rec: rec, Cfg: cfg}, AuthEnabled: true, Auth: svc, AuthRoutes: auth.NewHandler(svc, auth.HandlerConfig{}).Routes()}
	if withPush {
		pub, priv, _ := push.GenerateVAPID()
		v, err := push.ParseVAPID(pub, priv)
		if err != nil {
			t.Fatal(err)
		}
		d.Push = push.New(push.Config{VAPID: v, Subject: "mailto:ops@example.com"}, ps)
	}
	ts := httptest.NewServer(api.NewRouter(d))
	t.Cleanup(ts.Close)
	return &env{t: t, srv: ts, svc: svc, store: mem, hub: hub, appr: appr}, mem, ps
}

func browserSub(t *testing.T, endpoint string) map[string]any {
	t.Helper()
	k, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	return map[string]any{"endpoint": endpoint, "keys": map[string]string{
		"p256dh": base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), "auth": base64.RawURLEncoding.EncodeToString(auth)}}
}

func TestPushConfigDisabledWithoutVAPID(t *testing.T) {
	e, _, _ := newPushEnv(t, false)
	o := e.register("owner@example.com", "O")
	if got := e.status("GET", "/api/v1/push/config", "", nil); got != 401 {
		t.Fatalf("anonymous config = %d", got)
	}
	_, raw := e.do("GET", "/api/v1/push/config", o.AccessToken, nil, nil)
	if !strings.Contains(string(raw), `"enabled":false`) || strings.Contains(string(raw), "public_key") {
		t.Fatalf("config = %s", raw)
	}
	if got := e.status("POST", "/api/v1/push/subscriptions", o.AccessToken, browserSub(t, "https://fcm.googleapis.com/x")); got != 404 && got != 405 {
		t.Fatalf("subscribe without push = %d", got)
	}
}

func TestPushSubscribeUnsubscribeIsolatedAndAudited(t *testing.T) {
	e, mem, ps := newPushEnv(t, true)
	a := e.register("a@example.com", "A")
	b := e.register("b@example.com", "B")
	_, raw := e.do("GET", "/api/v1/push/config", a.AccessToken, nil, nil)
	if !strings.Contains(string(raw), `"enabled":true`) || !strings.Contains(string(raw), "public_key") || strings.Contains(string(raw), "private") {
		t.Fatalf("config = %s", raw)
	}
	if got := e.status("POST", "/api/v1/push/subscriptions", "", browserSub(t, "https://fcm.googleapis.com/x/1")); got != 401 {
		t.Fatalf("anonymous subscribe = %d", got)
	}
	if got := e.status("POST", "/api/v1/push/subscriptions", a.AccessToken, browserSub(t, "https://evil.example/x")); got != 400 {
		t.Fatalf("evil endpoint = %d", got)
	}
	resp, body := e.do("POST", "/api/v1/push/subscriptions", a.AccessToken, browserSub(t, "https://fcm.googleapis.com/x/1"), nil)
	if resp.StatusCode != 201 || strings.Contains(string(body), "fcm.googleapis.com") {
		t.Fatalf("subscribe = %d %s (the endpoint must not be echoed)", resp.StatusCode, body)
	}
	orgA, orgB := a.OrgID, b.OrgID
	if l, _ := ps.ListPushSubscriptions(context.Background(), orgA); len(l) != 1 {
		t.Fatalf("org A has %d subscriptions", len(l))
	}
	if l, _ := ps.ListPushSubscriptions(context.Background(), orgB); len(l) != 0 {
		t.Fatal("org B sees org A subscriptions")
	}
	// Another organization cannot delete it.
	e.status("DELETE", "/api/v1/push/subscriptions", b.AccessToken, map[string]string{"endpoint": "https://fcm.googleapis.com/x/1"})
	if l, _ := ps.ListPushSubscriptions(context.Background(), orgA); len(l) != 1 {
		t.Fatal("org B deleted org A subscription")
	}
	if got := e.status("DELETE", "/api/v1/push/subscriptions", a.AccessToken, map[string]string{"endpoint": "https://fcm.googleapis.com/x/1"}); got != 204 {
		t.Fatalf("unsubscribe = %d", got)
	}
	if l, _ := ps.ListPushSubscriptions(context.Background(), orgA); len(l) != 0 {
		t.Fatal("subscription kept")
	}
	page, _ := mem.QueryAudit(context.Background(), orgA, domain.AuditQuery{Limit: 100})
	seen := map[string]bool{}
	for _, it := range page.Items {
		seen[it.Action] = true
		if strings.Contains(strings.ToLower(strings.TrimSpace(it.EntityID+it.Actor)), "fcm.googleapis") {
			t.Fatal("audit leaks the endpoint")
		}
	}
	if !seen["push.subscribed"] || !seen["push.unsubscribed"] {
		t.Fatalf("audit actions: %v", seen)
	}
}
