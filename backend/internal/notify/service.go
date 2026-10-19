package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/mailer"
	"aiworkforce/backend/internal/policy"
)

// DefaultTokenTTL is how long an approval link works.
const DefaultTokenTTL = 24 * time.Hour

// MaxTokenTTL bounds a configured validity.
const MaxTokenTTL = 7 * 24 * time.Hour

// ApprovalPort is what the notifier needs from the approvals of the app.
type ApprovalPort interface {
	Get(ctx context.Context, org, id string) (domain.Approval, error)
	// Decide applies the decision as actor (a human identified by a link).
	Decide(ctx context.Context, org, id, decision, note, actor string) (domain.Approval, error)
}

// Service notifies and serves the approval links.
type Service struct {
	Signer    *Signer // nil: links are not offered
	Settings  SettingsStore
	Replay    ReplayStore
	Plans     *Plans
	Mail      mailer.Mailer // nil: no e-mail
	HTTP      *http.Client
	Approvals ApprovalPort
	// PublicURL is the public base of the API (links point to PublicURL/api/v1/notify/a).
	// AppURL is where the app lives (the "open in the app" button); default PublicURL.
	PublicURL string
	AppURL    string
	TTL       time.Duration
	// AgentName (optional) turns an agent id into a name for the message.
	AgentName func(ctx context.Context, org, id string) string
	Log       *slog.Logger
	Now       func() time.Time
	// Outbox (optional) is the durable retry queue (migration 784): Notify and NotifyCap queue messages and the worker
	// (StartOutbox) sends them with retries. Without it they are sent once, inline, as before.
	Outbox OutboxStore
	// Sealer (optional) seals the queued payloads.
	Sealer Sealer
	// MaxAttempts bounds the tries of one message (default MaxOutboxAttempts).
	MaxAttempts int
	// Jitter (optional) returns a value in [0,1) for the retry spread; default 0.5.
	Jitter func() float64

	capMu   sync.Mutex
	capSent map[string]time.Time
	hot     sync.Map // organizations with queued messages
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Service) ttl() time.Duration {
	switch {
	case s.TTL <= 0:
		return DefaultTokenTTL
	case s.TTL > MaxTokenTTL:
		return MaxTokenTTL
	}
	return s.TTL
}

func (s *Service) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// LinksAvailable: the deployment can sign links and knows its public address.
func (s *Service) LinksAvailable() bool { return s.Signer != nil && s.PublicURL != "" && s.Replay != nil }

// EmailAvailable: the deployment can send e-mail.
func (s *Service) EmailAvailable() bool { return s.Mail != nil }

func (s *Service) appURL() string {
	if s.AppURL != "" {
		return strings.TrimRight(s.AppURL, "/")
	}
	return strings.TrimRight(s.PublicURL, "/")
}

// LinkAllowed says whether an approval may be decided from a link, and why
// not. The rule is default-deny: only a plain, single-approver, low or medium
// risk approval qualifies; everything heavier needs the signed-in session.
func LinkAllowed(ap domain.Approval) (bool, string) {
	switch {
	case ap.Status != domain.ApprovalPending:
		return false, "not_pending"
	case RiskRank(ap.Risk) >= 3:
		return false, "high_risk"
	case ap.RequiredApprovals > 1:
		return false, "double_approval"
	case ap.RequiredRole != "":
		return false, "role_required"
	case ap.NoSelfApproval:
		return false, "maker_checker"
	case strings.Contains(strings.ToLower(ap.Action), "code.run") || strings.EqualFold(ap.Tool, application.CodeToolName):
		return false, "code_run"
	}
	return true, ""
}

func (s *Service) linkURL(tok string) string {
	return strings.TrimRight(s.PublicURL, "/") + "/api/v1/notify/a?t=" + url.QueryEscape(tok)
}

func (s *Service) tokenFor(org, approval, decision, to string) (string, error) {
	return s.Signer.Sign(Claims{Org: org, Approval: approval, Decision: decision, To: to}, s.ttl())
}

