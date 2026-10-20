package quality

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Signals gives the raw counts per agent for each range, from data the platform already records.
type Signals interface {
	Collect(ctx context.Context, org string, ranges []Range) ([]map[string]Counts, error)
}

// Config wires the service.
type Config struct {
	Store   Store
	Signals Signals
	OrgID   string
	// Agents lists the agents of the organization (the ranking covers all of them).
	Agents func(ctx context.Context, org string) ([]domain.Agent, error)
	// Audit records every write (metadata only, never the case input or an output). nil = none.
	Audit func(ctx context.Context, org, actor, action, entityID string, details map[string]any)
	Now   func() time.Time
	Log   *slog.Logger
}

// Service is the quality score and golden case use-case layer.
type Service struct{ cfg Config }

// New builds the service.
func New(cfg Config) *Service {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Service{cfg: cfg}
}

func (s *Service) org(ctx context.Context) string { return application.OrgFrom(ctx, s.cfg.OrgID) }

func (s *Service) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now().UTC()
	}
	return time.Now().UTC()
}

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func (s *Service) audit(ctx context.Context, org, actor, action, id string, d map[string]any) {
	if s.cfg.Audit != nil {
		s.cfg.Audit(ctx, org, actor, action, id, d)
	}
}

// AgentQuality is the score of one agent.
type AgentQuality struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
	Role    string `json:"role"`
	Result
	Trend      string `json:"trend"`
	TrendDelta *int   `json:"trend_delta"` // current minus previous score; nil when unknown
	Previous   *int   `json:"previous_score"`
	Counts     Counts `json:"counts"`
	GoldenSet  int    `json:"golden_cases"` // active golden cases defined for the agent
}

// Report is the response of the quality endpoints: the methodology always travels with the numbers.
type Report struct {
	Methodology Methodology    `json:"methodology"`
	GeneratedAt time.Time      `json:"generated_at"`
	Agents      []AgentQuality `json:"agents"`
}

