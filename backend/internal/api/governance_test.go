package api_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/audit"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/events"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/policy"
)

// proposalRuntime: one sales task that asks to send a proposal (an action the
// organization always approves) with content that must never reach the audit trail.
type proposalRuntime struct{ fakeRuntime }

func (proposalRuntime) RunTask(_ context.Context, in application.RunTaskRequest) (application.RunTaskResponse, error) {
	return application.RunTaskResponse{
		Output: domain.StructuredOutput{Summary: "ok", Confidence: 0.9},
		ToolRequests: []application.ToolRequest{{Tool: "email", Action: "send_proposal", Risk: "medium",
			Args: map[string]any{"to": "ana@acme.com", "subject": "CONFIDENTIAL-SUBJECT-77", "body": "CONFIDENTIAL-BODY-77", "amount": 1200}}},
	}, nil
}

type govEnv struct {
	*env
	mem *memory.Store
}

func newGovEnv(t *testing.T) *govEnv {
	t.Helper()
	cfg := application.DefaultConfig()
	cfg.IdleDelay = 0
	mem := memory.New()
	if err := mem.Seed(context.Background(), domain.SeedOrg(cfg.BudgetUSD), domain.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := events.NewHub(log)
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: mem, Pub: events.LocalBus{Hub: hub}, Log: log}
	appr := application.NewApprovals(cfg, mem, rec)
	q := &application.Queries{Store: mem, Cfg: cfg}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	orch := application.NewOrchestrator(ctx, cfg, mem, proposalRuntime{}, memory.NewLocker(), rec, appr, q, log)
	orch.SetPolicy(&application.PolicyService{Store: mem, Cfg: cfg})
	oc := &application.OrgConfig{Cfg: cfg, Store: mem, Core: mem, Orch: orch, Rec: rec}
	svc, err := auth.NewService(auth.Config{Store: auth.NewMemoryStore(), Secret: []byte("0123456789abcdef0123456789abcdef-test"),
		PasswordParams: auth.PasswordParams{Memory: 8, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}})
	if err != nil {
		t.Fatal(err)
	}
	d := api.Deps{Cfg: cfg, Queries: q, Orch: orch, Approvals: appr, Store: mem, Runtime: proposalRuntime{}, Hub: hub, Log: log, OrgConfig: oc,
		Audit: &application.AuditService{Store: mem, Rec: rec, Cfg: cfg}, AuthEnabled: true, Auth: svc, AuthRoutes: auth.NewHandler(svc, auth.HandlerConfig{}).Routes()}
	ts := httptest.NewServer(api.NewRouter(d))
	t.Cleanup(ts.Close)
	return &govEnv{env: &env{t: t, srv: ts, svc: svc, store: mem, hub: hub, appr: appr}, mem: mem}
}

func (e *govEnv) waitRequestDone(token, id string) {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, raw := e.do("GET", "/api/v1/requests/"+id, token, nil, nil)
		if strings.Contains(string(raw), `"status":"done"`) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatal("request did not finish")
}

func (e *govEnv) pending(token string) []domain.Approval {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, raw := e.do("GET", "/api/v1/approvals?status=pending", token, nil, nil)
		if aps := decodeInto[[]domain.Approval](e.t, raw); len(aps) > 0 {
			return aps
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatal("no pending approval")
	return nil
}

func TestAuditEndpointsRequireAuditRead(t *testing.T) {
	e := newGovEnv(t)
	owner := e.register("owner@example.com", "O")
	admin := e.member(owner, "admin@example.com", auth.RoleAdmin)
	member := e.member(owner, "member@example.com", auth.RoleMember)
	viewer := e.member(owner, "viewer@example.com", auth.RoleViewer)
	for _, p := range []string{"/api/v1/audit", "/api/v1/audit/export", "/api/v1/audit/verify"} {
		for tok, want := range map[string]int{"": 401, viewer: 403, member: 403, admin: 200, owner.AccessToken: 200} {
			if got := e.status("GET", p, tok, nil); got != want {
				t.Errorf("GET %s with %q = %d, want %d", p, tok[:min(len(tok), 6)], got, want)
			}
		}
	}
	// There is no way to change the trail over HTTP.
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		if c := e.status(m, "/api/v1/audit", owner.AccessToken, map[string]string{"a": "b"}); c != 405 && c != 404 {
			t.Errorf("%s /audit = %d: the audit trail is read-only", m, c)
		}
	}
	// Rules: everyone who sees approvals can read them; only the owner changes them.
	if c := e.status("GET", "/api/v1/policy/rules", member, nil); c != 200 {
		t.Errorf("member reads rules = %d", c)
	}
	if c := e.status("GET", "/api/v1/policy/rules", "", nil); c != 401 {
		t.Errorf("anonymous reads rules = %d", c)
	}
	body := map[string]any{"approval_amount_usd": 100}
	for tok, want := range map[string]int{viewer: 403, member: 403, admin: 403, owner.AccessToken: 200} {
		if got := e.status("PUT", "/api/v1/policy/rules", tok, body); got != want {
			t.Errorf("PUT rules = %d, want %d", got, want)
		}
	}
}

