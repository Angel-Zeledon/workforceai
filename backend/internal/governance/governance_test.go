package governance

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/audit"
	"aiworkforce/backend/internal/domain"
)

// ---- tokens ----

type memTokens struct {
	mu     sync.Mutex
	rows   map[string]APIToken // by hash
	org    map[string]string   // hash -> org
	hash   map[string]string   // id -> hash
	touchN int
}

func newMemTokens() *memTokens {
	return &memTokens{rows: map[string]APIToken{}, org: map[string]string{}, hash: map[string]string{}}
}

func (m *memTokens) Insert(_ context.Context, org string, t APIToken, h string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[h], m.org[h], m.hash[t.ID] = t, org, h
	return nil
}
func (m *memTokens) List(_ context.Context, org string) ([]APIToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []APIToken
	for h, t := range m.rows {
		if m.org[h] == org {
			out = append(out, t)
		}
	}
	return out, nil
}
func (m *memTokens) Revoke(_ context.Context, org, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.hash[id]
	if !ok || m.org[h] != org {
		return ErrNotFound
	}
	t := m.rows[h]
	if t.RevokedAt == nil {
		t.RevokedAt = &at
	}
	m.rows[h] = t
	return nil
}
func (m *memTokens) Lookup(_ context.Context, h string) (string, APIToken, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.rows[h]
	if !ok {
		return "", t, "", ErrNotFound
	}
	return m.org[h], t, h, nil
}
func (m *memTokens) Touch(_ context.Context, org, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.hash[id]
	t := m.rows[h]
	t.LastUsedAt = &at
	m.rows[h] = t
	m.touchN++
	return nil
}

func TestTokenHashingAndFormat(t *testing.T) {
	plain, prefix, hash, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plain, TokenPrefix) || len(plain) < 40 {
		t.Fatalf("token %q", plain)
	}
	if hash != HashToken(plain) || len(hash) != 64 || strings.Contains(hash, plain) {
		t.Fatal("hash must be sha256 hex of the token")
	}
	if !strings.HasPrefix(plain, prefix) || len(prefix) >= len(plain)/2 {
		t.Fatalf("prefix %q must be a short display prefix", prefix)
	}
	p2, _, h2, _ := GenerateToken()
	if p2 == plain || h2 == hash {
		t.Fatal("tokens must be unique")
	}
	// Known-answer test: sha256("abc").
	if HashToken("abc") != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatal("HashToken is not plain sha256 hex")
	}
}

