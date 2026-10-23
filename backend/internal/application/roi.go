package application

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
)

// AI payroll and return (B15, docs/architecture/ai-payroll-roi.md).
//
// The report puts the AI cost of each agent (measured: the usage ledger) next to an ESTIMATE of the human time its
// completed work would have taken. The estimate is a transparent, configurable model, never a measurement:
//
//	estimated minutes = completed tasks counted x minutes per task of the agent's role
//
// - Counted: tasks with status done that finished inside the window.
// - Excluded (and reported as such): tasks that needed rework or a retry, failed tasks and skipped tasks.
// - Minutes per task: the org's per-role value, else the org's default, else DefaultROIMinutes (an assumption the
//   report marks as such until an admin sets it).
// - Money: only when the org entered its own hourly rate. There is no default rate; without it the report is hours only.

// DefaultROIMinutes is the human-minutes assumption per completed task until an admin sets one.
const DefaultROIMinutes = 30

// Bounds of the model.
const (
	MaxROIMinutes       = 480   // per task (one working day)
	MaxROIHourlyRateUSD = 10000 // per hour
	DefaultROIWindow    = 30    // days
	MaxROIWindow        = 365   // days
	maxROIRoles         = 200
)

// ROISettings is the org's model. HourlyRateUSD nil = not set (hours only).
type ROISettings struct {
	HourlyRateUSD  *float64       `json:"hourly_rate_usd"`
	DefaultMinutes int            `json:"default_minutes"`
	RoleMinutes    map[string]int `json:"role_minutes"`
	UpdatedBy      string         `json:"updated_by,omitempty"`
	UpdatedAt      *time.Time     `json:"updated_at,omitempty"`
}

// DefaultROISettings is what an org has until an admin saves its own model.
func DefaultROISettings() ROISettings {
	return ROISettings{DefaultMinutes: DefaultROIMinutes, RoleMinutes: map[string]int{}}
}

// ROIStore persists the per-org model (memory and Postgres implement it).
type ROIStore interface {
	// GetROISettings returns the stored model; ok is false when none was ever saved.
	GetROISettings(ctx context.Context, orgID string) (s ROISettings, ok bool, err error)
	PutROISettings(ctx context.Context, orgID string, s ROISettings) error
}

// ROIMethodology travels with every report so the UI can show the assumptions next to the figures.
type ROIMethodology struct {
	Kind                 string         `json:"kind"` // always "estimate"
	WindowDays           int            `json:"window_days"`
	From                 time.Time      `json:"from"`
	To                   time.Time      `json:"to"`
	Counted              string         `json:"counted"`  // "done_in_window"
	Excluded             []string       `json:"excluded"` // rework, failed, skipped
	DefaultMinutes       int            `json:"default_minutes"`
	DefaultMinutesSource string         `json:"default_minutes_source"` // assumption | org
	RoleMinutes          map[string]int `json:"role_minutes"`
	HourlyRateUSD        *float64       `json:"hourly_rate_usd"`
	RateSource           string         `json:"rate_source"`   // org | unset
	AICostBasis          string         `json:"ai_cost_basis"` // usage_ledger
}

// ROIFigures are the numbers of one agent or of the whole org. Money fields are nil when they cannot be computed
// honestly (no hourly rate, or no estimated hours).
type ROIFigures struct {
	TasksCounted        int      `json:"tasks_counted"`
	ExcludedRework      int      `json:"excluded_rework"`
	ExcludedFailed      int      `json:"excluded_failed"`
	ExcludedSkipped     int      `json:"excluded_skipped"`
	EstMinutes          int      `json:"est_minutes"`
	EstHours            float64  `json:"est_hours"`
	AICostUSD           float64  `json:"ai_cost_usd"`
	EstHumanCostUSD     *float64 `json:"est_human_cost_usd"`
	AICostPerEstHourUSD *float64 `json:"ai_cost_per_est_hour_usd"`
}

// ROIAgent is one row of the report.
type ROIAgent struct {
	AgentID        string `json:"agent_id"`
	Role           string `json:"role"`
	MinutesPerTask int    `json:"minutes_per_task"`
	MinutesSource  string `json:"minutes_source"` // role | default | assumption
	ROIFigures
}

// ROIReport is GET /roi.
type ROIReport struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Methodology ROIMethodology `json:"methodology"`
	Org         ROIFigures     `json:"org"`
	Agents      []ROIAgent     `json:"agents"`
}

// ROIService serves the report and the model.
type ROIService struct {
	Store ROIStore
	Core  Store
	Rec   *Recorder
	Cfg   Config
	Now   func() time.Time
}

