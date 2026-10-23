package slackapp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
)

const (
	org     = "org-a"
	secret  = "8f742231b10e8888abcd99yyyzzz85a5"
	botTok  = "xoxb-1111-2222-abcdefghijklmnop"
	team    = "T0001"
	channel = "C0APPROVALS"
)

// ---- fakes ----

// fakeSealer binds the purpose and the organization like the vault (AAD) and never stores the plaintext as is.
type fakeSealer struct{ off bool }

func (f fakeSealer) Enabled() bool { return !f.off }
func (f fakeSealer) Seal(_ context.Context, o, purpose string, plain []byte) ([]byte, error) {
	rev := []byte(string(plain))
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return []byte(o + "|" + purpose + "|" + base64.StdEncoding.EncodeToString(rev)), nil
}
func (f fakeSealer) Unseal(_ context.Context, o, purpose string, sealed []byte) ([]byte, error) {
	parts := strings.SplitN(string(sealed), "|", 3)
	if len(parts) != 3 || parts[0] != o || parts[1] != purpose {
		return nil, errors.New("wrong key or purpose")
	}
	rev, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev, nil
}

// fakeSlack answers the Web API and records what the server sends to Slack.
type fakeSlack struct {
	mu        sync.Mutex
	authErr   string
	users     map[string]map[string]any
	posts     []map[string]any
	updates   []map[string]any
	responses []map[string]any
	calls     []string
}

func (f *fakeSlack) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	f.calls = append(f.calls, r.URL.Host+r.URL.Path)
	ok := func(v map[string]any) (*http.Response, error) {
		v["ok"] = true
		b, _ := json.Marshal(v)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b)), Header: http.Header{}}, nil
	}
	fail := func(code string) (*http.Response, error) {
		b, _ := json.Marshal(map[string]any{"ok": false, "error": code})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b)), Header: http.Header{}}, nil
	}
	if r.URL.Host == "hooks.slack.com" {
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		f.responses = append(f.responses, m)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: http.Header{}}, nil
	}
	if r.Header.Get("Authorization") != "Bearer "+botTok && !strings.HasSuffix(r.URL.Path, "auth.test") {
		return fail("invalid_auth")
	}
	switch r.URL.Path {
	case "/api/auth.test":
		if f.authErr != "" || r.Header.Get("Authorization") != "Bearer "+botTok {
			return fail(firstNonEmpty(f.authErr, "invalid_auth"))
		}
		return ok(map[string]any{"team_id": team, "team": "Acme", "user_id": "UBOT"})
	case "/api/users.info":
		form, _ := url.ParseQuery(string(body))
		u, found := f.users[form.Get("user")]
		if !found {
			return fail("user_not_found")
		}
		return ok(map[string]any{"user": u})
	case "/api/chat.postMessage":
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		f.posts = append(f.posts, m)
		return ok(map[string]any{"channel": m["channel"], "ts": "1700000000.000100"})
	case "/api/chat.update":
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		f.updates = append(f.updates, m)
		return ok(map[string]any{})
	}
	return fail("unknown_method")
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func (f *fakeSlack) lastResponse() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.responses) == 0 {
		return nil
	}
	return f.responses[len(f.responses)-1]
}

func (f *fakeSlack) responseCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.responses)
}

// fakeDir is the platform directory: members with their current role and whether their e-mail is verified.
type fakeDir struct {
	mu       sync.Mutex
	members  map[string]Member
	verified map[string]bool
}

func (d *fakeDir) Member(_ context.Context, o, userID string) (Member, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	m, ok := d.members[userID]
	if !ok || o != org {
		return Member{}, ErrNotFound
	}
	return m, nil
}

func (d *fakeDir) MemberByVerifiedEmail(ctx context.Context, o, email string) (Member, error) {
	d.mu.Lock()
	var id string
	for _, m := range d.members {
		if m.Email == email && d.verified[m.UserID] {
			id = m.UserID
		}
	}
	d.mu.Unlock()
	if id == "" {
		return Member{}, ErrNotFound
	}
	return d.Member(ctx, o, id)
}

func (d *fakeDir) set(id, role string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.members[id] = Member{UserID: id, Email: id + "@acme.test", Name: id, Role: role}
}

func (d *fakeDir) remove(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.members, id)
}

type nopPub struct{}

func (nopPub) Publish(context.Context, domain.Event) error { return nil }