func TestScopes(t *testing.T) {
	if _, err := ValidateScopes(nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty scopes: %v", err)
	}
	if _, err := ValidateScopes([]string{"admin:all"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown scope: %v", err)
	}
	if _, err := ValidateScopes([]string{"members:manage"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("a token must never get administrative scopes")
	}
	got, err := ValidateScopes([]string{"usage:read", "requests:read", "usage:read"})
	if err != nil || len(got) != 2 || got[0] != "requests:read" {
		t.Fatalf("dedupe/sort: %v %v", got, err)
	}
	if !HasScope([]string{ScopeRequestsWrite}, ScopeRequestsRead) {
		t.Fatal("write implies read")
	}
	if HasScope([]string{ScopeRequestsRead}, ScopeRequestsWrite) || HasScope([]string{ScopeUsageRead}, ScopeReportsRead) {
		t.Fatal("scope leak")
	}
}

func TestTokenLifecycle(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	st := newMemTokens()
	svc := &TokenService{Store: st, Now: func() time.Time { return now }}
	ctx := context.Background()

	tok, plain, err := svc.Create(ctx, "orgA", "u1", "ci", []string{ScopeRequestsRead}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.rows[plain]; ok {
		t.Fatal("plaintext used as key")
	}
	for h := range st.rows {
		if h == plain {
			t.Fatal("token stored in clear")
		}
	}
	org, got, err := svc.Authenticate(ctx, plain)
	if err != nil || org != "orgA" || got.ID != tok.ID {
		t.Fatalf("authenticate: %v %v %v", org, got, err)
	}
	// Garbage and near misses are all the same error.
	for _, bad := range []string{"", "aiw_", "Bearer x", plain + "x", plain[:len(plain)-1], strings.ToUpper(plain), "aiw_" + strings.Repeat("A", 43)} {
		if _, _, err := svc.Authenticate(ctx, bad); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	// last_used is throttled to once a minute.
	svc.Authenticate(ctx, plain)
	svc.Authenticate(ctx, plain)
	if st.touchN != 1 {
		t.Fatalf("touch called %d times", st.touchN)
	}
	now = now.Add(2 * time.Minute)
	svc.Authenticate(ctx, plain)
	if st.touchN != 2 {
		t.Fatalf("touch after a minute: %d", st.touchN)
	}
	// Another organization cannot revoke it.
	if err := st.Revoke(ctx, "orgB", tok.ID, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org revoke: %v", err)
	}
	// Expiry.
	now = now.Add(31 * 24 * time.Hour)
	if _, _, err := svc.Authenticate(ctx, plain); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired token accepted: %v", err)
	}
	// Revocation takes effect immediately.
	now = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	if _, _, err := svc.Authenticate(ctx, plain); err != nil {
		t.Fatal(err)
	}
	if err := st.Revoke(ctx, "orgA", tok.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Authenticate(ctx, plain); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked token accepted: %v", err)
	}
}

func TestTokenCreateValidationAndLimit(t *testing.T) {
	svc := &TokenService{Store: newMemTokens()}
	ctx := context.Background()
	if _, _, err := svc.Create(ctx, "o", "u", " ", []string{ScopeUsageRead}, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty name: %v", err)
	}
	if _, _, err := svc.Create(ctx, "o", "u", "x", []string{ScopeUsageRead}, 9999); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expiry: %v", err)
	}
	for i := 0; i < MaxActiveTokens; i++ {
		if _, _, err := svc.Create(ctx, "o", "u", "t", []string{ScopeUsageRead}, 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := svc.Create(ctx, "o", "u", "t", []string{ScopeUsageRead}, 0); !errors.Is(err, ErrTokenLimit) {
		t.Fatalf("limit: %v", err)
	}
	// Another organization is unaffected.
	if _, _, err := svc.Create(ctx, "other", "u", "t", []string{ScopeUsageRead}, 0); err != nil {
		t.Fatal(err)
	}
}

// ---- public API ----

type fakeBackend struct{ submitted []string }

func (f *fakeBackend) Agents(context.Context) (any, error)          { return []string{"a"}, nil }
func (f *fakeBackend) Requests(context.Context) (any, error)        { return []string{}, nil }
func (f *fakeBackend) Request(context.Context, string) (any, error) { return map[string]string{}, nil }
func (f *fakeBackend) Reports(context.Context) (any, error)         { return []string{}, nil }
func (f *fakeBackend) Report(context.Context, string) (any, error)  { return map[string]string{}, nil }
func (f *fakeBackend) Submit(_ context.Context, text string, _ float64) (string, error) {
	f.submitted = append(f.submitted, text)
	return "req-1", nil
}

func TestPublicAPIScopesAndRateLimit(t *testing.T) {
	st := newMemTokens()
	svc := &TokenService{Store: st}
	h := &Handler{Tokens: svc, Env: Env{
		Decode:  func(w http.ResponseWriter, r *http.Request, dst any) error { return errors.New("not used") },
		Fail:    func(w http.ResponseWriter, err error) { http.Error(w, err.Error(), 500) },
		WithOrg: func(ctx context.Context, org, actor string) context.Context { return ctx },
		Org:     func(*http.Request) string { return "o" }, Actor: func(*http.Request) string { return "a" },
	}}
	fb := &fakeBackend{}
	srv := httptest.NewServer(h.PublicRouter(fb))
	defer srv.Close()
	_, readTok, _ := svc.Create(context.Background(), "o", "u", "r", []string{ScopeRequestsRead}, 0)
	_, usageTok, _ := svc.Create(context.Background(), "o", "u", "u", []string{ScopeUsageRead}, 0)

	do := func(method, path, tok string) int {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(`{"text":"x"}`))
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := do("GET", "/requests", ""); c != 401 {
		t.Errorf("no token: %d", c)
	}
	if c := do("GET", "/requests", "aiw_notatoken_notatoken_notatoken"); c != 401 {
		t.Errorf("bad token: %d", c)
	}
	if c := do("GET", "/requests", readTok); c != 200 {
		t.Errorf("read scope: %d", c)
	}
	if c := do("POST", "/requests", readTok); c != 403 {
		t.Errorf("a read token must not submit work: %d", c)
	}
	if len(fb.submitted) != 0 {
		t.Fatal("work was submitted with a read-only token")
	}
	if c := do("GET", "/agents", readTok); c != 403 {
		t.Errorf("agents scope: %d", c)
	}
	if c := do("GET", "/reports", usageTok); c != 403 {
		t.Errorf("reports scope: %d", c)
	}
	// Rate limit: 120 per minute per token.
	limited := 0
	for i := 0; i < 130; i++ {
		if do("GET", "/requests", readTok) == 429 {
			limited++
		}
	}
	if limited == 0 {
		t.Error("no rate limiting")
	}
}

// ---- retention ----

type memRet struct {
	mu       sync.Mutex
	msgs     map[string][]time.Time // org -> message timestamps
	files    map[string][]time.Time // org -> live files updated_at
	convs    map[string][]time.Time // org -> last_message_at of conversations with no messages
	pol      map[string]RetentionPolicy
	next     map[string]time.Time
	touched  map[string]int
	trashed  int
	saved    int
	failures bool
}

func newMemRet() *memRet {
	return &memRet{msgs: map[string][]time.Time{}, files: map[string][]time.Time{}, convs: map[string][]time.Time{}, pol: map[string]RetentionPolicy{}, next: map[string]time.Time{}, touched: map[string]int{}}
}

func (m *memRet) GetPolicy(_ context.Context, org string) (RetentionPolicy, error) {
	return m.pol[org], nil
}
func (m *memRet) PutPolicy(_ context.Context, org string, p RetentionPolicy, _ string) error {
	m.pol[org] = p
	return nil
}
func (m *memRet) ClaimDue(_ context.Context, org string, now time.Time, every time.Duration) (RetentionPolicy, bool, error) {
	p, ok := m.pol[org]
	if !ok || !p.Enabled || m.next[org].After(now) {
		return p, false, nil
	}
	m.next[org] = now.Add(every)
	return p, true, nil
}
func countBefore(ts []time.Time, c time.Time) (n int) {
	for _, t := range ts {
		if t.Before(c) {
			n++
		}
	}
	return
}
func (m *memRet) CountChats(_ context.Context, org string, c time.Time) (int, int, error) {
	return countBefore(m.msgs[org], c), 0, nil
}
func (m *memRet) CountFiles(_ context.Context, org string, c time.Time) (int, error) {
	return countBefore(m.files[org], c), nil
}
func dropBefore(ts []time.Time, c time.Time, limit int) ([]time.Time, int) {
	var keep []time.Time
	n := 0
	for _, t := range ts {
		if t.Before(c) && n < limit {
			n++
			continue
		}
		keep = append(keep, t)
	}
	return keep, n
}
func (m *memRet) PurgeChats(_ context.Context, org string, c time.Time, limit int) (int, int, error) {
	var n int
	m.msgs[org], n = dropBefore(m.msgs[org], c, limit)
	return n, 0, nil
}
func (m *memRet) TrashFiles(_ context.Context, org string, c time.Time, limit int) (int, error) {
	var n int
	m.files[org], n = dropBefore(m.files[org], c, limit)
	m.trashed += n
	return n, nil
}
func (m *memRet) SaveResult(context.Context, string, time.Time, PurgeResult) error {
	m.saved++
	return nil
}

type orgs []string

func (o orgs) ListOrgIDs(context.Context) ([]string, error) { return o, nil }

var t0 = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

func days(n int) time.Time { return t0.AddDate(0, 0, -n) }

func TestPolicyValidationFloors(t *testing.T) {
	ok := []RetentionPolicy{{}, {Enabled: true, ChatsDays: 30}, {ChatsDays: 365, FilesDays: 90, AuditDays: 365}, {AuditDays: 3650}}
	for _, p := range ok {
		if err := p.Validate(); err != nil {
			t.Errorf("%+v: %v", p, err)
		}
	}
	bad := []RetentionPolicy{{ChatsDays: 1}, {ChatsDays: 29}, {FilesDays: 7}, {AuditDays: 364}, {ChatsDays: -5}, {ChatsDays: 4000}}
	for _, p := range bad {
		if err := p.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v accepted (%v)", p, err)
		}
	}
}

func TestPurgeKeepsRecentDataAndOtherOrgs(t *testing.T) {
	m := newMemRet()
	m.msgs["A"] = []time.Time{days(100), days(40), days(29), days(1)}
	m.files["A"] = []time.Time{days(400), days(10)}
	m.msgs["B"] = []time.Time{days(500)} // another organization: untouched
	var audited []map[string]any
	p := &Purger{Store: m, Now: func() time.Time { return t0 }, Audit: func(_ context.Context, org, action string, d map[string]any) {
		if org == "A" && action == "retention.purged" {
			audited = append(audited, d)
		}
	}}
	res, err := p.RunNow(context.Background(), "A", RetentionPolicy{Enabled: true, ChatsDays: 30, FilesDays: 365, AuditDays: 365})
	if err != nil {
		t.Fatal(err)
	}
	if res.Messages != 2 || res.FilesTrashed != 1 || len(m.msgs["A"]) != 2 || len(m.files["A"]) != 1 {
		t.Fatalf("result %+v, left msgs=%d files=%d", res, len(m.msgs["A"]), len(m.files["A"]))
	}
	if !res.AuditSkipped {
		t.Fatal("the audit trail must be reported as skipped, never purged")
	}
	if len(m.msgs["B"]) != 1 {
		t.Fatal("another organization lost data")
	}
	if len(audited) != 1 || audited[0]["messages"] != 2 {
		t.Fatalf("audit of the purge: %+v", audited)
	}
	for k := range audited[0] {
		if k == "text" || k == "content" {
			t.Fatal("audit details must hold counts only")
		}
	}
}

func TestPurgeDoesNothingWhenDisabledOrInvalid(t *testing.T) {
	m := newMemRet()
	m.msgs["A"] = []time.Time{days(900)}
	p := &Purger{Store: m, Now: func() time.Time { return t0 }}
	if res, err := p.RunNow(context.Background(), "A", RetentionPolicy{Enabled: false, ChatsDays: 30}); err != nil || res.Messages != 0 || len(m.msgs["A"]) != 1 {
		t.Fatalf("disabled policy deleted data: %+v %v", res, err)
	}
	// A policy edited straight in the database below the floor is refused at run time.
	if _, err := p.RunNow(context.Background(), "A", RetentionPolicy{Enabled: true, ChatsDays: 1}); !errors.Is(err, ErrInvalid) || len(m.msgs["A"]) != 1 {
		t.Fatalf("floor not enforced at run time: %v", err)
	}
	if _, err := p.RunNow(context.Background(), "A", RetentionPolicy{Enabled: true}); err != nil || len(m.msgs["A"]) != 1 {
		t.Fatal("an enabled policy with no durations must keep everything")
	}
}

func TestPreviewNeverDeletes(t *testing.T) {
	m := newMemRet()
	m.msgs["A"] = []time.Time{days(100), days(100), days(1)}
	m.files["A"] = []time.Time{days(400)}
	p := &Purger{Store: m, Now: func() time.Time { return t0 }}
	res, err := p.Preview(context.Background(), "A", RetentionPolicy{Enabled: false, ChatsDays: 30, FilesDays: 90})
	if err != nil {
		t.Fatal(err)
	}
	if !res.DryRun || res.Messages != 2 || res.FilesTrashed != 1 {
		t.Fatalf("preview %+v", res)
	}
	if len(m.msgs["A"]) != 3 || len(m.files["A"]) != 1 || m.trashed != 0 {
		t.Fatal("preview deleted data")
	}
}

func TestPurgeIsBatchedAndProtectedOrgsRefused(t *testing.T) {
	m := newMemRet()
	for i := 0; i < 12; i++ {
		m.msgs["A"] = append(m.msgs["A"], days(100))
	}
	m.msgs[domain.DemoOrgID] = []time.Time{days(100)}
	p := &Purger{Store: m, Now: func() time.Time { return t0 }, Batch: 5, Protected: map[string]bool{domain.DemoOrgID: true}}
	pol := RetentionPolicy{Enabled: true, ChatsDays: 30}
	res, _ := p.RunNow(context.Background(), "A", pol)
	if res.Messages != 5 || !res.Capped || len(m.msgs["A"]) != 7 {
		t.Fatalf("batch: %+v left %d", res, len(m.msgs["A"]))
	}
	if _, err := p.RunNow(context.Background(), domain.DemoOrgID, pol); !errors.Is(err, ErrInvalid) || len(m.msgs[domain.DemoOrgID]) != 1 {
		t.Fatalf("demo organization purged: %v", err)
	}
	if _, err := p.RunNow(context.Background(), "", pol); err == nil {
		t.Fatal("empty org accepted")
	}
}

func TestTickClaimsOncePerInterval(t *testing.T) {
	m := newMemRet()
	m.msgs["A"] = []time.Time{days(100)}
	m.msgs["B"] = []time.Time{days(100)}
	m.msgs[domain.DemoOrgID] = []time.Time{days(100)}
	m.pol["A"] = RetentionPolicy{Enabled: true, ChatsDays: 30}
	m.pol[domain.DemoOrgID] = RetentionPolicy{Enabled: true, ChatsDays: 30}
	// B has no policy at all.
	now := t0
	p := &Purger{Store: m, Orgs: orgs{"A", "B", domain.DemoOrgID}, Now: func() time.Time { return now }, Protected: map[string]bool{domain.DemoOrgID: true}}
	p.Tick(context.Background())
	if len(m.msgs["A"]) != 0 || len(m.msgs["B"]) != 1 || len(m.msgs[domain.DemoOrgID]) != 1 {
		t.Fatalf("tick: A=%d B=%d demo=%d", len(m.msgs["A"]), len(m.msgs["B"]), len(m.msgs[domain.DemoOrgID]))
	}
	m.msgs["A"] = []time.Time{days(100)}
	p.Tick(context.Background()) // same instant: not due again
	if len(m.msgs["A"]) != 1 {
		t.Fatal("organization purged twice in the same interval")
	}
	now = now.Add(25 * time.Hour)
	p.Tick(context.Background())
	if len(m.msgs["A"]) != 0 {
		t.Fatal("not purged when due again")
	}
}

// ---- reports ----

func TestReportCSVAndTotals(t *testing.T) {
	rep := UsageReport{By: ByAgent, Rows: []UsageRow{
		{Key: "a1", Label: "=HYPERLINK(\"http://x\")", Department: "+Sales", Requests: 2, Calls: 3, InputTokens: 10, OutputTokens: 5, CostUSD: 0.25},
		{Key: "a2", Label: "Bea, \"B\"", CostUSD: 0.75},
		{Key: "a3", Label: "none"},
	}}
	rep.Finish()
	if rep.Rows[0].Key != "a2" || rep.Total.CostUSD != 1.0 || rep.Rows[0].SharePct != 75 {
		t.Fatalf("finish: %+v", rep)
	}
	var buf bytes.Buffer
	if err := rep.WriteCSV(&buf); err != nil {
		t.Fatal(err)
	}
	recs, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 5 || recs[0][0] != "agent" || recs[4][0] != "TOTAL" {
		t.Fatalf("rows: %v", recs)
	}
	for _, r := range recs[1:] {
		for _, c := range r {
			if c != "" && strings.ContainsRune("=+-@", rune(c[0])) {
				t.Errorf("formula injection survives in %q", c)
			}
		}
	}
}

type fakeUsage struct{ rows []UsageRow }

func (f fakeUsage) Usage(_ context.Context, _, _ string, _, _ time.Time) ([]UsageRow, error) {
	return append([]UsageRow(nil), f.rows...), nil
}

func TestBuildUsageReportValidation(t *testing.T) {
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	st := fakeUsage{rows: []UsageRow{{Key: "d", CostUSD: 1}}}
	if _, err := BuildUsageReport(context.Background(), st, "o", "galaxy", nil, nil, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("dimension: %v", err)
	}
	from, to := now, now.Add(-time.Hour)
	if _, err := BuildUsageReport(context.Background(), st, "o", ByProject, &from, &to, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("inverted range: %v", err)
	}
	far := now.AddDate(-5, 0, 0)
	if _, err := BuildUsageReport(context.Background(), st, "o", ByDepartment, &far, nil, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("huge range: %v", err)
	}
	rep, err := BuildUsageReport(context.Background(), st, "o", ByDepartment, nil, nil, now)
	if err != nil || rep.From.Day() != 1 || rep.Total.CostUSD != 1 {
		t.Fatalf("default range is the current month: %+v %v", rep, err)
	}
}

func TestDepartmentOverview(t *testing.T) {
	agents := []AgentSpend{{ID: "1", Department: "Sales", MonthSpendUSD: 8}, {ID: "2", Department: "Sales", MonthSpendUSD: 4}, {ID: "3", MonthSpendUSD: 1}}
	got := Overview(agents, map[string]float64{"Sales": 10, "Legal": 5})
	by := map[string]DepartmentSpend{}
	for _, d := range got {
		by[d.Name] = d
	}
	if !by["Sales"].Over || by["Sales"].UsedPct != 120 || by["Sales"].Agents != 2 {
		t.Fatalf("sales %+v", by["Sales"])
	}
	if by["Legal"].Agents != 0 || by["Legal"].Over || by[""].Agents != 1 {
		t.Fatalf("others %+v", by)
	}
	if _, err := NormalizeDepartment(strings.Repeat("x", 61)); err == nil {
		t.Fatal("long department accepted")
	}
	if _, err := NormalizeDepartment("a\x00b"); err == nil {
		t.Fatal("control chars accepted")
	}
}

// ---- audit export with verification ----

type fakeAudit struct {
	entries []domain.AuditLog
	tamper  bool
}

func (f *fakeAudit) Export(_ context.Context, _ domain.AuditQuery, format string, w io.Writer) (application.ExportResult, error) {
	for _, e := range f.entries {
		if format == application.ExportCSV {
			continue
		}
		if err := audit.WriteJSONLRecord(w, e); err != nil {
			return application.ExportResult{}, err
		}
	}
	return application.ExportResult{Entries: len(f.entries), Format: format}, nil
}

func (f *fakeAudit) Verify(context.Context, *domain.AuditHead) (audit.Report, error) {
	var head domain.AuditHead
	for _, e := range f.entries {
		head = domain.AuditHead{Seq: e.Seq, Hash: e.Hash}
	}
	return audit.Report{OK: true, Checked: int64(len(f.entries)), HeadSeq: head.Seq, HeadHash: head.Hash}, nil
}

func chain(n int, tamper bool) []domain.AuditLog {
	var head domain.AuditHead
	var out []domain.AuditLog
	for i := 0; i < n; i++ {
		e := audit.Seal("o", head, domain.AuditLog{ID: string(rune('a' + i)), Actor: "u", Action: "x.y", Entity: "e", TS: time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC)})
		head = domain.AuditHead{Seq: e.Seq, Hash: e.Hash, TS: e.TS}
		out = append(out, e)
	}
	if tamper && n > 1 {
		out[1].Action = "tampered.action"
	}
	return out
}

func TestAuditExportJSONIncludesVerification(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	var buf bytes.Buffer
	var meta AuditExport
	err := ExportAudit(context.Background(), &fakeAudit{entries: chain(4, false)}, "o", domain.AuditQuery{Actor: "u"}, AuditJSON, now, &buf, func(x AuditExport) { meta = x })
	if err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	for _, want := range []string{`"verification"`, `"chain"`, `"ok": true`, `"records"`, `"actor": "u"`} {
		if !strings.Contains(s, want) {
			t.Errorf("document lacks %s", want)
		}
	}
	if meta.ContentType != "application/json" || meta.Headers["X-Audit-Chain-OK"] != "true" || meta.Headers["X-Audit-Export-OK"] != "true" {
		t.Fatalf("meta %+v", meta)
	}
}

func TestAuditExportDetectsTamperedRecords(t *testing.T) {
	var buf bytes.Buffer
	var meta AuditExport
	err := ExportAudit(context.Background(), &fakeAudit{entries: chain(4, true)}, "o", domain.AuditQuery{}, AuditJSON, time.Now(), &buf, func(x AuditExport) { meta = x })
	if err != nil {
		t.Fatal(err)
	}
	if meta.Headers["X-Audit-Export-OK"] != "false" || !strings.Contains(buf.String(), `"reason": "hash_mismatch"`) {
		t.Fatalf("a modified entry must fail the offline verification: %+v\n%s", meta.Headers, buf.String())
	}
}

func TestAuditExportZipAndBadFormat(t *testing.T) {
	var buf bytes.Buffer
	var meta AuditExport
	if err := ExportAudit(context.Background(), &fakeAudit{entries: chain(3, false)}, "o", domain.AuditQuery{}, AuditZip, time.Now(), &buf, func(x AuditExport) { meta = x }); err != nil {
		t.Fatal(err)
	}
	if meta.ContentType != "application/zip" || !bytes.HasPrefix(buf.Bytes(), []byte("PK")) {
		t.Fatalf("zip: %+v", meta)
	}
	if err := ExportAudit(context.Background(), &fakeAudit{}, "o", domain.AuditQuery{}, "xml", time.Now(), io.Discard, func(AuditExport) {}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad format: %v", err)
	}
}

// auditRet adds the optional audit purge to memRet.
type auditRet struct {
	*memRet
	audit      []time.Time // chained entries of org "A" (the last one is the head)
	purgedCall int
}

func (a *auditRet) due(cutoff time.Time) int {
	n := 0
	for i, ts := range a.audit {
		if i < len(a.audit)-1 && ts.Before(cutoff) { // the head never goes
			n++
		}
	}
	return n
}

func (a *auditRet) CountAudit(_ context.Context, org string, cutoff time.Time) (int, error) {
	if org != "A" {
		return 0, nil
	}
	return a.due(cutoff), nil
}

func (a *auditRet) PurgeAudit(_ context.Context, org string, cutoff time.Time, limit int) (int, error) {
	if org != "A" {
		return 0, nil
	}
	a.purgedCall++
	n := a.due(cutoff)
	if n > limit {
		n = limit
	}
	a.audit = a.audit[n:]
	return n, nil
}

func TestRetentionPurgesTheAuditTrailWhenTheStoreCanAndKeepsTheHead(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	a := &auditRet{memRet: newMemRet()}
	for _, d := range []int{900, 800, 500, 10, 1} { // days old
		a.audit = append(a.audit, t0.AddDate(0, 0, -d))
	}
	var audited map[string]any
	p := &Purger{Store: a, Now: func() time.Time { return t0 }, Batch: 1, Audit: func(_ context.Context, _, action string, d map[string]any) {
		if action == "retention.purged" {
			audited = d
		}
	}}
	pol := RetentionPolicy{Enabled: true, AuditDays: 365}
	pv, err := p.Preview(context.Background(), "A", pol)
	if err != nil || pv.AuditEntries != 3 || pv.AuditSkipped || a.purgedCall != 0 {
		t.Fatalf("preview %+v %v (calls %d)", pv, err, a.purgedCall)
	}
	res, err := p.RunNow(context.Background(), "A", pol)
	if err != nil || res.AuditEntries != 1 || !res.Capped || len(a.audit) != 4 {
		t.Fatalf("batched run %+v %v left %d", res, err, len(a.audit))
	}
	if audited["audit_entries"] != 1 {
		t.Fatalf("audit of the purge: %+v", audited)
	}
	p.Batch = 100
	if res, _ = p.RunNow(context.Background(), "A", pol); res.AuditEntries != 2 || len(a.audit) != 2 {
		t.Fatalf("second run %+v left %d", res, len(a.audit))
	}
	if res, _ = p.RunNow(context.Background(), "A", pol); res.AuditEntries != 0 || len(a.audit) != 2 {
		t.Fatalf("nothing else is due: %+v", res)
	}
}
