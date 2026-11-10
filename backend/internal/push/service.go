package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/domain"
)

// Subscription is one browser's push endpoint. The endpoint is a capability
// URL (whoever has it can send pushes to that browser): it is never logged nor
// returned by the API.
type Subscription struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"-"`
	UserID    string    `json:"-"`
	Endpoint  string    `json:"-"`
	P256dh    string    `json:"-"`
	Auth      string    `json:"-"`
	UserAgent string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

// Store persists subscriptions per organization.
type Store interface {
	// SavePushSubscription inserts or refreshes (same org + endpoint) a subscription.
	SavePushSubscription(ctx context.Context, s Subscription) (Subscription, error)
	// DeletePushSubscription removes the endpoint; userID "" matches any owner.
	DeletePushSubscription(ctx context.Context, orgID, userID, endpoint string) error
	ListPushSubscriptions(ctx context.Context, orgID string) ([]Subscription, error)
}

// MemoryStore is the in-memory Store (demo mode without Postgres).
type MemoryStore struct {
	mu   sync.Mutex
	subs map[string]map[string]Subscription // org -> endpoint -> sub
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{subs: map[string]map[string]Subscription{}} }

func (m *MemoryStore) SavePushSubscription(_ context.Context, s Subscription) (Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	org := m.subs[s.OrgID]
	if org == nil {
		org = map[string]Subscription{}
		m.subs[s.OrgID] = org
	}
	if prev, ok := org[s.Endpoint]; ok {
		s.ID, s.CreatedAt = prev.ID, prev.CreatedAt
	}
	org[s.Endpoint] = s
	return s, nil
}

func (m *MemoryStore) DeletePushSubscription(_ context.Context, orgID, userID, endpoint string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.subs[orgID][endpoint]; ok && (userID == "" || s.UserID == userID) {
		delete(m.subs[orgID], endpoint)
	}
	return nil
}

func (m *MemoryStore) ListPushSubscriptions(_ context.Context, orgID string) ([]Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Subscription{}
	for _, s := range m.subs[orgID] {
		out = append(out, s)
	}
	return out, nil
}

// DefaultAllowedHosts are the push services of the major browsers. The server
// only ever POSTs to these (plus PUSH_ALLOWED_HOSTS), so a forged subscription
// cannot turn the backend into a request forwarder (SSRF).
var DefaultAllowedHosts = []string{
	"fcm.googleapis.com", "android.googleapis.com", // Chrome, Edge (Chromium), Android
	"updates.push.services.mozilla.com", // Firefox
	"web.push.apple.com",                // Safari
	".notify.windows.com",               // legacy Edge / Windows (suffix)
	".push.apple.com",                   // Apple (suffix)
}

// Config of the service.
type Config struct {
	VAPID        *VAPID
	Subject      string   // mailto: or https: contact of the operator (VAPID "sub")
	AllowedHosts []string // exact hosts, or suffixes starting with "."
	Client       *http.Client
	Log          *slog.Logger
	Now          func() time.Time
}

// Service validates subscriptions and delivers notifications.
type Service struct {
	cfg   Config
	store Store
	sem   chan struct{}
}

// New returns nil when VAPID keys are missing: push is then disabled and the
// endpoints report it (GET /push/config enabled=false).
func New(cfg Config, store Store) *Service {
	if cfg.VAPID == nil || store == nil {
		return nil
	}
	if len(cfg.AllowedHosts) == 0 {
		cfg.AllowedHosts = DefaultAllowedHosts
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Service{cfg: cfg, store: store, sem: make(chan struct{}, 8)}
}

func (s *Service) PublicKey() string { return s.cfg.VAPID.Public }

// ValidateEndpoint accepts only https URLs of an allowed push service.
func (s *Service) ValidateEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || len(raw) > 2048 {
		return fmt.Errorf("%w: endpoint must be an https URL of a push service", domain.ErrInvalid)
	}
	host := strings.ToLower(u.Hostname())
	if net.ParseIP(host) != nil || (u.Port() != "" && u.Port() != "443") {
		return fmt.Errorf("%w: endpoint host not allowed", domain.ErrInvalid)
	}
	for _, h := range s.cfg.AllowedHosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" && (host == h || (strings.HasPrefix(h, ".") && strings.HasSuffix(host, h))) {
			return nil
		}
	}
	return fmt.Errorf("%w: endpoint host not allowed", domain.ErrInvalid)
}

// Subscribe validates and stores a browser subscription.
func (s *Service) Subscribe(ctx context.Context, sub Subscription) (Subscription, error) {
	if err := s.ValidateEndpoint(sub.Endpoint); err != nil {
		return sub, err
	}
	if k, err := decodeB64(sub.P256dh); err != nil || len(k) != 65 {
		return sub, fmt.Errorf("%w: keys.p256dh must be a 65-byte P-256 key", domain.ErrInvalid)
	}
	if a, err := decodeB64(sub.Auth); err != nil || len(a) != 16 {
		return sub, fmt.Errorf("%w: keys.auth must be 16 bytes", domain.ErrInvalid)
	}
	if len(sub.UserAgent) > 200 {
		sub.UserAgent = sub.UserAgent[:200]
	}
	sub.ID, sub.CreatedAt = uuid.NewString(), s.cfg.Now().UTC()
	return s.store.SavePushSubscription(ctx, sub)
}

func (s *Service) Unsubscribe(ctx context.Context, orgID, userID, endpoint string) error {
	return s.store.DeletePushSubscription(ctx, orgID, userID, endpoint)
}

// Message is the push payload. It carries only what the lock screen may show:
// never details, arguments, recipients or message content.
type Message struct {
	Type       string `json:"type"`
	ApprovalID string `json:"approval_id,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
	Title      string `json:"title"`
	Risk       string `json:"risk"`
	URL        string `json:"url"`
	// Count (help.notice): how many questions this notice is about. No question text travels (lock screen).
	Count int `json:"count,omitempty"`
}

