package application

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	_ "time/tzdata" // IANA zones for schedules, independent of the host image

	"github.com/google/uuid"

	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/domain"
)

// MaxSchedulesPerOrg bounds simple recurring tasks per organization.
const MaxSchedulesPerOrg = 20

// staleRunWindow: a schedule found overdue by more than this (server was
// down) is advanced without running, so a morning briefing never fires at night.
const staleRunWindow = 6 * time.Hour

// OrgConfig implements organization onboarding (business-type packs), the
// per-org language/regional-tone settings, workflow-template instantiation and
// simple recurring tasks (daily briefing). It also implements StyleProvider.
type OrgConfig struct {
	Cfg   Config
	Store ConfigStore
	Core  Store
	Orch  *Orchestrator
	Rec   *Recorder
	Cat   *catalog.Catalog
	Log   *slog.Logger
	Now   func() time.Time
}

var _ StyleProvider = (*OrgConfig)(nil)

func (s *OrgConfig) org(ctx context.Context) string { return OrgFrom(ctx, s.Cfg.OrgID) }

func (s *OrgConfig) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *OrgConfig) cat() *catalog.Catalog {
	if s.Cat != nil {
		return s.Cat
	}
	return catalog.Default()
}

// ---- settings ----

// Settings returns the organization settings including per-agent tones.
func (s *OrgConfig) Settings(ctx context.Context) (OrgSettings, error) {
	org := s.org(ctx)
	st, err := s.Store.GetOrgSettings(ctx, org)
	if err != nil {
		return OrgSettings{}, err
	}
	tones, err := s.Store.ListAgentTones(ctx, org)
	if err != nil {
		return OrgSettings{}, err
	}
	st.AgentTones = tones
	if st.AgentTones == nil {
		st.AgentTones = map[string]string{}
	}
	st.Recommendation = []string{}
	if p, ok := s.cat().Pack(st.BusinessType, st.Locale); ok {
		st.Recommendation = p.Templates
	}
	if st.Rules.AlwaysApprove == nil {
		st.Rules.AlwaysApprove = []string{}
	}
	return st, nil
}

// SettingsUpdate is a partial update; nil fields are left unchanged.
type SettingsUpdate struct {
	Locale *string `json:"locale"`
	Tone   *string `json:"tone"`
}

// UpdateSettings changes the organization language and/or regional tone.
func (s *OrgConfig) UpdateSettings(ctx context.Context, in SettingsUpdate) (OrgSettings, error) {
	org := s.org(ctx)
	st, err := s.Store.GetOrgSettings(ctx, org)
	if err != nil {
		return OrgSettings{}, err
	}
	if in.Locale != nil {
		loc, err := validLocale(*in.Locale)
		if err != nil {
			return OrgSettings{}, err
		}
		st.Locale = loc
	}
	if in.Tone != nil {
		t, ok := catalog.NormalizeTone(*in.Tone)
		if !ok {
			return OrgSettings{}, fmt.Errorf("%w: unknown tone %q", domain.ErrInvalid, *in.Tone)
		}
		st.Tone = t
	}
	st.Configured = true
	if err := s.Store.PutOrgSettings(ctx, org, st); err != nil {
		return OrgSettings{}, err
	}
	s.Rec.Audit(ctx, domain.AuditLog{Actor: ActorFrom(ctx, "system"), Action: "org.settings_updated", Entity: "org", EntityID: org,
		Details: map[string]any{"locale": st.Locale, "tone": st.Tone}})
	return s.Settings(ctx)
}

// SetAgentTone sets (or clears with "") the regional tone of one agent.
func (s *OrgConfig) SetAgentTone(ctx context.Context, agentID, tone string) error {
	org := s.org(ctx)
	if _, err := s.Core.GetAgent(ctx, org, agentID); err != nil {
		return err
	}
	t := strings.ToLower(strings.TrimSpace(tone))
	if t != "" {
		var ok bool
		if t, ok = catalog.NormalizeTone(t); !ok {
			return fmt.Errorf("%w: unknown tone %q", domain.ErrInvalid, tone)
		}
	}
	if err := s.Store.SetAgentTone(ctx, org, agentID, t); err != nil {
		return err
	}
	s.Rec.Audit(ctx, domain.AuditLog{Actor: ActorFrom(ctx, "system"), Action: "agent.tone_set", Entity: "agent", EntityID: agentID,
		Details: map[string]any{"tone": t}})
	return nil
}

