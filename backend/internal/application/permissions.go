package application

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/policy"
	"aiworkforce/backend/internal/roles"
)

// Approval permissions per agent (docs/architecture/06-permisos-autonomia.md section 11): the level in plain words,
// exceptions per kind of action and the safety floor. They are stored in org_settings.rules.permissions and applied
// by PolicyService as rules of the same engine. This service is who may change them and how:
//
//   - levels and exceptions: admins and owners, or the person responsible for the agent's team (IsTeamLead);
//   - lifting the safety floor: only the account owner, per agent and category, confirming the risk;
//   - agents and system principals never change anything; every change is audited.

// Plain levels.
const (
	LevelAskAll    = "ask_all"   // approve_each: asks for everything
	LevelImportant = "important" // rules + factory rules: only what leaves the company or cannot be undone
	LevelAlone     = "alone"     // autonomous: nothing but the safety floor
	LevelSuggest   = "suggest"   // advanced: only suggests
)

var plainToAutonomy = map[string]string{LevelAskAll: "approve_each", LevelImportant: "rules", LevelAlone: "autonomous", LevelSuggest: "suggest"}

// PlainLevel maps an autonomy level to its plain name ("" for an unknown one).
func PlainLevel(autonomy string) string {
	for p, a := range plainToAutonomy {
		if a == autonomy {
			return p
		}
	}
	return ""
}

// AutonomyOfLevel maps a plain level to the autonomy level ("" for an unknown one).
func AutonomyOfLevel(plain string) string { return plainToAutonomy[plain] }

var autonomyOrder = map[string]int{"suggest": 1, "approve_each": 2, "rules": 3, "autonomous": 4}

// isPermissionRule reports whether a verdict comes from the approval permissions of the agent.
func isPermissionRule(id string) bool {
	return strings.HasPrefix(id, "perm.") || strings.HasPrefix(id, "limit:perm.")
}

// PermissionsService manages the approval permissions.
type PermissionsService struct {
	Store  ConfigStore
	Agents interface {
		GetAgent(ctx context.Context, org, id string) (domain.Agent, error)
		ListAgents(ctx context.Context, org string) ([]domain.Agent, error)
	}
	// SetLevel writes the agent's autonomy (trust.Service.ChooseLevel: ceiling, controls, audit). Required.
	SetLevel func(ctx context.Context, agentID, autonomy, actor string) error
	// IsTeamLead tells whether a person is responsible for the agent's team. Optional (nil = only admins).
	IsTeamLead func(ctx context.Context, org, userID, agentID string) bool
	// Blocked returns a controls code when autonomy must not be raised now. Optional (shown in the view only).
	Blocked func(ctx context.Context, org, agentID string) string
	// Approvals reads an approval ("approve and do not ask again"). Optional (nil = not offered).
	Approvals interface {
		GetApproval(ctx context.Context, orgID, id string) (domain.Approval, error)
	}
	// ProjectScope tells the batch of a project and whether the project still runs. Optional (nil = no project or
	// batch scopes).
	ProjectScope func(ctx context.Context, org, projectID string) (batchID string, active bool)
	Rec          *Recorder
	OrgID        string
	Now          func() time.Time
	Log          *slog.Logger

	mu sync.Mutex
}

func (s *PermissionsService) org(ctx context.Context) string { return OrgFrom(ctx, s.OrgID) }

func (s *PermissionsService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *PermissionsService) audit(ctx context.Context, action, agentID string, d map[string]any) {
	if s.Rec != nil {
		s.Rec.Audit(ctx, domain.AuditLog{Actor: ActorFrom(ctx, "system"), Action: action, Entity: "agent", EntityID: agentID, Details: d})
	}
}

// LevelOption is one choice of the level picker.
type LevelOption struct {
	Level    string `json:"level"`
	Autonomy string `json:"autonomy"`
	Allowed  bool   `json:"allowed"`
	// Reason when not allowed: role_ceiling, or a controls code (kill_switch_active, read_only_mode, agent_paused).
	Reason string `json:"reason,omitempty"`
}

