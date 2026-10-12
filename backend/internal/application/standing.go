package application

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Standing per-project approvals.
//
// When a person approves an action they may choose "always allow this kind of
// action in this project". The choice becomes a StandingApproval: project +
// tool action (+ the agent, unless the person widens it to every agent of the
// project). From then on the same kind of action of that project is approved
// automatically, recorded as an approval decided by "standing:<id>" and audited.
//
// Safety rules (default deny):
//   - Only INTERNAL, REVERSIBLE actions qualify: an allow-list of workspace
//     tools and verbs (StandingEligibility). Anything external or irreversible
//     (sending, publishing, paying, deleting, sharing, hiring...) and code.run
//     never qualifies, however the tool names it; code.run always needs a
//     per-run approval bound to its hash and does not even consult this layer.
//   - Only low or medium risk, a single approver, no organization rule
//     (always-approve list, amount threshold, dual approval) behind the approval.
//   - It is checked when the grant is created AND on every use.
//   - It expires with the project (resolved through ProjectResolver) or after
//     StandingDays, and anyone with approvals:decide can revoke it.

// StandingApproval is one stored standing permission.
type StandingApproval struct {
	ID         string     `json:"id"`
	ProjectID  string     `json:"project_id"`
	AgentID    string     `json:"agent_id"` // "" = any agent of the project
	Tool       string     `json:"tool"`
	Action     string     `json:"action"`
	ApprovalID string     `json:"approval_id,omitempty"` // the approval being decided when it was chosen
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	RevokedBy  string     `json:"revoked_by,omitempty"`
	UseCount   int        `json:"use_count"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	// Status is derived on read: active | revoked | expired.
	Status string `json:"status"`
}

// Active reports whether the permission can be used at now.
func (s StandingApproval) Active(now time.Time) bool {
	return s.RevokedAt == nil && now.Before(s.ExpiresAt)
}

func (s StandingApproval) withStatus(now time.Time) StandingApproval {
	switch {
	case s.RevokedAt != nil:
		s.Status = "revoked"
	case !now.Before(s.ExpiresAt):
		s.Status = "expired"
	default:
		s.Status = "active"
	}
	return s
}

// StandingStore persists standing approvals (memory and Postgres implement it).
type StandingStore interface {
	CreateStanding(ctx context.Context, orgID string, s StandingApproval) error
	// ListStanding returns the standing approvals of a project, newest first
	// (revoked and expired ones included: they are the history).
	ListStanding(ctx context.Context, orgID, projectID string) ([]StandingApproval, error)
	GetStanding(ctx context.Context, orgID, id string) (StandingApproval, error)
	// RevokeStanding marks it revoked (domain.ErrNotFound if missing; revoking
	// twice keeps the first revocation).
	RevokeStanding(ctx context.Context, orgID, id, by string, at time.Time) error
	TouchStanding(ctx context.Context, orgID, id string, at time.Time) error
}

// ProjectResolver tells which project a request belongs to and whether that
// project is still running. Implemented by the projects service.
type ProjectResolver interface {
	ProjectOfRequest(ctx context.Context, org, requestID string) (projectID string, active bool)
	// ProjectActive reports whether the project still runs (false when finished,
	// cancelled or unknown).
	ProjectActive(ctx context.Context, org, projectID string) bool
}

// Defaults of the validity window.
const (
	DefaultStandingDays = 30
	MaxStandingDays     = 90
)

// standingActions is the allow-list: tool -> verbs that stay inside the
// workspace and can be undone (drafts, versioned files). Everything not listed
// is not eligible.
var standingActions = map[string]map[string]bool{
	"documents":   {"draft": true, "create": true, "update": true, "edit": true, "append": true},
	"spreadsheet": {"create": true, "update": true, "write": true, "edit": true, "append": true, "set_cell": true, "recalculate": true},
	"drive":       {"write": true, "move": true}, // versioned; delete is excluded below
}

// standingForbidden is a second barrier on the verb: external or irreversible
// meanings are refused even if a tool ever lists them in the allow-list.
var standingForbidden = regexp.MustCompile(`(^|_)(send|publish|post|pay|payment|transfer|wire|refund|charge|delete|remove|destroy|drop|purge|erase|trash|hire|fire|share|invite|notify|email|message|sign|submit|release|deploy|execute|run|export|cancel|terminate|approve|grant)($|_)`)

// StandingEligibility says whether an action may ever be covered by a standing
// approval, and why not. rule is the policy rule behind the approval
// (PolicyVerdict.RuleID); required is the number of approvers it needs.
func StandingEligibility(tool, action, risk, rule string, required int, requiredRole string) (bool, string) {
	tool, action = strings.ToLower(strings.TrimSpace(tool)), strings.ToLower(strings.TrimSpace(action))
	switch {
	case tool == CodeToolName || strings.EqualFold(action, CodeActionRun) || strings.Contains(action, "code.run"):
		return false, "code_run_needs_each_approval"
	case tool == "" || action == "":
		return false, "unknown_action"
	case !standingActions[tool][action]:
		return false, "not_internal_reversible"
	case standingForbidden.MatchString(action):
		return false, "external_or_irreversible"
	case risk != "low" && risk != "medium":
		return false, "risk_too_high"
	case required > 1 || requiredRole != "":
		return false, "governance_required"
	case !standingRuleOK(rule):
		return false, "organization_rule"
	}
	return true, ""
}

// standingRuleOK: only approvals that exist because of the agent's own autonomy
// level (or have no rule at all) can be remembered. A rule of the organization
// (always-approve list, amount threshold, limits, dual approval, high risk)
// keeps asking every time.
func standingRuleOK(rule string) bool {
	return rule == "" || rule == "baseline" || strings.HasPrefix(rule, "baseline.autonomy:") ||
		rule == "autonomy.approve_each" || rule == "native.autonomy"
}

// StandingService creates, matches, lists and revokes standing approvals.
type StandingService struct {
	Store StandingStore
	// Core reads the approval being remembered (the main store).
	Core interface {
		GetApproval(ctx context.Context, orgID, id string) (domain.Approval, error)
	}
	Projects ProjectResolver
	Rec      *Recorder
	Cfg      Config
	// Days is the default validity (0 = DefaultStandingDays), capped at MaxStandingDays.
	Days int
	Now  func() time.Time
}

func (s *StandingService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *StandingService) org(ctx context.Context) string { return OrgFrom(ctx, s.Cfg.OrgID) }

func (s *StandingService) days(req int) int {
	d := s.Days
	if d <= 0 {
		d = DefaultStandingDays
	}
	if req > 0 {
		d = req
	}
	return min(max(d, 1), MaxStandingDays)
}

// StandingGrantInput is what the approver chose.
type StandingGrantInput struct {
	ApprovalID string
	AnyAgent   bool // widen from this agent to every agent of the project
	Days       int  // 0 = the configured default
}

// Grant stores the standing approval for a just-approved approval. The server
// decides eligibility from the stored approval, never from the client.
func (s *StandingService) Grant(ctx context.Context, in StandingGrantInput) (StandingApproval, error) {
	org := s.org(ctx)
	ap, err := s.Core.GetApproval(ctx, org, in.ApprovalID)
	if err != nil {
		return StandingApproval{}, err
	}
	if ap.Status != domain.ApprovalApproved {
		return StandingApproval{}, fmt.Errorf("%w: only an approved approval can be remembered", domain.ErrInvalid)
	}
	if ap.ProjectID == "" || !ap.StandingEligible {
		return StandingApproval{}, fmt.Errorf("%w: this kind of action always needs an approval", domain.ErrInvalid)
	}
	if ok, why := StandingEligibility(ap.Tool, ap.Action, ap.Risk, ap.PolicyRule, ap.RequiredApprovals, ap.RequiredRole); !ok {
		return StandingApproval{}, fmt.Errorf("%w: %s", domain.ErrInvalid, why)
	}
	if s.Projects != nil && !s.Projects.ProjectActive(ctx, org, ap.ProjectID) {
		return StandingApproval{}, fmt.Errorf("%w: the project is not running", domain.ErrConflict)
	}
	now := s.now()
	agent := ap.AgentID
	if in.AnyAgent {
		agent = ""
	}
	// The same scope twice returns the live one instead of piling up rows.
	if cur, err := s.Store.ListStanding(ctx, org, ap.ProjectID); err == nil {
		for _, c := range cur {
			if c.Active(now) && c.AgentID == agent && c.Tool == ap.Tool && c.Action == ap.Action {
				return c.withStatus(now), nil
			}
		}
	}
	actor := ActorFrom(ctx, "user")
	st := StandingApproval{ID: newID(), ProjectID: ap.ProjectID, AgentID: agent, Tool: ap.Tool, Action: ap.Action, ApprovalID: ap.ID,
		CreatedBy: actor, CreatedAt: now, ExpiresAt: now.Add(time.Duration(s.days(in.Days)) * 24 * time.Hour)}
	if err := s.Store.CreateStanding(ctx, org, st); err != nil {
		return StandingApproval{}, err
	}
	s.audit(ctx, actor, "standing_approval.created", st, map[string]any{"approval_id": ap.ID, "expires_at": st.ExpiresAt, "any_agent": agent == ""})
	return st.withStatus(now), nil
}

// Match finds an active standing approval covering this exact kind of action
// of the project, and re-checks that the action is still eligible (so a stored
// row can never widen the allow-list). It does not consume anything.
func (s *StandingService) Match(ctx context.Context, projectID, agentID, tool, action, risk, rule string, required int, requiredRole string) (StandingApproval, bool) {
	if s == nil || s.Store == nil || projectID == "" {
		return StandingApproval{}, false
	}
	tool, action = strings.ToLower(tool), strings.ToLower(action)
	if ok, _ := StandingEligibility(tool, action, strings.ToLower(risk), rule, required, requiredRole); !ok {
		return StandingApproval{}, false
	}
	org := s.org(ctx)
	if s.Projects != nil && !s.Projects.ProjectActive(ctx, org, projectID) {
		return StandingApproval{}, false // expires with the project
	}
	list, err := s.Store.ListStanding(ctx, org, projectID)
	if err != nil {
		return StandingApproval{}, false // fail towards a human
	}
	now := s.now()
	for _, c := range list {
		if c.Active(now) && c.Tool == tool && c.Action == action && (c.AgentID == "" || c.AgentID == agentID) {
			return c.withStatus(now), true
		}
	}
	return StandingApproval{}, false
}

// Used records one use (counter and audit). Never fails the action.
func (s *StandingService) Used(ctx context.Context, st StandingApproval, approvalID, taskID string) {
	now := s.now()
	_ = s.Store.TouchStanding(ctx, s.org(ctx), st.ID, now)
	s.audit(ctx, "standing:"+st.ID, "standing_approval.used", st, map[string]any{"approval_id": approvalID, "task_id": taskID})
}

// List returns the standing approvals of a project (history included).
func (s *StandingService) List(ctx context.Context, projectID string) ([]StandingApproval, error) {
	list, err := s.Store.ListStanding(ctx, s.org(ctx), projectID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := make([]StandingApproval, 0, len(list))
	for _, c := range list {
		out = append(out, c.withStatus(now))
	}
	return out, nil
}

// Revoke ends a standing approval of the project. Revoking twice is a no-op.
func (s *StandingService) Revoke(ctx context.Context, projectID, id string) (StandingApproval, error) {
	org := s.org(ctx)
	cur, err := s.Store.GetStanding(ctx, org, id)
	if err != nil || cur.ProjectID != projectID {
		return StandingApproval{}, domain.ErrNotFound
	}
	now := s.now()
	if cur.RevokedAt != nil {
		return cur.withStatus(now), nil
	}
	actor := ActorFrom(ctx, "user")
	if err := s.Store.RevokeStanding(ctx, org, id, actor, now); err != nil {
		return StandingApproval{}, err
	}
	cur.RevokedAt, cur.RevokedBy = &now, actor
	s.audit(ctx, actor, "standing_approval.revoked", cur, nil)
	return cur.withStatus(now), nil
}

func (s *StandingService) audit(ctx context.Context, actor, action string, st StandingApproval, extra map[string]any) {
	if s.Rec == nil {
		return
	}
	d := map[string]any{"project_id": st.ProjectID, "agent_id": st.AgentID, "tool": st.Tool, "action": st.Action, "standing_id": st.ID}
	for k, v := range extra {
		d[k] = v
	}
	s.Rec.Audit(ctx, domain.AuditLog{Actor: actor, Action: action, Entity: "standing_approval", EntityID: st.ID, Details: d})
}
