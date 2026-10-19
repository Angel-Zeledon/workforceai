package notify

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/mailer"
	"aiworkforce/backend/internal/policy"
)

type fakeApprovals struct {
	mu      sync.Mutex
	ap      domain.Approval
	decided int
	actor   string
}

func (f *fakeApprovals) Get(_ context.Context, org, id string) (domain.Approval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.ap.ID {
		return domain.Approval{}, domain.ErrNotFound
	}
	return f.ap, nil
}

func (f *fakeApprovals) Decide(_ context.Context, org, id, decision, note, actor string) (domain.Approval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ap.Status != domain.ApprovalPending {
		return f.ap, domain.ErrConflict
	}
	f.decided++
	f.actor = actor
	if decision == "approve" {
		f.ap.Status = domain.ApprovalApproved
	} else {
		f.ap.Status = domain.ApprovalRejected
	}
	return f.ap, nil
}

type fakeMail struct {
	mu   sync.Mutex
	sent []mailer.Message
}

func (m *fakeMail) Send(_ context.Context, msg mailer.Message) error {
	m.mu.Lock()
	m.sent = append(m.sent, msg)
	m.mu.Unlock()
	return nil
}

type rt func(*http.Request) (*http.Response, error)

func (f rt) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newSvc(t *testing.T, ap domain.Approval) (*Service, *fakeApprovals, *fakeMail, *time.Time) {
	t.Helper()
	sg, _ := newTestSigner(t)
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	sg = sg.WithClock(func() time.Time { return now })
	fa := &fakeApprovals{ap: ap}
	fm := &fakeMail{}
	set := NewMemSettings()
	_ = set.PutSettings(context.Background(), Settings{OrgID: "o1", EmailEnabled: true, EmailTo: []string{"boss@example.com"}, MinRisk: "low", LinksEnabled: true, Locale: "en"})
	s := &Service{Signer: sg, Settings: set, Replay: NewMemReplay(), Plans: &Plans{Store: NewMemPlans()}, Mail: fm, Approvals: fa,
		PublicURL: "https://app.example.com", Now: func() time.Time { return now }}
	return s, fa, fm, &now
}

func pending(risk string) domain.Approval {
	return domain.Approval{ID: "ap1", Title: "Update the budget sheet", Details: "adds a row", Risk: risk, Status: domain.ApprovalPending, RequiredApprovals: 1}
}

func linksOf(t *testing.T, body string) (approve, reject string) {
	t.Helper()
	for _, l := range strings.Fields(body) {
		if strings.HasPrefix(l, "https://app.example.com/api/v1/notify/a?t=") {
			if approve == "" {
				approve = l
			} else if reject == "" {
				reject = l
			}
		}
	}
	return
}

func tokenOf(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("t")
}

func TestEmailCarriesTwoSignedLinks(t *testing.T) {
	s, _, fm, _ := newSvc(t, pending("low"))
	s.Notify(context.Background(), "o1", pending("low"))
	if len(fm.sent) != 1 {
		t.Fatalf("sent %d", len(fm.sent))
	}
	a, r := linksOf(t, fm.sent[0].Text)
	if a == "" || r == "" || a == r {
		t.Fatalf("links: %q %q", a, r)
	}
	ca, err := s.Signer.Verify(tokenOf(t, a))
	cr, err2 := s.Signer.Verify(tokenOf(t, r))
	if err != nil || err2 != nil || ca.Decision != DecisionApprove || cr.Decision != DecisionReject || ca.To != "boss@example.com" {
		t.Fatalf("claims: %+v %+v %v %v", ca, cr, err, err2)
	}
	if !strings.Contains(fm.sent[0].Subject, "Approval pending") {
		t.Fatalf("subject %q", fm.sent[0].Subject)
	}
}

func TestGetNeverDecidesPostDecidesOnce(t *testing.T) {
	s, fa, fm, _ := newSvc(t, pending("medium"))
	s.Notify(context.Background(), "o1", pending("medium"))
	a, _ := linksOf(t, fm.sent[0].Text)
	h := s.Landing()

	// A prefetching scanner opens the link ten times: nothing is decided.
	for i := 0; i < 10; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", a, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `name="t"`) {
			t.Fatalf("get %d: %d %s", i, w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatal("token pages must not be cached or leaked through referrers")
		}
	}
	if fa.decided != 0 {
		t.Fatal("GET decided")
	}

	post := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/v1/notify/a", strings.NewReader(url.Values{"t": {tokenOf(t, a)}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		h.ServeHTTP(w, req)
		return w
	}
	if w := post(); w.Code != 200 || fa.decided != 1 || fa.ap.Status != domain.ApprovalApproved || fa.actor != "link:boss@example.com" {
		t.Fatalf("first post: %d decided=%d actor=%q", w.Code, fa.decided, fa.actor)
	}
	// Replay of the very same link: refused, nothing decided twice.
	if w := post(); w.Code != http.StatusGone || fa.decided != 1 {
		t.Fatalf("replay: %d decided=%d", w.Code, fa.decided)
	}
}

