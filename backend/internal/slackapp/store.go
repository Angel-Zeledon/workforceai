package slackapp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// Store errors.
var (
	ErrNotFound    = errors.New("slackapp: not found")
	ErrCodeInvalid = errors.New("slackapp: link code unknown, used or expired")
)

// Record is the stored Slack app of one organization. The two secrets are kept only sealed (vault, purposes
// sealSigning / sealBot); the store never sees them in the clear.
type Record struct {
	OrgID            string
	Enabled          bool
	SigningSecretEnc []byte
	BotTokenEnc      []byte
	TokenLast4       string
	TeamID           string // the workspace of the bot token (auth.test); inbound requests of another team are refused
	TeamName         string
	BotUserID        string
	ChannelID        string // where approval requests are posted ("" = none are posted; the slash command still works)
	MinRisk          string // low | medium | high: lowest risk posted to the channel
	Locale           string // es | en | pt
	EmailMatching    bool   // map Slack users to platform users by an e-mail verified on both sides
	HighRiskInApp    bool   // high-risk approvals (and code execution) are approved only in the app
	VerifiedAt       *time.Time
	UpdatedBy        string
	UpdatedAt        time.Time
}

// DefaultRecord is an organization without a Slack app.
func DefaultRecord(org string) Record {
	return Record{OrgID: org, MinRisk: "medium", Locale: "es", EmailMatching: true, HighRiskInApp: true}
}

// Link maps a Slack user of a workspace to a platform user of the organization.
type Link struct {
	OrgID       string    `json:"-"`
	TeamID      string    `json:"team_id"`
	SlackUserID string    `json:"slack_user_id"`
	UserID      string    `json:"user_id"`
	Method      string    `json:"method"` // code | email
	CreatedAt   time.Time `json:"created_at"`
}

// Link methods.
const (
	LinkByCode  = "code"
	LinkByEmail = "email"
)

// Store persists the channel. Every method is scoped to one organization (Row-Level Security in Postgres).
type Store interface {
	GetRecord(ctx context.Context, org string) (Record, bool, error)
	PutRecord(ctx context.Context, r Record) error
	// DeleteAll removes the app, its user links, pending codes, dedupe rows and message references (disconnect).
	DeleteAll(ctx context.Context, org string) error

	GetLink(ctx context.Context, org, team, slackUser string) (Link, error) // ErrNotFound
	LinksOf(ctx context.Context, org, userID string) ([]Link, error)
	PutLink(ctx context.Context, l Link) error // upsert on (org, team, slack user)
	DeleteLinksOf(ctx context.Context, org, userID string) (int, error)

	// CreateCode stores a one-time link code (hashed) for userID; ConsumeCode atomically uses it (ErrCodeInvalid).
	CreateCode(ctx context.Context, org, codeHash, userID string, expires time.Time) error
	ConsumeCode(ctx context.Context, org, codeHash string, now time.Time) (string, error)

	// Claim records an interaction key; false when it was already seen (a retry or a double click).
	Claim(ctx context.Context, org, key string, now time.Time) (bool, error)
	SetOutcome(ctx context.Context, org, key, outcome string) error

	// PutMessage / GetMessage remember where the request of an approval was posted (to update it later).
	PutMessage(ctx context.Context, org, approvalID, channel, ts string) error
	GetMessage(ctx context.Context, org, approvalID string) (channel, ts string, err error)
}

// MemStore is the in-memory Store (no database; lost on restart).
type MemStore struct {
	mu       sync.Mutex
	records  map[string]Record
	links    map[string]Link
	codes    map[string]memCode
	claims   map[string]string
	messages map[string][2]string
}

type memCode struct {
	user    string
	expires time.Time
	used    bool
}

// NewMemStore builds an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{records: map[string]Record{}, links: map[string]Link{}, codes: map[string]memCode{},
		claims: map[string]string{}, messages: map[string][2]string{}}
}

var _ Store = (*MemStore)(nil)

func k(parts ...string) string { return strings.Join(parts, "\x00") }

func (m *MemStore) GetRecord(_ context.Context, org string) (Record, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[org]
	if !ok {
		return DefaultRecord(org), false, nil
	}
	return r, true, nil
}

func (m *MemStore) PutRecord(_ context.Context, r Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[r.OrgID] = r
	return nil
}

func (m *MemStore) DeleteAll(_ context.Context, org string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.records, org)
	for key, l := range m.links {
		if l.OrgID == org {
			delete(m.links, key)
		}
	}
	prefix := org + "\x00"
	for key := range m.codes {
		if strings.HasPrefix(key, prefix) {
			delete(m.codes, key)
		}
	}
	for key := range m.claims {
		if strings.HasPrefix(key, prefix) {
			delete(m.claims, key)
		}
	}
	for key := range m.messages {
		if strings.HasPrefix(key, prefix) {
			delete(m.messages, key)
		}
	}
	return nil
}

func (m *MemStore) GetLink(_ context.Context, org, team, slackUser string) (Link, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.links[k(org, team, slackUser)]
	if !ok {
		return Link{}, ErrNotFound
	}
	return l, nil
}

func (m *MemStore) LinksOf(_ context.Context, org, userID string) ([]Link, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Link{}
	for _, l := range m.links {
		if l.OrgID == org && l.UserID == userID {
			out = append(out, l)
		}
	}
	return out, nil
}

func (m *MemStore) PutLink(_ context.Context, l Link) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.links[k(l.OrgID, l.TeamID, l.SlackUserID)] = l
	return nil
}

func (m *MemStore) DeleteLinksOf(_ context.Context, org, userID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for key, l := range m.links {
		if l.OrgID == org && l.UserID == userID {
			delete(m.links, key)
			n++
		}
	}
	return n, nil
}

func (m *MemStore) CreateCode(_ context.Context, org, codeHash, userID string, expires time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// One pending code per person: a new one replaces the previous.
	for key, c := range m.codes {
		if c.user == userID && strings.HasPrefix(key, org+"\x00") {
			delete(m.codes, key)
		}
	}
	m.codes[k(org, codeHash)] = memCode{user: userID, expires: expires}
	return nil
}

func (m *MemStore) ConsumeCode(_ context.Context, org, codeHash string, now time.Time) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.codes[k(org, codeHash)]
	if !ok || c.used || now.After(c.expires) {
		return "", ErrCodeInvalid
	}
	c.used = true
	m.codes[k(org, codeHash)] = c
	return c.user, nil
}

func (m *MemStore) Claim(_ context.Context, org, key string, _ time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.claims[k(org, key)]; ok {
		return false, nil
	}
	m.claims[k(org, key)] = OutcomeProcessing
	return true, nil
}

func (m *MemStore) SetOutcome(_ context.Context, org, key, outcome string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.claims[k(org, key)] = outcome
	return nil
}

// OutcomeOf returns the stored outcome of an interaction key (tests).
func (m *MemStore) OutcomeOf(org, key string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.claims[k(org, key)]
}

func (m *MemStore) PutMessage(_ context.Context, org, approvalID, channel, ts string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages[k(org, approvalID)] = [2]string{channel, ts}
	return nil
}

func (m *MemStore) GetMessage(_ context.Context, org, approvalID string) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.messages[k(org, approvalID)]
	if !ok {
		return "", "", ErrNotFound
	}
	return v[0], v[1], nil
}