// CategoryView is how one category is treated for one agent.
type CategoryView struct {
	Category  string            `json:"category"`
	Floor     bool              `json:"floor"`
	Lifted    *policy.FloorLift `json:"lifted,omitempty"`
	Exception *policy.Exception `json:"exception,omitempty"`
	// Effective is what happens now: ask | never | ask_if.
	Effective string `json:"effective"`
}

// AgentPermissionsView is GET /agents/{id}/permissions.
type AgentPermissionsView struct {
	AgentID  string `json:"agent_id"`
	Name     string `json:"name"`
	Autonomy string `json:"autonomy"`
	Level    string `json:"level"`
	// Factory is false for an agent that still uses the previous behaviour (no permissions chosen yet): its
	// exceptions and the safety floor apply only once a level is chosen.
	Factory      bool           `json:"factory"`
	Ceiling      string         `json:"ceiling"`
	Options      []LevelOption  `json:"options"`
	Categories   []CategoryView `json:"categories"`
	CanEdit      bool           `json:"can_edit"`
	CanLiftFloor bool           `json:"can_lift_floor"`
	UpdatedBy    string         `json:"updated_by,omitempty"`
	UpdatedAt    *time.Time     `json:"updated_at,omitempty"`
}

// AgentPermissionsSummary is one agent in the overview of Settings.
type AgentPermissionsSummary struct {
	AgentID    string             `json:"agent_id"`
	Name       string             `json:"name"`
	Level      string             `json:"level"`
	Factory    bool               `json:"factory"`
	Exceptions []policy.Exception `json:"exceptions"`
	FloorLifts []policy.FloorLift `json:"floor_lifts"`
}

// PermissionsOverview is GET /permissions.
type PermissionsOverview struct {
	DefaultLevel   string                    `json:"default_level"` // plain level, "" = the role's default
	Agents         []AgentPermissionsSummary `json:"agents"`
	Categories     []string                  `json:"categories"`
	Floor          []string                  `json:"floor"`
	CanEditDefault bool                      `json:"can_edit_default"`
	Scopes         []policy.ScopeGrant       `json:"scopes"`
}

// ExceptionInput is the body of PUT /agents/{id}/permissions/exceptions/{category}.
type ExceptionInput struct {
	Mode   string        `json:"mode"`
	If     *policy.AskIf `json:"if,omitempty"`
	Source string        `json:"source,omitempty"` // panel | settings | approval:<id>
}

func (s *PermissionsService) permissions(ctx context.Context, org string) (*policy.Permissions, error) {
	st, err := s.Store.GetOrgSettings(ctx, org)
	if err != nil {
		return nil, err
	}
	return st.Rules.Permissions, nil
}

// update applies fn to a copy of the permissions and stores them. Caller holds no lock.
func (s *PermissionsService) update(ctx context.Context, org string, fn func(p *policy.Permissions) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.Store.GetOrgSettings(ctx, org)
	if err != nil {
		return err
	}
	p := st.Rules.Permissions.Clone()
	if p == nil {
		p = &policy.Permissions{}
	}
	if p.Agents == nil {
		p.Agents = map[string]policy.AgentPermissions{}
	}
	if err := fn(p); err != nil {
		return err
	}
	if p.DefaultLevel == "" && len(p.Agents) == 0 && len(p.Scopes) == 0 {
		p = nil
	}
	st.Rules.Permissions = p
	if st.Rules.AlwaysApprove == nil {
		st.Rules.AlwaysApprove = []string{}
	}
	return s.Store.PutOrgSettings(ctx, org, st)
}

func isMachine(actor string) bool {
	a := strings.ToLower(strings.TrimSpace(actor))
	return a == "system" || a == "orchestrator" || strings.HasPrefix(a, "agent:")
}

// editable reports whether the acting person may change levels and exceptions of the agent.
func (s *PermissionsService) editable(ctx context.Context, org, agentID string) bool {
	actor := ActorFrom(ctx, "")
	if isMachine(actor) || strings.EqualFold(actor, agentID) {
		return false
	}
	if policy.RoleRank(ActorRoleFrom(ctx, policy.RoleOwner)) >= policy.RoleRank(policy.RoleAdmin) {
		return true
	}
	return actor != "" && s.IsTeamLead != nil && s.IsTeamLead(ctx, org, actor, agentID)
}

