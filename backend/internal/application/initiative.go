package application

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Proactive employees (docs/architecture/initiative.md).
//
// An agent may raise an INITIATIVE: a short suggestion for the humans ("the margin dropped 3 %, shall I look into
// it?"). It is only a notification record. Accepting it does not run anything by itself: it submits a normal request
// (Orchestrator.Submit), so planning, approvals, spend caps, kill switch, read-only mode, pause and audit apply
// exactly as if the person had typed the text. Agents can neither accept nor approve anything.
//
// The organization limits how many suggestions are raised (per day, per month and, optionally, per agent per day):
// the initiative budget. A suggestion costs no model call (it is built from a signal that already exists); the work
// it may start is charged to the usual caps when a person accepts it. The same signal is not suggested again inside
// a dedupe window.

// InitiativeStatus is the life cycle of a suggestion: pending -> accepted | dismissed.
type InitiativeStatus string

const (
	InitiativePending   InitiativeStatus = "pending"
	InitiativeAccepted  InitiativeStatus = "accepted"
	InitiativeDismissed InitiativeStatus = "dismissed"
)

// SignalTaskFailed is the signal kind raised when a task fails.
const SignalTaskFailed = "task_failed"

// Defaults of the initiative budget and limits of the text fields.
const (
	DefaultInitiativeDaily   = 3
	DefaultInitiativeMonthly = 30
	MaxInitiativeLimit       = 1000
	DefaultInitiativeDedupe  = 24 * time.Hour
	maxInitiativeTitle       = 160
	maxInitiativeBody        = 1000
	maxInitiativePrompt      = 4000
)

// Initiative is one suggestion raised by an agent.
type Initiative struct {
	ID      string `json:"id"`
	AgentID string `json:"agent_id"`
	// Signal is the kind of event behind it and SignalKey identifies the subject, for dedupe.
	Signal    string           `json:"signal"`
	SignalKey string           `json:"signal_key"`
	Title     string           `json:"title"`
	Body      string           `json:"body"`
	Prompt    string           `json:"prompt"` // the request that is submitted if a person accepts
	Status    InitiativeStatus `json:"status"`
	CreatedAt time.Time        `json:"created_at"`
	// ResolvedAt and ResolvedBy are set when it was accepted or dismissed; RequestID when it was accepted.
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy string     `json:"resolved_by,omitempty"`
	RequestID  string     `json:"request_id,omitempty"`
}

// InitiativeSettings is the initiative budget of an organization.
type InitiativeSettings struct {
	Enabled      bool `json:"enabled"`
	DailyLimit   int  `json:"daily_limit"`
	MonthlyLimit int  `json:"monthly_limit"`
	// AgentDaily overrides, per agent, how many suggestions it may raise per day (0 = the agent is muted).
	AgentDaily map[string]int `json:"agent_daily,omitempty"`
}

// DefaultInitiativeSettings is what an organization has until an admin changes it.
func DefaultInitiativeSettings() InitiativeSettings {
	return InitiativeSettings{Enabled: true, DailyLimit: DefaultInitiativeDaily, MonthlyLimit: DefaultInitiativeMonthly}
}

// InitiativeStore persists suggestions and settings (memory and Postgres implement it).
type InitiativeStore interface {
	CreateInitiative(ctx context.Context, orgID string, it Initiative) error
	// ListInitiatives returns the suggestions newest first; status "" = all; limit <= 0 = 100.
	ListInitiatives(ctx context.Context, orgID string, status InitiativeStatus, limit int) ([]Initiative, error)
	GetInitiative(ctx context.Context, orgID, id string) (Initiative, error)
	// ResolveInitiative moves a suggestion from one status to another in one step (domain.ErrConflict when it is not
	// in status "from", domain.ErrNotFound when missing). Moving back to pending clears the resolution; from == to
	// only refreshes by, requestID and at.
	ResolveInitiative(ctx context.Context, orgID, id string, from, to InitiativeStatus, by, requestID string, at time.Time) (Initiative, error)
	// CountInitiatives counts the suggestions raised since a moment (any status); agentID "" counts all agents.
	CountInitiatives(ctx context.Context, orgID, agentID string, since time.Time) (int, error)
	// HasInitiativeSignal reports whether the same signal was raised since a moment.
	HasInitiativeSignal(ctx context.Context, orgID, signal, key string, since time.Time) (bool, error)
	// GetInitiativeSettings returns the stored settings; ok is false when none was ever saved.
	GetInitiativeSettings(ctx context.Context, orgID string) (s InitiativeSettings, ok bool, err error)
	PutInitiativeSettings(ctx context.Context, orgID string, s InitiativeSettings) error
}

// RequestSubmitter is the existing path a request takes (Orchestrator.Submit).
type RequestSubmitter interface {
	Submit(ctx context.Context, text string) (string, error)
}

// InitiativeRaiser is the small hook other code calls when it sees a signal. Implemented by InitiativeService.
type InitiativeRaiser interface {
	TaskFailed(ctx context.Context, t domain.Task, cause error)
}

