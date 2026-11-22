package agenthistory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// DefaultKeep is how many versions are kept per agent.
const DefaultKeep = 100

// Source fills its part of the snapshot of one agent. The first source must fail with domain.ErrNotFound for an
// agent that does not exist. A source that cannot read its part right now returns an error: no version is written
// (a partial snapshot would look like a change).
type Source func(ctx context.Context, org, agentID string, c *Config) error

// Service records and serves the history.
type Service struct {
	Store   Store
	Sources []Source
	// Agents lists the ids of the agents of an organization (RecordAll). Optional.
	Agents func(ctx context.Context, org string) ([]string, error)
	// Audit writes an audit entry (restores). Optional.
	Audit func(ctx context.Context, org string, a domain.AuditLog)
	Keep  int
	Now   func() time.Time
	Log   *slog.Logger

	locks sync.Map // org|agent -> *sync.Mutex: one writer per agent in this process
	wg    sync.WaitGroup
}

func (s *Service) keep() int {
	if s.Keep > 0 {
		return s.Keep
	}
	return DefaultKeep
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Service) lock(org, agentID string) func() {
	m, _ := s.locks.LoadOrStore(key(org, agentID), &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// Capture composes the current snapshot of an agent from every source.
func (s *Service) Capture(ctx context.Context, org, agentID string) (Config, error) {
	var c Config
	for _, src := range s.Sources {
		if err := src(ctx, org, agentID, &c); err != nil {
			return Config{}, err
		}
	}
	return c.Normalize(), nil
}

// IsMachine reports whether an actor is a machine (agents, the system, the orchestrator).
func IsMachine(actor string) bool {
	a := strings.ToLower(strings.TrimSpace(actor))
	return a == "" || a == "system" || a == "orchestrator" || a == "scheduler" || a == "runtime" || strings.HasPrefix(a, "agent:")
}

func actorKind(actor string) string {
	if IsMachine(actor) || actor == "user" {
		return ActorSystem
	}
	return ActorPerson
}

// Record appends a version when the configuration of the agent differs from its latest version. It reports the
// version and whether it was written. The first version of an agent is written whatever the cause.
func (s *Service) Record(ctx context.Context, org, agentID string, c Cause) (Version, bool, error) {
	if org == "" || agentID == "" {
		return Version{}, false, nil
	}
	unlock := s.lock(org, agentID)
	defer unlock()
	for attempt := 0; ; attempt++ {
		v, ok, err := s.recordOnce(ctx, org, agentID, c)
		if errors.Is(err, domain.ErrConflict) && attempt < 2 { // another replica wrote the same number: read again
			continue
		}
		return v, ok, err
	}
}

func (s *Service) recordOnce(ctx context.Context, org, agentID string, c Cause) (Version, bool, error) {
	cfg, err := s.Capture(ctx, org, agentID)
	if err != nil {
		return Version{}, false, err
	}
	last, had, err := s.Store.Last(ctx, org, agentID)
	if err != nil {
		return Version{}, false, err
	}
	fields := []string{}
	if had {
		fields = ChangedFields(last.Config, cfg)
		if len(fields) == 0 {
			return last, false, nil
		}
	}
	actor := strings.TrimSpace(c.Actor)
	if actor == "" {
		actor = ActorSystem
	}
	v := Version{AgentID: agentID, Version: last.Version + 1, CreatedAt: s.now(), Actor: actor, ActorKind: actorKind(actor),
		Source: clip(c.Source, 120), Fields: fields, RestoredFrom: c.RestoredFrom, Config: cfg}
	if v.ActorKind == ActorPerson {
		v.ActorName = clip(c.ActorName, 120)
	}
	if v.Source == "" {
		v.Source = "unknown"
	}
	if err := s.Store.Append(ctx, org, v, s.keep()); err != nil {
		return Version{}, false, err
	}
	return v, true, nil
}

// EnsureBaseline writes "how it was when the history started" for an agent without versions, so the first change
// after that can be undone. No-op when the agent already has a history.
func (s *Service) EnsureBaseline(ctx context.Context, org, agentID string) {
	if org == "" || agentID == "" {
		return
	}
	if _, had, err := s.Store.Last(ctx, org, agentID); err != nil || had {
		return
	}
	if _, _, err := s.Record(ctx, org, agentID, Cause{Actor: ActorSystem, Source: "baseline"}); err != nil && !errors.Is(err, domain.ErrNotFound) {
		s.log().Warn("agent history: baseline", "agent", agentID, "err", err)
	}
}

// RecordAll records every agent of the organization (routes that change agents without naming one).
func (s *Service) RecordAll(ctx context.Context, org string, c Cause) {
	if s.Agents == nil || org == "" {
		return
	}
	ids, err := s.Agents(ctx, org)
	if err != nil {
		s.log().Warn("agent history: list agents", "err", err)
		return
	}
	for _, id := range ids {
		if _, _, err := s.Record(ctx, org, id, c); err != nil && !errors.Is(err, domain.ErrNotFound) {
			s.log().Warn("agent history: record", "agent", id, "err", err)
		}
	}
}

// Go runs fn in the background, tracked by Flush.
func (s *Service) Go(fn func()) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				s.log().Error("agent history: panic", "panic", r)
			}
		}()
		fn()
	}()
}

// Flush waits for the background records (tests and shutdown).
func (s *Service) Flush() { s.wg.Wait() }

// List returns the history, newest first (an agent without one gets its baseline first).
func (s *Service) List(ctx context.Context, org, agentID string, limit int) ([]Version, error) {
	if _, err := s.Capture(ctx, org, agentID); err != nil {
		return nil, err
	}
	s.EnsureBaseline(ctx, org, agentID)
	if limit <= 0 || limit > s.keep() {
		limit = s.keep()
	}
	return s.Store.List(ctx, org, agentID, limit)
}

// Detail returns one version with its changes against the previous one.
func (s *Service) Detail(ctx context.Context, org, agentID string, n int) (Detail, error) {
	v, err := s.Store.Get(ctx, org, agentID, n)
	if err != nil {
		return Detail{}, err
	}
	d := Detail{Version: v, Changes: []Change{}}
	if n > 1 {
		if p, err := s.Store.Get(ctx, org, agentID, n-1); err == nil {
			pn := p.Version
			d.Previous, d.Changes = &pn, Diff(p.Config, v.Config)
		} else if !errors.Is(err, domain.ErrNotFound) {
			return Detail{}, err
		}
	}
	return d, nil
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func notFound(agentID string, n int) error {
	return fmt.Errorf("%w: version %d of agent %s", domain.ErrNotFound, n, agentID)
}