func validLocale(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "es":
		return "es", nil
	case "en":
		return "en", nil
	}
	return "", fmt.Errorf("%w: unsupported locale %q", domain.ErrInvalid, v)
}

// Style implements StyleProvider. An organization that configured nothing
// yields an empty RunStyle, so runtime payloads stay exactly as before.
func (s *OrgConfig) Style(ctx context.Context) (RunStyle, error) {
	org := s.org(ctx)
	st, err := s.Store.GetOrgSettings(ctx, org)
	if err != nil {
		return RunStyle{}, err
	}
	tones, err := s.Store.ListAgentTones(ctx, org)
	if err != nil {
		return RunStyle{}, err
	}
	out := RunStyle{AgentTones: tones}
	if st.Configured {
		out.Locale = st.Locale
		if st.Tone != "neutral" {
			out.Tone = st.Tone
		}
	}
	return out, nil
}

// ---- onboarding ----

// BriefingInput configures the daily briefing created by onboarding.
type BriefingInput struct {
	Enabled  bool   `json:"enabled"`
	Hour     int    `json:"hour"`
	Minute   int    `json:"minute"`
	Timezone string `json:"timezone"`
	Weekdays []int  `json:"weekdays"`
}

// OnboardInput is the body of the first-run wizard.
type OnboardInput struct {
	PackKey  string         `json:"pack_key"`
	Locale   string         `json:"locale"`
	Tone     string         `json:"tone"`
	Briefing *BriefingInput `json:"briefing"`
	// Force re-applies a pack on an organization that already completed onboarding.
	Force bool `json:"force"`
}

// OnboardResult summarizes what the pack seeded.
type OnboardResult struct {
	Settings         OrgSettings `json:"settings"`
	AgentsConfigured []string    `json:"agents_configured"`
	MemorySeeded     int         `json:"memory_seeded"`
	Schedule         *Schedule   `json:"schedule"`
}

// Onboard applies a business-type pack: autonomy per agent, initial memory,
// seeded approval rules and (optionally) the daily briefing. Idempotent per
// pack: memory is upserted by key and the briefing schedule is replaced.
func (s *OrgConfig) Onboard(ctx context.Context, in OnboardInput) (OnboardResult, error) {
	org := s.org(ctx)
	cur, err := s.Store.GetOrgSettings(ctx, org)
	if err != nil {
		return OnboardResult{}, err
	}
	if cur.Onboarded && !in.Force {
		return OnboardResult{}, fmt.Errorf("%w: onboarding already completed", domain.ErrConflict)
	}
	locale := cur.Locale
	if in.Locale != "" {
		if locale, err = validLocale(in.Locale); err != nil {
			return OnboardResult{}, err
		}
	}
	tone := cur.Tone
	if in.Tone != "" {
		var ok bool
		if tone, ok = catalog.NormalizeTone(in.Tone); !ok {
			return OnboardResult{}, fmt.Errorf("%w: unknown tone %q", domain.ErrInvalid, in.Tone)
		}
	}
	pack, ok := s.cat().Pack(in.PackKey, locale)
	if !ok {
		return OnboardResult{}, fmt.Errorf("%w: unknown pack %q", domain.ErrNotFound, in.PackKey)
	}
	var sched *Schedule
	if in.Briefing != nil && in.Briefing.Enabled {
		sc, err := s.buildSchedule(ctx, "daily_briefing", nil, in.Briefing.Hour, in.Briefing.Minute, in.Briefing.Timezone, in.Briefing.Weekdays)
		if err != nil {
			return OnboardResult{}, err
		}
		sched = &sc
	}

	agents, err := s.Core.ListAgents(ctx, org)
	if err != nil {
		return OnboardResult{}, err
	}
	have := map[string]bool{}
	for _, a := range agents {
		have[a.ID] = true
	}
	res := OnboardResult{AgentsConfigured: []string{}}
	for _, a := range pack.Agents {
		if !have[a.ID] {
			continue
		}
		if err := s.Store.SetAgentAutonomy(ctx, org, a.ID, a.Autonomy); err != nil {
			return OnboardResult{}, err
		}
		res.AgentsConfigured = append(res.AgentsConfigured, a.ID)
	}
	for _, m := range pack.Memory {
		if !have[m.AgentID] {
			continue
		}
		if err := s.Core.SetMemory(ctx, org, m.AgentID, domain.Memory{Key: m.Key, Value: m.Value, Scope: m.Scope}); err != nil {
			return OnboardResult{}, err
		}
		res.MemorySeeded++
	}
	now := s.now()
	cur.Configured, cur.Locale, cur.Tone = true, locale, tone
	cur.BusinessType, cur.PackVersion = pack.Key, pack.Version
	rules := pack.Rules
	rules.Governance = cur.Rules.Governance // a pack seeds the basics; it never erases governance an owner configured
	cur.Onboarded, cur.OnboardedAt, cur.Rules = true, &now, rules
	if err := s.Store.PutOrgSettings(ctx, org, cur); err != nil {
		return OnboardResult{}, err
	}
	if sched != nil {
		if err := s.replaceSchedule(ctx, *sched); err != nil {
			return OnboardResult{}, err
		}
		res.Schedule = sched
	}
	s.Rec.Audit(ctx, domain.AuditLog{Actor: ActorFrom(ctx, "system"), Action: "onboarding.completed", Entity: "org", EntityID: org,
		Details: map[string]any{"pack": pack.Key, "version": pack.Version, "locale": locale, "tone": tone,
			"agents": res.AgentsConfigured, "memory_seeded": res.MemorySeeded, "briefing": sched != nil}})
	if res.Settings, err = s.Settings(ctx); err != nil {
		return OnboardResult{}, err
	}
	return res, nil
}

