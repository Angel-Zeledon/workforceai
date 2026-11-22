package agenthistory

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/policy"
)

// Reasons a field is not restored (stable codes; the UI words them).
const (
	ReasonAutonomyEarned     = "autonomy_earned"      // the old version had MORE autonomy: it is earned with evidence
	ReasonPermissionsFreer   = "permissions_freer"    // the old version asked for FEWER approvals
	ReasonSkillUnavailable   = "skill_unavailable"    // the skill no longer exists or is not active
	ReasonBudgetAboveCeiling = "budget_above_ceiling" // the old cap is above the department's or the organization's
	ReasonToolsFreer         = "tools_freer"          // the old version had more tools
	ReasonControl            = "control"              // pause, read-only and rehearsal are changed from their switches
	ReasonStructural         = "structural"           // name, title, role and department are not restored
	ReasonAccess             = "access"               // who may use the agent, its folders and its position are granted by hand
	ReasonSecret             = "secret"               // a secret is never kept, so it cannot be brought back
	ReasonNotAllowed         = "not_allowed"          // the person may not change that field
	ReasonUnavailable        = "unavailable"          // that part is not available in this deployment
	ReasonFailed             = "failed"               // the service refused it (Detail says why)
)

// Operations of a restore.
const (
	OpSetProfile       = "set_profile"
	OpLowerAutonomy    = "lower_autonomy"
	OpAdoptPermissions = "adopt_permissions"
	OpSetException     = "set_exception"
	OpRemoveException  = "remove_exception"
	OpRestoreFloor     = "restore_floor"
	OpAssignSkill      = "assign_skill"
	OpUnassignSkill    = "unassign_skill"
	OpSetBudget        = "set_budget"
	OpSetJob           = "set_job"
)

// ErrUnsupported is returned by an Executor for a part the deployment does not have.
var ErrUnsupported = errors.New("agent history: not supported here")

// Step is one thing a restore does (or skips, with a reason).
type Step struct {
	Field     string     `json:"field"`
	Op        string     `json:"op,omitempty"`
	Category  string     `json:"category,omitempty"`
	Skill     *SkillRef  `json:"skill,omitempty"`
	Autonomy  string     `json:"autonomy,omitempty"`
	BudgetUSD *float64   `json:"budget_usd,omitempty"`
	Exception *Exception `json:"-"`
	Reason    string     `json:"reason,omitempty"`
	Detail    string     `json:"detail,omitempty"`
}

// Plan is what a restore would do.
type Plan struct {
	Apply []Step `json:"apply"`
	Skip  []Step `json:"skip"`
	// target is the configuration to write for the profile and the job.
	target Config
	tools  []string
}

// PlanEnv are the facts a plan needs.
type PlanEnv struct {
	// SkillUsable reports whether a skill still exists and is active (nil: no skill can be assigned).
	SkillUsable func(id string) bool
	// BudgetCeiling is the lowest positive limit above the agent: its department's or the organization's (0 = none).
	BudgetCeiling float64
}

// Executor applies the steps through the services that own each field, with the person's context (same
// validations and permissions as changing them by hand). A part the deployment lacks returns ErrUnsupported.
type Executor interface {
	SetProfile(ctx context.Context, agentID string, c Config) error // instructions, creativity, tags, tone
	LowerAutonomy(ctx context.Context, agentID, to string) error    // through the trust ladder
	AdoptPermissions(ctx context.Context, agentID string) error      // turn on the factory rules at the current level
	SetException(ctx context.Context, agentID string, e Exception) error
	RemoveException(ctx context.Context, agentID, category string) error
	RestoreFloor(ctx context.Context, agentID, category string) error
	AssignSkill(ctx context.Context, agentID, skillID string) error
	UnassignSkill(ctx context.Context, agentID, skillID string) error
	SetBudget(ctx context.Context, agentID string, usd float64) error
	SetJob(ctx context.Context, agentID string, j Job, tools []string) error
}

var autonomyRank = map[string]int{"suggest": 1, "approve_each": 2, "rules": 3, "autonomous": 4}

// AutonomyRank orders the levels (0 = unknown).
func AutonomyRank(a string) int { return autonomyRank[a] }

func modeRank(e *Exception, ref string) int {
	if e == nil { // no exception: the level decides
		if ref == "autonomous" {
			return 1
		}
		return 3
	}
	switch e.Mode {
	case policy.ModeAsk:
		return 3
	case policy.ModeAskIf:
		return 2
	}
	return 1
}