type harness struct {
	t     *testing.T
	svc   *Service
	store *MemStore
	mem   *memory.Store
	slack *fakeSlack
	dir   *fakeDir
	now   time.Time
	kill  string
	ro    bool

	mu     sync.Mutex
	audits []domain.AuditLog
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, store: NewMemStore(), mem: memory.New(), slack: &fakeSlack{users: map[string]map[string]any{}},
		dir: &fakeDir{members: map[string]Member{}, verified: map[string]bool{}}, now: time.Unix(1_800_000_000, 0).UTC()}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rec := &application.Recorder{OrgID: org, Store: h.mem, Pub: nopPub{}, Log: log}
	appr := application.NewApprovals(application.Config{OrgID: org}, h.mem, rec)
	h.svc = &Service{Store: h.store, Sealer: fakeSealer{}, Slack: &Client{HTTP: &http.Client{Transport: h.slack}}, Directory: h.dir,
		Approvals: ApprovalsAdapter{Approvals: appr, Store: h.mem},
		Controls:  func(context.Context, string) (string, bool) { return h.kill, h.ro },
		Status: func(ctx context.Context, o string) (OfficeStatus, error) {
			aps, _ := h.mem.ListApprovals(ctx, o, "pending")
			return OfficeStatus{PendingApprovals: len(aps), Running: 2, KillSwitch: h.kill, ReadOnly: h.ro}, nil
		},
		Audit: func(_ context.Context, _ string, e domain.AuditLog) {
			h.mu.Lock()
			h.audits = append(h.audits, e)
			h.mu.Unlock()
		},
		AppURL: "https://app.example", PublicURL: "https://api.example", Log: log,
		Now: func() time.Time { return h.now }, Go: func(f func()) { f() }}
	on := true
	s, b, c := secret, botTok, channel
	if _, err := h.svc.Save(context.Background(), org, "owner-1", Input{SigningSecret: &s, BotToken: &b, ChannelID: &c, Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) approval(id string, mut func(*domain.Approval)) domain.Approval {
	ap := domain.Approval{ID: id, TaskID: "task-" + id, AgentID: "agent-1", Action: "email.send", Title: "Send the invoice to ACME",
		Details: "SECRET-DETAIL account 1234", Risk: "medium", Status: domain.ApprovalPending, RequiredApprovals: 1, CreatedAt: h.now}
	if mut != nil {
		mut(&ap)
	}
	if err := h.mem.CreateApproval(context.Background(), org, ap); err != nil {
		h.t.Fatal(err)
	}
	return ap
}

func (h *harness) get(id string) domain.Approval {
	ap, err := h.mem.GetApproval(context.Background(), org, id)
	if err != nil {
		h.t.Fatal(err)
	}
	return ap
}

// link links a Slack user to a platform user through the real code flow (issue in the app, type in Slack).
func (h *harness) link(slackUser, userID string) {
	h.t.Helper()
	code, _, err := h.svc.IssueCode(context.Background(), org, userID)
	if err != nil {
		h.t.Fatal(err)
	}
	out := h.command(slackUser, "vincular "+code)
	if !strings.Contains(fmt.Sprint(out["text"]), "Listo") {
		h.t.Fatalf("link failed: %v", out)
	}
}

func (h *harness) signed(path, body string, ts time.Time, sec string) *http.Request {
	tsv := strconv.FormatInt(ts.Unix(), 10)
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Slack-Request-Timestamp", tsv)
	r.Header.Set("X-Slack-Signature", Sign(sec, tsv, []byte(body)))
	return r
}

func interactionBody(teamID, user, action, value, actionTS string) string {
	p := map[string]any{"type": "block_actions", "team": map[string]any{"id": teamID}, "user": map[string]any{"id": user, "team_id": teamID},
		"response_url": "https://hooks.slack.com/actions/" + teamID + "/1/abc",
		"actions":      []map[string]any{{"action_id": action, "value": value, "action_ts": actionTS, "block_id": "approval:" + value}}}
	b, _ := json.Marshal(p)
	return "payload=" + url.QueryEscape(string(b))
}

func (h *harness) press(user, action, id, actionTS string) *httptest.ResponseRecorder {
	h.t.Helper()
	w := httptest.NewRecorder()
	h.svc.InteractionsHandler(func(*http.Request) string { return org }).ServeHTTP(w,
		h.signed("/i", interactionBody(team, user, action, id, actionTS), h.now, secret))
	return w
}

func (h *harness) command(user, txt string) map[string]any {
	h.t.Helper()
	body := url.Values{"team_id": {team}, "user_id": {user}, "command": {"/oficina"}, "text": {txt}}.Encode()
	w := httptest.NewRecorder()
	h.svc.CommandsHandler(func(*http.Request) string { return org }).ServeHTTP(w, h.signed("/c", body, h.now, secret))
	if w.Code != 200 {
		h.t.Fatalf("command %q = %d %s", txt, w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		h.t.Fatal(err)
	}
	return out
}

func (h *harness) decisions(outcome string) []domain.AuditLog {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []domain.AuditLog
	for _, a := range h.audits {
		if a.Action == "channel.slack.decision" && (outcome == "" || det(a, "outcome") == outcome) {
			out = append(out, a)
		}
	}
	return out
}

// det reads one detail of an audit entry.
func det(a domain.AuditLog, k string) any {
	if m, ok := a.Details.(map[string]any); ok {
		return m[k]
	}
	b, _ := json.Marshal(a.Details)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m[k]
}

func replyText(m map[string]any) string {
	b, _ := json.Marshal(m)
	return string(b)
}

// ---- signature ----

func TestVerifySignature(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	body := []byte("payload=%7B%22type%22%3A%22block_actions%22%7D")
	ts := strconv.FormatInt(now.Unix(), 10)
	good := Sign(secret, ts, body)
	if err := Verify(secret, ts, good, body, now); err != nil {
		t.Fatalf("valid signature refused: %v", err)
	}
	if err := Verify(secret, ts, good, body, now.Add(4*time.Minute)); err != nil {
		t.Fatalf("4 minutes old must still verify: %v", err)
	}
	cases := []struct {
		name          string
		ts, sig       string
		body          []byte
		at            time.Time
		want          error
		secretOveride string
	}{
		{"stale (6 min old, replay)", ts, good, body, now.Add(6 * time.Minute), ErrStale, ""},
		{"from the future", ts, good, body, now.Add(-6 * time.Minute), ErrStale, ""},
		{"tampered body", ts, good, append(append([]byte{}, body...), 'x'), now, ErrBadSignature, ""},
		{"tampered timestamp", strconv.FormatInt(now.Unix()+1, 10), good, body, now, ErrBadSignature, ""},
		{"wrong secret", ts, good, body, now, ErrBadSignature, "another-secret-of-another-org-xx"},
		{"v1 prefix", ts, "v1=" + strings.TrimPrefix(good, "v0="), body, now, ErrBadSignature, ""},
		{"missing signature", ts, "", body, now, ErrNoSignature, ""},
		{"missing timestamp", "", good, body, now, ErrNoSignature, ""},
		{"garbage timestamp", "abc", good, body, now, ErrNoSignature, ""},
	}
	for _, c := range cases {
		sec := secret
		if c.secretOveride != "" {
			sec = c.secretOveride
		}
		if err := Verify(sec, c.ts, c.sig, c.body, c.at); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
}

func TestInboundRequestsNeedAValidFreshSignatureAndTheRightWorkspace(t *testing.T) {
	h := newHarness(t)
	h.dir.set("admin-1", "admin")
	h.link("U1", "admin-1")
	h.approval("ap1", nil)
	handler := h.svc.InteractionsHandler(func(*http.Request) string { return org })
	body := interactionBody(team, "U1", ActionApprove, "ap1", "1.1")

	for name, req := range map[string]*http.Request{
		"stale":       h.signed("/i", body, h.now.Add(-6*time.Minute), secret),
		"wrongSecret": h.signed("/i", body, h.now, "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"),
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d", name, w.Code)
		}
	}
	// Tampered: signed for one body, another body is sent (an approve turned into another approval id).
	r := h.signed("/i", body, h.now, secret)
	r.Body = io.NopCloser(strings.NewReader(interactionBody(team, "U1", ActionApprove, "ap2", "1.1")))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("tampered: status %d", w.Code)
	}
	// A request signed correctly but for another workspace.
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, h.signed("/i", interactionBody("T9999", "U1", ActionApprove, "ap1", "1.1"), h.now, secret))
	if w.Code != http.StatusForbidden {
		t.Errorf("other workspace: status %d", w.Code)
	}
	// An organization without a Slack app does not exist for Slack.
	w = httptest.NewRecorder()
	h.svc.InteractionsHandler(func(*http.Request) string { return "org-b" }).ServeHTTP(w, h.signed("/i", body, h.now, secret))
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown org: status %d", w.Code)
	}
	// Oversized bodies are refused before anything is verified or parsed.
	big := strings.Repeat("a", MaxBodyBytes+10)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, h.signed("/i", big, h.now, secret))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized: status %d", w.Code)
	}
	if h.get("ap1").Status != domain.ApprovalPending || len(h.decisions("")) != 0 {
		t.Fatal("a refused request decided something")
	}
	// The genuine request works.
	if w := h.press("U1", ActionApprove, "ap1", "1.1"); w.Code != 200 {
		t.Fatalf("genuine: %d", w.Code)
	}
	if h.get("ap1").Status != domain.ApprovalApproved {
		t.Fatal("the genuine request did not decide")
	}
}

