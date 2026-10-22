package trust

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/policy"
	"aiworkforce/backend/internal/roles"
)

// Agents is the part of the application store the ladder reads.
type Agents interface {
	GetAgent(ctx context.Context, org, id string) (domain.Agent, error)
	ListAgents(ctx context.Context, org string) ([]domain.Agent, error)
}

// Config wires the service.
type Config struct {
	Store   Store
	Signals Signals
	Agents  Agents
	// SetAutonomy writes agents.autonomy (application.ConfigStore.SetAgentAutonomy).
	SetAutonomy func(ctx context.Context, org, agentID, level string) error
	// Quality returns the B11 score of an agent (nil when there is not enough data). Optional.
	Quality func(ctx context.Context, org, agentID string) (*Quality, error)
	// Blocked returns a controls code (kill_switch_active, read_only_mode, agent_paused...) when autonomy must not be
	// RAISED right now; "" when it may. Lowering autonomy is never blocked. Optional (nil = never blocked).
	Blocked func(ctx context.Context, org, agentID string) string
	// Rec audits and emits (nil = neither; tests).
	Rec   *application.Recorder
	OrgID string
	Now   func() time.Time
	Log   *slog.Logger
}

// Service is the trust ladder use-case layer.
type Service struct {
	cfg Config
	mu  sync.Mutex // serializes evaluations and decisions inside this process
}

// New builds the service.
func New(cfg Config) *Service {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Service{cfg: cfg}
}

// BlockedError says why a raise of autonomy has to wait (kill switch, read-only mode, paused agent).
type BlockedError struct{ Code string }

func (e BlockedError) Error() string { return "autonomy cannot be raised now: " + e.Code }

// Unwrap makes it a domain.ErrConflict.
func (e BlockedError) Unwrap() error { return domain.ErrConflict }

// ErrEvidenceChanged: the proposal no longer matches the evidence or the agent's level; it was superseded.
var ErrEvidenceChanged = fmt.Errorf("%w: the evidence or the agent's level changed since this proposal was made", domain.ErrConflict)

func (s *Service) org(ctx context.Context) string { return application.OrgFrom(ctx, s.cfg.OrgID) }

func (s *Service) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now().UTC()
	}
	return time.Now().UTC()
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "tp_" + hex.EncodeToString(b)
}

func (s *Service) audit(ctx context.Context, org string, a domain.AuditLog) {
	if s.cfg.Rec != nil {
		s.cfg.Rec.Audit(application.WithOrg(ctx, org), a)
	}
}

func (s *Service) emit(ctx context.Context, org string, a application.Action) {
	if s.cfg.Rec != nil {
		a.SkipAudit = true
		s.cfg.Rec.Emit(application.WithOrg(ctx, org), a)
	}
}

// AgentTrust is the view of one agent on the ladder.
type AgentTrust struct {
	AgentID       string    `json:"agent_id"`
	Name          string    `json:"name"`
	Role          string    `json:"role"`
	Level         string    `json:"level"`
	Ceiling       string    `json:"ceiling"`
	TrackingSince time.Time `json:"tracking_since"`
	LevelSince    time.Time `json:"level_since"`
	EvidenceSince time.Time `json:"evidence_since"`
	Counts        Counts    `json:"counts"`
	Quality       *Quality  `json:"quality"`
	Assessment
	Pending    *Proposal `json:"pending"`     // the proposal waiting for a human, if any
	LastChange *Proposal `json:"last_change"` // the latest accepted, applied or reverted change
	CanRevert  bool      `json:"can_revert"`  // LastChange is an automatic demotion a human may still undo
}

// Report wraps the views with the methodology.
type Report struct {
	Methodology Methodology  `json:"methodology"`
	GeneratedAt time.Time    `json:"generated_at"`
	Agents      []AgentTrust `json:"agents"`
}

