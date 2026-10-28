package api_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/events"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/roles"
	"aiworkforce/backend/internal/slackapp"
	"aiworkforce/backend/internal/vault"
)

// slackAPIFake answers auth.test for the bot token used below.
type slackAPIFake struct{}

func (slackAPIFake) RoundTrip(r *http.Request) (*http.Response, error) {
	body := `{"ok":false,"error":"unknown_method"}`
	if r.URL.Path == "/api/auth.test" && r.Header.Get("Authorization") == "Bearer xoxb-1111-2222-abcdefghijklmnop" {
		body = `{"ok":true,"team_id":"T0001","team":"Acme","user_id":"UBOT"}`
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
}

func newSlackEnv(t *testing.T) *env {
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
	svc, err := auth.NewService(auth.Config{Store: auth.NewMemoryStore(), Secret: []byte("0123456789abcdef0123456789abcdef-test"),
		PasswordParams: auth.PasswordParams{Memory: 8, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}})
	if err != nil {
		t.Fatal(err)
	}
	kw, err := vault.NewEnvKeyWrapper(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	slack := &slackapp.Service{Store: slackapp.NewMemStore(), Sealer: vault.New(vault.NewMemRepo(), kw),
		Slack: &slackapp.Client{HTTP: &http.Client{Transport: slackAPIFake{}}}, Approvals: slackapp.ApprovalsAdapter{Approvals: appr, Store: mem},
		PublicURL: "https://api.example", Log: log, Go: func(f func()) { f() },
		Audit: func(ctx context.Context, org string, a domain.AuditLog) { rec.Audit(application.WithOrg(ctx, org), a) }}
	d := api.Deps{Cfg: cfg, Queries: &application.Queries{Store: mem, Cfg: cfg}, Approvals: appr, Store: mem, Hub: hub, Log: log, Rec: rec,
		AuthEnabled: true, Auth: svc, AuthRoutes: auth.NewHandler(svc, auth.HandlerConfig{}).Routes(), Slack: slack}
	ts := httptest.NewServer(api.NewRouter(d))
	t.Cleanup(ts.Close)
	return &env{t: t, srv: ts, svc: svc, store: mem, hub: hub, appr: appr}
}

func TestSlackChannelRoutesAndPermissions(t *testing.T) {
	e := newSlackEnv(t)
	o := e.register("owner@example.com", "Acme")
	member := e.member(o, "member@example.com", auth.RoleMember)

	if c := e.status("GET", "/api/v1/channels/slack/settings", "", nil); c != 401 {
		t.Fatalf("anonymous settings = %d", c)
	}
	if c := e.status("GET", "/api/v1/channels/slack/settings", member, nil); c != 403 {
		t.Fatalf("member settings = %d", c)
	}
	if c := e.status("PUT", "/api/v1/channels/slack/settings", member, map[string]any{"enabled": true}); c != 403 {
		t.Fatalf("member put = %d", c)
	}
	resp, raw := e.do("GET", "/api/v1/channels/slack/settings", o.AccessToken, nil, nil)
	want := "https://api.example/api/v1/channels/slack/" + url.PathEscape(o.OrgID) + "/interactions"
	if resp.StatusCode != 200 || !strings.Contains(string(raw), want) || !strings.Contains(string(raw), `"vault_available":true`) {
		t.Fatalf("owner settings = %d %s", resp.StatusCode, raw)
	}

	// Every member may link their own Slack account (one-time code), never anonymously.
	if c := e.status("POST", "/api/v1/channels/slack/link-code", "", nil); c != 401 {
		t.Fatalf("anonymous code = %d", c)
	}
	resp, raw = e.do("POST", "/api/v1/channels/slack/link-code", member, nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(raw), `"code"`) || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("member code = %d %s", resp.StatusCode, raw)
	}

	// The public request URL is mounted without the bearer middleware; before the app exists it is a 404.
	in := "/api/v1/channels/slack/" + url.PathEscape(o.OrgID) + "/interactions"
	if c := e.status("POST", in, "", nil); c != 404 {
		t.Fatalf("unconfigured request URL = %d", c)
	}
	const sec = "8f742231b10e8888abcd99yyyzzz85a5"
	resp, raw = e.do("PUT", "/api/v1/channels/slack/settings", o.AccessToken, map[string]any{
		"signing_secret": sec, "bot_token": "xoxb-1111-2222-abcdefghijklmnop", "enabled": true}, nil)
	if resp.StatusCode != 200 || strings.Contains(string(raw), sec) || strings.Contains(string(raw), "abcdefghijklmnop") || !strings.Contains(string(raw), `"team_id":"T0001"`) {
		t.Fatalf("save = %d %s", resp.StatusCode, raw)
	}
	resp, raw = e.do("PUT", "/api/v1/channels/slack/settings", o.AccessToken, map[string]any{"bot_token": "xoxb-9999-refused-token-x"}, nil)
	if resp.StatusCode != 400 || !strings.Contains(string(raw), "slack_auth_failed") {
		t.Fatalf("refused token = %d %s", resp.StatusCode, raw)
	}
	// Unsigned: 401. Signed by the organization's secret: accepted (the press itself is refused: nobody is linked).
	body := "payload=" + url.QueryEscape(`{"type":"block_actions","team":{"id":"T0001"},"user":{"id":"U1"},"actions":[{"action_id":"approval_approve","value":"x","action_ts":"1"}]}`)
	if c := e.status("POST", in, "", []byte(body)); c != 401 {
		t.Fatalf("unsigned = %d", c)
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	resp, _ = e.do("POST", in, "", []byte(body), map[string]string{"Content-Type": "application/x-www-form-urlencoded",
		"X-Slack-Request-Timestamp": ts, "X-Slack-Signature": slackapp.Sign(sec, ts, []byte(body))})
	if resp.StatusCode != 200 {
		t.Fatalf("signed = %d", resp.StatusCode)
	}
	var page domain.AuditPage
	page, _ = e.store.QueryAudit(context.Background(), o.OrgID, domain.AuditQuery{Action: "channel.slack.*"})
	b, _ := json.Marshal(page.Items)
	if !strings.Contains(string(b), "channel.slack.settings_updated") || !strings.Contains(string(b), `"outcome":"unlinked"`) || strings.Contains(string(b), sec) {
		t.Fatalf("audit = %s", b)
	}
}