// ---- identity ----

func TestUnmappedSlackUserCannotDecide(t *testing.T) {
	h := newHarness(t)
	h.dir.set("admin-1", "admin")
	h.approval("ap1", nil)
	// Not linked, and Slack does not vouch for the e-mail: refused, nothing decided.
	h.slack.users["U1"] = map[string]any{"id": "U1", "team_id": team, "is_email_confirmed": false, "profile": map[string]any{"email": "admin-1@acme.test"}}
	h.press("U1", ActionApprove, "ap1", "1.1")
	if h.get("ap1").Status != domain.ApprovalPending {
		t.Fatal("an unmapped Slack user decided")
	}
	if got := h.decisions(OutcomeUnlinked); len(got) != 1 || det(got[0], "channel") != "slack" || !strings.HasPrefix(got[0].Actor, "slack:") {
		t.Fatalf("refusal audit = %+v", got)
	}
	if !strings.Contains(replyText(h.slack.lastResponse()), "vincular") || h.slack.lastResponse()["replace_original"] != false {
		t.Fatalf("the person must get an ephemeral 'link your account' reply: %v", h.slack.lastResponse())
	}
	// Slack confirms the e-mail but the platform never verified it: still refused.
	h.slack.users["U1"]["is_email_confirmed"] = true
	h.press("U1", ActionApprove, "ap1", "1.2")
	if h.get("ap1").Status != domain.ApprovalPending || len(h.decisions(OutcomeUnlinked)) != 2 {
		t.Fatal("an e-mail verified on one side only must not map")
	}
	// A bot user with a matching, verified e-mail never maps.
	h.dir.verified["admin-1"] = true
	h.slack.users["UBOT2"] = map[string]any{"id": "UBOT2", "team_id": team, "is_bot": true, "is_email_confirmed": true, "profile": map[string]any{"email": "admin-1@acme.test"}}
	h.press("UBOT2", ActionApprove, "ap1", "1.3")
	if h.get("ap1").Status != domain.ApprovalPending {
		t.Fatal("a bot mapped by e-mail")
	}
	// The organization turned e-mail matching off: verified on both sides is not enough without the explicit link.
	off := false
	if _, err := h.svc.Save(context.Background(), org, "owner-1", Input{EmailMatching: &off}); err != nil {
		t.Fatal(err)
	}
	h.press("U1", ActionApprove, "ap1", "1.4")
	if h.get("ap1").Status != domain.ApprovalPending {
		t.Fatal("e-mail matching is off but the user was mapped")
	}
	// Both sides verified and matching on: mapped, decided as that person, audited with the identity method.
	on := true
	if _, err := h.svc.Save(context.Background(), org, "owner-1", Input{EmailMatching: &on}); err != nil {
		t.Fatal(err)
	}
	h.press("U1", ActionApprove, "ap1", "1.5")
	ap := h.get("ap1")
	if ap.Status != domain.ApprovalApproved || len(ap.Decisions) != 1 || ap.Decisions[0].By != "admin-1" || ap.Decisions[0].Role != "admin" {
		t.Fatalf("e-mail mapped decision = %+v", ap)
	}
	if got := h.decisions(OutcomeApproved); len(got) != 1 || got[0].Actor != "admin-1" || det(got[0], "identity") != LinkByEmail {
		t.Fatalf("approved audit = %+v", got)
	}
}

