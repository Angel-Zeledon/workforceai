package notify

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/mailer"
	"aiworkforce/backend/internal/policy"
)

// flakyMail fails while fail > 0.
type flakyMail struct {
	mu   sync.Mutex
	fail int
	sent []mailer.Message
}

func (m *flakyMail) Send(_ context.Context, msg mailer.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail > 0 {
		m.fail--
		return errors.New("smtp down")
	}
	m.sent = append(m.sent, msg)
	return nil
}

// revSealer is a reversible fake of the vault (unreadable by a substring search) that binds org and purpose like the real one.
type revSealer struct{ on bool }

func (r revSealer) Enabled() bool { return r.on }
func (r revSealer) Seal(_ context.Context, org, purpose string, plain []byte) ([]byte, error) {
	out := []byte(org + "|" + purpose + "|")
	for _, b := range plain {
		out = append(out, b^0x5a)
	}
	return out, nil
}
func (r revSealer) Unseal(_ context.Context, org, purpose string, sealed []byte) ([]byte, error) {
	pre := []byte(org + "|" + purpose + "|")
	if !bytes.HasPrefix(sealed, pre) {
		return nil, errors.New("wrong purpose")
	}
	out := []byte{}
	for _, b := range sealed[len(pre):] {
		out = append(out, b^0x5a)
	}
	return out, nil
}

func outboxSvc(t *testing.T, mail mailer.Mailer) (*Service, *MemOutbox, *MemSettings, *time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	set := NewMemSettings()
	_ = set.PutSettings(context.Background(), Settings{OrgID: "o1", EmailEnabled: true, EmailTo: []string{"boss@example.com"}, MinRisk: "low", Locale: "en"})
	ob := NewMemOutbox()
	s := &Service{Settings: set, Plans: &Plans{Store: NewMemPlans()}, Mail: mail, Outbox: ob, PublicURL: "https://app.example.com",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Jitter: func() float64 { return 0.5 }}
	s.Now = func() time.Time { return now }
	return s, ob, set, &now
}

func TestNotifyQueuesAndSendsOnTheFirstAttempt(t *testing.T) {
	fm := &flakyMail{}
	s, ob, _, _ := outboxSvc(t, fm)
	s.Notify(context.Background(), "o1", pending("low"))
	if len(fm.sent) != 1 || fm.sent[0].To != "boss@example.com" || !strings.Contains(fm.sent[0].Subject+fm.sent[0].Text, "budget sheet") {
		t.Fatalf("sent %+v", fm.sent)
	}
	rows := ob.Rows("o1")
	if len(rows) != 1 || rows[0].Status != OutboxSent || rows[0].Attempts != 1 || rows[0].SentAt == nil {
		t.Fatalf("rows %+v", rows)
	}
}

func TestFailedEmailIsRetriedWithBackoffThenSent(t *testing.T) {
	fm := &flakyMail{fail: 2}
	s, ob, _, now := outboxSvc(t, fm)
	s.Notify(context.Background(), "o1", pending("low"))
	r := ob.Rows("o1")[0]
	if r.Status != OutboxPending || r.Attempts != 1 || !r.NextAttemptAt.After(*now) || r.LastError == "" {
		t.Fatalf("after the first failure: %+v", r)
	}
	if n, _ := s.ProcessOutbox(context.Background(), "o1"); n != 0 {
		t.Fatal("a message is not retried before its time")
	}
	*now = now.Add(time.Minute)
	if n, _ := s.ProcessOutbox(context.Background(), "o1"); n != 1 {
		t.Fatalf("second attempt not made (%d)", n)
	}
	if r = ob.Rows("o1")[0]; r.Status != OutboxPending || r.Attempts != 2 {
		t.Fatalf("after the second failure: %+v", r)
	}
	*now = now.Add(time.Hour)
	_, _ = s.ProcessOutbox(context.Background(), "o1")
	if r = ob.Rows("o1")[0]; r.Status != OutboxSent || r.Attempts != 3 || len(fm.sent) != 1 {
		t.Fatalf("third attempt: %+v sent %d", r, len(fm.sent))
	}
}

