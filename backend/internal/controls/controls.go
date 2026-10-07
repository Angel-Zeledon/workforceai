// Package controls implements the emergency and governance switches of
// docs/architecture/integrations-credentials.md section 14: global kill switch
// (freeze / lockdown), read-only mode, agent pause and plan-review settings.
// Enforcement points ask Service.Check / Service.Admit; every error denies.
package controls

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Kill switch levels.
const (
	LevelNone     = "none"
	LevelFreeze   = "freeze"
	LevelLockdown = "lockdown"
)

// Org modes.
const (
	ModeNormal   = "normal"
	ModeReadOnly = "read_only"
)

// Agent control states.
const (
	AgentActive = "active"
	AgentPaused = "paused"
)

// Drain modes of a paused agent: graceful lets the call in flight finish (no
// new work starts); immediate stops everything, tool calls included.
const (
	DrainGraceful  = "graceful"
	DrainImmediate = "immediate"
)

// Plan review settings (owner decision 7: default touches_writes).
const (
	PlanReviewAlways        = "always"
	PlanReviewTouchesWrites = "touches_writes"
	PlanReviewNever         = "never"
)

// Deny codes.
const (
	CodeKillSwitch   = "kill_switch_active"
	CodeAgentPaused  = "agent_paused"
	CodeReadOnly     = "read_only_mode"
	CodeUnavailable  = "controls_unavailable"
	CodeToolDisabled = "tool_disabled"
)

var (
	ErrInvalid  = errors.New("controls: invalid input")
	ErrNoReason = errors.New("controls: a written reason is required")
)

// Settings are the organization-level governance settings.
type Settings struct {
	PlanReview string `json:"plan_review"`
}

// OrgState is the persisted state of an organization's controls.
type OrgState struct {
	Mode            string     `json:"mode"`
	KillSwitchLevel string     `json:"kill_switch_level"`
	Reason          string     `json:"reason"`
	SetBy           string     `json:"set_by"`
	SetAt           *time.Time `json:"set_at"`
	Settings        Settings   `json:"settings"`
	// DisabledTools is the per-tool kill switch: "email" blocks every action of
	// the tool, "email.send" only that action.
	DisabledTools []string `json:"disabled_tools"`
}

// AgentControl is the per-agent switch state.
type AgentControl struct {
	AgentID     string     `json:"agent_id"`
	Control     string     `json:"control"` // active | paused
	Drain       string     `json:"drain,omitempty"`
	ReadOnly    bool       `json:"read_only"`
	PausedBy    string     `json:"paused_by"`
	PausedAt    *time.Time `json:"paused_at"`
	PauseReason string     `json:"reason"`
}

// Store persists controls.
type Store interface {
	GetOrg(ctx context.Context, org string) (OrgState, error) // defaults when absent
	PutOrg(ctx context.Context, org string, s OrgState) error
	GetAgent(ctx context.Context, org, agentID string) (AgentControl, error) // defaults when absent
	PutAgent(ctx context.Context, org string, a AgentControl) error
	ListAgents(ctx context.Context, org string) ([]AgentControl, error)
}

// Verdict is the answer of an enforcement-point check.
type Verdict struct {
	Allowed bool
	Code    string
}

// Hooks let other packages react to control changes without import cycles.
type Hooks struct {
	// OnKillSwitch runs after the kill switch was set (lockdown: suspend
	// connections, cancel held actions).
	OnKillSwitch func(ctx context.Context, org, level, actor string)
	// OnRelease runs after the kill switch was lifted.
	OnRelease func(ctx context.Context, org, actor string, resumeConnections bool)
	// OnAgentPause runs when an agent is paused ("immediate" cancels in-flight work).
	OnAgentPause func(ctx context.Context, org, agentID, drain string)
}

// AuditFunc and EmitFunc mirror the connections package.
type (
	AuditFunc func(ctx context.Context, org, actor, action, entity, entityID string, details map[string]any)
	EmitFunc  func(ctx context.Context, org, eventType string, payload map[string]any)
)