// FromApproval builds the notification of a new approval (title + risk only).
func FromApproval(a domain.Approval) Message {
	title := strings.TrimSpace(a.Title)
	if utf8.RuneCountInString(title) > 80 {
		title = string([]rune(title)[:79]) + "…"
	}
	risk := strings.ToLower(a.Risk)
	if risk != "low" && risk != "medium" && risk != "high" {
		risk = "medium"
	}
	return Message{Type: domain.EvApprovalRequest, ApprovalID: a.ID, Title: title, Risk: risk, URL: "/approvals/" + url.PathEscape(a.ID)}
}

// Notify sends m to every subscription of the organization. Endpoints the push
// service reports as gone (404/410) are deleted.
func (s *Service) Notify(ctx context.Context, orgID string, m Message) { s.notify(ctx, orgID, m, nil) }

// NotifyUsers sends m only to the subscriptions of these people (E0: a notice goes to the person it is for).
// An empty list sends nothing.
func (s *Service) NotifyUsers(ctx context.Context, orgID string, m Message, users []string) {
	if len(users) == 0 {
		return
	}
	allow := make(map[string]bool, len(users))
	for _, u := range users {
		if u != "" {
			allow[u] = true
		}
	}
	s.notify(ctx, orgID, m, allow)
}

func (s *Service) notify(ctx context.Context, orgID string, m Message, allow map[string]bool) {
	all, err := s.store.ListPushSubscriptions(ctx, orgID)
	if err != nil {
		s.cfg.Log.Warn("push: list subscriptions", "org", orgID, "err", err)
		return
	}
	subs := all
	if allow != nil {
		subs = nil
		for _, sub := range all {
			if allow[sub.UserID] {
				subs = append(subs, sub)
			}
		}
	}
	body, _ := json.Marshal(m)
	var wg sync.WaitGroup
	for _, sub := range subs {
		wg.Add(1)
		s.sem <- struct{}{}
		go func(sub Subscription) {
			defer func() { <-s.sem; wg.Done() }()
			status, err := s.send(ctx, sub, body)
			switch {
			case err != nil:
				s.cfg.Log.Warn("push: send failed", "org", orgID, "subscription", sub.ID, "err", err)
			case status == http.StatusNotFound || status == http.StatusGone:
				_ = s.store.DeletePushSubscription(ctx, orgID, "", sub.Endpoint)
			case status >= 300:
				s.cfg.Log.Warn("push: rejected by push service", "org", orgID, "subscription", sub.ID, "status", status)
			}
		}(sub)
	}
	wg.Wait()
}

func (s *Service) send(ctx context.Context, sub Subscription, body []byte) (int, error) {
	// Re-validate: rows written before an allow-list change must not be used.
	if err := s.ValidateEndpoint(sub.Endpoint); err != nil {
		return 0, err
	}
	ua, err := decodeB64(sub.P256dh)
	if err != nil {
		return 0, err
	}
	secret, err := decodeB64(sub.Auth)
	if err != nil {
		return 0, err
	}
	enc, err := encrypt(body, ua, secret)
	if err != nil {
		return 0, err
	}
	auth, err := s.cfg.VAPID.authHeader(sub.Endpoint, s.cfg.Subject, s.cfg.Now())
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(enc))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", "86400")
	req.Header.Set("Urgency", "high")
	req.Header.Set("Authorization", auth)
	res, err := s.cfg.Client.Do(req)
	if err != nil {
		return 0, err
	}
	res.Body.Close()
	return res.StatusCode, nil
}