func TestSiblingLinkCannotOverrideAfterDecision(t *testing.T) {
	s, fa, fm, _ := newSvc(t, pending("low"))
	s.Notify(context.Background(), "o1", pending("low"))
	a, r := linksOf(t, fm.sent[0].Text)
	if _, _, err := s.Act(context.Background(), tokenOf(t, a)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Act(context.Background(), tokenOf(t, r)); !errors.Is(err, ErrNotPending) {
		t.Fatalf("reject link after approval: %v", err)
	}
	if fa.decided != 1 {
		t.Fatal("decided twice")
	}
}

func TestConcurrentClicksDecideOnce(t *testing.T) {
	s, fa, fm, _ := newSvc(t, pending("low"))
	s.Notify(context.Background(), "o1", pending("low"))
	a, _ := linksOf(t, fm.sent[0].Text)
	tok := tokenOf(t, a)
	var wg sync.WaitGroup
	var ok, used int
	var mu sync.Mutex
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := s.Act(context.Background(), tok)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				ok++
			} else if errors.Is(err, ErrUsed) || errors.Is(err, ErrNotPending) {
				used++
			}
		}()
	}
	wg.Wait()
	if ok != 1 || fa.decided != 1 {
		t.Fatalf("ok=%d decided=%d used=%d", ok, fa.decided, used)
	}
}

func TestExpiredLinkIsRefused(t *testing.T) {
	s, fa, fm, now := newSvc(t, pending("low"))
	s.Notify(context.Background(), "o1", pending("low"))
	a, _ := linksOf(t, fm.sent[0].Text)
	*now = now.Add(25 * time.Hour)
	if _, _, err := s.Act(context.Background(), tokenOf(t, a)); !errors.Is(err, ErrExpired) {
		t.Fatalf("want expired, got %v", err)
	}
	if fa.decided != 0 {
		t.Fatal("expired link decided")
	}
}

func TestHighRiskAndHeavyApprovalsNeverGetLinks(t *testing.T) {
	heavy := map[string]domain.Approval{}
	h := pending("high")
	heavy["high"] = h
	d := pending("low")
	d.RequiredApprovals = 2
	heavy["double"] = d
	rr := pending("low")
	rr.RequiredRole = "admin"
	heavy["role"] = rr
	m := pending("medium")
	m.NoSelfApproval = true
	heavy["maker-checker"] = m
	c := pending("low")
	c.Action = "code.run"
	heavy["code"] = c
	for name, ap := range heavy {
		s, fa, fm, _ := newSvc(t, ap)
		s.Notify(context.Background(), "o1", ap)
		if len(fm.sent) != 1 {
			t.Fatalf("%s: the person must still be told (%d mails)", name, len(fm.sent))
		}
		if a, _ := linksOf(t, fm.sent[0].Text); a != "" {
			t.Fatalf("%s: got a decision link", name)
		}
		if !strings.Contains(fm.sent[0].Text, "inside the app") || !strings.Contains(fm.sent[0].Text, "/approvals/ap1") {
			t.Fatalf("%s: should point to the app: %s", name, fm.sent[0].Text)
		}
		// Even a token forged by someone holding the key for a heavy approval is refused at use time.
		tok, _ := s.Signer.Sign(Claims{Org: "o1", Approval: "ap1", Decision: DecisionApprove}, time.Hour)
		if _, _, err := s.Act(context.Background(), tok); !errors.Is(err, ErrNeedsApp) {
			t.Fatalf("%s: act: %v", name, err)
		}
		if fa.decided != 0 {
			t.Fatalf("%s: decided", name)
		}
	}
}