// InitiativeService raises, lists, accepts and dismisses suggestions and keeps the budget.
type InitiativeService struct {
	Store  InitiativeStore
	Submit RequestSubmitter
	Rec    *Recorder
	Cfg    Config
	// Dedupe is the window in which the same signal is not suggested again (0 = DefaultInitiativeDedupe).
	Dedupe time.Duration
	Now    func() time.Time

	mu sync.Mutex // serializes budget check + insert inside this process
}

var _ InitiativeRaiser = (*InitiativeService)(nil)

func (s *InitiativeService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *InitiativeService) org(ctx context.Context) string { return OrgFrom(ctx, s.Cfg.OrgID) }

// RaiseInput describes a suggestion. SignalKey identifies the subject for dedupe (e.g. the task title).
type RaiseInput struct {
	AgentID   string
	Signal    string
	SignalKey string
	Title     string
	Body      string
	Prompt    string
}

// RaiseOutcome says whether a suggestion was stored and, when not, why: disabled | duplicate | daily_limit |
// monthly_limit | agent_limit.
type RaiseOutcome struct {
	Initiative Initiative `json:"initiative"`
	Raised     bool       `json:"raised"`
	Reason     string     `json:"reason,omitempty"`
}

// Settings returns the stored settings or the defaults.
func (s *InitiativeService) Settings(ctx context.Context) (InitiativeSettings, error) {
	st, ok, err := s.Store.GetInitiativeSettings(ctx, s.org(ctx))
	if err != nil {
		return InitiativeSettings{}, err
	}
	if !ok {
		return DefaultInitiativeSettings(), nil
	}
	return st, nil
}

// PutSettings validates and stores the budget (audited).
func (s *InitiativeService) PutSettings(ctx context.Context, in InitiativeSettings) (InitiativeSettings, error) {
	if in.DailyLimit < 0 || in.MonthlyLimit < 0 || in.DailyLimit > MaxInitiativeLimit || in.MonthlyLimit > MaxInitiativeLimit {
		return InitiativeSettings{}, fmt.Errorf("%w: limits must be between 0 and %d", domain.ErrInvalid, MaxInitiativeLimit)
	}
	clean := map[string]int{}
	for id, n := range in.AgentDaily {
		id = strings.TrimSpace(id)
		if id == "" || n < 0 || n > MaxInitiativeLimit {
			return InitiativeSettings{}, fmt.Errorf("%w: agent limits must be between 0 and %d", domain.ErrInvalid, MaxInitiativeLimit)
		}
		clean[id] = n
	}
	in.AgentDaily = clean
	if err := s.Store.PutInitiativeSettings(ctx, s.org(ctx), in); err != nil {
		return InitiativeSettings{}, err
	}
	s.audit(ctx, ActorFrom(ctx, "user"), "initiative.settings_changed", "settings", map[string]any{
		"enabled": in.Enabled, "daily_limit": in.DailyLimit, "monthly_limit": in.MonthlyLimit, "agent_overrides": len(in.AgentDaily)})
	return in, nil
}