func (s *PermissionsService) ownerActing(ctx context.Context, agentID string) bool {
	actor := ActorFrom(ctx, "")
	return !isMachine(actor) && !strings.EqualFold(actor, agentID) &&
		strings.EqualFold(ActorRoleFrom(ctx, policy.RoleOwner), policy.RoleOwner)
}

func (s *PermissionsService) refuse(ctx context.Context, agentID, what, why string) error {
	s.audit(ctx, "permissions.refused", agentID, map[string]any{"what": what, "reason": why})
	return fmt.Errorf("%w: %s", domain.ErrForbidden, why)
}

func (s *PermissionsService) mayEdit(ctx context.Context, org, agentID, what string) error {
	if !s.editable(ctx, org, agentID) {
		return s.refuse(ctx, agentID, what, "only admins, the owner or the person responsible for the agent's office can change its permissions")
	}
	return nil
}

func effective(a domain.Agent, ap policy.AgentPermissions, factory bool, cat string) string {
	if factory && policy.IsFloor(cat) && !ap.Lifted(cat) {
		return policy.ModeAsk
	}
	switch a.Autonomy {
	case "suggest", "approve_each":
		return policy.ModeAsk
	}
	if e, ok := ap.Exception(cat); ok && factory {
		return e.Mode
	}
	if a.Autonomy == "autonomous" {
		return policy.ModeNever
	}
	if !factory { // the previous behaviour: rules runs everything but high risk and the always-approve list
		if cat == policy.KindContracts {
			return policy.ModeAsk
		}
		return policy.ModeNever
	}
	return policy.ModeAsk
}

func (s *PermissionsService) view(ctx context.Context, org string, a domain.Agent, p *policy.Permissions) AgentPermissionsView {
	ap, factory := p.For(a.ID)
	ceiling := roles.AutonomyCeiling(a.Role)
	v := AgentPermissionsView{AgentID: a.ID, Name: a.Name, Autonomy: a.Autonomy, Level: PlainLevel(a.Autonomy), Factory: factory, Ceiling: ceiling,
		CanEdit: s.editable(ctx, org, a.ID), CanLiftFloor: s.ownerActing(ctx, a.ID)}
	blocked := ""
	if s.Blocked != nil {
		blocked = s.Blocked(ctx, org, a.ID)
	}
	for _, l := range []string{LevelAskAll, LevelImportant, LevelAlone, LevelSuggest} {
		au := plainToAutonomy[l]
		o := LevelOption{Level: l, Autonomy: au, Allowed: true}
		switch {
		case autonomyOrder[au] > autonomyOrder[ceiling]:
			o.Allowed, o.Reason = false, "role_ceiling"
		case autonomyOrder[au] > autonomyOrder[a.Autonomy] && blocked != "":
			o.Allowed, o.Reason = false, blocked
		}
		v.Options = append(v.Options, o)
	}
	for _, c := range policy.PermissionCategories {
		cv := CategoryView{Category: c, Floor: policy.IsFloor(c), Effective: effective(a, ap, factory, c)}
		if e, ok := ap.Exception(c); ok {
			e := e
			cv.Exception = &e
		}
		for _, l := range ap.FloorLifts {
			if l.Category == c {
				l := l
				cv.Lifted = &l
			}
		}
		v.Categories = append(v.Categories, cv)
	}
	if factory {
		v.UpdatedBy = ap.UpdatedBy
		if !ap.UpdatedAt.IsZero() {
			t := ap.UpdatedAt
			v.UpdatedAt = &t
		}
	}
	return v
}

// View returns the permissions of one agent.
func (s *PermissionsService) View(ctx context.Context, agentID string) (AgentPermissionsView, error) {
	org := s.org(ctx)
	a, err := s.Agents.GetAgent(ctx, org, agentID)
	if err != nil {
		return AgentPermissionsView{}, err
	}
	p, err := s.permissions(ctx, org)
	if err != nil {
		return AgentPermissionsView{}, err
	}
	return s.view(ctx, org, a, p), nil
}