// ensure returns the record of an agent, creating it (tracking starts now, at the current level) or resynchronizing
// it when the level was changed elsewhere (then the evidence starts again and pending proposals are superseded).
func (s *Service) ensure(ctx context.Context, org string, a domain.Agent, now time.Time) (Record, error) {
	r, ok, err := s.cfg.Store.GetRecord(ctx, org, a.ID)
	if err != nil {
		return r, err
	}
	switch {
	case !ok:
		r = Record{AgentID: a.ID, StartLevel: a.Autonomy, Level: a.Autonomy, LevelSince: now, TrackingSince: now, UpdatedAt: now}
	case r.Level != a.Autonomy:
		r.Level, r.LevelSince, r.UpdatedAt = a.Autonomy, now, now
		if err := s.supersedePending(ctx, org, a.ID, "level changed outside the trust ladder", now); err != nil {
			return r, err
		}
	default:
		return r, nil
	}
	return r, s.cfg.Store.PutRecord(ctx, org, r)
}

func (s *Service) supersedePending(ctx context.Context, org, agentID, note string, now time.Time) error {
	ps, err := s.cfg.Store.ListProposals(ctx, org, agentID, StatusPending, 20)
	if err != nil {
		return err
	}
	for _, p := range ps {
		if _, err := s.cfg.Store.ResolveProposal(ctx, org, p.ID, StatusPending, StatusSuperseded, "system", note, now); err != nil && !errors.Is(err, domain.ErrConflict) {
			return err
		}
		s.emit(ctx, org, application.Action{Type: domain.EvTrustProposal, AgentID: agentID, Entity: "trust_proposal", EntityID: p.ID,
			Payload: map[string]any{"proposal_id": p.ID, "agent_id": agentID, "kind": p.Kind, "status": StatusSuperseded}})
	}
	return nil
}

// view computes the assessment of one agent (it may create or resynchronize its record).
func (s *Service) view(ctx context.Context, org string, a domain.Agent, now time.Time) (AgentTrust, error) {
	r, err := s.ensure(ctx, org, a, now)
	if err != nil {
		return AgentTrust{}, err
	}
	since := EvidenceSince(now, r.LevelSince)
	counts, err := s.cfg.Signals.Collect(ctx, org, a.ID, since, BurstSince(now, r.LevelSince))
	if err != nil {
		return AgentTrust{}, err
	}
	pcounts := counts
	if r.TrackingSince.Before(since) {
		if pcounts, err = s.cfg.Signals.Collect(ctx, org, a.ID, r.TrackingSince, now); err != nil {
			return AgentTrust{}, err
		}
	}
	var q *Quality
	if s.cfg.Quality != nil {
		if q, err = s.cfg.Quality(ctx, org, a.ID); err != nil {
			s.cfg.Log.Warn("trust: quality score unavailable", "agent", a.ID, "err", err)
			q = nil // a missing signal never counts against the agent
		}
	}
	recent, err := s.cfg.Store.ListProposals(ctx, org, a.ID, "", 50)
	if err != nil {
		return AgentTrust{}, err
	}
	in := Input{Level: a.Autonomy, Ceiling: roles.AutonomyCeiling(a.Role), Now: now, TrackingSince: r.TrackingSince, LevelSince: r.LevelSince,
		ProbationCounts: pcounts, Counts: counts, Quality: q}
	v := AgentTrust{AgentID: a.ID, Name: a.Name, Role: a.Role, Level: a.Autonomy, Ceiling: in.Ceiling, TrackingSince: r.TrackingSince,
		LevelSince: r.LevelSince, EvidenceSince: since, Counts: counts, Quality: q}
	for i := range recent {
		p := recent[i]
		switch {
		case p.Status == StatusPending && v.Pending == nil:
			v.Pending = &p
		case (p.Status == StatusAccepted || p.Status == StatusApplied || p.Status == StatusReverted) && v.LastChange == nil:
			v.LastChange = &p
		case p.Status == StatusDeclined && p.Kind == KindPromotion && p.FromLevel == a.Autonomy && p.ResolvedAt != nil && in.LastDeclined == nil:
			in.LastDeclined = p.ResolvedAt
		}
	}
	v.Assessment = Assess(in)
	v.CanRevert = v.LastChange != nil && revertable(*v.LastChange, a, now)
	return v, nil
}