func TestLinksOffInSettings(t *testing.T) {
	s, _, fm, _ := newSvc(t, pending("low"))
	st, _ := s.Settings.GetSettings(context.Background(), "o1")
	st.LinksEnabled = false
	_ = s.Settings.PutSettings(context.Background(), st)
	s.Notify(context.Background(), "o1", pending("low"))
	if a, _ := linksOf(t, fm.sent[0].Text); a != "" {
		t.Fatal("links were sent although they are off")
	}
	tok, _ := s.Signer.Sign(Claims{Org: "o1", Approval: "ap1", Decision: DecisionApprove}, time.Hour)
	if _, _, err := s.Act(context.Background(), tok); !errors.Is(err, ErrLinksOff) {
		t.Fatalf("act with links off: %v", err)
	}
}

func TestOtherOrganizationTokenCannotTouchApproval(t *testing.T) {
	s, fa, _, _ := newSvc(t, pending("low"))
	// The fake store only knows ap1; a token for another org's id is just "not found".
	tok, _ := s.Signer.Sign(Claims{Org: "o2", Approval: "zzz", Decision: DecisionApprove}, time.Hour)
	if _, _, err := s.Act(context.Background(), tok); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if fa.decided != 0 {
		t.Fatal("decided")
	}
}

func TestMinRiskFilter(t *testing.T) {
	s, _, fm, _ := newSvc(t, pending("low"))
	st, _ := s.Settings.GetSettings(context.Background(), "o1")
	st.MinRisk = "medium"
	_ = s.Settings.PutSettings(context.Background(), st)
	s.Notify(context.Background(), "o1", pending("low"))
	if len(fm.sent) != 0 {
		t.Fatal("a low risk approval was sent above the threshold")
	}
	s.Notify(context.Background(), "o1", pending("medium"))
	if len(fm.sent) != 1 {
		t.Fatal("medium should be sent")
	}
}

func TestSlackAndTeamsGetLinkButtons(t *testing.T) {
	s, _, _, _ := newSvc(t, pending("low"))
	var mu sync.Mutex
	got := map[string]string{}
	s.HTTP = &http.Client{Transport: rt(func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got[r.URL.Host] = string(b)
		mu.Unlock()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: http.Header{}}, nil
	})}
	st, _ := s.Settings.GetSettings(context.Background(), "o1")
	st.EmailEnabled = false
	st.SlackWebhook = "https://hooks.slack.com/services/T000/B000/XXXXXXXX"
	st.TeamsWebhook = "https://contoso.webhook.office.com/webhookb2/abc/IncomingWebhook/def/ghi"
	_ = s.Settings.PutSettings(context.Background(), st)
	s.Notify(context.Background(), "o1", pending("low"))
	slack, teams := got["hooks.slack.com"], got["contoso.webhook.office.com"]
	if !strings.Contains(slack, `"type":"actions"`) || strings.Count(slack, "api/v1/notify/a?t=") != 2 {
		t.Fatalf("slack: %s", slack)
	}
	if !strings.Contains(teams, "Action.OpenUrl") || strings.Count(teams, "api/v1/notify/a?t=") != 2 {
		t.Fatalf("teams: %s", teams)
	}
}

func TestWebhookValidation(t *testing.T) {
	ok := map[string]string{
		KindSlack: "https://hooks.slack.com/services/T0/B0/xx",
		KindTeams: "https://org.webhook.office.com/webhookb2/a/IncomingWebhook/b/c",
	}
	for k, v := range ok {
		if err := ValidateWebhook(k, v); err != nil {
			t.Errorf("%s: %v", k, err)
		}
	}
	bad := []struct{ kind, u string }{
		{KindSlack, "http://hooks.slack.com/services/x"},
		{KindSlack, "https://127.0.0.1/services/x"},
		{KindSlack, "https://hooks.slack.com.evil.example/services/x"},
		{KindSlack, "https://evil.example/hooks.slack.com"},
		{KindSlack, "https://user:pw@hooks.slack.com/x"},
		{KindSlack, "https://hooks.slack.com:8443/x"},
		{KindSlack, "https://169.254.169.254/latest"},
		{KindTeams, "https://office.com.evil.example/x"},
		{KindTeams, "https://hooks.slack.com/services/x"},
		{KindTeams, "https://webhook.office.com.attacker.io/x"},
		{KindSlack, "ftp://hooks.slack.com/x"},
		{KindSlack, ""},
	}
	for _, b := range bad {
		if err := ValidateWebhook(b.kind, b.u); err == nil {
			t.Errorf("%s %q accepted", b.kind, b.u)
		}
	}
}