func TestLinkCodeIsSingleUseOrgBoundAndBraked(t *testing.T) {
	h := newHarness(t)
	h.dir.set("admin-1", "admin")
	code, exp, err := h.svc.IssueCode(context.Background(), org, "admin-1")
	if err != nil || len(code) != 9 || !exp.After(h.now) {
		t.Fatalf("code = %q %v %v", code, exp, err)
	}
	if out := h.command("U1", "vincular "+strings.ToLower(code)); !strings.Contains(fmt.Sprint(out["text"]), "admin-1@acme.test") {
		t.Fatalf("link = %v", out)
	}
	if l, err := h.store.GetLink(context.Background(), org, team, "U1"); err != nil || l.UserID != "admin-1" || l.Method != LinkByCode {
		t.Fatalf("stored link = %+v %v", l, err)
	}
	// Used once: another Slack user cannot reuse it.
	if out := h.command("U2", "vincular "+code); !strings.Contains(fmt.Sprint(out["text"]), "no es válido") {
		t.Fatalf("reuse = %v", out)
	}
	// Expired codes do not work.
	code2, _, _ := h.svc.IssueCode(context.Background(), org, "admin-1")
	h.now = h.now.Add(DefaultCodeTTL + time.Second)
	if out := h.command("U3", "vincular "+code2); !strings.Contains(fmt.Sprint(out["text"]), "no es válido") {
		t.Fatalf("expired = %v", out)
	}
	// Guessing is braked: after maxLinkFailures misses even a valid code is refused for a while.
	for i := 0; i < maxLinkFailures; i++ {
		h.command("U4", "vincular AAAA-BBBB")
	}
	code3, _, _ := h.svc.IssueCode(context.Background(), org, "admin-1")
	if out := h.command("U4", "vincular "+code3); !strings.Contains(fmt.Sprint(out["text"]), "no es válido") {
		t.Fatalf("brake = %v", out)
	}
	if _, err := h.store.GetLink(context.Background(), org, team, "U4"); !errors.Is(err, ErrNotFound) {
		t.Fatal("a braked user was linked")
	}
	// Unlink from Slack.
	if out := h.command("U1", "desvincular"); !strings.Contains(fmt.Sprint(out["text"]), "desvinculada") {
		t.Fatalf("unlink = %v", out)
	}
	if links, _ := h.svc.LinksOf(context.Background(), org, "admin-1"); len(links) != 0 {
		t.Fatal("still linked")
	}
}