// Overview returns the organization default and the exceptions of every agent.
func (s *PermissionsService) Overview(ctx context.Context) (PermissionsOverview, error) {
	org := s.org(ctx)
	agents, err := s.Agents.ListAgents(ctx, org)
	if err != nil {
		return PermissionsOverview{}, err
	}
	p, err := s.permissions(ctx, org)
	if err != nil {
		return PermissionsOverview{}, err
	}
	out := PermissionsOverview{Agents: []AgentPermissionsSummary{}, Categories: policy.PermissionCategories, Floor: policy.FloorCategories,
		CanEditDefault: policy.RoleRank(ActorRoleFrom(ctx, policy.RoleOwner)) >= policy.RoleRank(policy.RoleAdmin) && !isMachine(ActorFrom(ctx, ""))}
	out.Scopes = []policy.ScopeGrant{}
	if p != nil {
		out.DefaultLevel = PlainLevel(p.DefaultLevel)
		now := s.now()
		for _, g := range p.Scopes {
			if now.Before(g.ExpiresAt) {
				out.Scopes = append(out.Scopes, g)
			}
		}
	}
	for _, a := range agents {
		ap, ok := p.For(a.ID)
		out.Agents = append(out.Agents, AgentPermissionsSummary{AgentID: a.ID, Name: a.Name, Level: PlainLevel(a.Autonomy), Factory: ok,
			Exceptions: append([]policy.Exception{}, ap.Exceptions...), FloorLifts: append([]policy.FloorLift{}, ap.FloorLifts...)})
	}
	return out, nil
}

func touch(ap policy.AgentPermissions, by string, now time.Time) policy.AgentPermissions {
	if ap.Since.IsZero() {
		ap.Since = now
	}
	ap.UpdatedBy, ap.UpdatedAt = by, now
	return ap
}

// ChooseLevel sets the agent's level (plain name) and turns on its factory rules.
func (s *PermissionsService) ChooseLevel(ctx context.Context, agentID, plain string) (AgentPermissionsView, error) {
	org := s.org(ctx)
	to := AutonomyOfLevel(plain)
	if to == "" {
		return AgentPermissionsView{}, fmt.Errorf("%w: unknown level %q", domain.ErrInvalid, plain)
	}
	if err := s.mayEdit(ctx, org, agentID, "level"); err != nil {
		return AgentPermissionsView{}, err
	}
	a, err := s.Agents.GetAgent(ctx, org, agentID)
	if err != nil {
		return AgentPermissionsView{}, err
	}
	if c := roles.AutonomyCeiling(a.Role); autonomyOrder[to] > autonomyOrder[c] {
		return AgentPermissionsView{}, fmt.Errorf("%w: %s is above the %s ceiling of this role", domain.ErrForbidden, to, c)
	}
	actor := ActorFrom(ctx, "user")
	if a.Autonomy != to {
		if err := s.SetLevel(ctx, agentID, to, actor); err != nil {
			return AgentPermissionsView{}, err
		}
	}
	from := a.Autonomy
	now := s.now()
	if err := s.update(ctx, org, func(p *policy.Permissions) error {
		ap := p.Agents[agentID]
		p.Agents[agentID] = touch(ap, actor, now)
		return nil
	}); err != nil {
		return AgentPermissionsView{}, err
	}
	s.audit(ctx, "permissions.level_changed", agentID, map[string]any{"from": from, "to": to, "level": plain})
	return s.View(ctx, agentID)
}