// Service is the controls use case.
type Service struct {
	Store Store
	Audit AuditFunc
	Emit  EmitFunc
	Hooks Hooks
	Now   func() time.Time

	envLevel atomic.Value // string: KILL_SWITCH from the environment (process-wide)
	mu       sync.Mutex
}

// New builds a Service and loads KILL_SWITCH from the environment.
func New(st Store, audit AuditFunc, emit EmitFunc) *Service {
	s := &Service{Store: st, Audit: audit, Emit: emit, Now: time.Now}
	if s.Audit == nil {
		s.Audit = func(context.Context, string, string, string, string, string, map[string]any) {}
	}
	if s.Emit == nil {
		s.Emit = func(context.Context, string, string, map[string]any) {}
	}
	s.ReloadEnv()
	return s
}

// ReloadEnv re-reads KILL_SWITCH=freeze|lockdown (boot and SIGHUP). It is a
// process-wide override that no API call can lift: unset the variable and
// reload to release it.
func (s *Service) ReloadEnv() string {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("KILL_SWITCH")))
	if v != LevelFreeze && v != LevelLockdown {
		v = LevelNone
	}
	s.envLevel.Store(v)
	return v
}

// SetEnvLevel sets the process-wide override directly (tests, ctl command).
func (s *Service) SetEnvLevel(level string) { s.envLevel.Store(level) }