// ---- permissions ----

func TestSlackAppliesTheSameRolesAsTheWeb(t *testing.T) {
	h := newHarness(t)
	for id, role := range map[string]string{"viewer-1": "viewer", "member-1": "member", "admin-1": "admin", "admin-2": "admin", "owner-1": "owner"} {
		h.dir.set(id, role)
	}
	h.link("UV", "viewer-1")
	h.link("UM", "member-1")
	h.link("UA", "admin-1")
	h.link("UB", "admin-2")
	h.link("UO", "owner-1")

	// Members and viewers cannot decide (the web route needs approvals:decide), not even reject.
	h.approval("ap1", nil)
	h.press("UM", ActionApprove, "ap1", "1")
	h.press("UV", ActionReject, "ap1", "2")
	if h.get("ap1").Status != domain.ApprovalPending || len(h.decisions(OutcomeForbidden)) != 2 {
		t.Fatalf("member/viewer decided: %+v", h.get("ap1"))
	}

	// Required role owner: an admin is refused (and the refusal is audited by the approvals with channel=slack).
	h.approval("ap2", func(a *domain.Approval) { a.RequiredRole = "owner" })
	h.press("UA", ActionApprove, "ap2", "3")
	if h.get("ap2").Status != domain.ApprovalPending || len(h.decisions(OutcomeRoleTooLow)) != 1 {
		t.Fatal("admin approved an owner-only approval")
	}
	page, _ := h.mem.QueryAudit(context.Background(), org, domain.AuditQuery{Action: "approval.decision_refused"})
	if len(page.Items) != 1 || det(page.Items[0], "channel") != "slack" || page.Items[0].Actor != "admin-1" {
		t.Fatalf("application refusal audit = %+v", page.Items)
	}
	h.press("UO", ActionApprove, "ap2", "4")
	if h.get("ap2").Status != domain.ApprovalApproved {
		t.Fatal("the owner could not approve")
	}

	// Double approval with maker-checker: the requester never approves, the same person never counts twice.
	h.approval("ap3", func(a *domain.Approval) {
		a.RequiredApprovals, a.RequiredRole, a.RequestedBy, a.NoSelfApproval = 2, "admin", "owner-1", true
	})
	h.press("UO", ActionApprove, "ap3", "5")
	if len(h.decisions(OutcomeSelf)) != 1 || len(h.get("ap3").Decisions) != 0 {
		t.Fatal("the requester approved their own action")
	}
	h.press("UA", ActionApprove, "ap3", "6")
	if ap := h.get("ap3"); ap.Status != domain.ApprovalPending || len(ap.Decisions) != 1 {
		t.Fatalf("after the first approval = %+v", ap)
	}
	if !strings.Contains(replyText(h.slack.lastResponse()), "1 de 2") || h.slack.lastResponse()["replace_original"] != true {
		t.Fatalf("progress not shown on the message: %v", h.slack.lastResponse())
	}
	h.press("UA", ActionApprove, "ap3", "7") // a NEW press (another action_ts) of the same person
	if ap := h.get("ap3"); ap.Status != domain.ApprovalPending || len(ap.Decisions) != 1 || len(h.decisions(OutcomeAlready)) != 1 {
		t.Fatalf("the same person counted twice: %+v", ap)
	}
	h.press("UB", ActionApprove, "ap3", "8")
	if ap := h.get("ap3"); ap.Status != domain.ApprovalApproved || len(ap.Decisions) != 2 {
		t.Fatalf("second approver = %+v", ap)
	}
	page, _ = h.mem.QueryAudit(context.Background(), org, domain.AuditQuery{Action: "approval.approved"})
	if len(page.Items) != 2 {
		t.Fatalf("approval.approved entries = %d, want 2 (ap2, ap3)", len(page.Items))
	}
	for _, e := range page.Items {
		if det(e, "channel") != "slack" {
			t.Fatalf("approval audit without channel=slack: %+v", e)
		}
	}

	// The role is read NOW: an admin demoted after linking can no longer decide; a removed member neither.
	h.approval("ap4", nil)
	h.dir.set("admin-1", "member")
	h.press("UA", ActionApprove, "ap4", "9")
	h.dir.remove("admin-2")
	h.press("UB", ActionReject, "ap4", "10")
	if h.get("ap4").Status != domain.ApprovalPending || len(h.decisions(OutcomeNotMember)) != 1 {
		t.Fatal("a demoted or removed person decided from Slack")
	}
	// An already decided approval: the message is updated with the real state, nothing changes.
	h.press("UO", ActionReject, "ap2", "11")
	if h.get("ap2").Status != domain.ApprovalApproved || len(h.decisions(OutcomeNotPending)) != 1 {
		t.Fatal("a closed approval was decided again")
	}
}