// SetException creates or replaces the exception of one category.
func (s *PermissionsService) SetException(ctx context.Context, agentID, cat string, in ExceptionInput) (AgentPermissionsView, error) {
	org := s.org(ctx)
	if err := s.mayEdit(ctx, org, agentID, "exception"); err != nil {
		return AgentPermissionsView{}, err
	}
	if _, err := s.Agents.GetAgent(ctx, org, agentID); err != nil {
		return AgentPermissionsView{}, err
	}
	actor, now := ActorFrom(ctx, "user"), s.now()
	src := strings.TrimSpace(in.Source)
	if src == "" {
		src = "panel"
	}
	ex := policy.Exception{Category: cat, Mode: in.Mode, If: in.If, CreatedBy: actor, CreatedAt: now, Source: clipStr(src, 80)}
	if in.Mode != policy.ModeAskIf {
		ex.If = nil
	}
	if err := ex.Validate(); err != nil {
		return AgentPermissionsView{}, fmt.Errorf("%w: %v", domain.ErrInvalid, err)
	}
	if err := s.update(ctx, org, func(p *policy.Permissions) error {
		ap, ok := p.Agents[agentID]
		if !ok {
			return fmt.Errorf("%w: choose_level_first: choose a level for this agent before adding exceptions", domain.ErrConflict)
		}
		ap.Exceptions = slices.DeleteFunc(ap.Exceptions, func(e policy.Exception) bool { return e.Category == cat })
		ap.Exceptions = append(ap.Exceptions, ex)
		p.Agents[agentID] = touch(ap, actor, now)
		return nil
	}); err != nil {
		return AgentPermissionsView{}, err
	}
	d := map[string]any{"category": cat, "mode": ex.Mode, "source": ex.Source}
	if ex.If != nil {
		d["per_day_over"], d["new_recipient"] = ex.If.PerDayOver, ex.If.NewRecipient
		if ex.If.AmountOver != nil {
			d["amount_over"] = *ex.If.AmountOver
		}
	}
	s.audit(ctx, "permissions.exception_set", agentID, d)
	return s.View(ctx, agentID)
}

// RemoveException removes the exception of one category (the level decides again).
func (s *PermissionsService) RemoveException(ctx context.Context, agentID, cat string) (AgentPermissionsView, error) {
	org := s.org(ctx)
	if err := s.mayEdit(ctx, org, agentID, "exception"); err != nil {
		return AgentPermissionsView{}, err
	}
	actor, now := ActorFrom(ctx, "user"), s.now()
	removed := false
	if err := s.update(ctx, org, func(p *policy.Permissions) error {
		ap, ok := p.Agents[agentID]
		if !ok {
			return nil
		}
		n := len(ap.Exceptions)
		ap.Exceptions = slices.DeleteFunc(ap.Exceptions, func(e policy.Exception) bool { return e.Category == cat })
		removed = len(ap.Exceptions) != n
		p.Agents[agentID] = touch(ap, actor, now)
		return nil
	}); err != nil {
		return AgentPermissionsView{}, err
	}
	if removed {
		s.audit(ctx, "permissions.exception_removed", agentID, map[string]any{"category": cat})
	}
	return s.View(ctx, agentID)
}

// LiftFloor lifts the safety floor of one category for one agent. Only the account owner, confirming the risk.
func (s *PermissionsService) LiftFloor(ctx context.Context, agentID, cat string, understood bool) (AgentPermissionsView, error) {
	org := s.org(ctx)
	if !s.ownerActing(ctx, agentID) {
		return AgentPermissionsView{}, s.refuse(ctx, agentID, "floor", "only the account owner can lift the safety floor")
	}
	if !policy.IsFloor(cat) {
		return AgentPermissionsView{}, fmt.Errorf("%w: %q is not a safety-floor category", domain.ErrInvalid, cat)
	}
	if !understood {
		return AgentPermissionsView{}, fmt.Errorf("%w: confirm that you understand the risk (understood=true)", domain.ErrInvalid)
	}
	if _, err := s.Agents.GetAgent(ctx, org, agentID); err != nil {
		return AgentPermissionsView{}, err
	}
	actor, now := ActorFrom(ctx, "owner"), s.now()
	if err := s.update(ctx, org, func(p *policy.Permissions) error {
		ap, ok := p.Agents[agentID]
		if !ok {
			return fmt.Errorf("%w: choose_level_first: choose a level for this agent first", domain.ErrConflict)
		}
		if !ap.Lifted(cat) {
			ap.FloorLifts = append(ap.FloorLifts, policy.FloorLift{Category: cat, By: actor, At: now})
		}
		p.Agents[agentID] = touch(ap, actor, now)
		return nil
	}); err != nil {
		return AgentPermissionsView{}, err
	}
	s.audit(ctx, "permissions.floor_lifted", agentID, map[string]any{"category": cat, "understood": true})
	return s.View(ctx, agentID)
}