func revertable(p Proposal, a domain.Agent, now time.Time) bool {
	return p.Status == StatusApplied && p.Auto && p.Kind == KindDemotion && a.Autonomy == p.ToLevel &&
		now.Sub(p.CreatedAt) <= RevertDays*24*time.Hour && Rank(p.FromLevel) <= Rank(roles.AutonomyCeiling(a.Role))
}

// Get returns the ladder of one agent.
func (s *Service) Get(ctx context.Context, agentID string) (Report, error) {
	org := s.org(ctx)
	a, err := s.cfg.Agents.GetAgent(ctx, org, agentID)
	if err != nil {
		return Report{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	v, err := s.view(ctx, org, a, now)
	if err != nil {
		return Report{}, err
	}
	return Report{Methodology: Describe(), GeneratedAt: now, Agents: []AgentTrust{v}}, nil
}

// Overview returns the ladder of every agent, pending proposals first.
func (s *Service) Overview(ctx context.Context) (Report, error) {
	org := s.org(ctx)
	agents, err := s.cfg.Agents.ListAgents(ctx, org)
	if err != nil {
		return Report{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	rep := Report{Methodology: Describe(), GeneratedAt: now, Agents: make([]AgentTrust, 0, len(agents))}
	for _, a := range agents {
		v, err := s.view(ctx, org, a, now)
		if err != nil {
			return Report{}, err
		}
		rep.Agents = append(rep.Agents, v)
	}
	sort.SliceStable(rep.Agents, func(i, j int) bool { return rep.Agents[i].Pending != nil && rep.Agents[j].Pending == nil })
	return rep, nil
}

// setLevel is the ONLY place that writes an agent's autonomy. Raising it needs a human decision (human=true) and
// never goes above the role's ceiling; the ladder's own evaluation can only lower it.
func (s *Service) setLevel(ctx context.Context, org string, a domain.Agent, to string, human bool, now time.Time) error {
	if Rank(to) == 0 {
		return fmt.Errorf("%w: unknown autonomy level %q", domain.ErrInvalid, to)
	}
	if Rank(to) > Rank(a.Autonomy) {
		if !human {
			return fmt.Errorf("%w: the trust ladder never raises autonomy without a human decision", domain.ErrForbidden)
		}
		if Rank(to) > Rank(roles.AutonomyCeiling(a.Role)) {
			return fmt.Errorf("%w: %s is above the %s ceiling of this role", domain.ErrForbidden, to, roles.AutonomyCeiling(a.Role))
		}
	}
	if err := s.cfg.SetAutonomy(ctx, org, a.ID, to); err != nil {
		return err
	}
	r, ok, err := s.cfg.Store.GetRecord(ctx, org, a.ID)
	if err != nil {
		return err
	}
	if !ok {
		r = Record{AgentID: a.ID, StartLevel: a.Autonomy, TrackingSince: now}
	}
	r.Level, r.LevelSince, r.UpdatedAt = to, now, now
	return s.cfg.Store.PutRecord(ctx, org, r)
}

func evidence(v AgentTrust) Evidence {
	return Evidence{Since: v.EvidenceSince, Counts: v.Counts, Quality: v.Quality, Criteria: v.Criteria, Triggers: v.Triggers,
		Ceiling: v.Ceiling, Version: MethodologyVersion}
}

func firedKeys(v AgentTrust, auto bool) []string {
	out := []string{}
	for _, t := range v.Triggers {
		if t.Fired && t.Auto == auto {
			out = append(out, t.Key)
		}
	}
	return out
}

// Evaluate looks at the evidence of one agent and acts on it: it applies an automatic demotion, raises a
// suggested demotion or promotion for a human, or supersedes a pending proposal the evidence no longer supports.
// It never raises autonomy. It returns the resulting view.
func (s *Service) Evaluate(ctx context.Context, agentID string) (AgentTrust, error) {
	org := s.org(ctx)
	a, err := s.cfg.Agents.GetAgent(ctx, org, agentID)
	if err != nil {
		return AgentTrust{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	v, err := s.view(ctx, org, a, now)
	if err != nil {
		return AgentTrust{}, err
	}
	switch v.Recommendation {
	case RecDemoteAuto:
		if err := s.supersedePending(ctx, org, a.ID, "an automatic demotion took place", now); err != nil {
			return v, err
		}
		if err := s.setLevel(ctx, org, a, v.Target, false, now); err != nil {
			return v, err
		}
		p := Proposal{ID: newID(), AgentID: a.ID, Kind: KindDemotion, FromLevel: a.Autonomy, ToLevel: v.Target, Status: StatusApplied, Auto: true,
			Reasons: firedKeys(v, true), Evidence: evidence(v), CreatedAt: now}
		if err := s.cfg.Store.CreateProposal(ctx, org, p); err != nil {
			return v, err
		}
		s.changed(ctx, org, "system", p, "auto_demotion")
	case RecDemote, RecPromote:
		kind := map[string]string{RecDemote: KindDemotion, RecPromote: KindPromotion}[v.Recommendation]
		if v.Pending != nil && v.Pending.Kind == kind && v.Pending.ToLevel == v.Target {
			break // already waiting for a human
		}
		if err := s.supersedePending(ctx, org, a.ID, "the evidence changed", now); err != nil {
			return v, err
		}
		reasons := []string{"criteria_met"}
		if kind == KindDemotion {
			reasons = firedKeys(v, false)
		}
		p := Proposal{ID: newID(), AgentID: a.ID, Kind: kind, FromLevel: a.Autonomy, ToLevel: v.Target, Status: StatusPending,
			Reasons: reasons, Evidence: evidence(v), CreatedAt: now}
		if err := s.cfg.Store.CreateProposal(ctx, org, p); err != nil {
			return v, err
		}
		s.audit(ctx, org, domain.AuditLog{Actor: "system", Action: "trust." + kind + "_suggested", Entity: "agent", EntityID: a.ID,
			Details: map[string]any{"proposal_id": p.ID, "from": p.FromLevel, "to": p.ToLevel, "reasons": p.Reasons,
				"decisions": v.Counts.Decisions(), "approved": v.Counts.Approved, "methodology_version": MethodologyVersion}})
		s.emit(ctx, org, application.Action{Type: domain.EvTrustProposal, AgentID: a.ID, Entity: "trust_proposal", EntityID: p.ID,
			Payload: map[string]any{"proposal_id": p.ID, "agent_id": a.ID, "kind": kind, "status": StatusPending, "from": p.FromLevel, "to": p.ToLevel}})
	default:
		if v.Pending != nil {
			if err := s.supersedePending(ctx, org, a.ID, "the evidence no longer supports it", now); err != nil {
				return v, err
			}
		}
	}
	a, err = s.cfg.Agents.GetAgent(ctx, org, agentID)
	if err != nil {
		return AgentTrust{}, err
	}
	return s.view(ctx, org, a, now)
}

// changed audits and announces a change of level.
func (s *Service) changed(ctx context.Context, org, actor string, p Proposal, source string) {
	dir := "down"
	if Rank(p.ToLevel) > Rank(p.FromLevel) {
		dir = "up"
	}
	from, to := p.FromLevel, p.ToLevel
	if p.Status == StatusReverted {
		from, to, dir = p.ToLevel, p.FromLevel, "up"
	}
	s.audit(ctx, org, domain.AuditLog{Actor: actor, Action: "agent.autonomy_changed", Entity: "agent", EntityID: p.AgentID,
		Details: map[string]any{"from": from, "to": to, "direction": dir, "source": "trust." + source, "proposal_id": p.ID, "auto": p.Auto && source == "auto_demotion",
			"reasons": p.Reasons, "decisions": p.Evidence.Counts.Decisions(), "approved": p.Evidence.Counts.Approved, "methodology_version": p.Evidence.Version}})
	s.emit(ctx, org, application.Action{Type: domain.EvAutonomyChanged, AgentID: p.AgentID, Entity: "agent", EntityID: p.AgentID,
		Payload: map[string]any{"agent_id": p.AgentID, "from": from, "to": to, "direction": dir, "source": source, "proposal_id": p.ID, "reasons": p.Reasons}})
}

// isAgentPrincipal mirrors application's rule: agents and system principals never decide anything for humans.
func isAgentPrincipal(actor, agentID string) bool {
	actor = strings.ToLower(strings.TrimSpace(actor))
	return actor == "" || actor == "system" || actor == "orchestrator" || strings.HasPrefix(actor, "agent:") ||
		(agentID != "" && actor == strings.ToLower(agentID))
}

// MinDeciderRole is the least role that may accept, decline or revert a change of level.
const MinDeciderRole = "admin"

// decider checks who is deciding. Without auth the demo acts as owner (like approvals).
func (s *Service) decider(ctx context.Context, org string, p Proposal, action string) (string, error) {
	actor := application.ActorFrom(ctx, "")
	role := application.ActorRoleFrom(ctx, policy.RoleOwner)
	why := ""
	switch {
	case isAgentPrincipal(actor, p.AgentID):
		why = "agents_cannot_decide"
	case policy.RoleRank(role) < policy.RoleRank(MinDeciderRole):
		why = "role_too_low"
	}
	if why != "" {
		s.audit(ctx, org, domain.AuditLog{Actor: actor, Action: "trust.decision_refused", Entity: "trust_proposal", EntityID: p.ID,
			Details: map[string]any{"reason": why, "action": action, "agent_id": p.AgentID, "kind": p.Kind}})
		return "", fmt.Errorf("%w: only a person with the %s role or higher can decide a change of autonomy", domain.ErrForbidden, MinDeciderRole)
	}
	return actor, nil
}

func (s *Service) blocked(ctx context.Context, org, agentID string) error {
	if s.cfg.Blocked == nil {
		return nil
	}
	if code := s.cfg.Blocked(ctx, org, agentID); code != "" {
		return BlockedError{Code: code}
	}
	return nil
}

// Accept applies a pending proposal. A promotion is re-checked against the current evidence, the role's ceiling and
// the controls (kill switch, read-only mode, paused agent) before it is applied.
func (s *Service) Accept(ctx context.Context, id, note string) (Proposal, error) {
	org := s.org(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.cfg.Store.GetProposal(ctx, org, id)
	if err != nil {
		return Proposal{}, err
	}
	actor, err := s.decider(ctx, org, p, "accept")
	if err != nil {
		return Proposal{}, err
	}
	if p.Status != StatusPending {
		return Proposal{}, fmt.Errorf("%w: proposal is %s", domain.ErrConflict, p.Status)
	}
	a, err := s.cfg.Agents.GetAgent(ctx, org, p.AgentID)
	if err != nil {
		return Proposal{}, err
	}
	now := s.now()
	if p.Kind == KindPromotion {
		if err := s.blocked(ctx, org, a.ID); err != nil {
			return Proposal{}, err
		}
	}
	v, err := s.view(ctx, org, a, now) // also supersedes it when the level changed elsewhere
	if err != nil {
		return Proposal{}, err
	}
	want := map[string]string{KindPromotion: RecPromote, KindDemotion: RecDemote}[p.Kind]
	stillValid := a.Autonomy == p.FromLevel && v.Target == p.ToLevel &&
		(v.Recommendation == want || (p.Kind == KindDemotion && v.Recommendation == RecDemoteAuto))
	if !stillValid {
		_, _ = s.cfg.Store.ResolveProposal(ctx, org, p.ID, StatusPending, StatusSuperseded, "system", "the evidence changed before the decision", now)
		return Proposal{}, ErrEvidenceChanged
	}
	note = clip(note, 500)
	p, err = s.cfg.Store.ResolveProposal(ctx, org, p.ID, StatusPending, StatusAccepted, actor, note, now)
	if err != nil {
		return Proposal{}, err
	}
	if err := s.setLevel(ctx, org, a, p.ToLevel, true, now); err != nil {
		_, _ = s.cfg.Store.ResolveProposal(context.WithoutCancel(ctx), org, p.ID, StatusAccepted, StatusPending, "", "", now)
		return Proposal{}, err
	}
	s.changed(ctx, org, actor, p, p.Kind+"_accepted")
	return p, nil
}

// Decline rejects a pending proposal. A declined promotion is not suggested again at the same level for
// DeclineCooldownDays.
func (s *Service) Decline(ctx context.Context, id, note string) (Proposal, error) {
	org := s.org(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.cfg.Store.GetProposal(ctx, org, id)
	if err != nil {
		return Proposal{}, err
	}
	actor, err := s.decider(ctx, org, p, "decline")
	if err != nil {
		return Proposal{}, err
	}
	p, err = s.cfg.Store.ResolveProposal(ctx, org, id, StatusPending, StatusDeclined, actor, clip(note, 500), s.now())
	if err != nil {
		return Proposal{}, err
	}
	s.audit(ctx, org, domain.AuditLog{Actor: actor, Action: "trust.proposal_declined", Entity: "agent", EntityID: p.AgentID,
		Details: map[string]any{"proposal_id": p.ID, "kind": p.Kind, "from": p.FromLevel, "to": p.ToLevel}})
	s.emit(ctx, org, application.Action{Type: domain.EvTrustProposal, AgentID: p.AgentID, Entity: "trust_proposal", EntityID: p.ID,
		Payload: map[string]any{"proposal_id": p.ID, "agent_id": p.AgentID, "kind": p.Kind, "status": StatusDeclined}})
	return p, nil
}

// Revert undoes an automatic demotion (a false alarm). It is a raise of autonomy, so it needs a human with the
// decider role, is refused under the kill switch, read-only mode or a paused agent, never exceeds the ceiling and
// is only possible for RevertDays while the agent is still at the demoted level.
func (s *Service) Revert(ctx context.Context, id, note string) (Proposal, error) {
	org := s.org(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.cfg.Store.GetProposal(ctx, org, id)
	if err != nil {
		return Proposal{}, err
	}
	actor, err := s.decider(ctx, org, p, "revert")
	if err != nil {
		return Proposal{}, err
	}
	a, err := s.cfg.Agents.GetAgent(ctx, org, p.AgentID)
	if err != nil {
		return Proposal{}, err
	}
	now := s.now()
	if !revertable(p, a, now) {
		return Proposal{}, fmt.Errorf("%w: only an automatic demotion of the last %d days, with the agent still at that level, can be reverted", domain.ErrConflict, RevertDays)
	}
	if err := s.blocked(ctx, org, a.ID); err != nil {
		return Proposal{}, err
	}
	p, err = s.cfg.Store.ResolveProposal(ctx, org, id, StatusApplied, StatusReverted, actor, clip(note, 500), now)
	if err != nil {
		return Proposal{}, err
	}
	if err := s.setLevel(ctx, org, a, p.FromLevel, true, now); err != nil {
		_, _ = s.cfg.Store.ResolveProposal(context.WithoutCancel(ctx), org, id, StatusReverted, StatusApplied, "", "", now)
		return Proposal{}, err
	}
	s.changed(ctx, org, actor, p, "demotion_reverted")
	return p, nil
}

// Proposals returns the history of one agent (newest first).
func (s *Service) Proposals(ctx context.Context, agentID string, limit int) ([]Proposal, error) {
	org := s.org(ctx)
	if _, err := s.cfg.Agents.GetAgent(ctx, org, agentID); err != nil {
		return nil, err
	}
	return s.cfg.Store.ListProposals(ctx, org, agentID, "", min(max(limit, 1), 200))
}

// Reset (demo reset) puts every agent the ladder moved back at the level it had when tracking started and forgets
// the records and proposals, so a reset demo starts from the seed again.
func (s *Service) Reset(ctx context.Context) error {
	org := s.org(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	recs, err := s.cfg.Store.ListRecords(ctx, org)
	if err != nil {
		return err
	}
	restored := 0
	for _, r := range recs {
		a, err := s.cfg.Agents.GetAgent(ctx, org, r.AgentID)
		if err != nil || r.StartLevel == "" || a.Autonomy != r.Level || r.Level == r.StartLevel {
			continue
		}
		if err := s.cfg.SetAutonomy(ctx, org, a.ID, r.StartLevel); err != nil {
			return err
		}
		restored++
	}
	if err := s.cfg.Store.Reset(ctx, org); err != nil {
		return err
	}
	s.audit(ctx, org, domain.AuditLog{Actor: application.ActorFrom(ctx, "system"), Action: "trust.reset", Entity: "org", EntityID: org,
		Details: map[string]any{"restored_agents": restored}})
	return nil
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
