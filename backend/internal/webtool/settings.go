package webtool

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Tool and action names (the agent grant is the tool name "web").
const (
	ToolName     = "web"
	ActionSearch = "search"
	ActionFetch  = "fetch"

	ModeOpen      = "open"
	ModeAllowlist = "allowlist"

	DefaultMaxCallsPerTask = 10
	DefaultMaxCallsPerDay  = 100
	HardMaxCallsPerTask    = 50
	maxDomains             = 200
)

// Settings is the per-organization configuration of the web tool. The zero value is OFF.
type Settings struct {
	Enabled         bool      `json:"enabled"`
	Mode            string    `json:"mode"` // open | allowlist
	AllowedDomains  []string  `json:"allowed_domains"`
	BlockedDomains  []string  `json:"blocked_domains"`
	MaxCallsPerTask int       `json:"max_calls_per_task"`
	MaxCallsPerDay  int       `json:"max_calls_per_day"`
	UpdatedBy       string    `json:"updated_by,omitempty"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
}

var reDomain = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// NormalizeDomain accepts "Example.com", "https://example.com/x", "*.example.com" and returns "example.com".
func NormalizeDomain(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(strings.TrimPrefix(s, "*."), ".")
	s = strings.TrimSuffix(s, ".")
	if _, err := netip.ParseAddr(s); err == nil || !reDomain.MatchString(s) || len(s) > 253 {
		return "", fmt.Errorf("%w: invalid domain %q", domain.ErrInvalid, s)
	}
	return s, nil
}

func normalizeList(in []string) ([]string, error) {
	if len(in) > maxDomains {
		return nil, fmt.Errorf("%w: at most %d domains per list", domain.ErrInvalid, maxDomains)
	}
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if strings.TrimSpace(s) == "" {
			continue
		}
		d, err := NormalizeDomain(s)
		if err != nil {
			return nil, err
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Normalize validates and canonicalizes settings coming from the API.
func (st Settings) Normalize() (Settings, error) {
	switch st.Mode {
	case "":
		st.Mode = ModeAllowlist
	case ModeOpen, ModeAllowlist:
	default:
		return st, fmt.Errorf("%w: mode must be open or allowlist", domain.ErrInvalid)
	}
	var err error
	if st.AllowedDomains, err = normalizeList(st.AllowedDomains); err != nil {
		return st, err
	}
	if st.BlockedDomains, err = normalizeList(st.BlockedDomains); err != nil {
		return st, err
	}
	if st.MaxCallsPerTask < 0 || st.MaxCallsPerTask > HardMaxCallsPerTask || st.MaxCallsPerDay < 0 || st.MaxCallsPerDay > 100000 {
		return st, fmt.Errorf("%w: invalid limits", domain.ErrInvalid)
	}
	return st, nil
}

// MatchesDomain reports whether host is one of the domains or a subdomain of one.
func MatchesDomain(host string, list []string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, d := range list {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// HostAllowed applies, in order: the blocked list (always wins), the operator allow-list (platform), and the
// organization mode.
func (st Settings) HostAllowed(host string, operator []string) error {
	if MatchesDomain(host, st.BlockedDomains) {
		return ErrDomainPolicy
	}
	if len(operator) > 0 && !MatchesDomain(host, operator) {
		return ErrDomainPolicy
	}
	if st.Mode != ModeOpen && !MatchesDomain(host, st.AllowedDomains) {
		return ErrDomainPolicy
	}
	return nil
}

// Call is one recorded use of the tool (query or URL only, never content): the quota ledger and the usage view.
type Call struct {
	ID, AgentID, TaskID string
	Action              string
	Target              string
	Status              string
	At                  time.Time
}

// Store persists settings and the call ledger. Every method is org-scoped.
type Store interface {
	GetSettings(ctx context.Context, org string) (Settings, error)
	PutSettings(ctx context.Context, org string, s Settings) error
	RecordCall(ctx context.Context, org string, c Call) error
	CountSince(ctx context.Context, org string, since time.Time) (search, fetch int, err error)
	CountTask(ctx context.Context, org, taskID string) (int, error)
}

// MemoryStore is the in-memory Store (tests, no database).
type MemoryStore struct {
	mu       sync.Mutex
	settings map[string]Settings
	calls    map[string][]Call
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{settings: map[string]Settings{}, calls: map[string][]Call{}}
}

func (m *MemoryStore) GetSettings(_ context.Context, org string) (Settings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings[org], nil
}

func (m *MemoryStore) PutSettings(_ context.Context, org string, s Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings[org] = s
	return nil
}

func (m *MemoryStore) RecordCall(_ context.Context, org string, c Call) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls[org] = append(m.calls[org], c)
	return nil
}

func (m *MemoryStore) CountSince(_ context.Context, org string, since time.Time) (int, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, f := 0, 0
	for _, c := range m.calls[org] {
		if c.At.Before(since) {
			continue
		}
		if c.Action == ActionSearch {
			s++
		} else {
			f++
		}
	}
	return s, f, nil
}

func (m *MemoryStore) CountTask(_ context.Context, org, taskID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, c := range m.calls[org] {
		if c.TaskID == taskID {
			n++
		}
	}
	return n, nil
}
