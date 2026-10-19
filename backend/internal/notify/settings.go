package notify

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Settings are the notification channels of one organization.
type Settings struct {
	OrgID        string    `json:"-"`
	EmailEnabled bool      `json:"email_enabled"`
	EmailTo      []string  `json:"email_to"`
	SlackWebhook string    `json:"-"` // a secret: never returned, see View
	TeamsWebhook string    `json:"-"`
	MinRisk      string    `json:"min_risk"` // low | medium | high: the lowest risk that is notified
	LinksEnabled bool      `json:"links_enabled"`
	Locale       string    `json:"locale"` // es | en | pt: language of the messages
	UpdatedBy    string    `json:"updated_by,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
}

// SettingsView is what the API returns: the webhooks are masked.
type SettingsView struct {
	Settings
	SlackConfigured bool   `json:"slack_configured"`
	SlackMasked     string `json:"slack_masked,omitempty"`
	TeamsConfigured bool   `json:"teams_configured"`
	TeamsMasked     string `json:"teams_masked,omitempty"`
	EmailAvailable  bool   `json:"email_available"` // the deployment has SMTP
	LinksAvailable  bool   `json:"links_available"` // the deployment has a signing key and a public URL
}

// SettingsInput is a change. A nil pointer keeps the stored value; an empty
// string in Slack/Teams removes the webhook.
type SettingsInput struct {
	EmailEnabled *bool    `json:"email_enabled"`
	EmailTo      []string `json:"email_to"`
	SlackWebhook *string  `json:"slack_webhook"`
	TeamsWebhook *string  `json:"teams_webhook"`
	MinRisk      *string  `json:"min_risk"`
	LinksEnabled *bool    `json:"links_enabled"`
	Locale       *string  `json:"locale"`
}

// MaxRecipients bounds the e-mail list.
const MaxRecipients = 10

// DefaultSettings is what an organization has before it configures anything:
// nothing is sent.
func DefaultSettings(org string) Settings {
	return Settings{OrgID: org, MinRisk: "low", LinksEnabled: true, Locale: "es", EmailTo: []string{}}
}

// Apply validates in and returns the new settings (s is not modified).
func (s Settings) Apply(in SettingsInput, by string, now time.Time) (Settings, error) {
	if in.EmailEnabled != nil {
		s.EmailEnabled = *in.EmailEnabled
	}
	if in.EmailTo != nil {
		to, err := NormalizeRecipients(in.EmailTo)
		if err != nil {
			return s, err
		}
		s.EmailTo = to
	}
	if in.SlackWebhook != nil {
		w := strings.TrimSpace(*in.SlackWebhook)
		if w != "" {
			if err := ValidateWebhook(KindSlack, w); err != nil {
				return s, err
			}
		}
		s.SlackWebhook = w
	}
	if in.TeamsWebhook != nil {
		w := strings.TrimSpace(*in.TeamsWebhook)
		if w != "" {
			if err := ValidateWebhook(KindTeams, w); err != nil {
				return s, err
			}
		}
		s.TeamsWebhook = w
	}
	if in.MinRisk != nil {
		switch r := strings.ToLower(strings.TrimSpace(*in.MinRisk)); r {
		case "low", "medium", "high":
			s.MinRisk = r
		default:
			return s, fmt.Errorf("%w: min_risk must be low, medium or high", domain.ErrInvalid)
		}
	}
	if in.LinksEnabled != nil {
		s.LinksEnabled = *in.LinksEnabled
	}
	if in.Locale != nil {
		switch l := strings.ToLower(strings.TrimSpace(*in.Locale)); l {
		case "es", "en", "pt":
			s.Locale = l
		default:
			return s, fmt.Errorf("%w: locale must be es, en or pt", domain.ErrInvalid)
		}
	}
	if s.EmailEnabled && len(s.EmailTo) == 0 {
		return s, fmt.Errorf("%w: e-mail notifications need at least one recipient", domain.ErrInvalid)
	}
	s.UpdatedBy, s.UpdatedAt = by, now.UTC()
	return s, nil
}

// NormalizeRecipients lowercases, validates and de-duplicates addresses.
func NormalizeRecipients(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		a, err := mail.ParseAddress(raw)
		if err != nil || a.Address == "" || strings.ContainsAny(a.Address, "\r\n") {
			return nil, fmt.Errorf("%w: %q is not an e-mail address", domain.ErrInvalid, raw)
		}
		addr := strings.ToLower(a.Address)
		if !seen[addr] {
			seen[addr] = true
			out = append(out, addr)
		}
	}
	if len(out) > MaxRecipients {
		return nil, fmt.Errorf("%w: at most %d recipients", domain.ErrInvalid, MaxRecipients)
	}
	return out, nil
}

// Webhook kinds.
const (
	KindSlack = "slack"
	KindTeams = "teams"
)

// ValidateWebhook accepts only https URLs on the hosts of the chat product, so
// the notifier can never be turned into a request to an internal address (SSRF).
func ValidateWebhook(kind, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return fmt.Errorf("%w: the webhook must be an https URL", domain.ErrInvalid)
	}
	host := strings.ToLower(u.Hostname())
	if net.ParseIP(host) != nil || (u.Port() != "" && u.Port() != "443") {
		return fmt.Errorf("%w: the webhook host is not allowed", domain.ErrInvalid)
	}
	ok := false
	switch kind {
	case KindSlack:
		ok = host == "hooks.slack.com" || host == "hooks.slack-gov.com"
	case KindTeams:
		for _, suf := range []string{".webhook.office.com", ".logic.azure.com", ".api.powerplatform.com", ".powerplatform.com"} {
			if strings.HasSuffix(host, suf) {
				ok = true
			}
		}
		ok = ok || host == "outlook.office.com"
	}
	if !ok {
		return fmt.Errorf("%w: the webhook host is not allowed for %s", domain.ErrInvalid, kind)
	}
	return nil
}

// Mask shows only where a webhook points, never the secret path.
func Mask(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	p := strings.Trim(u.Path, "/")
	tail := ""
	if len(p) > 4 {
		tail = p[len(p)-4:]
	}
	return u.Host + "/…" + tail
}

// View masks the secrets.
func (s Settings) View(emailAvailable, linksAvailable bool) SettingsView {
	if s.EmailTo == nil {
		s.EmailTo = []string{}
	}
	return SettingsView{Settings: s, EmailAvailable: emailAvailable, LinksAvailable: linksAvailable,
		SlackConfigured: s.SlackWebhook != "", SlackMasked: maskIf(s.SlackWebhook),
		TeamsConfigured: s.TeamsWebhook != "", TeamsMasked: maskIf(s.TeamsWebhook)}
}

func maskIf(w string) string {
	if w == "" {
		return ""
	}
	return Mask(w)
}

// RiskRank orders risks; unknown values rank as high (notify rather than hide).
func RiskRank(r string) int {
	switch strings.ToLower(r) {
	case "low":
		return 1
	case "medium":
		return 2
	default:
		return 3
	}
}

// SettingsStore persists Settings.
type SettingsStore interface {
	// GetSettings returns DefaultSettings when the organization has none.
	GetSettings(ctx context.Context, org string) (Settings, error)
	PutSettings(ctx context.Context, s Settings) error
}

// MemSettings is the in-memory SettingsStore.
type MemSettings struct {
	mu sync.Mutex
	m  map[string]Settings
}

// NewMemSettings returns an empty store.
func NewMemSettings() *MemSettings { return &MemSettings{m: map[string]Settings{}} }

func (m *MemSettings) GetSettings(_ context.Context, org string) (Settings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.m[org]; ok {
		return s, nil
	}
	return DefaultSettings(org), nil
}

func (m *MemSettings) PutSettings(_ context.Context, s Settings) error {
	if s.OrgID == "" {
		return errors.New("notify: settings without organization")
	}
	m.mu.Lock()
	m.m[s.OrgID] = s
	m.mu.Unlock()
	return nil
}