func (s *Service) env() string {
	if v, ok := s.envLevel.Load().(string); ok {
		return v
	}
	return LevelNone
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// State returns the org controls.
func (s *Service) State(ctx context.Context, org string) (OrgState, error) {
	st, err := s.Store.GetOrg(ctx, org)
	if err != nil {
		return OrgState{}, err
	}
	norm(&st)
	if e := s.env(); e != LevelNone && rank(e) > rank(st.KillSwitchLevel) {
		st.KillSwitchLevel, st.Reason = e, "KILL_SWITCH environment variable"
	}
	return st, nil
}

func norm(st *OrgState) {
	if st.DisabledTools == nil {
		st.DisabledTools = []string{}
	}
	if st.Mode == "" {
		st.Mode = ModeNormal
	}
	if st.KillSwitchLevel == "" {
		st.KillSwitchLevel = LevelNone
	}
	if st.Settings.PlanReview == "" {
		st.Settings.PlanReview = PlanReviewTouchesWrites
	}
}

func rank(l string) int {
	switch l {
	case LevelFreeze:
		return 1
	case LevelLockdown:
		return 2
	}
	return 0
}

// Check is the single enforcement question for anything that touches a
// connection: may agentID run an action (with or without side effects)?
// Any error denies (fail closed).
func (s *Service) Check(ctx context.Context, org, agentID string, sideEffects bool) Verdict {
	st, err := s.State(ctx, org)
	if err != nil {
		return Verdict{Code: CodeUnavailable}
	}
	if st.KillSwitchLevel != LevelNone {
		return Verdict{Code: CodeKillSwitch}
	}
	var ac AgentControl
	if agentID != "" {
		if ac, err = s.Store.GetAgent(ctx, org, agentID); err != nil {
			return Verdict{Code: CodeUnavailable}
		}
		if ac.Control == AgentPaused && ac.Drain != DrainGraceful {
			return Verdict{Code: CodeAgentPaused}
		}
	}
	if sideEffects && (st.Mode == ModeReadOnly || ac.ReadOnly) {
		return Verdict{Code: CodeReadOnly}
	}
	return Verdict{Allowed: true}
}

// CheckTool is Check plus the per-tool kill switch.
func (s *Service) CheckTool(ctx context.Context, org, agentID, tool, action string, sideEffects bool) Verdict {
	if v := s.Check(ctx, org, agentID, sideEffects); !v.Allowed {
		return v
	}
	if s.ToolBlocked(ctx, org, tool, action) {
		return Verdict{Code: CodeToolDisabled}
	}
	return Verdict{Allowed: true}
}

// ToolBlocked reports whether a tool (or tool.action) is switched off. Errors block.
func (s *Service) ToolBlocked(ctx context.Context, org, tool, action string) bool {
	st, err := s.State(ctx, org)
	if err != nil {
		return true
	}
	tool, action = strings.ToLower(tool), strings.ToLower(action)
	for _, d := range st.DisabledTools {
		if d == tool || (action != "" && d == tool+"."+action) {
			return true
		}
	}
	return false
}

// SetToolDisabled switches a tool (or "tool.action") off or on. Switching off
// needs controls:killswitch; switching on needs controls:release (API).
func (s *Service) SetToolDisabled(ctx context.Context, org, tool string, disabled bool, reason, actor string) (OrgState, error) {
	tool = strings.ToLower(strings.TrimSpace(tool))
	if tool == "" || len(tool) > 80 || strings.ContainsAny(tool, " /\\") {
		return OrgState{}, fmt.Errorf("%w: invalid tool name", ErrInvalid)
	}
	s.mu.Lock()
	st, err := s.Store.GetOrg(ctx, org)
	if err != nil {
		s.mu.Unlock()
		return OrgState{}, err
	}
	norm(&st)
	var next []string
	for _, d := range st.DisabledTools {
		if d != tool {
			next = append(next, d)
		}
	}
	if disabled {
		next = append(next, tool)
	}
	st.DisabledTools = next
	if st.DisabledTools == nil {
		st.DisabledTools = []string{}
	}
	if err := s.Store.PutOrg(ctx, org, st); err != nil {
		s.mu.Unlock()
		return OrgState{}, err
	}
	s.mu.Unlock()
	s.changed(ctx, org, actor, "control.tool", st, map[string]any{"tool": tool, "disabled": disabled, "tool_reason": reason})
	return st, nil
}

// Admit decides whether an agent may START new work (scheduler claim):
// kill switch, paused or draining agents do not start tasks.
func (s *Service) Admit(ctx context.Context, org, agentID string) Verdict {
	v := s.Check(ctx, org, agentID, false)
	if !v.Allowed {
		return v
	}
	if agentID != "" {
		if ac, err := s.Store.GetAgent(ctx, org, agentID); err != nil {
			return Verdict{Code: CodeUnavailable}
		} else if ac.Control == AgentPaused {
			return Verdict{Code: CodeAgentPaused}
		}
	}
	return Verdict{Allowed: true}
}

// SideEffectsBlocked reports whether side-effect actions of agentID are
// currently denied by read-only mode (org or agent); errors block.
func (s *Service) SideEffectsBlocked(ctx context.Context, org, agentID string) bool {
	v := s.Check(ctx, org, agentID, true)
	return !v.Allowed && v.Code == CodeReadOnly
}

func (s *Service) changed(ctx context.Context, org, actor, action string, st OrgState, extra map[string]any) {
	p := map[string]any{"scope": "org", "mode": st.Mode, "kill_switch_level": st.KillSwitchLevel, "reason": st.Reason, "by": actor, "controls": st}
	for k, v := range extra {
		p[k] = v
	}
	s.Audit(ctx, org, actor, action, "org", org, p)
	s.Emit(ctx, org, "control.changed", p)
}

// SetMode switches read-only mode on/off. Turning it ON needs controls:pause;
// turning it OFF needs controls:release: the API enforces that.
func (s *Service) SetMode(ctx context.Context, org, mode, actor, reason string) (OrgState, error) {
	if mode != ModeNormal && mode != ModeReadOnly {
		return OrgState{}, fmt.Errorf("%w: mode must be normal or read_only", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.Store.GetOrg(ctx, org)
	if err != nil {
		return OrgState{}, err
	}
	norm(&st)
	if st.Mode == mode {
		return st, nil
	}
	now := s.now()
	st.Mode = mode
	if st.KillSwitchLevel == LevelNone {
		st.Reason, st.SetBy, st.SetAt = reason, actor, &now
	}
	if err := s.Store.PutOrg(ctx, org, st); err != nil {
		return OrgState{}, err
	}
	s.changed(ctx, org, actor, "control.mode", st, nil)
	return st, nil
}

// SetSettings updates governance settings (plan review).
func (s *Service) SetSettings(ctx context.Context, org string, set Settings, actor string) (OrgState, error) {
	switch set.PlanReview {
	case "", PlanReviewAlways, PlanReviewTouchesWrites, PlanReviewNever:
	default:
		return OrgState{}, fmt.Errorf("%w: plan_review must be always, touches_writes or never", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.Store.GetOrg(ctx, org)
	if err != nil {
		return OrgState{}, err
	}
	norm(&st)
	if set.PlanReview != "" {
		st.Settings.PlanReview = set.PlanReview
	}
	if err := s.Store.PutOrg(ctx, org, st); err != nil {
		return OrgState{}, err
	}
	s.changed(ctx, org, actor, "control.settings", st, nil)
	return st, nil
}

// KillSwitch activates freeze or lockdown. Anyone with controls:killswitch
// may activate (activating is easy; lifting is deliberate).
func (s *Service) KillSwitch(ctx context.Context, org, level, reason, actor string) (OrgState, error) {
	if level != LevelFreeze && level != LevelLockdown {
		return OrgState{}, fmt.Errorf("%w: level must be freeze or lockdown", ErrInvalid)
	}
	s.mu.Lock()
	st, err := s.Store.GetOrg(ctx, org)
	if err != nil {
		s.mu.Unlock()
		return OrgState{}, err
	}
	norm(&st)
	now := s.now()
	st.KillSwitchLevel, st.Reason, st.SetBy, st.SetAt = level, reason, actor, &now
	if err := s.Store.PutOrg(ctx, org, st); err != nil {
		s.mu.Unlock()
		return OrgState{}, err
	}
	s.mu.Unlock()
	s.changed(ctx, org, actor, "control.kill_switch", st, nil)
	if s.Hooks.OnKillSwitch != nil {
		s.Hooks.OnKillSwitch(ctx, org, level, actor)
	}
	return st, nil
}

// Release lifts the kill switch. Only owners may call it (API) and a written
// reason is mandatory. After a lockdown the connections are resumed only if
// resumeConnections is true.
func (s *Service) Release(ctx context.Context, org, reason, actor string, resumeConnections bool) (OrgState, error) {
	if strings.TrimSpace(reason) == "" {
		return OrgState{}, ErrNoReason
	}
	s.mu.Lock()
	st, err := s.Store.GetOrg(ctx, org)
	if err != nil {
		s.mu.Unlock()
		return OrgState{}, err
	}
	norm(&st)
	was := st.KillSwitchLevel
	now := s.now()
	st.KillSwitchLevel, st.Reason, st.SetBy, st.SetAt = LevelNone, reason, actor, &now
	if err := s.Store.PutOrg(ctx, org, st); err != nil {
		s.mu.Unlock()
		return OrgState{}, err
	}
	s.mu.Unlock()
	s.changed(ctx, org, actor, "control.release", st, map[string]any{"released_level": was, "resume_connections": resumeConnections})
	if s.Hooks.OnRelease != nil {
		s.Hooks.OnRelease(ctx, org, actor, resumeConnections)
	}
	if e := s.env(); e != LevelNone {
		// the environment override still holds: report it
		st.KillSwitchLevel = e
	}
	return st, nil
}

// PauseAgent pauses an agent ("graceful" = finish the current call, then stop;
// "immediate" = no further calls at all).
func (s *Service) PauseAgent(ctx context.Context, org, agentID, drain, reason, actor string) (AgentControl, error) {
	if agentID == "" {
		return AgentControl{}, fmt.Errorf("%w: agent id required", ErrInvalid)
	}
	ac, err := s.Store.GetAgent(ctx, org, agentID)
	if err != nil {
		return AgentControl{}, err
	}
	now := s.now()
	ac.AgentID, ac.PausedBy, ac.PausedAt, ac.PauseReason = agentID, actor, &now, reason
	ac.Control, ac.Drain = AgentPaused, DrainGraceful // new work stops; the current call may finish
	if drain == DrainImmediate {
		ac.Drain = DrainImmediate
	}
	if err := s.Store.PutAgent(ctx, org, ac); err != nil {
		return AgentControl{}, err
	}
	p := map[string]any{"agent_id": agentID, "control": ac.Control, "by": actor, "reason": reason}
	s.Audit(ctx, org, actor, "agent.control_changed", "agent", agentID, p)
	s.Emit(ctx, org, "agent.control_changed", p)
	if s.Hooks.OnAgentPause != nil {
		s.Hooks.OnAgentPause(ctx, org, agentID, drain)
	}
	return ac, nil
}

// ResumeAgent resumes a paused agent.
func (s *Service) ResumeAgent(ctx context.Context, org, agentID, actor string) (AgentControl, error) {
	ac, err := s.Store.GetAgent(ctx, org, agentID)
	if err != nil {
		return AgentControl{}, err
	}
	ac.AgentID, ac.Control, ac.Drain, ac.PausedBy, ac.PausedAt, ac.PauseReason = agentID, AgentActive, "", "", nil, ""
	if err := s.Store.PutAgent(ctx, org, ac); err != nil {
		return AgentControl{}, err
	}
	p := map[string]any{"agent_id": agentID, "control": AgentActive, "by": actor}
	s.Audit(ctx, org, actor, "agent.control_changed", "agent", agentID, p)
	s.Emit(ctx, org, "agent.control_changed", p)
	return ac, nil
}

// SetAgentReadOnly toggles the per-agent read-only switch.
func (s *Service) SetAgentReadOnly(ctx context.Context, org, agentID string, ro bool, actor string) (AgentControl, error) {
	ac, err := s.Store.GetAgent(ctx, org, agentID)
	if err != nil {
		return AgentControl{}, err
	}
	ac.AgentID, ac.ReadOnly = agentID, ro
	if err := s.Store.PutAgent(ctx, org, ac); err != nil {
		return AgentControl{}, err
	}
	p := map[string]any{"agent_id": agentID, "control": ac.Control, "read_only": ro, "by": actor}
	s.Audit(ctx, org, actor, "agent.control_changed", "agent", agentID, p)
	s.Emit(ctx, org, "agent.control_changed", p)
	return ac, nil
}

// Agents lists non-default agent control rows.
func (s *Service) Agents(ctx context.Context, org string) ([]AgentControl, error) {
	l, err := s.Store.ListAgents(ctx, org)
	if l == nil {
		l = []AgentControl{}
	}
	return l, err
}

// Agent returns one agent's control state.
func (s *Service) Agent(ctx context.Context, org, id string) (AgentControl, error) {
	ac, err := s.Store.GetAgent(ctx, org, id)
	ac.AgentID = id
	if ac.Control == "" {
		ac.Control = AgentActive
	}
	return ac, err
}

// MemStore is an in-memory Store.
type MemStore struct {
	mu     sync.Mutex
	orgs   map[string]OrgState
	agents map[string]AgentControl
	// Fail makes every call fail (tests: fail-closed behaviour).
	Fail error
}

// NewMemStore creates an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{orgs: map[string]OrgState{}, agents: map[string]AgentControl{}}
}

func (m *MemStore) GetOrg(_ context.Context, org string) (OrgState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return OrgState{}, m.Fail
	}
	s := m.orgs[org]
	norm(&s)
	return s, nil
}

func (m *MemStore) PutOrg(_ context.Context, org string, s OrgState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	m.orgs[org] = s
	return nil
}

func (m *MemStore) GetAgent(_ context.Context, org, id string) (AgentControl, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return AgentControl{}, m.Fail
	}
	a, ok := m.agents[org+"/"+id]
	if !ok {
		return AgentControl{AgentID: id, Control: AgentActive}, nil
	}
	return a, nil
}

func (m *MemStore) PutAgent(_ context.Context, org string, a AgentControl) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	m.agents[org+"/"+a.AgentID] = a
	return nil
}

func (m *MemStore) ListAgents(_ context.Context, org string) ([]AgentControl, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AgentControl
	for k, a := range m.agents {
		if strings.HasPrefix(k, org+"/") {
			out = append(out, a)
		}
	}
	return out, nil
}