// condsAtLeast reports whether "ask if a" asks at least whenever "ask if b" does.
func condsAtLeast(a, b *policy.AskIf) bool {
	if b == nil {
		return true
	}
	if a == nil {
		return false
	}
	if b.PerDayOver > 0 && (a.PerDayOver == 0 || a.PerDayOver > b.PerDayOver) {
		return false
	}
	if b.AmountOver != nil && (a.AmountOver == nil || *a.AmountOver > *b.AmountOver) {
		return false
	}
	return !b.NewRecipient || a.NewRecipient
}

// atLeastAsStrict reports whether exception a (nil = none) asks at least as often as b at level ref.
func atLeastAsStrict(a, b *Exception, ref string) bool {
	ra, rb := modeRank(a, ref), modeRank(b, ref)
	if ra != rb {
		return ra > rb
	}
	if a != nil && b != nil && a.Mode == policy.ModeAskIf && b.Mode == policy.ModeAskIf {
		return condsAtLeast(a.If, b.If)
	}
	return true
}

func exceptionOf(p *Permissions, cat string) *Exception {
	if p == nil {
		return nil
	}
	for i := range p.Exceptions {
		if p.Exceptions[i].Category == cat {
			e := p.Exceptions[i]
			return &e
		}
	}
	return nil
}

func sameException(a, b *Exception) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.Mode != b.Mode {
		return false
	}
	if a.Mode != policy.ModeAskIf {
		return true
	}
	return condsAtLeast(a.If, b.If) && condsAtLeast(b.If, a.If)
}

func lifted(p *Permissions, cat string) bool { return p != nil && slices.Contains(p.FloorLifted, cat) }

func subset(a, b []string) bool {
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

func jobEqual(a, b *Job) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return slices.Equal(a.Responsibilities, b.Responsibilities) && a.ReportsTo == b.ReportsTo && a.Escalation == b.Escalation &&
		string(a.Schedule) == string(b.Schedule)
}