// Raise stores a suggestion when the budget and the dedupe window allow it. A refusal is not an error: it comes back
// in the outcome so callers (hooks) can ignore it. Nothing is executed.
func (s *InitiativeService) Raise(ctx context.Context, in RaiseInput) (RaiseOutcome, error) {
	in.AgentID, in.Signal, in.SignalKey = strings.TrimSpace(in.AgentID), strings.TrimSpace(in.Signal), strings.ToLower(strings.TrimSpace(in.SignalKey))
	in.Title, in.Body, in.Prompt = clip(in.Title, maxInitiativeTitle), clip(in.Body, maxInitiativeBody), clip(in.Prompt, maxInitiativePrompt)
	if in.AgentID == "" || in.Signal == "" || in.Title == "" || in.Prompt == "" {
		return RaiseOutcome{}, fmt.Errorf("%w: agent, signal, title and prompt are required", domain.ErrInvalid)
	}
	org := s.org(ctx)
	cfg, err := s.Settings(ctx)
	if err != nil {
		return RaiseOutcome{}, err
	}
	if !cfg.Enabled {
		return RaiseOutcome{Reason: "disabled"}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	dedupe := s.Dedupe
	if dedupe <= 0 {
		dedupe = DefaultInitiativeDedupe
	}
	if dup, err := s.Store.HasInitiativeSignal(ctx, org, in.Signal, in.SignalKey, now.Add(-dedupe)); err != nil {
		return RaiseOutcome{}, err
	} else if dup {
		return RaiseOutcome{Reason: "duplicate"}, nil
	}
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if n, err := s.Store.CountInitiatives(ctx, org, "", monthStart); err != nil {
		return RaiseOutcome{}, err
	} else if n >= cfg.MonthlyLimit {
		return RaiseOutcome{Reason: "monthly_limit"}, nil
	}
	if n, err := s.Store.CountInitiatives(ctx, org, "", dayStart); err != nil {
		return RaiseOutcome{}, err
	} else if n >= cfg.DailyLimit {
		return RaiseOutcome{Reason: "daily_limit"}, nil
	}
	if lim, ok := cfg.AgentDaily[in.AgentID]; ok {
		if n, err := s.Store.CountInitiatives(ctx, org, in.AgentID, dayStart); err != nil {
			return RaiseOutcome{}, err
		} else if n >= lim {
			return RaiseOutcome{Reason: "agent_limit"}, nil
		}
	}
	it := Initiative{ID: newID(), AgentID: in.AgentID, Signal: in.Signal, SignalKey: in.SignalKey, Title: in.Title, Body: in.Body,
		Prompt: in.Prompt, Status: InitiativePending, CreatedAt: now}
	if err := s.Store.CreateInitiative(ctx, org, it); err != nil {
		return RaiseOutcome{}, err
	}
	if s.Rec != nil {
		s.Rec.Emit(ctx, Action{Type: domain.EvInitiativeRaised, AgentID: it.AgentID, Entity: "initiative", EntityID: it.ID,
			Payload: map[string]any{"initiative_id": it.ID, "agent_id": it.AgentID, "signal": it.Signal, "title": it.Title}})
	}
	return RaiseOutcome{Initiative: it, Raised: true}, nil
}

// TaskFailed is the concrete trigger: a task that failed becomes a suggestion to look into it. The orchestrator calls
// it from failTask; it never fails the caller. Tasks stopped by a budget or a provider balance are blocked, not
// failed, and do not reach here.
func (s *InitiativeService) TaskFailed(ctx context.Context, t domain.Task, cause error) {
	if s == nil || s.Store == nil || t.AgentID == "" {
		return
	}
	reason := ""
	if cause != nil {
		reason = truncate(cause.Error(), 200)
	}
	_, _ = s.Raise(context.WithoutCancel(ctx), RaiseInput{
		AgentID: t.AgentID, Signal: SignalTaskFailed, SignalKey: t.AgentID + ":" + t.Title,
		Title:  fmt.Sprintf("La tarea %q falló", truncate(t.Title, 100)),
		Body:   reason,
		Prompt: fmt.Sprintf("Revisa por qué falló la tarea %q y propón cómo resolverlo. Motivo registrado: %s", truncate(t.Title, 200), reason),
	})
}

// List returns the suggestions newest first (status "" = all).
func (s *InitiativeService) List(ctx context.Context, status InitiativeStatus, limit int) ([]Initiative, error) {
	switch status {
	case "", InitiativePending, InitiativeAccepted, InitiativeDismissed:
	default:
		return nil, fmt.Errorf("%w: unknown status", domain.ErrInvalid)
	}
	return s.Store.ListInitiatives(ctx, s.org(ctx), status, limit)
}

// Accept turns a pending suggestion into a normal request through the usual path. The suggestion is claimed first
// (so two people cannot both accept it); if the request is refused (kill switch, budget, delegation rules...) the claim
// is undone and the error comes back.
func (s *InitiativeService) Accept(ctx context.Context, id string) (Initiative, error) {
	if s.Submit == nil {
		return Initiative{}, fmt.Errorf("%w: requests are not available", domain.ErrConflict)
	}
	org, actor, now := s.org(ctx), ActorFrom(ctx, "user"), s.now()
	it, err := s.Store.ResolveInitiative(ctx, org, id, InitiativePending, InitiativeAccepted, actor, "", now)
	if err != nil {
		return Initiative{}, err
	}
	reqID, err := s.Submit.Submit(ctx, it.Prompt)
	if err != nil {
		_, _ = s.Store.ResolveInitiative(context.WithoutCancel(ctx), org, id, InitiativeAccepted, InitiativePending, "", "", now)
		return Initiative{}, err
	}
	if it, err = s.Store.ResolveInitiative(ctx, org, id, InitiativeAccepted, InitiativeAccepted, actor, reqID, now); err != nil {
		return Initiative{}, err
	}
	s.audit(ctx, actor, "initiative.accepted", it.ID, map[string]any{"agent_id": it.AgentID, "signal": it.Signal, "request_id": reqID})
	return it, nil
}

// Dismiss rejects a pending suggestion. Dismissed ones still count against the budget of the day.
func (s *InitiativeService) Dismiss(ctx context.Context, id string) (Initiative, error) {
	actor := ActorFrom(ctx, "user")
	it, err := s.Store.ResolveInitiative(ctx, s.org(ctx), id, InitiativePending, InitiativeDismissed, actor, "", s.now())
	if err != nil {
		return Initiative{}, err
	}
	s.audit(ctx, actor, "initiative.dismissed", it.ID, map[string]any{"agent_id": it.AgentID, "signal": it.Signal})
	return it, nil
}

func (s *InitiativeService) audit(ctx context.Context, actor, action, entityID string, details map[string]any) {
	if s.Rec == nil {
		return
	}
	s.Rec.Audit(ctx, domain.AuditLog{Actor: actor, Action: action, Entity: "initiative", EntityID: entityID, Details: details})
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