// Overview scores every agent of the organization, best first. Agents without a score come last
// (alphabetically), so the ranking never orders by data that is not there.
func (s *Service) Overview(ctx context.Context, windowDays int) (Report, error) {
	days := ClampWindow(windowDays)
	org, now := s.org(ctx), s.now()
	agents, err := s.cfg.Agents(ctx, org)
	if err != nil {
		return Report{}, err
	}
	cur, prev := Ranges(now, days)
	sig, err := s.cfg.Signals.Collect(ctx, org, []Range{cur, prev})
	if err != nil {
		return Report{}, err
	}
	gc, err := s.cfg.Store.GoldenCounts(ctx, org, cur.From, cur.To)
	if err != nil {
		return Report{}, err
	}
	gp, err := s.cfg.Store.GoldenCounts(ctx, org, prev.From, prev.To)
	if err != nil {
		return Report{}, err
	}
	cases, err := s.cfg.Store.ListCases(ctx, org, "")
	if err != nil {
		return Report{}, err
	}
	active := map[string]int{}
	for _, c := range cases {
		if c.Active {
			active[c.AgentID]++
		}
	}
	rep := Report{Methodology: Describe(days), GeneratedAt: now, Agents: make([]AgentQuality, 0, len(agents))}
	for _, a := range agents {
		cc, pc := sig[0][a.ID], sig[1][a.ID]
		cc.GoldenRuns, cc.GoldenPassed = gc[a.ID].Runs, gc[a.ID].Passed
		pc.GoldenRuns, pc.GoldenPassed = gp[a.ID].Runs, gp[a.ID].Passed
		r, pr := Compute(cc), Compute(pc)
		q := AgentQuality{AgentID: a.ID, Name: a.Name, Role: a.Role, Result: r, Counts: cc, GoldenSet: active[a.ID], Previous: pr.Score}
		q.Trend, q.TrendDelta = Trend(r, pr)
		rep.Agents = append(rep.Agents, q)
	}
	sort.SliceStable(rep.Agents, func(i, j int) bool {
		a, b := rep.Agents[i], rep.Agents[j]
		switch {
		case a.Score != nil && b.Score != nil && *a.Score != *b.Score:
			return *a.Score > *b.Score
		case (a.Score != nil) != (b.Score != nil):
			return a.Score != nil
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return rep, nil
}

// ForAgent scores one agent (the report holds a single entry); unknown agents are not found.
func (s *Service) ForAgent(ctx context.Context, agentID string, windowDays int) (Report, error) {
	rep, err := s.Overview(ctx, windowDays)
	if err != nil {
		return Report{}, err
	}
	for _, a := range rep.Agents {
		if a.AgentID == agentID {
			rep.Agents = []AgentQuality{a}
			return rep, nil
		}
	}
	return Report{}, fmt.Errorf("%w: agent", domain.ErrNotFound)
}

func (s *Service) requireAgent(ctx context.Context, org, agentID string) error {
	agents, err := s.cfg.Agents(ctx, org)
	if err != nil {
		return err
	}
	for _, a := range agents {
		if a.ID == agentID {
			return nil
		}
	}
	return fmt.Errorf("%w: agent", domain.ErrNotFound)
}

// ListCases returns the golden cases of an agent.
func (s *Service) ListCases(ctx context.Context, agentID string) ([]Case, error) {
	org := s.org(ctx)
	if err := s.requireAgent(ctx, org, agentID); err != nil {
		return nil, err
	}
	return s.cfg.Store.ListCases(ctx, org, agentID)
}

// CreateCase defines a golden case for an agent.
func (s *Service) CreateCase(ctx context.Context, actor, agentID string, in CaseInput) (Case, error) {
	org := s.org(ctx)
	if err := s.requireAgent(ctx, org, agentID); err != nil {
		return Case{}, err
	}
	in, err := in.normalize()
	if err != nil {
		return Case{}, err
	}
	have, err := s.cfg.Store.ListCases(ctx, org, agentID)
	if err != nil {
		return Case{}, err
	}
	if len(have) >= MaxCasesPerAgent {
		return Case{}, fmt.Errorf("%w: at most %d golden cases per agent", domain.ErrConflict, MaxCasesPerAgent)
	}
	now := s.now()
	c := Case{ID: newID("gc_"), AgentID: agentID, Name: in.Name, Input: in.Input, Checks: in.Checks, Active: in.Active == nil || *in.Active,
		CreatedBy: actor, CreatedAt: now, UpdatedAt: now}
	if err := s.cfg.Store.CreateCase(ctx, org, c); err != nil {
		return Case{}, err
	}
	s.audit(ctx, org, actor, "quality.case_created", c.ID, map[string]any{"agent_id": agentID, "name": c.Name, "checks": len(c.Checks)})
	return c, nil
}

// UpdateCase replaces the editable fields of a case (the agent of a case never changes).
func (s *Service) UpdateCase(ctx context.Context, actor, id string, in CaseInput) (Case, error) {
	org := s.org(ctx)
	c, err := s.cfg.Store.GetCase(ctx, org, id)
	if err != nil {
		return Case{}, err
	}
	in, err = in.normalize()
	if err != nil {
		return Case{}, err
	}
	c.Name, c.Input, c.Checks, c.UpdatedAt = in.Name, in.Input, in.Checks, s.now()
	if in.Active != nil {
		c.Active = *in.Active
	}
	if err := s.cfg.Store.UpdateCase(ctx, org, c); err != nil {
		return Case{}, err
	}
	s.audit(ctx, org, actor, "quality.case_updated", c.ID, map[string]any{"agent_id": c.AgentID, "name": c.Name, "checks": len(c.Checks), "active": c.Active})
	return c, nil
}

// DeleteCase removes a case; its recorded runs stay, so the history behind a score is never rewritten.
func (s *Service) DeleteCase(ctx context.Context, actor, id string) error {
	org := s.org(ctx)
	c, err := s.cfg.Store.GetCase(ctx, org, id)
	if err != nil {
		return err
	}
	if err := s.cfg.Store.DeleteCase(ctx, org, id); err != nil {
		return err
	}
	s.audit(ctx, org, actor, "quality.case_deleted", id, map[string]any{"agent_id": c.AgentID, "name": c.Name})
	return nil
}

// RunInput records the result of running a case. Send Output to have the checks evaluated here
// (the text is not stored), or Passed when the verdict comes from outside.
type RunInput struct {
	Passed *bool   `json:"passed"`
	Output *string `json:"output"`
	TaskID string  `json:"task_id"`
	Note   string  `json:"note"`
}

// RecordRun stores a pass/fail run of a case.
func (s *Service) RecordRun(ctx context.Context, actor, caseID string, in RunInput) (Run, error) {
	org := s.org(ctx)
	c, err := s.cfg.Store.GetCase(ctx, org, caseID)
	if err != nil {
		return Run{}, err
	}
	if !c.Active {
		return Run{}, fmt.Errorf("%w: the golden case is inactive", domain.ErrConflict)
	}
	in.TaskID, in.Note = strings.TrimSpace(in.TaskID), strings.TrimSpace(in.Note)
	if len(in.TaskID) > 100 || utf8.RuneCountInString(in.Note) > MaxNoteRunes {
		return Run{}, invalid("task_id or note is too long")
	}
	r := Run{ID: newID("gr_"), CaseID: c.ID, AgentID: c.AgentID, TaskID: in.TaskID, Note: in.Note, RecordedBy: actor, CreatedAt: s.now(), ChecksTotal: len(c.Checks)}
	switch {
	case in.Output != nil:
		if len(*in.Output) > MaxOutputBytes {
			return Run{}, invalid("output is larger than %d bytes", MaxOutputBytes)
		}
		r.ChecksPassed = Evaluate(c.Checks, *in.Output)
		r.Passed = r.ChecksPassed == r.ChecksTotal
	case in.Passed != nil:
		r.Passed = *in.Passed
		if r.Passed {
			r.ChecksPassed = r.ChecksTotal
		}
	default:
		return Run{}, invalid("send output or passed")
	}
	if err := s.cfg.Store.AddRun(ctx, org, r); err != nil {
		return Run{}, err
	}
	s.audit(ctx, org, actor, "quality.run_recorded", c.ID, map[string]any{"agent_id": c.AgentID, "run_id": r.ID, "passed": r.Passed,
		"checks_passed": r.ChecksPassed, "checks_total": r.ChecksTotal, "task_id": r.TaskID})
	return r, nil
}

// ListRuns returns the newest runs of a case.
func (s *Service) ListRuns(ctx context.Context, caseID string, limit int) ([]Run, error) {
	org := s.org(ctx)
	if _, err := s.cfg.Store.GetCase(ctx, org, caseID); err != nil {
		return nil, err
	}
	return s.cfg.Store.ListRuns(ctx, org, caseID, min(max(limit, 1), 200))
}