// message builds the notification of an approval for one recipient label.
func (s *Service) message(ctx context.Context, org string, st Settings, ap domain.Approval, to string) Message {
	m := Message{Locale: st.Locale, Title: ap.Title, Risk: ap.Risk, Details: Clip(ap.Details, 400), Open: s.appURL()}
	if m.Open != "" {
		m.Open += "/approvals/" + url.PathEscape(ap.ID)
	}
	if s.AgentName != nil {
		m.Agent = oneLine(s.AgentName(ctx, org, ap.AgentID))
	}
	ok, _ := LinkAllowed(ap)
	if !ok {
		m.NeedsApp = true
		return m
	}
	if st.LinksEnabled && s.LinksAvailable() {
		a, e1 := s.tokenFor(org, ap.ID, DecisionApprove, to)
		r, e2 := s.tokenFor(org, ap.ID, DecisionReject, to)
		if e1 == nil && e2 == nil {
			m.Approve, m.Reject = s.linkURL(a), s.linkURL(r)
		}
	}
	return m
}

// Notify sends the approval to every configured channel. It never blocks the
// approval itself: errors are logged and nothing is retried inline.
func (s *Service) Notify(ctx context.Context, org string, ap domain.Approval) {
	if s.Settings == nil || ap.ID == "" || ap.Status != domain.ApprovalPending {
		return
	}
	st, err := s.Settings.GetSettings(ctx, org)
	if err != nil {
		s.log().Warn("notify: settings", "org", org, "err", err)
		return
	}
	if RiskRank(ap.Risk) < RiskRank(st.MinRisk) {
		return
	}
	if st.EmailEnabled && s.Mail != nil {
		for _, to := range st.EmailTo {
			m := s.message(ctx, org, st, ap, to)
			s.sendEmail(ctx, org, to, mailer.Message{Subject: m.Subject(), Text: m.PlainText(), HTML: m.HTMLBody()})
		}
	}
	if st.SlackWebhook != "" || st.TeamsWebhook != "" {
		s.chat(ctx, org, st, s.message(ctx, org, st, ap, "chat"))
	}
	s.flush(ctx, org)
}

// flush makes the first attempt of what was just queued right away; retries are the worker's job.
func (s *Service) flush(ctx context.Context, org string) {
	if s.Outbox != nil {
		if _, err := s.ProcessOutbox(ctx, org); err != nil {
			s.log().Warn("notify: outbox", "org", org, "err", err)
		}
	}
}

// chat sends m to the Slack and Teams webhooks of st (through the outbox when there is one).
func (s *Service) chat(ctx context.Context, org string, st Settings, m Message) {
	if st.SlackWebhook != "" {
		if body, err := m.SlackPayload(); err == nil {
			s.sendChat(ctx, org, KindSlack, st.SlackWebhook, body)
		}
	}
	if st.TeamsWebhook != "" {
		if body, err := m.TeamsPayload(); err == nil {
			s.sendChat(ctx, org, KindTeams, st.TeamsWebhook, body)
		}
	}
}