func TestRetriedInteractionDecidesOnce(t *testing.T) {
	h := newHarness(t)
	h.dir.set("admin-1", "admin")
	h.link("UA", "admin-1")
	h.approval("ap1", func(a *domain.Approval) { a.RequiredApprovals, a.RequiredRole = 2, "admin" })
	for i := 0; i < 3; i++ { // Slack delivering the same press three times
		if w := h.press("UA", ActionApprove, "ap1", "1700000000.123"); w.Code != 200 {
			t.Fatalf("retry %d: %d", i, w.Code)
		}
	}
	ap := h.get("ap1")
	if len(ap.Decisions) != 1 || len(h.decisions("")) != 1 || h.slack.responseCount() != 1 {
		t.Fatalf("a retried press decided %d times (%d audits, %d replies)", len(ap.Decisions), len(h.decisions("")), h.slack.responseCount())
	}
	if got := h.store.OutcomeOf(org, DedupeKey(team, "UA", ActionApprove, "ap1", "1700000000.123")); got != OutcomePartial {
		t.Fatalf("stored outcome = %q", got)
	}
}

func TestKillSwitchReadOnlyAndHighRiskKeepApprovalsInTheApp(t *testing.T) {
	h := newHarness(t)
	h.dir.set("owner-1", "owner")
	h.link("UO", "owner-1")
	h.approval("ap1", nil)
	h.approval("ap2", nil)
	h.kill = "freeze"
	h.press("UO", ActionApprove, "ap1", "1")
	if h.get("ap1").Status != domain.ApprovalPending || len(h.decisions(OutcomeKillSwitch)) != 1 {
		t.Fatal("approved while the kill switch is on")
	}
	h.press("UO", ActionReject, "ap1", "2") // rejecting only stops work: allowed
	if h.get("ap1").Status != domain.ApprovalRejected {
		t.Fatal("a rejection must work under the kill switch")
	}
	h.kill, h.ro = "none", true
	h.press("UO", ActionApprove, "ap2", "3")
	if h.get("ap2").Status != domain.ApprovalPending || len(h.decisions(OutcomeReadOnly)) != 1 {
		t.Fatal("approved in read-only mode")
	}
	h.kill = "unknown" // the controls could not be read: fail closed
	h.ro = false
	h.press("UO", ActionApprove, "ap2", "4")
	if h.get("ap2").Status != domain.ApprovalPending {
		t.Fatal("approved with an unknown control state")
	}
	h.kill = ""
	// High risk and code execution: never approved from Slack while the organization keeps them in the app.
	h.approval("ap3", func(a *domain.Approval) { a.Risk = "high" })
	h.approval("ap4", func(a *domain.Approval) { a.Action = "code.run" })
	h.press("UO", ActionApprove, "ap3", "5")
	h.press("UO", ActionApprove, "ap4", "6")
	if h.get("ap3").Status != domain.ApprovalPending || h.get("ap4").Status != domain.ApprovalPending || len(h.decisions(OutcomeNeedsApp)) != 2 {
		t.Fatal("high risk / code.run approved from Slack")
	}
	// The channel turned off: nothing is decided.
	off := false
	if _, err := h.svc.Save(context.Background(), org, "owner-1", Input{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	h.press("UO", ActionReject, "ap3", "7")
	if h.get("ap3").Status != domain.ApprovalPending || len(h.decisions(OutcomeDisabled)) != 1 {
		t.Fatal("decided while the channel is off")
	}
}

// ---- settings ----

func TestSecretsAreSealedNeverReturnedAndCheckedWithSlack(t *testing.T) {
	h := newHarness(t)
	r, _, _ := h.store.GetRecord(context.Background(), org)
	if bytes.Contains(r.SigningSecretEnc, []byte(secret)) || bytes.Contains(r.BotTokenEnc, []byte(botTok)) || r.TeamID != team || r.TokenLast4 != "mnop" {
		t.Fatalf("record = %+v", r)
	}
	v, _ := h.svc.Settings(context.Background(), org)
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), secret) || strings.Contains(string(b), botTok) || !v.Configured || !v.Enabled ||
		v.InteractionsURL != "https://api.example/api/v1/channels/slack/org-a/interactions" || v.CommandsURL != "https://api.example/api/v1/channels/slack/org-a/commands" {
		t.Fatalf("view = %s", b)
	}
	// Without a vault key nothing secret is stored.
	h.svc.Sealer = fakeSealer{off: true}
	s := secret
	if _, err := h.svc.Save(context.Background(), "org-b", "owner", Input{SigningSecret: &s}); !errors.Is(err, ErrVaultRequired) {
		t.Fatalf("no vault = %v", err)
	}
	h.svc.Sealer = fakeSealer{}
	// Slack refuses the token: nothing is stored.
	h.slack.authErr = "invalid_auth"
	bad := "xoxb-9999-bad-token-value"
	var se *SlackAuthError
	if _, err := h.svc.Save(context.Background(), "org-b", "owner", Input{BotToken: &bad}); !errors.As(err, &se) || se.Code != "invalid_auth" {
		t.Fatalf("refused token = %v", err)
	}
	if _, found, _ := h.store.GetRecord(context.Background(), "org-b"); found {
		t.Fatal("a refused token was stored")
	}
	// Invalid inputs.
	for _, in := range []Input{{ChannelID: ptr("#general")}, {MinRisk: ptr("extreme")}, {SigningSecret: ptr("short")}, {BotToken: ptr("xoxp-user-token-123456")}} {
		if _, err := h.svc.Save(context.Background(), org, "owner", in); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("input %+v accepted: %v", in, err)
		}
	}
	// A sealed secret of another organization cannot be opened as this one's (the vault binds org and purpose).
	other := r
	other.OrgID = "org-b"
	if sig, bot := h.svc.secrets(context.Background(), other); sig != "" || bot != "" {
		t.Fatal("secrets opened under another organization")
	}
	// Disconnect removes everything.
	if err := h.svc.Disconnect(context.Background(), org, "owner-1"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := h.store.GetRecord(context.Background(), org); found {
		t.Fatal("still configured")
	}
}