func TestSettingsApplyAndMask(t *testing.T) {
	now := time.Now()
	s := DefaultSettings("o1")
	en, hook := true, "https://hooks.slack.com/services/T0/B0/SECRETVALUE"
	n, err := s.Apply(SettingsInput{EmailEnabled: &en, EmailTo: []string{"A@Example.com", "a@example.com", " b@example.com "}, SlackWebhook: &hook}, "u1", now)
	if err != nil || len(n.EmailTo) != 2 || n.EmailTo[0] != "a@example.com" {
		t.Fatalf("%+v %v", n, err)
	}
	v := n.View(true, true)
	if strings.Contains(v.SlackMasked, "SECRETVALUE") || !v.SlackConfigured {
		t.Fatalf("mask leaked: %q", v.SlackMasked)
	}
	// E-mail on without recipients is refused; so is a bad address or locale.
	if _, err := DefaultSettings("o").Apply(SettingsInput{EmailEnabled: &en}, "u", now); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("no recipients: %v", err)
	}
	bad := "not-an-email"
	if _, err := s.Apply(SettingsInput{EmailTo: []string{bad}}, "u", now); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("bad address: %v", err)
	}
	loc := "fr"
	if _, err := s.Apply(SettingsInput{Locale: &loc}, "u", now); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("bad locale: %v", err)
	}
	// Removing a webhook with an empty string.
	empty := ""
	n2, err := n.Apply(SettingsInput{SlackWebhook: &empty}, "u", now)
	if err != nil || n2.SlackWebhook != "" {
		t.Fatalf("remove: %+v %v", n2, err)
	}
}

func TestHeaderInjectionInTitle(t *testing.T) {
	ap := pending("low")
	ap.Title = "hello\r\nBcc: attacker@example.com"
	s, _, fm, _ := newSvc(t, ap)
	s.Notify(context.Background(), "o1", ap)
	if strings.ContainsAny(fm.sent[0].Subject, "\r\n") {
		t.Fatalf("subject has a line break: %q", fm.sent[0].Subject)
	}
}

func TestCapNoticeSentOncePerDay(t *testing.T) {
	s, _, fm, now := newSvc(t, pending("low"))
	u := Usage{CapStatus: policy.EvaluateCap(policy.PlanFree, true, 1, 1, 0)}
	s.NotifyCap(context.Background(), "o1", u)
	s.NotifyCap(context.Background(), "o1", u)
	if len(fm.sent) != 1 {
		t.Fatalf("%d notices", len(fm.sent))
	}
	if !strings.Contains(fm.sent[0].Text, "paused") {
		t.Fatalf("text: %s", fm.sent[0].Text)
	}
	*now = now.Add(25 * time.Hour)
	s.NotifyCap(context.Background(), "o1", u)
	if len(fm.sent) != 2 {
		t.Fatalf("the notice should repeat after a day, got %d", len(fm.sent))
	}
}

func TestPlansOrgCapAndFailClosed(t *testing.T) {
	ctx := context.Background()
	p := &Plans{Store: NewMemPlans()}
	if c := p.OrgCap(ctx, "o1"); c != 0 {
		t.Fatalf("no default plan: cap %v", c)
	}
	p = &Plans{Store: NewMemPlans(), DefaultFreeCapUSD: 2}
	if c := p.OrgCap(ctx, "o1"); c != 2 {
		t.Fatalf("default free cap: %v", c)
	}
	// An explicit paid plan lifts the default free cap; an explicit free plan with a broken cap blocks.
	_ = p.Store.PutPlan(ctx, Plan{OrgID: "o2", Plan: policy.PlanPaid}, "t")
	if c := p.OrgCap(ctx, "o2"); c != 0 {
		t.Fatalf("paid: %v", c)
	}
	_ = p.Store.PutPlan(ctx, Plan{OrgID: "o3", Plan: policy.PlanFree, CapUSD: 0}, "t")
	if c := p.OrgCap(ctx, "o3"); c <= 0 || c > 1e-6 {
		t.Fatalf("broken free plan must block, got cap %v", c)
	}
	u := p.Status(ctx, "o1", 0, 1.9, 0.2)
	if !u.Paused || u.CapUSD != 2 || u.State != policy.CapExhausted {
		t.Fatalf("status: %+v", u)
	}
	// The operator cap wins when stricter.
	u = p.Status(ctx, "o1", 1, 0.5, 0)
	if u.CapUSD != 1 {
		t.Fatalf("operator cap: %+v", u)
	}
}
