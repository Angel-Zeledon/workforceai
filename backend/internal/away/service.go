package away

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// MarkStore keeps the last-seen mark of each person per organization (memory or Postgres, migration 792).
type MarkStore interface {
	GetAwayMark(ctx context.Context, orgID, userID string) (Mark, bool, error)
	PutAwayMark(ctx context.Context, orgID, userID string, m Mark) error
}

// History is the read side of what the platform already records. Both the memory and the Postgres stores
// implement it.
type History interface {
	ListAgents(ctx context.Context, orgID string) ([]domain.Agent, error)
	ListApprovals(ctx context.Context, orgID, status string) ([]domain.Approval, error)
	GetRequest(ctx context.Context, orgID, id string) (domain.Request, error)
	ListEvents(ctx context.Context, orgID string, q domain.EventQuery) ([]domain.Event, error)
	CountEvents(ctx context.Context, orgID string, from, to time.Time) (map[string]int, error)
	SpendByAgent(ctx context.Context, orgID string, from, to time.Time) ([]domain.SpendRow, error)
	QueryAudit(ctx context.Context, orgID string, q domain.AuditQuery) (domain.AuditPage, error)
}

// Defaults.
const (
	DefaultMinHours    = 8.0
	MaxMinHours        = 168.0               // one week
	DefaultMaxLookback = 14 * 24 * time.Hour // a longer absence is summarized over its last 14 days
	notableLimit       = 500
	completedLimit     = 2000
	statesLimit        = 5000
	maxRequestTitles   = 20
)

// Config wires the service.
type Config struct {
	Marks   MarkStore
	History History
	OrgID   string
	// Gap is the silence that starts a new visit (DefaultGap when 0).
	Gap time.Duration
	// MinHours is the default absence that opens the summary on its own (DefaultMinHours when 0).
	MinHours float64
	// MaxLookback caps how far back a summary reaches (DefaultMaxLookback when 0).
	MaxLookback time.Duration
	Now         func() time.Time
	Log         *slog.Logger
}

// Service is the "while you were away" use case.
type Service struct{ cfg Config }