func ptr(s string) *string { return &s }

// ---- outbound ----

func TestApprovalMessagesCarryNoDetailsAndAreUpdated(t *testing.T) {
	h := newHarness(t)
	ap := h.approval("ap1", nil)
	h.svc.Post(context.Background(), org, ap)
	if len(h.slack.posts) != 1 {
		t.Fatalf("posts = %d", len(h.slack.posts))
	}
	msg := replyText(h.slack.posts[0])
	if strings.Contains(msg, "SECRET-DETAIL") || !strings.Contains(msg, `"value":"ap1"`) || !strings.Contains(msg, ActionApprove) ||
		!strings.Contains(msg, "https://app.example/approvals/ap1") || h.slack.posts[0]["channel"] != channel {
		t.Fatalf("posted message = %s", msg)
	}
	// Below min_risk (medium by default): not posted.
	h.svc.Post(context.Background(), org, h.approval("ap0", func(a *domain.Approval) { a.Risk = "low" }))
	if len(h.slack.posts) != 1 {
		t.Fatal("a low-risk approval was posted")
	}
	// High risk: posted without the approve button.
	h.svc.Post(context.Background(), org, h.approval("ap2", func(a *domain.Approval) { a.Risk = "high" }))
	if m := replyText(h.slack.posts[1]); strings.Contains(m, ActionApprove) || !strings.Contains(m, ActionReject) {
		t.Fatalf("high-risk message = %s", m)
	}
	// Title cannot inject mentions or links.
	h.svc.Post(context.Background(), org, h.approval("ap5", func(a *domain.Approval) { a.Title = "Pay <!channel> <https://evil.example|here>" }))
	if m := replyText(h.slack.posts[2]); strings.Contains(m, "<!channel>") || strings.Contains(m, "<https://evil") {
		t.Fatalf("unescaped title = %s", m)
	}
	// Decided in the app: the observer updates the posted message.
	obs := &Observer{Inner: nopPub{}}
	obs.Bind(h.svc)
	ap.Status = domain.ApprovalApproved
	_ = obs.Publish(context.Background(), domain.Event{Type: domain.EvApprovalResolved, OrgID: org, Payload: map[string]any{"approval": ap}})
	if len(h.slack.updates) != 1 || h.slack.updates[0]["ts"] != "1700000000.000100" || strings.Contains(replyText(h.slack.updates[0]), ActionApprove) {
		t.Fatalf("updates = %v", h.slack.updates)
	}
}