// ---- workflow templates ----

// InstantiateTemplate runs a workflow template with parameters as a normal
// request (same orchestration, approvals and report as a typed request).
// locale overrides the organization language for the generated task texts.
func (s *OrgConfig) InstantiateTemplate(ctx context.Context, key string, params map[string]string, locale string) (string, error) {
	if locale == "" {
		st, err := s.Store.GetOrgSettings(ctx, s.org(ctx))
		if err != nil {
			return "", err
		}
		locale = st.Locale
	}
	plan, err := s.cat().Instantiate(key, params, locale)
	if err != nil {
		return "", err
	}
	return s.Orch.SubmitPlan(ctx, plan.Text, planResponse(plan))
}

func planResponse(p catalog.Plan) PlanResponse {
	out := PlanResponse{Objectives: p.Objectives, ClarifyingQuestions: []string{}}
	for _, t := range p.Tasks {
		out.Tasks = append(out.Tasks, PlannedTask{Key: t.Key, Title: t.Title, Description: t.Description, AgentID: t.AgentID, DependsOn: t.DependsOn})
	}
	return out
}

// ---- schedules ----

// ScheduleInput creates or updates a simple recurring task.
type ScheduleInput struct {
	TemplateKey string            `json:"template_key"`
	Params      map[string]string `json:"params"`
	Hour        int               `json:"hour"`
	Minute      int               `json:"minute"`
	Timezone    string            `json:"timezone"`
	Weekdays    []int             `json:"weekdays"`
	Enabled     *bool             `json:"enabled"`
}

// NextRun returns the first run strictly after `after` at hour:minute in the
// schedule time zone on an allowed weekday (empty = every day).
func NextRun(after time.Time, hour, minute int, tz string, weekdays []int) (time.Time, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: unknown time zone %q", domain.ErrInvalid, tz)
	}
	local := after.In(loc)
	for d := 0; d <= 8; d++ {
		day := local.AddDate(0, 0, d)
		cand := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)
		if !cand.After(after) {
			continue
		}
		if len(weekdays) > 0 && !slices.Contains(weekdays, int(cand.Weekday())) {
			continue
		}
		return cand.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("%w: schedule never runs", domain.ErrInvalid)
}