// New builds the service.
func New(cfg Config) *Service {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Gap <= 0 {
		cfg.Gap = DefaultGap
	}
	if cfg.MinHours <= 0 {
		cfg.MinHours = DefaultMinHours
	}
	if cfg.MaxLookback <= 0 {
		cfg.MaxLookback = DefaultMaxLookback
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

// ClampMinHours keeps a requested threshold in [0, MaxMinHours]; a negative or missing one means the default.
func (s *Service) ClampMinHours(h float64) float64 {
	if h < 0 || math.IsNaN(h) || math.IsInf(h, 0) {
		return s.cfg.MinHours
	}
	return math.Min(h, MaxMinHours)
}

func userOf(v Viewer) (string, error) {
	if v.UserID == "" {
		return "", fmt.Errorf("%w: user required", domain.ErrInvalid)
	}
	return v.UserID, nil
}

// Summary computes what happened while v was away. It never writes: the heartbeat moves the mark. minHours < 0
// means the configured default.
func (s *Service) Summary(ctx context.Context, v Viewer, minHours float64) (Summary, error) {
	user, err := userOf(v)
	if err != nil {
		return Summary{}, err
	}
	org, now := s.org(ctx), s.now()
	if minHours < 0 {
		minHours = s.cfg.MinHours
	}
	minHours = s.ClampMinHours(minHours)
	m, found, err := s.cfg.Marks.GetAwayMark(ctx, org, user)
	if err != nil {
		return Summary{}, err
	}
	from, to, ok := Absence(m, found, now, s.cfg.Gap)
	if !ok {
		out := emptySummary(now, minHours)
		out.FirstVisit, out.Quiet = !found, true
		return out, nil
	}
	away := to.Sub(from)
	if away > s.cfg.MaxLookback {
		from = to.Add(-s.cfg.MaxLookback)
	}
	f, err := s.collect(ctx, org, from, to, v.ReadApprovals)
	if err != nil {
		return Summary{}, err
	}
	out := Build(f, v, from, to, now, minHours)
	out.Clipped = away > s.cfg.MaxLookback
	out.AwayMinutes = int(away / time.Minute) // the whole absence, even when only its last part is summarized
	out.Dismissed = Dismissed(m, found, now, s.cfg.Gap)
	out.Show = !out.Dismissed && !out.Quiet && float64(out.AwayMinutes) >= minHours*60
	return out, nil
}

// collect gathers the facts of [from, to) from the stores. Lists are capped; counts are exact.
func (s *Service) collect(ctx context.Context, org string, from, to time.Time, approvals bool) (Facts, error) {
	h := s.cfg.History
	f := Facts{Requests: map[string]domain.Request{}}
	var err error
	if f.Agents, err = h.ListAgents(ctx, org); err != nil {
		return f, err
	}
	if approvals {
		if f.Pending, err = h.ListApprovals(ctx, org, string(domain.ApprovalPending)); err != nil {
			return f, err
		}
	}
	if f.Counts, err = h.CountEvents(ctx, org, from, to); err != nil {
		return f, err
	}
	if f.Notable, err = h.ListEvents(ctx, org, domain.EventQuery{From: from, To: to, Types: NotableTypes, Limit: notableLimit, Desc: true}); err != nil {
		return f, err
	}
	if f.Completed, err = h.ListEvents(ctx, org, domain.EventQuery{From: from, To: to, Types: []string{domain.EvTaskCompleted}, Limit: completedLimit}); err != nil {
		return f, err
	}
	if f.States, err = h.ListEvents(ctx, org, domain.EventQuery{From: from, To: to, Types: []string{domain.EvAgentState}, Limit: statesLimit}); err != nil {
		return f, err
	}
	page, err := h.QueryAudit(ctx, org, domain.AuditQuery{From: &from, To: &to, Action: evRequestFailure, Entity: "request", Limit: domain.MaxAuditPage, Desc: true})
	if err != nil {
		return f, err
	}
	f.FailedRequests = dedupeRequests(page.Items)
	if f.Spend, err = h.SpendByAgent(ctx, org, from, to); err != nil {
		return f, err
	}
	// Titles of the requests the items point to (completed and failed), newest first, a bounded number of reads.
	ids := []string{}
	for _, e := range f.Notable {
		if e.Type == domain.EvRequestCompleted {
			ids = append(ids, str(payload(e), "request_id"))
		}
	}
	for _, a := range f.FailedRequests {
		ids = append(ids, a.EntityID)
	}
	for _, id := range ids {
		if len(f.Requests) >= maxRequestTitles {
			break
		}
		if _, done := f.Requests[id]; done || id == "" {
			continue
		}
		r, err := h.GetRequest(ctx, org, id)
		if err != nil {
			s.cfg.Log.Debug("away summary: request title", "request", id, "err", err)
			r = domain.Request{ID: id}
		}
		f.Requests[id] = r
	}
	return f, nil
}

// dedupeRequests keeps one failure per request (a request is failed once even if retried and failed again).
func dedupeRequests(in []domain.AuditLog) []domain.AuditLog {
	seen := map[string]bool{}
	out := []domain.AuditLog{}
	for _, a := range in {
		if a.EntityID == "" || seen[a.EntityID] {
			continue
		}
		seen[a.EntityID] = true
		out = append(out, a)
	}
	return out
}

// Heartbeat records that the person is looking now; after a gap it starts a new visit.
func (s *Service) Heartbeat(ctx context.Context, v Viewer) (Mark, error) {
	user, err := userOf(v)
	if err != nil {
		return Mark{}, err
	}
	org, now := s.org(ctx), s.now()
	m, found, err := s.cfg.Marks.GetAwayMark(ctx, org, user)
	if err != nil {
		return Mark{}, err
	}
	next := Touch(m, found, now, s.cfg.Gap)
	if err := s.cfg.Marks.PutAwayMark(ctx, org, user, next); err != nil {
		return Mark{}, err
	}
	return next, nil
}

// Dismiss closes the summary for the visit in progress (it opens again after the next absence).
func (s *Service) Dismiss(ctx context.Context, v Viewer) (Mark, error) {
	user, err := userOf(v)
	if err != nil {
		return Mark{}, err
	}
	org, now := s.org(ctx), s.now()
	m, found, err := s.cfg.Marks.GetAwayMark(ctx, org, user)
	if err != nil {
		return Mark{}, err
	}
	next := Touch(m, found, now, s.cfg.Gap) // dismissing is also being here
	next.DismissedAt = &now
	if err := s.cfg.Marks.PutAwayMark(ctx, org, user, next); err != nil {
		return Mark{}, err
	}
	return next, nil
}