// PlanRestore decides, field by field, what bringing back `old` over `cur` does. It is pure: the safety rules
// live here, the effects in the Executor.
func PlanRestore(old, cur Config, env PlanEnv) Plan {
	old, cur = old.Normalize(), cur.Normalize()
	p := Plan{Apply: []Step{}, Skip: []Step{}, target: cur}
	skip := func(field, reason string) { p.Skip = append(p.Skip, Step{Field: field, Reason: reason}) }

	// What is never restored, only shown.
	for _, f := range []struct {
		name   string
		differ bool
		reason string
	}{
		{"name", old.Name != cur.Name, ReasonStructural}, {"title", old.Title != cur.Title, ReasonStructural},
		{"role", old.Role != cur.Role, ReasonStructural}, {"department", old.Department != cur.Department, ReasonStructural},
		{"paused", old.Paused != cur.Paused, ReasonControl}, {"read_only", old.ReadOnly != cur.ReadOnly, ReasonControl},
		{"rehearsal", old.Rehearsal != cur.Rehearsal, ReasonControl},
	} {
		if f.differ {
			skip(f.name, f.reason)
		}
	}
	for _, c := range Diff(old, cur) {
		switch {
		case c.Secret:
			skip(c.Field, ReasonSecret)
		case c.Field == "access" || c.Field == "folders" || c.Field == "position":
			skip(c.Field, ReasonAccess)
		}
	}

	// Autonomy: only down, and through the trust ladder.
	level := cur.Autonomy
	if old.Autonomy != cur.Autonomy {
		ro, rc := AutonomyRank(old.Autonomy), AutonomyRank(cur.Autonomy)
		if ro > 0 && rc > 0 && ro < rc {
			p.Apply = append(p.Apply, Step{Field: "autonomy", Op: OpLowerAutonomy, Autonomy: old.Autonomy})
			level = old.Autonomy
		} else {
			skip("autonomy", ReasonAutonomyEarned)
		}
	}

	// Approval permissions: only what asks at least as often, category by category. The factory rules are never
	// switched off (that is the previous, freer behaviour); turning them on is stricter.
	ref := level
	if AutonomyRank(ref) < AutonomyRank("rules") {
		ref = "rules" // below "rules" everything asks anyway: compare as the level where exceptions matter
	}
	curP := cur.Permissions
	if old.Permissions != nil && curP == nil {
		p.Apply = append(p.Apply, Step{Field: "permissions", Op: OpAdoptPermissions})
		curP = &Permissions{}
	}
	if old.Permissions == nil && cur.Permissions != nil {
		// no choices back then: compare against an entry without exceptions or lifts
	}
	if curP != nil {
		for _, cat := range policy.PermissionCategories {
			oe, ce := exceptionOf(old.Permissions, cat), exceptionOf(curP, cat)
			if !sameException(oe, ce) {
				switch {
				case !atLeastAsStrict(oe, ce, ref):
					p.Skip = append(p.Skip, Step{Field: "permissions", Category: cat, Reason: ReasonPermissionsFreer})
				case oe == nil:
					p.Apply = append(p.Apply, Step{Field: "permissions", Op: OpRemoveException, Category: cat})
				default:
					p.Apply = append(p.Apply, Step{Field: "permissions", Op: OpSetException, Category: cat, Exception: oe})
				}
			}
		}
		for _, cat := range policy.FloorCategories {
			ol, cl := lifted(old.Permissions, cat), lifted(curP, cat)
			switch {
			case ol == cl:
			case cl: // the floor is lifted now and was not back then: putting it back only adds supervision
				p.Apply = append(p.Apply, Step{Field: "permissions", Op: OpRestoreFloor, Category: cat})
			default:
				p.Skip = append(p.Skip, Step{Field: "permissions", Category: cat, Reason: ReasonPermissionsFreer})
			}
		}
	}

	// Work profile (instructions, creativity, tags, tone).
	if old.Instructions != cur.Instructions || old.Creativity != cur.Creativity || !slices.Equal(old.Tags, cur.Tags) || old.Tone != cur.Tone {
		p.target.Instructions, p.target.Creativity, p.target.Tags, p.target.Tone = old.Instructions, old.Creativity, old.Tags, old.Tone
		fields := []string{}
		for _, f := range []struct {
			n string
			d bool
		}{{"instructions", old.Instructions != cur.Instructions}, {"creativity", old.Creativity != cur.Creativity},
			{"tags", !slices.Equal(old.Tags, cur.Tags)}, {"tone", old.Tone != cur.Tone}} {
			if f.d {
				fields = append(fields, f.n)
			}
		}
		for _, f := range fields {
			p.Apply = append(p.Apply, Step{Field: f, Op: OpSetProfile})
		}
	}

	// Skills of the library: assign back only what still exists and is active; unassign what was added since.
	for _, s := range old.Skills {
		if slices.ContainsFunc(cur.Skills, func(x SkillRef) bool { return x.ID == s.ID }) {
			continue
		}
		s := s
		if env.SkillUsable == nil || !env.SkillUsable(s.ID) {
			p.Skip = append(p.Skip, Step{Field: "skills", Skill: &s, Reason: ReasonSkillUnavailable})
			continue
		}
		p.Apply = append(p.Apply, Step{Field: "skills", Op: OpAssignSkill, Skill: &s})
	}
	for _, s := range cur.Skills {
		if !slices.ContainsFunc(old.Skills, func(x SkillRef) bool { return x.ID == s.ID }) {
			s := s
			p.Apply = append(p.Apply, Step{Field: "skills", Op: OpUnassignSkill, Skill: &s})
		}
	}

	// Monthly cap: never above the department's or the organization's limit.
	if old.BudgetUSD != cur.BudgetUSD {
		v := old.BudgetUSD
		if v > 0 && env.BudgetCeiling > 0 && v > env.BudgetCeiling {
			p.Skip = append(p.Skip, Step{Field: "budget_usd", BudgetUSD: &v, Reason: ReasonBudgetAboveCeiling,
				Detail: fmt.Sprintf("%.2f > %.2f", v, env.BudgetCeiling)})
		} else {
			p.Apply = append(p.Apply, Step{Field: "budget_usd", Op: OpSetBudget, BudgetUSD: &v})
		}
	}

	// Job description and tools: the tools only narrow.
	p.tools = cur.Tools
	toolsChange := false
	if !slices.Equal(old.Tools, cur.Tools) {
		if subset(old.Tools, cur.Tools) {
			p.tools, toolsChange = old.Tools, true
		} else {
			skip("tools", ReasonToolsFreer)
		}
	}
	jobChange := !jobEqual(old.Job, cur.Job)
	switch {
	case jobChange && (old.Job == nil || cur.Job == nil):
		skip("job", ReasonUnavailable)
		jobChange = false
	case jobChange:
		p.target.Job = old.Job
	}
	if jobChange || toolsChange {
		if jobChange {
			p.Apply = append(p.Apply, Step{Field: "job", Op: OpSetJob})
		}
		if toolsChange {
			p.Apply = append(p.Apply, Step{Field: "tools", Op: OpSetJob})
		}
	}
	return p
}

// RestoreResult is the answer of a restore.
type RestoreResult struct {
	From      int      `json:"from"`
	Version   *Version `json:"version,omitempty"` // the new version (nil when nothing changed)
	Applied   []Step   `json:"applied"`
	Skipped   []Step   `json:"skipped"`
	Unchanged bool     `json:"unchanged"`
}