func (s *OrgConfig) buildSchedule(ctx context.Context, tpl string, params map[string]string, hour, minute int, tz string, weekdays []int) (Schedule, error) {
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return Schedule{}, fmt.Errorf("%w: invalid time %02d:%02d", domain.ErrInvalid, hour, minute)
	}
	if tz == "" {
		tz = "UTC"
	}
	seen := map[int]bool{}
	for _, w := range weekdays {
		if w < 0 || w > 6 || seen[w] {
			return Schedule{}, fmt.Errorf("%w: invalid weekdays", domain.ErrInvalid)
		}
		seen[w] = true
	}
	slices.Sort(weekdays)
	if weekdays == nil {
		weekdays = []int{}
	}
	if params == nil {
		params = map[string]string{}
	}
	// The template must be instantiable as is (no missing required params).
	if _, err := s.cat().Instantiate(tpl, params, "es"); err != nil {
		return Schedule{}, err
	}
	next, err := NextRun(s.now(), hour, minute, tz, weekdays)
	if err != nil {
		return Schedule{}, err
	}
	return Schedule{ID: uuid.NewString(), TemplateKey: tpl, Params: params, Hour: hour, Minute: minute,
		Timezone: tz, Weekdays: weekdays, Enabled: true, NextRunAt: &next, CreatedAt: s.now()}, nil
}

// replaceSchedule keeps a single schedule per template: it removes previous
// ones of the same template and stores the new one.
func (s *OrgConfig) replaceSchedule(ctx context.Context, sc Schedule) error {
	org := s.org(ctx)
	old, err := s.Store.ListSchedules(ctx, org)
	if err != nil {
		return err
	}
	for _, o := range old {
		if o.TemplateKey == sc.TemplateKey {
			if err := s.Store.DeleteSchedule(ctx, org, o.ID); err != nil {
				return err
			}
		}
	}
	return s.Store.PutSchedule(ctx, org, sc)
}

func (s *OrgConfig) ListSchedules(ctx context.Context) ([]Schedule, error) {
	return s.Store.ListSchedules(ctx, s.org(ctx))
}

func (s *OrgConfig) CreateSchedule(ctx context.Context, in ScheduleInput) (Schedule, error) {
	org := s.org(ctx)
	existing, err := s.Store.ListSchedules(ctx, org)
	if err != nil {
		return Schedule{}, err
	}
	if len(existing) >= MaxSchedulesPerOrg {
		return Schedule{}, fmt.Errorf("%w: schedule limit reached (%d)", domain.ErrConflict, MaxSchedulesPerOrg)
	}
	tpl := in.TemplateKey
	if tpl == "" {
		tpl = "daily_briefing"
	}
	sc, err := s.buildSchedule(ctx, tpl, in.Params, in.Hour, in.Minute, in.Timezone, in.Weekdays)
	if err != nil {
		return Schedule{}, err
	}
	if in.Enabled != nil && !*in.Enabled {
		sc.Enabled, sc.NextRunAt = false, nil
	}
	if err := s.Store.PutSchedule(ctx, org, sc); err != nil {
		return Schedule{}, err
	}
	s.Rec.Audit(ctx, domain.AuditLog{Actor: ActorFrom(ctx, "system"), Action: "schedule.created", Entity: "schedule", EntityID: sc.ID,
		Details: map[string]any{"template": sc.TemplateKey, "hour": sc.Hour, "minute": sc.Minute, "timezone": sc.Timezone}})
	return sc, nil
}

// UpdateSchedule replaces time, zone, weekdays and enabled flag of a schedule.
func (s *OrgConfig) UpdateSchedule(ctx context.Context, id string, in ScheduleInput) (Schedule, error) {
	org := s.org(ctx)
	cur, err := s.Store.GetSchedule(ctx, org, id)
	if err != nil {
		return Schedule{}, err
	}
	sc, err := s.buildSchedule(ctx, cur.TemplateKey, cur.Params, in.Hour, in.Minute, in.Timezone, in.Weekdays)
	if err != nil {
		return Schedule{}, err
	}
	sc.ID, sc.CreatedAt, sc.LastRunAt = cur.ID, cur.CreatedAt, cur.LastRunAt
	sc.Enabled = cur.Enabled
	if in.Enabled != nil {
		sc.Enabled = *in.Enabled
	}
	if !sc.Enabled {
		sc.NextRunAt = nil
	}
	if err := s.Store.PutSchedule(ctx, org, sc); err != nil {
		return Schedule{}, err
	}
	s.Rec.Audit(ctx, domain.AuditLog{Actor: ActorFrom(ctx, "system"), Action: "schedule.updated", Entity: "schedule", EntityID: sc.ID,
		Details: map[string]any{"enabled": sc.Enabled, "hour": sc.Hour, "minute": sc.Minute, "timezone": sc.Timezone}})
	return sc, nil
}