func TestDoubleApprovalOverTheAPIAndAuditTrail(t *testing.T) {
	e := newGovEnv(t)
	owner := e.register("owner@example.com", "O")
	admin1 := e.member(owner, "a1@example.com", auth.RoleAdmin)
	admin2 := e.member(owner, "a2@example.com", auth.RoleAdmin)
	member := e.member(owner, "m@example.com", auth.RoleMember)

	resp, raw := e.do("PUT", "/api/v1/policy/rules", owner.AccessToken, map[string]any{
		"governance": map[string]any{"dual_approval": map[string]any{"actions": []string{"send_proposal"}}, "amount_tiers": []map[string]any{{"amount_above": 1000, "role": "admin"}}},
	}, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(raw), `"enforced":true`) {
		t.Fatalf("PUT rules = %d %s", resp.StatusCode, raw)
	}
	if c, _ := e.do("PUT", "/api/v1/policy/rules", owner.AccessToken, map[string]any{"governance": map[string]any{"amount_tiers": []map[string]any{{"amount_above": 1, "role": "member"}}}}, nil); c.StatusCode != 400 {
		t.Fatalf("an invalid rule must be rejected, got %d", c.StatusCode)
	}

	// The owner asks the team for a proposal: they are the requester.
	resp, raw = e.do("POST", "/api/v1/requests", owner.AccessToken, map[string]string{"text": "envía la propuesta"}, nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("create request = %d %s", resp.StatusCode, raw)
	}
	reqID := decodeInto[map[string]string](t, raw)["request_id"]
	ap := e.pending(admin1)[0]
	if ap.RequiredApprovals != 2 || ap.RequiredRole != "admin" || ap.RequestedBy != owner.User.ID || len(ap.Decisions) != 0 {
		t.Fatalf("approval = %+v", ap)
	}
	decide := func(tok, decision string) (int, domain.Approval) {
		t.Helper()
		r, raw := e.do("POST", "/api/v1/approvals/"+ap.ID+"/decision", tok, map[string]string{"decision": decision, "note": "ok"}, nil)
		var a domain.Approval
		if r.StatusCode == 200 {
			a = decodeInto[domain.Approval](t, raw)
		}
		return r.StatusCode, a
	}
	if c, _ := decide(member, "approve"); c != 403 {
		t.Fatalf("a member cannot decide approvals = %d", c)
	}
	if c, _ := decide(owner.AccessToken, "approve"); c != 403 {
		t.Fatalf("the requester cannot approve their own request = %d", c)
	}
	c, first := decide(admin1, "approve")
	if c != 200 || first.Status != domain.ApprovalPending || len(first.Decisions) != 1 {
		t.Fatalf("first approval = %d %+v", c, first)
	}
	if c, _ := decide(admin1, "approve"); c != 409 {
		t.Fatalf("the same admin twice = %d", c)
	}
	c, done := decide(admin2, "approve")
	if c != 200 || done.Status != domain.ApprovalApproved || len(done.Decisions) != 2 {
		t.Fatalf("second approval = %d %+v", c, done)
	}
	e.waitRequestDone(admin1, reqID)

	// The trail tells the whole story, filterable by request and type.
	_, raw = e.do("GET", "/api/v1/audit?request_id="+reqID+"&type=approval.*&order=asc", admin1, nil, nil)
	page := decodeInto[struct {
		Items []audit.Record `json:"items"`
	}](t, raw)
	var kinds []string
	for _, it := range page.Items {
		kinds = append(kinds, it.Action)
		if it.RequestID != reqID {
			t.Errorf("filter leaked entry of request %q", it.RequestID)
		}
	}
	if got := strings.Join(kinds, ","); got != "approval.requested,approval.decision_refused,approval.partial,approval.approved" {
		t.Fatalf("approval entries = %s", got)
	}
	_, raw = e.do("GET", "/api/v1/audit?actor=sales&type=policy.decision", admin1, nil, nil)
	if !strings.Contains(string(raw), `"rule_id":"dual_approval.action:send_proposal"`) || !strings.Contains(string(raw), `"args_hash"`) {
		t.Fatalf("the policy decision is audited: %s", raw)
	}
	for _, p := range []string{"/api/v1/audit?limit=1000", "/api/v1/audit/export?format=jsonl", "/api/v1/audit/export?format=csv"} {
		_, raw := e.do("GET", p, admin1, nil, nil)
		if strings.Contains(string(raw), "CONFIDENTIAL") {
			t.Fatalf("content reached the audit trail (%s)", p)
		}
	}
}