// post sends a JSON body to a webhook after re-validating its address (a row
// written before an allow-list change is not trusted).
func (s *Service) post(ctx context.Context, kind, hook string, body []byte) error {
	if err := ValidateWebhook(kind, hook); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := s.client().Do(req)
	if err != nil {
		return errors.New("webhook unreachable") // the URL is a secret: never put it in an error
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	if res.StatusCode >= 300 {
		return &httpStatusError{Code: res.StatusCode}
	}
	return nil
}

// NotifyCap tells the organization that its plan cap was reached and the work
// is paused. At most once per day per organization.
func (s *Service) NotifyCap(ctx context.Context, org string, u Usage) {
	if s.Settings == nil {
		return
	}
	s.capMu.Lock()
	if s.capSent == nil {
		s.capSent = map[string]time.Time{}
	}
	if t, ok := s.capSent[org]; ok && s.now().Sub(t) < 24*time.Hour {
		s.capMu.Unlock()
		return
	}
	s.capSent[org] = s.now()
	s.capMu.Unlock()
	st, err := s.Settings.GetSettings(ctx, org)
	if err != nil {
		return
	}
	if s.Plans != nil && s.Plans.Store != nil {
		_ = s.Plans.Store.MarkCapNotified(ctx, org, s.now())
	}
	m := Message{Locale: st.Locale, Open: s.appURL(), Notice: fmt.Sprintf("%s (%.2f / %.2f USD)", tr(st.Locale, "capBody"), u.SpentUSD, u.CapUSD)}
	if st.EmailEnabled && s.Mail != nil {
		for _, to := range st.EmailTo {
			s.sendEmail(ctx, org, to, mailer.Message{Subject: m.Subject(), Text: m.PlainText(), HTML: m.HTMLBody()})
		}
	}
	s.chat(ctx, org, st, m)
	s.flush(ctx, org)
}

// SendTest posts a harmless test message to the configured channels and
// returns what was attempted.
func (s *Service) SendTest(ctx context.Context, org string) (map[string]string, error) {
	st, err := s.Settings.GetSettings(ctx, org)
	if err != nil {
		return nil, err
	}
	res := map[string]string{}
	m := Message{Locale: st.Locale, Open: s.appURL(), Notice: tr(st.Locale, "test")}
	if st.EmailEnabled && s.Mail != nil {
		res["email"] = "ok"
		for _, to := range st.EmailTo {
			if err := s.Mail.Send(ctx, mailer.Message{To: to, Subject: m.Subject(), Text: m.PlainText(), HTML: m.HTMLBody()}); err != nil {
				res["email"] = "failed"
			}
		}
	}
	for kind, hook := range map[string]string{KindSlack: st.SlackWebhook, KindTeams: st.TeamsWebhook} {
		if hook == "" {
			continue
		}
		var body []byte
		if kind == KindSlack {
			body, _ = m.SlackPayload()
		} else {
			body, _ = m.TeamsPayload()
		}
		res[kind] = "ok"
		if err := s.post(ctx, kind, hook, body); err != nil {
			res[kind] = "failed"
		}
	}
	return res, nil
}

// Decision outcomes of Act (stable codes: the landing page translates them).
var (
	ErrLinksOff    = errors.New("notify: links are turned off")
	ErrNeedsApp    = errors.New("notify: this approval needs the session")
	ErrNotPending  = errors.New("notify: approval already decided")
	ErrUnavailable = errors.New("notify: links are not available")
)

// Peek verifies a token and loads its approval without consuming anything
// (the confirmation page of a GET).
func (s *Service) Peek(ctx context.Context, tok string) (Claims, domain.Approval, error) {
	if !s.LinksAvailable() || s.Approvals == nil {
		return Claims{}, domain.Approval{}, ErrUnavailable
	}
	c, err := s.Signer.Verify(tok)
	if err != nil {
		return c, domain.Approval{}, err
	}
	ap, err := s.Approvals.Get(ctx, c.Org, c.Approval)
	if err != nil {
		return c, ap, err
	}
	if ap.Status != domain.ApprovalPending {
		return c, ap, ErrNotPending
	}
	if ok, _ := LinkAllowed(ap); !ok {
		return c, ap, ErrNeedsApp
	}
	if st, err := s.Settings.GetSettings(ctx, c.Org); err == nil && !st.LinksEnabled {
		return c, ap, ErrLinksOff
	}
	return c, ap, nil
}

// Act consumes the token and applies its decision. The order matters: every
// refusal that does not burn the link happens before Consume, and Consume
// happens before Decide so two simultaneous clicks cannot both decide.
func (s *Service) Act(ctx context.Context, tok string) (Claims, domain.Approval, error) {
	c, ap, err := s.Peek(ctx, tok)
	if err != nil {
		return c, ap, err
	}
	if err := s.Replay.Consume(ctx, c.Org, c.JTI, time.Unix(c.Exp, 0)); err != nil {
		return c, ap, err
	}
	actor := "link:" + c.To
	if c.To == "" {
		actor = "link"
	}
	out, err := s.Approvals.Decide(ctx, c.Org, c.Approval, c.Decision, "decided from a notification link", actor)
	if err != nil {
		return c, out, err
	}
	return c, out, nil
}

// ---- adapter to the approvals of the app ----

// ApprovalsAdapter implements ApprovalPort on application.Approvals.
type ApprovalsAdapter struct {
	Approvals *application.Approvals
	Store     application.Store
}

// Get implements ApprovalPort.
func (a ApprovalsAdapter) Get(ctx context.Context, org, id string) (domain.Approval, error) {
	return a.Store.GetApproval(ctx, org, id)
}

// Decide implements ApprovalPort. A link carries no role: the people who get
// the link were chosen by an admin, so the decision counts as an admin's,
// within what LinkAllowed already restricted.
func (a ApprovalsAdapter) Decide(ctx context.Context, org, id, decision, note, actor string) (domain.Approval, error) {
	ctx = application.WithOrg(ctx, org)
	ctx = application.WithActor(ctx, actor)
	ctx = application.WithActorRole(ctx, policy.RoleAdmin)
	return a.Approvals.Decide(ctx, id, decision, note)
}

// ---- event decorator ----

// Publisher is the event port (same shape as application.Publisher).
type Publisher interface {
	Publish(ctx context.Context, e domain.Event) error
}

// Observer decorates the event publisher like push.Notifier: events pass
// through unchanged and first; approval.requested / approval.reminder notify
// the channels, and budget.exceeded of the organization cap tells the
// organization that its work is paused. Everything runs in the background.
type Observer struct {
	Inner Publisher
	svc   atomic.Pointer[Service]
	// UsageOf returns the current usage of the organization (for the cap notice).
	UsageOf func(ctx context.Context, org string) (Usage, bool)
}

// Bind installs the service (it needs the approvals, which are built after the publisher).
func (o *Observer) Bind(s *Service) { o.svc.Store(s) }

// Publish implements Publisher.
func (o *Observer) Publish(ctx context.Context, e domain.Event) error {
	err := o.Inner.Publish(ctx, e)
	svc := o.svc.Load()
	if svc == nil {
		return err
	}
	switch e.Type {
	case domain.EvApprovalRequest:
		if ap, ok := approvalOf(e.Payload); ok {
			go o.bg(ctx, svc, func(c context.Context) { svc.Notify(c, e.OrgID, ap) })
		}
	case domain.EvBudgetExceeded:
		if p, ok := e.Payload.(map[string]any); ok && fmt.Sprint(p["scope"]) == string(domain.ScopeOrg) {
			go o.bg(ctx, svc, func(c context.Context) {
				u := Usage{}
				if o.UsageOf != nil {
					if v, ok := o.UsageOf(c, e.OrgID); ok {
						u = v
					}
				}
				if u.CapUSD == 0 {
					u.CapUSD, _ = p["cap_usd"].(float64)
					u.SpentUSD, _ = p["spent_usd"].(float64)
				}
				svc.NotifyCap(c, e.OrgID, u)
			})
		}
	}
	return err
}

func (o *Observer) bg(ctx context.Context, svc *Service, fn func(context.Context)) {
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			svc.log().Error("notify: panic", "panic", r)
		}
	}()
	fn(c)
}

func approvalOf(payload any) (domain.Approval, bool) {
	if p, ok := payload.(map[string]any); ok {
		switch a := p["approval"].(type) {
		case domain.Approval:
			return a, a.ID != ""
		case *domain.Approval:
			if a != nil {
				return *a, a.ID != ""
			}
		}
	}
	return domain.Approval{}, false
}