// Preview returns what restoring version n would do, without doing it.
func (s *Service) Preview(ctx context.Context, org, agentID string, n int, env PlanEnv) (Plan, error) {
	old, err := s.Store.Get(ctx, org, agentID, n)
	if err != nil {
		return Plan{}, err
	}
	cur, err := s.Capture(ctx, org, agentID)
	if err != nil {
		return Plan{}, err
	}
	return PlanRestore(old.Config, cur, env), nil
}

func reasonOf(err error) string {
	switch {
	case errors.Is(err, ErrUnsupported):
		return ReasonUnavailable
	case errors.Is(err, domain.ErrForbidden):
		return ReasonNotAllowed
	}
	return ReasonFailed
}

// Restore brings back version n as a NEW version: every applicable field goes through the Executor (the services
// of each field, with the person's context), the rest is skipped with a reason. Agents and the system never restore.
func (s *Service) Restore(ctx context.Context, org, agentID string, n int, c Cause, ex Executor, env PlanEnv) (RestoreResult, error) {
	if IsMachine(c.Actor) || c.Actor == agentID {
		return RestoreResult{}, fmt.Errorf("%w: only a person can restore an agent's configuration", domain.ErrForbidden)
	}
	old, err := s.Store.Get(ctx, org, agentID, n)
	if err != nil {
		return RestoreResult{}, err
	}
	cur, err := s.Capture(ctx, org, agentID)
	if err != nil {
		return RestoreResult{}, err
	}
	plan := PlanRestore(old.Config, cur, env)
	res := RestoreResult{From: n, Applied: []Step{}, Skipped: append([]Step{}, plan.Skip...)}
	// The profile fields share one call, and so do the job and the tools: their outcome is that call's.
	var profileErr, jobErr error
	profileDone, jobDone := false, false
	for _, st := range plan.Apply {
		var err error
		switch st.Op {
		case OpSetProfile:
			if !profileDone {
				profileErr, profileDone = ex.SetProfile(ctx, agentID, plan.target), true
			}
			err = profileErr
		case OpLowerAutonomy:
			err = ex.LowerAutonomy(ctx, agentID, st.Autonomy)
		case OpAdoptPermissions:
			err = ex.AdoptPermissions(ctx, agentID)
		case OpSetException:
			err = ex.SetException(ctx, agentID, *st.Exception)
		case OpRemoveException:
			err = ex.RemoveException(ctx, agentID, st.Category)
		case OpRestoreFloor:
			err = ex.RestoreFloor(ctx, agentID, st.Category)
		case OpAssignSkill:
			err = ex.AssignSkill(ctx, agentID, st.Skill.ID)
		case OpUnassignSkill:
			err = ex.UnassignSkill(ctx, agentID, st.Skill.ID)
		case OpSetBudget:
			err = ex.SetBudget(ctx, agentID, *st.BudgetUSD)
		case OpSetJob:
			if !jobDone {
				j := cur.Job
				if plan.target.Job != nil {
					j = plan.target.Job
				}
				if j == nil {
					jobErr = ErrUnsupported
				} else {
					jobErr = ex.SetJob(ctx, agentID, *j, plan.tools)
				}
				jobDone = true
			}
			err = jobErr
		}
		if err != nil {
			st.Reason, st.Op = reasonOf(err), ""
			if st.Reason == ReasonFailed {
				st.Detail = clip(err.Error(), 200)
			}
			res.Skipped = append(res.Skipped, st)
			continue
		}
		res.Applied = append(res.Applied, st)
	}
	v, wrote, err := s.Record(ctx, org, agentID, Cause{Actor: c.Actor, ActorName: c.ActorName, Source: SourceRestore, RestoredFrom: &n})
	if err != nil {
		return res, err
	}
	if wrote {
		res.Version = &v
	} else {
		res.Unchanged = true
	}
	if s.Audit != nil {
		skipped := []map[string]string{}
		for _, st := range res.Skipped {
			m := map[string]string{"field": st.Field, "reason": st.Reason}
			if st.Category != "" {
				m["category"] = st.Category
			}
			if st.Skill != nil {
				m["skill"] = st.Skill.ID
			}
			skipped = append(skipped, m)
		}
		applied := []string{}
		for _, st := range res.Applied {
			if !slices.Contains(applied, st.Field) {
				applied = append(applied, st.Field)
			}
		}
		d := map[string]any{"restored_from": n, "applied": applied, "skipped": skipped, "unchanged": res.Unchanged}
		if res.Version != nil {
			d["version"] = res.Version.Version
		}
		s.Audit(ctx, org, domain.AuditLog{Actor: c.Actor, Action: "agent.config_restored", Entity: "agent", EntityID: agentID, Details: d})
	}
	return res, nil
}