func (s *OrgConfig) DeleteSchedule(ctx context.Context, id string) error {
	if err := s.Store.DeleteSchedule(ctx, s.org(ctx), id); err != nil {
		return err
	}
	s.Rec.Audit(ctx, domain.AuditLog{Actor: ActorFrom(ctx, "system"), Action: "schedule.deleted", Entity: "schedule", EntityID: id})
	return nil
}

// RunDue visits the given organizations and runs every enabled schedule whose
// next_run_at has passed. Multi-instance safe: the run is claimed with an
// atomic compare-and-set of next_run_at, so each occurrence runs at most once.
func (s *OrgConfig) RunDue(ctx context.Context, orgs []string) int {
	now := s.now()
	ran := 0
	for _, org := range orgs {
		octx := WithOrg(ctx, org)
		list, err := s.Store.ListSchedules(octx, org)
		if err != nil {
			s.Log.Warn("scheduler: list schedules", "org", org, "err", err)
			continue
		}
		for _, sc := range list {
			if !sc.Enabled || sc.NextRunAt == nil || sc.NextRunAt.After(now) {
				continue
			}
			next, err := NextRun(now, sc.Hour, sc.Minute, sc.Timezone, sc.Weekdays)
			if err != nil {
				s.Log.Warn("scheduler: next run", "schedule", sc.ID, "err", err)
				continue
			}
			claimed, err := s.Store.ClaimSchedule(octx, org, sc.ID, *sc.NextRunAt, next, now)
			if err != nil || !claimed {
				if err != nil {
					s.Log.Warn("scheduler: claim", "schedule", sc.ID, "err", err)
				}
				continue
			}
			if now.Sub(*sc.NextRunAt) > staleRunWindow {
				s.Rec.Audit(octx, domain.AuditLog{Actor: "scheduler", Action: "schedule.skipped_stale", Entity: "schedule", EntityID: sc.ID})
				continue
			}
			id, err := s.runSchedule(octx, sc)
			if err != nil {
				s.Log.Warn("scheduler: run", "schedule", sc.ID, "err", err)
				s.Rec.Audit(octx, domain.AuditLog{Actor: "scheduler", Action: "schedule.failed", Entity: "schedule", EntityID: sc.ID,
					Details: map[string]any{"error": err.Error()}})
				continue
			}
			s.Rec.Audit(octx, domain.AuditLog{Actor: "scheduler", Action: "schedule.ran", Entity: "schedule", EntityID: sc.ID,
				Details: map[string]any{"request_id": id, "template": sc.TemplateKey}})
			ran++
		}
	}
	return ran
}

func (s *OrgConfig) runSchedule(ctx context.Context, sc Schedule) (string, error) {
	return s.InstantiateTemplate(WithWorkPriority(WithActor(ctx, "scheduler"), PrioritySchedule), sc.TemplateKey, sc.Params, "")
}

// StartScheduler polls for due schedules until ctx is cancelled. lister may be
// nil (only the default organization is visited).
func (s *OrgConfig) StartScheduler(ctx context.Context, every time.Duration, lister OrgLister) {
	if every <= 0 {
		every = 30 * time.Second
	}
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				orgs := []string{s.Cfg.OrgID}
				if lister != nil {
					ids, err := lister.ListOrgIDs(ctx)
					if err != nil {
						s.Log.Warn("scheduler: list orgs", "err", err)
					} else {
						for _, id := range ids {
							if !slices.Contains(orgs, id) {
								orgs = append(orgs, id)
							}
						}
					}
				}
				s.RunDue(ctx, orgs)
			}
		}
	}()
}

// Catalog exposes the template catalog used by this service.
func (s *OrgConfig) Catalog() *catalog.Catalog { return s.cat() }

// Locale resolves the language for a request: the explicit value when valid,
// else the organization language, else Spanish.
func (s *OrgConfig) Locale(ctx context.Context, explicit string) string {
	if explicit != "" {
		return catalog.NormalizeLocale(explicit)
	}
	if st, err := s.Store.GetOrgSettings(ctx, s.org(ctx)); err == nil && st.Locale != "" {
		return st.Locale
	}
	return "es"
}