func TestMessageBecomesDeadAfterTheLastAttempt(t *testing.T) {
	fm := &flakyMail{fail: 1000}
	s, ob, _, now := outboxSvc(t, fm)
	s.MaxAttempts = 3
	s.Notify(context.Background(), "o1", pending("low"))
	for i := 0; i < 5; i++ {
		*now = now.Add(24 * time.Hour)
		_, _ = s.ProcessOutbox(context.Background(), "o1")
	}
	r := ob.Rows("o1")[0]
	if r.Status != OutboxDead || r.Attempts != 3 {
		t.Fatalf("%+v", r)
	}
	if ok, _ := ob.HasPending(context.Background(), "o1"); ok {
		t.Fatal("a dead letter is not pending")
	}
	if n, err := ob.Prune(context.Background(), "o1", now.Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("prune %d %v", n, err)
	}
}

func TestRemovingAChannelStopsWhatIsQueuedForIt(t *testing.T) {
	fm := &flakyMail{fail: 1}
	s, ob, set, now := outboxSvc(t, fm)
	s.Notify(context.Background(), "o1", pending("low"))
	st, _ := set.GetSettings(context.Background(), "o1")
	st.EmailEnabled, st.EmailTo = false, nil
	_ = set.PutSettings(context.Background(), st)
	*now = now.Add(time.Hour)
	_, _ = s.ProcessOutbox(context.Background(), "o1")
	if r := ob.Rows("o1")[0]; r.Status != OutboxDead || !strings.Contains(r.LastError, "not configured") || len(fm.sent) != 0 {
		t.Fatalf("%+v sent %d", r, len(fm.sent))
	}
}