func TestAuditQueryPaginationAndValidation(t *testing.T) {
	e := newGovEnv(t)
	owner := e.register("owner@example.com", "O")
	for i := 0; i < 5; i++ {
		e.status("PUT", "/api/v1/policy/rules", owner.AccessToken, map[string]any{"approval_amount_usd": 100 + i})
	}
	seen, cursor := map[string]bool{}, ""
	for pages := 0; pages < 20; pages++ {
		path := "/api/v1/audit?type=policy.rules_updated&limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		_, raw := e.do("GET", path, owner.AccessToken, nil, nil)
		pg := decodeInto[struct {
			Items      []audit.Record `json:"items"`
			NextCursor string         `json:"next_cursor"`
		}](t, raw)
		for _, it := range pg.Items {
			if seen[it.ID] {
				t.Fatalf("entry %s twice", it.ID)
			}
			seen[it.ID] = true
		}
		if pg.NextCursor == "" {
			break
		}
		cursor = pg.NextCursor
	}
	if len(seen) != 5 {
		t.Fatalf("paged %d entries, want 5", len(seen))
	}
	for _, bad := range []string{"from=yesterday", "to=2026-13-45", "limit=0", "limit=abc", "order=sideways", "cursor=%21%21", "from=2026-02-01&to=2026-01-01"} {
		if c := e.status("GET", "/api/v1/audit?"+bad, owner.AccessToken, nil); c != 400 {
			t.Errorf("?%s = %d, want 400", bad, c)
		}
	}
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	_, raw := e.do("GET", "/api/v1/audit?from="+future, owner.AccessToken, nil, nil)
	if !strings.Contains(string(raw), `"items":[]`) {
		t.Fatalf("empty range = %s", raw)
	}
	// A date alone is accepted (UTC midnight).
	if c := e.status("GET", "/api/v1/audit?from="+time.Now().UTC().Format("2006-01-02"), owner.AccessToken, nil); c != 200 {
		t.Fatalf("date filter = %d", c)
	}
}

func TestAuditExportFormats(t *testing.T) {
	e := newGovEnv(t)
	owner := e.register("owner@example.com", "O")
	for i := 0; i < 4; i++ {
		e.status("PUT", "/api/v1/policy/rules", owner.AccessToken, map[string]any{"approval_amount_usd": 10 + i})
	}
	resp, raw := e.do("GET", "/api/v1/audit/export?format=jsonl", owner.AccessToken, nil, nil)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/x-ndjson") ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") || !strings.Contains(resp.Header.Get("Content-Disposition"), ".jsonl") {
		t.Fatalf("jsonl headers: %d %v", resp.StatusCode, resp.Header)
	}
	recs, err := audit.ReadJSONL(raw)
	if err != nil || len(recs) < 4 {
		t.Fatalf("records = %d, %v, raw=%s", len(recs), err, raw)
	}
	if rep := audit.VerifyRecords(recs, true); !rep.OK {
		t.Fatalf("the export verifies offline: %+v", rep)
	}
	tampered := bytes.Replace(raw, []byte(`"policy.rules_updated"`), []byte(`"nothing.to.see"`), 1)
	trecs, _ := audit.ReadJSONL(tampered)
	if rep := audit.VerifyRecords(trecs, true); rep.OK {
		t.Fatal("an edited export must not verify")
	}

	resp, raw = e.do("GET", "/api/v1/audit/export?format=csv&type=policy.rules_updated", owner.AccessToken, nil, nil)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("csv headers: %d %v", resp.StatusCode, resp.Header)
	}
	rows, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
	if err != nil || len(rows) != 5 || rows[0][0] != "seq" || rows[0][11] != "hash" {
		t.Fatalf("csv: %d rows, %v", len(rows), err)
	}
	if c := e.status("GET", "/api/v1/audit/export?format=xml", owner.AccessToken, nil); c != 400 {
		t.Fatalf("unknown format = %d", c)
	}
	// Exporting is audited.
	_, raw = e.do("GET", "/api/v1/audit?type=audit.exported", owner.AccessToken, nil, nil)
	if strings.Count(string(raw), `"action":"audit.exported"`) != 2 {
		t.Fatalf("each export leaves a trace: %s", raw)
	}
}