// RestoreFloor puts the safety floor back (it only adds supervision: whoever may edit the agent can do it).
func (s *PermissionsService) RestoreFloor(ctx context.Context, agentID, cat string) (AgentPermissionsView, error) {
	org := s.org(ctx)
	if err := s.mayEdit(ctx, org, agentID, "floor"); err != nil {
		return AgentPermissionsView{}, err
	}
	actor, now := ActorFrom(ctx, "user"), s.now()
	restored := false
	if err := s.update(ctx, org, func(p *policy.Permissions) error {
		ap, ok := p.Agents[agentID]
		if !ok {
			return nil
		}
		n := len(ap.FloorLifts)
		ap.FloorLifts = slices.DeleteFunc(ap.FloorLifts, func(l policy.FloorLift) bool { return l.Category == cat })
		restored = n != len(ap.FloorLifts)
		p.Agents[agentID] = touch(ap, actor, now)
		return nil
	}); err != nil {
		return AgentPermissionsView{}, err
	}
	if restored {
		s.audit(ctx, "permissions.floor_restored", agentID, map[string]any{"category": cat})
	}
	return s.View(ctx, agentID)
}

// SetDefault sets the level new agents start at ("" = the role's default). Admins and owners.
func (s *PermissionsService) SetDefault(ctx context.Context, plain string) (PermissionsOverview, error) {
	org := s.org(ctx)
	if isMachine(ActorFrom(ctx, "")) || policy.RoleRank(ActorRoleFrom(ctx, policy.RoleOwner)) < policy.RoleRank(policy.RoleAdmin) {
		return PermissionsOverview{}, s.refuse(ctx, "", "default", "only admins and the owner can change the default level")
	}
	to := ""
	if plain != "" {
		if to = AutonomyOfLevel(plain); to == "" {
			return PermissionsOverview{}, fmt.Errorf("%w: unknown level %q", domain.ErrInvalid, plain)
		}
	}
	if err := s.update(ctx, org, func(p *policy.Permissions) error { p.DefaultLevel = to; return nil }); err != nil {
		return PermissionsOverview{}, err
	}
	if s.Rec != nil {
		s.Rec.Audit(ctx, domain.AuditLog{Actor: ActorFrom(ctx, "system"), Action: "permissions.default_changed", Entity: "org", EntityID: org,
			Details: map[string]any{"level": plain, "autonomy": to}})
	}
	return s.Overview(ctx)
}

// AdoptNew gives a newly hired agent the factory rules and, when the organization chose a default level, that
// level (never above the role's ceiling). An agent that already has permissions is left alone.
func (s *PermissionsService) AdoptNew(ctx context.Context, agentID string) error {
	org := s.org(ctx)
	p, err := s.permissions(ctx, org)
	if err != nil {
		return err
	}
	if _, ok := p.For(agentID); ok {
		return nil
	}
	a, err := s.Agents.GetAgent(ctx, org, agentID)
	if err != nil {
		return err
	}
	if p != nil && p.DefaultLevel != "" && s.SetLevel != nil {
		to := p.DefaultLevel
		if c := roles.AutonomyCeiling(a.Role); autonomyOrder[to] > autonomyOrder[c] {
			to = c
		}
		if to != a.Autonomy {
			if err := s.SetLevel(ctx, agentID, to, "org_default"); err != nil && s.Log != nil {
				s.Log.Warn("permissions: default level not applied", "agent", agentID, "err", err)
			}
		}
	}
	now := s.now()
	if err := s.update(ctx, org, func(p *policy.Permissions) error {
		if _, ok := p.Agents[agentID]; !ok {
			p.Agents[agentID] = policy.AgentPermissions{Since: now, UpdatedBy: "org_default", UpdatedAt: now}
		}
		return nil
	}); err != nil {
		return err
	}
	def := ""
	if p != nil {
		def = PlainLevel(p.DefaultLevel)
	}
	s.audit(WithActor(ctx, "system"), "permissions.adopted", agentID, map[string]any{"default_level": def})
	return nil
}

