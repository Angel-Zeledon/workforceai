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
	ApprovalID string `json:"approval_id"`
	Title      string `json:"title"`
	Risk       string `json:"risk"`
	URL        string `json:"url"`
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
func (s *Service) Notify(ctx context.Context, orgID string, m Message) {
	subs, err := s.store.ListPushSubscriptions(ctx, orgID)
	if err != nil {
		s.cfg.Log.Warn("push: list subscriptions", "org", orgID, "err", err)
		return
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

// Notifier decorates the event publisher: every event goes through unchanged,
// and approval.requested also triggers a push in the background. Pushes only
// notify; deciding still needs an authenticated human through the API.
type Notifier struct {
	Inner Publisher
	Svc   *Service
}

func (n Notifier) Publish(ctx context.Context, e domain.Event) error {
	err := n.Inner.Publish(ctx, e)
	if n.Svc != nil && (e.Type == domain.EvApprovalRequest || e.Type == domain.EvApprovalReminder) {
		if ap, ok := approvalOf(e.Payload); ok {
			go func() {
				c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
				defer cancel()
				m := FromApproval(ap)
				m.Type = e.Type
				n.Svc.Notify(c, e.OrgID, m)
			}()
		}
	}
	return err
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
