package application

import (
	"context"
	"time"

	"aiworkforce/backend/internal/catalog"
)

// OrgSettings is the per-organization configuration: language, regional tone,
// the business type chosen in the first-run wizard and the seeded rules.
// The zero value (nothing configured) is valid and keeps legacy behaviour.
type OrgSettings struct {
	// Configured is true once the organization saved settings (false = legacy defaults).
	Configured     bool              `json:"configured"`
	Locale         string            `json:"locale"` // "es" (default) | "en"
	Tone           string            `json:"tone"`   // neutral|mx|co|ar|cl|es
	BusinessType   string            `json:"business_type"`
	PackVersion    int               `json:"pack_version"`
	Onboarded      bool              `json:"onboarding_completed"`
	OnboardedAt    *time.Time        `json:"onboarding_completed_at"`
	Rules          catalog.PackRules `json:"rules"`
	AgentTones     map[string]string `json:"agent_tones"`
	Recommendation []string          `json:"recommended_templates"`
}

// DefaultOrgSettings are the settings of an organization that configured nothing.
func DefaultOrgSettings() OrgSettings {
	return OrgSettings{Locale: "es", Tone: "neutral", Rules: catalog.PackRules{AlwaysApprove: []string{}},
		AgentTones: map[string]string{}, Recommendation: []string{}}
}

// Schedule is a simple recurring task: "run template X every day (or on the
// given weekdays) at HH:MM in a time zone". Used for the daily briefing.
type Schedule struct {
	ID          string            `json:"id"`
	TemplateKey string            `json:"template_key"`
	Params      map[string]string `json:"params"`
	Hour        int               `json:"hour"`
	Minute      int               `json:"minute"`
	Timezone    string            `json:"timezone"`
	Weekdays    []int             `json:"weekdays"` // 0=Sunday..6=Saturday; empty = every day
	Enabled     bool              `json:"enabled"`
	NextRunAt   *time.Time        `json:"next_run_at"`
	LastRunAt   *time.Time        `json:"last_run_at"`
	CreatedAt   time.Time         `json:"created_at"`
}

// ConfigStore is the persistence port of organization configuration. It is a
// separate port (not part of Store) so that it can evolve independently; both
// the memory and the Postgres stores implement it. Every method is org-scoped
// and, in Postgres, runs under Row-Level Security.
type ConfigStore interface {
	GetOrgSettings(ctx context.Context, orgID string) (OrgSettings, error)
	PutOrgSettings(ctx context.Context, orgID string, s OrgSettings) error
	ListAgentTones(ctx context.Context, orgID string) (map[string]string, error)
	// SetAgentTone stores a per-agent tone override; "" removes it.
	SetAgentTone(ctx context.Context, orgID, agentID, tone string) error
	// SetAgentAutonomy updates agents.autonomy (suggest|approve_each|rules|autonomous).
	SetAgentAutonomy(ctx context.Context, orgID, agentID, autonomy string) error

	ListSchedules(ctx context.Context, orgID string) ([]Schedule, error)
	GetSchedule(ctx context.Context, orgID, id string) (Schedule, error)
	PutSchedule(ctx context.Context, orgID string, s Schedule) error
	DeleteSchedule(ctx context.Context, orgID, id string) error
	// ClaimSchedule atomically moves next_run_at from expected to next and
	// records the run. It returns false when another instance claimed it first.
	ClaimSchedule(ctx context.Context, orgID, id string, expected, next, ranAt time.Time) (bool, error)
}

// OrgLister lists the organizations the background scheduler must visit.
type OrgLister interface {
	ListOrgIDs(ctx context.Context) ([]string, error)
}

// RunStyle is the language and regional tone applied to the runtime calls of
// one request. Empty values are omitted from the runtime payload.
type RunStyle struct {
	Locale     string
	Tone       string
	AgentTones map[string]string
}

// ToneFor returns the tone of an agent: its own override, else the org tone.
func (s RunStyle) ToneFor(agentID string) string {
	if t := s.AgentTones[agentID]; t != "" {
		return t
	}
	return s.Tone
}

// StyleProvider resolves the RunStyle of an organization.
type StyleProvider interface {
	Style(ctx context.Context) (RunStyle, error)
}

// SetStyle installs the optional style provider (locale and regional tone).
func (o *Orchestrator) SetStyle(p StyleProvider) { o.style = p }

func (o *Orchestrator) loadStyle(ctx context.Context) RunStyle {
	if o.style == nil {
		return RunStyle{}
	}
	st, err := o.style.Style(ctx)
	if err != nil {
		o.log.Warn("load org style", "err", err)
		return RunStyle{}
	}
	return st
}