func TestSlackIsQueuedWithoutTheURLAndRetriedOn5xxButNotOn4xx(t *testing.T) {
	const hook = "https://hooks.slack.com/services/T000/B000/SECRET"
	calls, status := 0, 503
	s, ob, set, now := outboxSvc(t, nil)
	s.HTTP = &http.Client{Transport: rt(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != hook {
			t.Errorf("posted to %s", r.URL)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	st, _ := set.GetSettings(context.Background(), "o1")
	st.EmailEnabled, st.SlackWebhook = false, hook
	_ = set.PutSettings(context.Background(), st)

	s.Notify(context.Background(), "o1", pending("low"))
	r := ob.Rows("o1")[0]
	if r.Channel != ChanSlack || r.Status != OutboxPending || strings.Contains(string(r.Payload), "SECRET") || strings.Contains(r.LastError, "SECRET") {
		t.Fatalf("%+v", r)
	}
	status = 200
	*now = now.Add(time.Hour)
	_, _ = s.ProcessOutbox(context.Background(), "o1")
	if r = ob.Rows("o1")[0]; r.Status != OutboxSent || calls != 2 {
		t.Fatalf("%+v calls %d", r, calls)
	}

	status, calls = 404, 0
	s.Notify(context.Background(), "o1", pending("low"))
	dead := 0
	for _, row := range ob.Rows("o1") {
		if row.Status == OutboxDead {
			dead++
		}
	}
	if dead != 1 || calls != 1 {
		t.Fatalf("a 404 is final: dead=%d calls=%d", dead, calls)
	}
}

func TestQueuedPayloadsAreSealedWhenTheVaultIsOn(t *testing.T) {
	fm := &flakyMail{fail: 1}
	s, ob, _, now := outboxSvc(t, fm)
	s.Sealer = revSealer{on: true}
	s.Notify(context.Background(), "o1", pending("low"))
	r := ob.Rows("o1")[0]
	if !r.Sealed || bytes.Contains(r.Payload, []byte("budget sheet")) {
		t.Fatalf("payload must be sealed: %+v", r)
	}
	*now = now.Add(time.Hour)
	_, _ = s.ProcessOutbox(context.Background(), "o1")
	if r = ob.Rows("o1")[0]; r.Status != OutboxSent || len(fm.sent) != 1 || !strings.Contains(fm.sent[0].Subject+fm.sent[0].Text, "budget sheet") {
		t.Fatalf("%+v", r)
	}
}

func TestWithoutOutboxMailIsSentInlineOnce(t *testing.T) {
	fm := &flakyMail{}
	s, _, _, _ := outboxSvc(t, fm)
	s.Outbox = nil
	s.Notify(context.Background(), "o1", pending("low"))
	if len(fm.sent) != 1 {
		t.Fatalf("sent %d", len(fm.sent))
	}
}

func TestClaimLeasesRowsSoTwoWorkersNeverTakeTheSameOne(t *testing.T) {
	ob := NewMemOutbox()
	now := time.Now()
	_ = ob.Enqueue(context.Background(), OutboxItem{OrgID: "o1", ID: "a", Channel: ChanEmail, Status: OutboxPending, NextAttemptAt: now, CreatedAt: now})
	_ = ob.Enqueue(context.Background(), OutboxItem{OrgID: "o2", ID: "b", Channel: ChanEmail, Status: OutboxPending, NextAttemptAt: now, CreatedAt: now})
	if got, _ := ob.Claim(context.Background(), "o1", now, 10, time.Minute); len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("first claim %+v", got)
	}
	if got, _ := ob.Claim(context.Background(), "o1", now, 10, time.Minute); len(got) != 0 {
		t.Fatal("a leased row is not claimed again")
	}
	if got, _ := ob.Claim(context.Background(), "o1", now.Add(2*time.Minute), 10, time.Minute); len(got) != 1 {
		t.Fatal("an expired lease is claimed again")
	}
}

func TestOutboxOutcome(t *testing.T) {
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	if o := OutboxOutcome(now, 1, nil, 0.5, 0); o.Status != OutboxSent || o.SentAt == nil {
		t.Fatalf("%+v", o)
	}
	if o := OutboxOutcome(now, 1, errors.New("x"), 0.5, 0); o.Status != OutboxPending || o.Next.Sub(now) != 30*time.Second {
		t.Fatalf("%+v", o)
	}
	if o := OutboxOutcome(now, MaxOutboxAttempts, errors.New("x"), 0.5, 0); o.Status != OutboxDead {
		t.Fatalf("%+v", o)
	}
	if o := OutboxOutcome(now, 1, &httpStatusError{Code: 429}, 0.5, 0); o.Status != OutboxPending {
		t.Fatalf("429 is transient: %+v", o)
	}
	if o := OutboxOutcome(now, 1, &httpStatusError{Code: 400}, 0.5, 0); o.Status != OutboxDead {
		t.Fatalf("400 is final: %+v", o)
	}
	if o := OutboxOutcome(now, 1, ValidateWebhook(KindSlack, "http://x"), 0.5, 0); o.Status != OutboxDead {
		t.Fatalf("a disallowed address is final: %+v", o)
	}
	if a, b := OutboxBackoff(1, 0), OutboxBackoff(99, 0); a >= b {
		t.Fatalf("backoff must grow: %v %v", a, b)
	}
}

// ---- sealed webhook URLs (migration 783) ----

func TestWebhookSealing(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	const hook = "https://hooks.slack.com/services/T000/B000/SECRET"
	s := revSealer{on: true}

	enc, plain, err := sealHook(ctx, s, "o1", KindSlack, hook)
	if err != nil || plain != "" || len(enc) == 0 || bytes.Contains(enc, []byte("SECRET")) {
		t.Fatalf("sealed: %v %q %q", err, enc, plain)
	}
	got, reseal := openHook(ctx, s, log, "o1", KindSlack, enc, "")
	if got != hook || reseal {
		t.Fatalf("open: %q %v", got, reseal)
	}
	// a Slack URL does not open as a Teams URL, nor for another organization
	if got, _ := openHook(ctx, s, log, "o1", KindTeams, enc, ""); got != "" {
		t.Fatal("purpose is bound")
	}
	if got, _ := openHook(ctx, s, log, "o2", KindSlack, enc, ""); got != "" {
		t.Fatal("organization is bound")
	}
	// rows written before 783 are read from the plaintext column and flagged for sealing
	if got, reseal := openHook(ctx, s, log, "o1", KindSlack, nil, hook); got != hook || !reseal {
		t.Fatalf("legacy row: %q %v", got, reseal)
	}
	// no vault key: plaintext stays plaintext, nothing to reseal
	off := revSealer{on: false}
	enc, plain, _ = sealHook(ctx, off, "o1", KindSlack, hook)
	if enc != nil || plain != hook {
		t.Fatalf("no key: %q %q", enc, plain)
	}
	if got, reseal := openHook(ctx, off, log, "o1", KindSlack, nil, hook); got != hook || reseal {
		t.Fatalf("no key read: %q %v", got, reseal)
	}
	// a sealed value without a key yields an off channel, never garbage
	if got, _ := openHook(ctx, off, log, "o1", KindSlack, []byte("x"), ""); got != "" {
		t.Fatal("sealed without key must be empty")
	}
	// an empty URL clears both columns
	if enc, plain, _ = sealHook(ctx, s, "o1", KindSlack, ""); enc != nil || plain != "" {
		t.Fatal("removed webhook")
	}
}

// ---- monthly plan reset (migration 785) ----

func TestPlanCapCountsOnlyTheCurrentPeriod(t *testing.T) {
	ps := NewMemPlans()
	pl := &Plans{Store: ps, CacheTTL: time.Nanosecond}
	ctx := context.Background()
	_ = ps.PutPlan(ctx, Plan{OrgID: "o1", Plan: policy.PlanFree, CapUSD: 5}, "t")
	if c := pl.OrgCap(ctx, "o1"); c != 5 {
		t.Fatalf("cap before any reset = %v", c)
	}
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if done, _ := ps.ResetPeriod(ctx, "o1", start.AddDate(0, -1, 0), 4.9); !done { // first run: only stamps the period
		t.Fatal("first run")
	}
	if p, _, _ := ps.GetPlan(ctx, "o1"); p.SpendBaseUSD != 0 {
		t.Fatal("the first run must not unlock anybody")
	}
	if done, _ := ps.ResetPeriod(ctx, "o1", start, 4.9); !done {
		t.Fatal("november reset")
	}
	if done, _ := ps.ResetPeriod(ctx, "o1", start, 9); done {
		t.Fatal("a period is reset once")
	}
	time.Sleep(time.Millisecond)
	// lifetime spend 4.9 at the reset, now 6.4: the plan has used 1.5 of 5 and the budget cap is the base on top
	if c := pl.OrgCap(ctx, "o1"); c != 9.9 {
		t.Fatalf("budget cap = %v, want 9.9", c)
	}
	u := pl.Status(ctx, "o1", 0, 6.4, 0.5)
	if u.CapUSD != 5 || u.SpentUSD < 1.499 || u.SpentUSD > 1.501 || u.State != policy.CapOK || u.Paused {
		t.Fatalf("usage %+v", u)
	}
	// an operator cap is a lifetime number and still applies
	u = pl.Status(ctx, "o1", 8, 8, 0)
	if !u.Paused || u.State != policy.CapExhausted {
		t.Fatalf("operator cap exhausted: %+v", u)
	}
}

func TestPlanResetterResetsOnTheFirstOfTheMonthOnce(t *testing.T) {
	ps := NewMemPlans()
	ctx := context.Background()
	_ = ps.PutPlan(ctx, Plan{OrgID: "o1", Plan: policy.PlanFree, CapUSD: 5}, "t")
	_ = ps.PutPlan(ctx, Plan{OrgID: "o2", Plan: policy.PlanPaid}, "t")
	spent := map[string]float64{"o1": 3, "o2": 100, "o3": 50}
	now := time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC)
	var audited []string
	r := &PlanResetter{Plans: &Plans{Store: ps}, Orgs: orgList{"o1", "o2", "o3"}, Now: func() time.Time { return now },
		Spend: func(_ context.Context, org string) (float64, error) { return spent[org], nil },
		Audit: func(_ context.Context, org, action string, _ map[string]any) { audited = append(audited, org+":"+action) },
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil))}

	if rep := r.RunOnce(ctx); rep.Reset != 0 || rep.Checked != 2 { // october is only stamped
		t.Fatalf("first sweep %+v", rep)
	}
	p, _, _ := ps.GetPlan(ctx, "o1")
	if p.PeriodStart == nil || !p.PeriodStart.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) || p.SpendBaseUSD != 0 {
		t.Fatalf("stamp: %+v", p)
	}
	if rep := r.RunOnce(ctx); rep.Checked != 0 {
		t.Fatalf("nothing is due within the month: %+v", rep)
	}

	now = time.Date(2026, 11, 1, 0, 5, 0, 0, time.UTC)
	spent["o1"] = 4.5
	if rep := r.RunOnce(ctx); rep.Reset != 2 {
		t.Fatalf("november sweep %+v", rep)
	}
	p, _, _ = ps.GetPlan(ctx, "o1")
	if p.SpendBaseUSD != 4.5 || !p.PeriodStart.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("reset: %+v", p)
	}
	if rep := r.RunOnce(ctx); rep.Reset != 0 {
		t.Fatalf("a second sweep must not reset again: %+v", rep)
	}
	if len(audited) != 2 || audited[0] != "o1:plan.period_reset" {
		t.Fatalf("audit %v", audited)
	}
	if p, ok, _ := ps.GetPlan(ctx, "o3"); ok {
		t.Fatalf("an organization without an explicit plan is untouched: %+v", p)
	}
}

type orgList []string

func (o orgList) ListOrgIDs(context.Context) ([]string, error) { return o, nil }

var _ = domain.ErrNotFound