func (s *ROIService) org(ctx context.Context) string { return OrgFrom(ctx, s.Cfg.OrgID) }

func (s *ROIService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Settings returns the stored model or the defaults (saved=false).
func (s *ROIService) Settings(ctx context.Context) (ROISettings, bool, error) {
	st, ok, err := s.Store.GetROISettings(ctx, s.org(ctx))
	if err != nil {
		return ROISettings{}, false, err
	}
	if !ok {
		return DefaultROISettings(), false, nil
	}
	if st.RoleMinutes == nil {
		st.RoleMinutes = map[string]int{}
	}
	return st, true, nil
}

// PutSettings validates and stores the model (audited). A rate <= 0 or null clears it (hours only).
func (s *ROIService) PutSettings(ctx context.Context, in ROISettings) (ROISettings, error) {
	if in.DefaultMinutes < 0 || in.DefaultMinutes > MaxROIMinutes {
		return ROISettings{}, fmt.Errorf("%w: default_minutes must be between 0 and %d", domain.ErrInvalid, MaxROIMinutes)
	}
	if in.HourlyRateUSD != nil {
		r := *in.HourlyRateUSD
		switch {
		case math.IsNaN(r) || math.IsInf(r, 0) || r > MaxROIHourlyRateUSD:
			return ROISettings{}, fmt.Errorf("%w: hourly_rate_usd must be between 0 and %d", domain.ErrInvalid, MaxROIHourlyRateUSD)
		case r <= 0:
			in.HourlyRateUSD = nil
		default:
			v := math.Round(r*100) / 100
			in.HourlyRateUSD = &v
		}
	}
	if len(in.RoleMinutes) > maxROIRoles {
		return ROISettings{}, fmt.Errorf("%w: at most %d roles", domain.ErrInvalid, maxROIRoles)
	}
	clean := map[string]int{}
	for role, m := range in.RoleMinutes {
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "" || len(role) > 64 || m < 0 || m > MaxROIMinutes {
			return ROISettings{}, fmt.Errorf("%w: role minutes must be between 0 and %d", domain.ErrInvalid, MaxROIMinutes)
		}
		clean[role] = m
	}
	in.RoleMinutes = clean
	now := s.now()
	in.UpdatedAt, in.UpdatedBy = &now, ActorFrom(ctx, "user")
	if err := s.Store.PutROISettings(ctx, s.org(ctx), in); err != nil {
		return ROISettings{}, err
	}
	if s.Rec != nil {
		s.Rec.Audit(ctx, domain.AuditLog{Actor: in.UpdatedBy, Action: "roi.settings_changed", Entity: "settings", EntityID: "roi",
			Details: map[string]any{"default_minutes": in.DefaultMinutes, "roles": len(in.RoleMinutes), "hourly_rate_set": in.HourlyRateUSD != nil}})
	}
	return in, nil
}

// Report computes the estimate for the last windowDays days (clamped to 1..MaxROIWindow, 0 = DefaultROIWindow).
func (s *ROIService) Report(ctx context.Context, windowDays int) (ROIReport, error) {
	if windowDays <= 0 {
		windowDays = DefaultROIWindow
	}
	windowDays = min(windowDays, MaxROIWindow)
	st, saved, err := s.Settings(ctx)
	if err != nil {
		return ROIReport{}, err
	}
	org := s.org(ctx)
	agents, err := s.Core.ListAgents(ctx, org)
	if err != nil {
		return ROIReport{}, err
	}
	tasks, err := s.Core.ListTasks(ctx, org, "", "")
	if err != nil {
		return ROIReport{}, err
	}
	usage, err := s.Core.ListUsage(ctx, org)
	if err != nil {
		return ROIReport{}, err
	}
	to := s.now()
	return ComputeROI(st, saved, agents, tasks, usage, to.AddDate(0, 0, -windowDays), to, windowDays), nil
}

// roiExclusion classifies a finished task: "" counts; otherwise the reason it does not.
func roiExclusion(t domain.Task) string {
	if t.Status == domain.TaskFailed {
		return "failed"
	}
	if t.Output == nil {
		return ""
	}
	if sk, _ := t.Output.Metrics["skipped"].(bool); sk {
		return "skipped"
	}
	if r := t.Output.Review; r != nil && (r.Reworks > 0 || r.Verdict != domain.VerdictPass) {
		return "rework"
	}
	if metricInt(t.Output.Metrics["attempts"]) > 1 { // needed a retry (W2)
		return "rework"
	}
	return ""
}

// ComputeROI is the pure model behind GET /roi (exported for tests).
func ComputeROI(st ROISettings, saved bool, agents []domain.Agent, tasks []domain.Task, usage []domain.UsageEntry, from, to time.Time, windowDays int) ROIReport {
	defMin, defSrc := st.DefaultMinutes, "org"
	if !saved {
		defMin, defSrc = DefaultROIMinutes, "assumption"
	}
	roles := map[string]string{}
	for _, a := range agents {
		roles[a.ID] = strings.ToLower(a.Role)
	}
	rows := map[string]*ROIAgent{}
	row := func(id string) *ROIAgent {
		if r := rows[id]; r != nil {
			return r
		}
		role := roles[id]
		r := &ROIAgent{AgentID: id, Role: role, MinutesPerTask: defMin, MinutesSource: defSrc}
		if m, ok := st.RoleMinutes[role]; ok && role != "" {
			r.MinutesPerTask, r.MinutesSource = m, "role"
		} else if defSrc == "org" {
			r.MinutesSource = "default"
		}
		rows[id] = r
		return r
	}
	in := func(ts time.Time) bool { return !ts.Before(from) && ts.Before(to) }
	for _, a := range agents {
		row(a.ID)
	}
	for _, t := range tasks {
		if t.FinishedAt == nil || !in(*t.FinishedAt) || (t.Status != domain.TaskDone && t.Status != domain.TaskFailed) || t.AgentID == "" {
			continue
		}
		r := row(t.AgentID)
		switch roiExclusion(t) {
		case "failed":
			r.ExcludedFailed++
		case "skipped":
			r.ExcludedSkipped++
		case "rework":
			r.ExcludedRework++
		default:
			r.TasksCounted++
		}
	}
	for _, u := range usage {
		if u.AgentID == "" || !in(u.TS) {
			continue
		}
		row(u.AgentID).AICostUSD += u.CostUSD
	}
	rep := ROIReport{GeneratedAt: to, Agents: []ROIAgent{}, Methodology: ROIMethodology{Kind: "estimate", WindowDays: windowDays, From: from, To: to,
		Counted: "done_in_window", Excluded: []string{"rework", "failed", "skipped"}, DefaultMinutes: defMin, DefaultMinutesSource: defSrc,
		RoleMinutes: map[string]int{}, HourlyRateUSD: st.HourlyRateUSD, RateSource: "unset", AICostBasis: "usage_ledger"}}
	if st.HourlyRateUSD != nil {
		rep.Methodology.RateSource = "org"
	}
	for k, v := range st.RoleMinutes {
		rep.Methodology.RoleMinutes[k] = v
	}
	for _, r := range rows {
		r.EstMinutes = r.TasksCounted * r.MinutesPerTask
		r.finish(st.HourlyRateUSD)
		if r.TasksCounted+r.ExcludedFailed+r.ExcludedRework+r.ExcludedSkipped == 0 && r.AICostUSD == 0 {
			continue // an agent with nothing in the window is noise
		}
		o := &rep.Org
		o.TasksCounted += r.TasksCounted
		o.ExcludedRework += r.ExcludedRework
		o.ExcludedFailed += r.ExcludedFailed
		o.ExcludedSkipped += r.ExcludedSkipped
		o.EstMinutes += r.EstMinutes
		o.AICostUSD += r.AICostUSD
		rep.Agents = append(rep.Agents, *r)
	}
	rep.Org.finish(st.HourlyRateUSD)
	sort.Slice(rep.Agents, func(i, j int) bool {
		if rep.Agents[i].EstMinutes != rep.Agents[j].EstMinutes {
			return rep.Agents[i].EstMinutes > rep.Agents[j].EstMinutes
		}
		return rep.Agents[i].AgentID < rep.Agents[j].AgentID
	})
	return rep
}

// finish derives hours and the money comparisons from minutes and cost.
func (f *ROIFigures) finish(rate *float64) {
	f.EstHours = math.Round(float64(f.EstMinutes)/60*100) / 100
	f.AICostUSD = round6(f.AICostUSD)
	f.EstHumanCostUSD, f.AICostPerEstHourUSD = nil, nil
	if rate != nil && f.EstMinutes > 0 {
		v := math.Round(float64(f.EstMinutes)/60**rate*100) / 100
		f.EstHumanCostUSD = &v
	}
	if f.EstMinutes > 0 {
		v := round6(f.AICostUSD / (float64(f.EstMinutes) / 60))
		f.AICostPerEstHourUSD = &v
	}
}