func clipStr(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// PermissionsObserver wraps the event publisher: a hired agent (agent.created) gets the factory rules and the
// organization's default level right away, before it can take its first action.
type PermissionsObserver struct {
	Inner Publisher
	svc   atomic.Pointer[PermissionsService]
}

// Bind installs the service (late binding).
func (o *PermissionsObserver) Bind(s *PermissionsService) { o.svc.Store(s) }

// Publish implements Publisher.
func (o *PermissionsObserver) Publish(ctx context.Context, e domain.Event) error {
	err := o.Inner.Publish(ctx, e)
	if s := o.svc.Load(); s != nil && e.Type == domain.EvAgentCreated && e.AgentID != "" && e.OrgID != "" {
		c, cancel := context.WithTimeout(WithOrg(context.WithoutCancel(ctx), e.OrgID), 10*time.Second)
		defer cancel()
		if aerr := s.AdoptNew(c, e.AgentID); aerr != nil && s.Log != nil {
			s.Log.Warn("permissions: adopt new agent", "agent", e.AgentID, "err", aerr)
		}
	}
	return err
}

// ---- "Approve and do not ask again" (Pendientes) and project / batch scopes ----

// Remember scopes.
const (
	RememberAgent   = "agent"   // this agent, this category: an exception "never ask"
	RememberProject = "project" // this agent, this category, only in this project
	RememberBatch   = "batch"   // every agent of every project of this batch, this category
)

// ScopeDays is how long a project or batch scope lasts at most (it also ends with the project).
const ScopeDays = 30

// RememberResult says what was created, so the card can summarize it and offer to undo it.
type RememberResult struct {
	Scope    string `json:"scope"`
	Category string `json:"category"`
	AgentID  string `json:"agent_id"`
	// ScopeGrant is set for project and batch scopes (undo: DELETE /permissions/scopes/{id}); for the agent scope
	// undo is DELETE /agents/{agent_id}/permissions/exceptions/{category}.
	ScopeGrant *policy.ScopeGrant `json:"scope_grant,omitempty"`
}

// RememberableCategory returns the category an approval can be remembered for: only approvals asked by the agent's
// own permissions (never the safety floor, the organization rules, high risk or the gateway).
func RememberableCategory(rule string) (string, bool) {
	for _, pre := range []string{"perm.important:", "perm.ask:", "perm.if_amount:", "perm.if_new_recipient:", "limit:perm.per_day:"} {
		if strings.HasPrefix(rule, pre) {
			c := strings.TrimPrefix(rule, pre)
			return c, policy.ValidCategory(c) && !policy.IsFloor(c)
		}
	}
	return "", false
}

// Remember creates "do not ask again" for the approval's agent and category, in the chosen scope.
func (s *PermissionsService) Remember(ctx context.Context, approvalID, scope string) (RememberResult, error) {
	org := s.org(ctx)
	if s.Approvals == nil {
		return RememberResult{}, fmt.Errorf("%w: not available", domain.ErrInvalid)
	}
	ap, err := s.Approvals.GetApproval(ctx, org, approvalID)
	if err != nil {
		return RememberResult{}, err
	}
	if ap.Status == domain.ApprovalRejected {
		return RememberResult{}, fmt.Errorf("%w: a rejected approval cannot be remembered", domain.ErrConflict)
	}
	cat, ok := RememberableCategory(ap.PolicyRule)
	if !ok {
		return RememberResult{}, fmt.Errorf("%w: not_rememberable: this approval was asked by a rule that always asks", domain.ErrConflict)
	}
	if err := s.mayEdit(ctx, org, ap.AgentID, "remember"); err != nil {
		return RememberResult{}, err
	}
	res := RememberResult{Scope: scope, Category: cat, AgentID: ap.AgentID}
	switch scope {
	case RememberAgent, "":
		res.Scope = RememberAgent
		if _, err := s.SetException(ctx, ap.AgentID, cat, ExceptionInput{Mode: policy.ModeNever, Source: "approval:" + ap.ID}); err != nil {
			return RememberResult{}, err
		}
		return res, nil
	case RememberProject, RememberBatch:
	default:
		return RememberResult{}, fmt.Errorf("%w: unknown scope %q", domain.ErrInvalid, scope)
	}
	if ap.ProjectID == "" || s.ProjectScope == nil {
		return RememberResult{}, fmt.Errorf("%w: no_project: this approval does not come from a project", domain.ErrConflict)
	}
	batchID, active := s.ProjectScope(ctx, org, ap.ProjectID)
	if !active {
		return RememberResult{}, fmt.Errorf("%w: no_project: the project is no longer running", domain.ErrConflict)
	}
	now := s.now()
	g := policy.ScopeGrant{ID: newID(), Kind: policy.ScopeProject, ScopeID: ap.ProjectID, AgentID: ap.AgentID, Category: cat, ApprovalID: ap.ID,
		CreatedBy: ActorFrom(ctx, "user"), CreatedAt: now, ExpiresAt: now.Add(ScopeDays * 24 * time.Hour)}
	if scope == RememberBatch {
		if batchID == "" {
			return RememberResult{}, fmt.Errorf("%w: no_batch: this project is not part of a batch", domain.ErrConflict)
		}
		g.Kind, g.ScopeID, g.AgentID = policy.ScopeBatch, batchID, "" // the batch: every agent of its projects
	}
	if err := s.update(ctx, org, func(p *policy.Permissions) error {
		if _, ok := p.Agents[ap.AgentID]; !ok {
			return fmt.Errorf("%w: choose_level_first: choose a level for this agent first", domain.ErrConflict)
		}
		p.Scopes = append(p.Scopes, g)
		return nil
	}); err != nil {
		return RememberResult{}, err
	}
	s.audit(ctx, "permissions.scope_granted", ap.AgentID, map[string]any{"scope": g.Kind, "scope_id": g.ScopeID, "category": cat,
		"approval_id": ap.ID, "grant_id": g.ID, "expires_at": g.ExpiresAt})
	res.ScopeGrant = &g
	return res, nil
}

// Scopes lists the live project / batch scopes, optionally only those of one project or batch.
func (s *PermissionsService) Scopes(ctx context.Context, projectID, batchID string) ([]policy.ScopeGrant, error) {
	p, err := s.permissions(ctx, s.org(ctx))
	if err != nil {
		return nil, err
	}
	out := []policy.ScopeGrant{}
	if p == nil {
		return out, nil
	}
	now := s.now()
	for _, g := range p.Scopes {
		if !now.Before(g.ExpiresAt) {
			continue
		}
		all := projectID == "" && batchID == ""
		if all || (g.Kind == policy.ScopeProject && g.ScopeID == projectID) || (g.Kind == policy.ScopeBatch && batchID != "" && g.ScopeID == batchID) {
			out = append(out, g)
		}
	}
	return out, nil
}

// RevokeScope removes a project / batch scope. Whoever may edit its agent can (admins for a batch-wide one).
func (s *PermissionsService) RevokeScope(ctx context.Context, id string) error {
	org := s.org(ctx)
	p, err := s.permissions(ctx, org)
	if err != nil {
		return err
	}
	var grant policy.ScopeGrant
	found := false
	if p != nil {
		for _, g := range p.Scopes {
			if g.ID == id {
				grant, found = g, true
			}
		}
	}
	if !found {
		return fmt.Errorf("%w: scope %s", domain.ErrNotFound, id)
	}
	if err := s.mayEdit(ctx, org, grant.AgentID, "scope"); err != nil {
		return err
	}
	if err := s.update(ctx, org, func(p *policy.Permissions) error {
		p.Scopes = slices.DeleteFunc(p.Scopes, func(x policy.ScopeGrant) bool { return x.ID == id })
		return nil
	}); err != nil {
		return err
	}
	s.audit(ctx, "permissions.scope_revoked", grant.AgentID, map[string]any{"scope": grant.Kind, "scope_id": grant.ScopeID, "category": grant.Category, "grant_id": id})
	return nil
}