// Publisher is the event port (same shape as application.Publisher).
type Publisher interface {
	Publish(ctx context.Context, e domain.Event) error
}

// Member is one person of an organization and their role (owner|admin|member|viewer).
type Member struct {
	UserID string
	Role   string
}

// Recipients says who a push is for (E0, docs/architecture/equipos-oficinas.md). It is filled once the
// identity store exists (cmd/server); with no Members (a deployment without authentication) every
// subscription of the organization is notified, as before.
type Recipients struct {
	// Members lists the people of an organization with their roles.
	Members func(ctx context.Context, org string) ([]Member, error)
	// CanDecide reports whether a person may decide an approval now (role, no self-approval, double approval and,
	// D3, the office rule: an office's approvals go to its responsibles).
	CanDecide func(ctx context.Context, org string, ap domain.Approval, userID, role string) bool
}

// approvers are the people who may decide ap (scoped=false: no identity store, notify everyone).
func (r *Recipients) approvers(ctx context.Context, org string, ap domain.Approval) (users []string, scoped bool) {
	if r == nil || r.Members == nil || r.CanDecide == nil {
		return nil, false
	}
	ms, err := r.Members(ctx, org)
	if err != nil {
		return nil, true // fail closed: nobody rather than everybody
	}
	for _, m := range ms {
		if r.CanDecide(ctx, org, ap, m.UserID, m.Role) {
			users = append(users, m.UserID)
		}
	}
	return users, true
}

// Notifier decorates the event publisher: every event goes through unchanged,
// and approval.requested also triggers a push in the background. Pushes only
// notify; deciding still needs an authenticated human through the API.
//
// E0: an approval is pushed to the people who can decide it; the result of a request (request.completed,
// request.failed) to the person who asked for it, and to nobody when no person asked (scheduled work).
type Notifier struct {
	Inner Publisher
	Svc   *Service
	Who   *Recipients // optional
}

func (n Notifier) Publish(ctx context.Context, e domain.Event) error {
	err := n.Inner.Publish(ctx, e)
	if n.Svc == nil {
		return err
	}
	switch e.Type {
	case domain.EvApprovalRequest, domain.EvApprovalReminder:
		if ap, ok := approvalOf(e.Payload); ok {
			go func() {
				c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
				defer cancel()
				m := FromApproval(ap)
				m.Type = e.Type
				if users, scoped := n.Who.approvers(c, e.OrgID, ap); scoped {
					n.Svc.NotifyUsers(c, e.OrgID, m, users)
					return
				}
				n.Svc.Notify(c, e.OrgID, m)
			}()
		}
	case domain.EvHelpNotice: // needs your help: to the person asked, or to the owners and admins when nobody was
		p, _ := e.Payload.(map[string]any)
		asked, _ := p["asked_for"].(string)
		count, _ := p["count"].(int)
		go func() {
			c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			m := Message{Type: domain.EvHelpNotice, Count: max(count, 1), URL: "/approvals/inbox"}
			users := []string{}
			if asked != "" {
				users = append(users, asked)
			} else if n.Who != nil && n.Who.Members != nil {
				ms, _ := n.Who.Members(c, e.OrgID)
				for _, mb := range ms {
					if mb.Role == "owner" || mb.Role == "admin" {
						users = append(users, mb.UserID)
					}
				}
			}
			if len(users) == 0 && (n.Who == nil || n.Who.Members == nil) {
				n.Svc.Notify(c, e.OrgID, m) // no identity store (demo): everyone subscribed
				return
			}
			n.Svc.NotifyUsers(c, e.OrgID, m, users)
		}()
	case domain.EvRequestCompleted, domain.EvRequestFailed:
		id, by := requestOf(e.Payload)
		if id == "" || by == "" {
			return err
		}
		go func() {
			c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			n.Svc.NotifyUsers(c, e.OrgID, FromRequest(e.Type, id), []string{by})
		}()
	}
	return err
}

// FromRequest is the notification of a finished request: no title nor content (the lock screen may be
// seen by anyone), the service worker shows a localized "your request is ready / failed" line.
func FromRequest(typ, requestID string) Message {
	return Message{Type: typ, RequestID: requestID, URL: "/chats?pedido=" + url.QueryEscape(requestID)} // opens its pedido beside the chat that asked for it
}

func requestOf(payload any) (id, by string) {
	switch p := payload.(type) {
	case map[string]any:
		id, _ = p["request_id"].(string)
		by, _ = p["requested_by"].(string)
	}
	return id, by
}

func approvalOf(payload any) (domain.Approval, bool) {
	switch p := payload.(type) {
	case map[string]any:
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

// ErrDisabled is returned by the API when VAPID keys are not configured.
var ErrDisabled = errors.New("push notifications are not configured")