func TestAuditVerifyEndpoint(t *testing.T) {
	e := newGovEnv(t)
	owner := e.register("owner@example.com", "O")
	for i := 0; i < 5; i++ {
		e.status("PUT", "/api/v1/policy/rules", owner.AccessToken, map[string]any{"approval_amount_usd": 10 + i})
	}
	_, raw := e.do("GET", "/api/v1/audit/verify", owner.AccessToken, nil, nil)
	rep := decodeInto[audit.Report](t, raw)
	if !rep.OK || rep.Checked < 5 {
		t.Fatalf("honest trail: %s", raw)
	}
	// Anchor: an externally stored (seq, hash) pair.
	url := fmt.Sprintf("/api/v1/audit/verify?anchor_seq=%d&anchor_hash=%s", rep.LastSeq, rep.HeadHash)
	if rep2 := decodeInto[audit.Report](t, func() []byte { _, b := e.do("GET", url, owner.AccessToken, nil, nil); return b }()); !rep2.OK || !rep2.AnchorChecked {
		t.Fatalf("anchor: %+v", rep2)
	}
	if c := e.status("GET", "/api/v1/audit/verify?anchor_seq=3", owner.AccessToken, nil); c != 400 {
		t.Fatalf("a half anchor = %d", c)
	}

	// Someone with database access edits an entry.
	org := owner.OrgID
	e.mem.UnsafeMutateAudit(func(all *[]domain.AuditLog) {
		for i := range *all {
			if (*all)[i].OrgID == org && (*all)[i].Seq == 2 {
				(*all)[i].Actor = "mallory"
			}
		}
	})
	_, raw = e.do("GET", "/api/v1/audit/verify", owner.AccessToken, nil, nil)
	rep = decodeInto[audit.Report](t, raw)
	if rep.OK || rep.Reason != "hash_mismatch" || rep.BrokenAtSeq != 2 {
		t.Fatalf("tampering must be reported: %s", raw)
	}
}

func TestAuditIsIsolatedBetweenOrganizations(t *testing.T) {
	e := newGovEnv(t)
	a := e.register("a@example.com", "A")
	b := e.register("b@example.com", "B")
	e.status("PUT", "/api/v1/policy/rules", a.AccessToken, map[string]any{"approval_amount_usd": 111})
	_, raw := e.do("GET", "/api/v1/audit?type=policy.rules_updated", b.AccessToken, nil, nil)
	if strings.Contains(string(raw), `"action":"policy.rules_updated"`) {
		t.Fatalf("organization B sees organization A's audit trail: %s", raw)
	}
	_, raw = e.do("GET", "/api/v1/audit?type=policy.rules_updated", a.AccessToken, nil, nil)
	if !strings.Contains(string(raw), `"action":"policy.rules_updated"`) {
		t.Fatalf("organization A lost its own entry: %s", raw)
	}
	_, raw = e.do("GET", "/api/v1/audit/export", b.AccessToken, nil, nil)
	if strings.Contains(string(raw), "policy.rules_updated") {
		t.Fatal("export leaks another organization")
	}
}

func TestRoleParityBetweenPolicyAndAuth(t *testing.T) {
	for _, r := range []auth.Role{auth.RoleOwner, auth.RoleAdmin, auth.RoleMember, auth.RoleViewer} {
		if got := policy.RoleRank(string(r)); got != r.Rank() {
			t.Errorf("rank(%s): policy=%d auth=%d", r, got, r.Rank())
		}
	}
	// Agents never hold approval or policy permissions: no role is "agent", and
	// roles that can decide are exactly admin and owner.
	for _, r := range []auth.Role{auth.RoleMember, auth.RoleViewer} {
		if r.Has(auth.PermApprovalsDecide) || r.Has(auth.PermPolicyManage) {
			t.Errorf("%s must not decide approvals or manage rules", r)
		}
	}
	if auth.RoleAdmin.Has(auth.PermPolicyManage) || !auth.RoleOwner.Has(auth.PermPolicyManage) {
		t.Error("policy:manage is owner-only")
	}
}