func TestSlashCommands(t *testing.T) {
	h := newHarness(t)
	h.dir.set("viewer-1", "viewer")
	h.dir.set("admin-1", "admin")
	h.approval("ap1", nil)
	if out := h.command("U9", "estado"); !strings.Contains(fmt.Sprint(out["text"]), "vincular") {
		t.Fatalf("unlinked status = %v", out)
	}
	h.link("UV", "viewer-1")
	h.link("UA", "admin-1")
	if out := h.command("UV", "estado"); !strings.Contains(replyText(out), "Aprobaciones pendientes: 1") || out["response_type"] != "ephemeral" {
		t.Fatalf("status = %v", out)
	}
	if out := replyText(h.command("UV", "pendientes")); strings.Contains(out, ActionApprove) || strings.Contains(out, ActionReject) || !strings.Contains(out, "Send the invoice") {
		t.Fatalf("viewer pending list offers buttons: %s", out)
	}
	if out := replyText(h.command("UA", "pending")); !strings.Contains(out, ActionApprove) || strings.Contains(out, "SECRET-DETAIL") {
		t.Fatalf("admin pending list = %s", out)
	}
	if out := h.command("UA", ""); !strings.Contains(fmt.Sprint(out["text"]), "/oficina estado") {
		t.Fatalf("help = %v", out)
	}
}

func TestResponseURLMustBeSlack(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://hooks.slack.com/actions/T1/1/x": true,
		"http://hooks.slack.com/actions/T1/1/x":  false,
		"https://hooks.slack.com:8443/x":         false,
		"https://evil.example/hooks.slack.com":   false,
		"https://hooks.slack.com.evil.example/x": false,
		"https://user@hooks.slack.com/x":         false,
		"https://169.254.169.254/latest":         false,
	} {
		if got := ValidResponseURL(raw); got != want {
			t.Errorf("%s = %v", raw, got)
		}
	}
}
