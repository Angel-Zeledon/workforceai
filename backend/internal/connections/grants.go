package connections

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// GrantInput creates or updates the grant of one agent on one connection.
type GrantInput struct {
	Capabilities     []string
	ResourceScope    ResourceScope
	Constraints      Constraints
	MaxRisk          string
	Limits           Limits
	RedactionProfile string
	ValidUntil       *time.Time
	Alias            string
	IsDefault        *bool
	AutonomyOverride string
	// AllowNoRedaction must be set by the API only for owners.
	AllowNoRedaction bool
}

var capNameRe = regexp.MustCompile(`^[a-z][a-z_]*\.[a-z][a-z_]*$`)

// forbiddenCaps can never be granted to an agent, whatever a manifest says:
// agents NEVER receive approval, connection-management or control powers.
var forbiddenCaps = []string{"approve", "approvals", "connections", "controls", "admin", "release", "killswitch"}

// IsForbiddenCapability reports capabilities agents may never hold.
func IsForbiddenCapability(c string) bool {
	root := strings.ToLower(strings.SplitN(c, ".", 2)[0])
	root = strings.SplitN(root, ":", 2)[0]
	return slices.Contains(forbiddenCaps, root) || strings.Contains(strings.ToLower(c), "approve")
}

func hasWrite(m Manifest, caps []string) bool {
	for _, c := range caps {
		if m.Capabilities[c].SideEffects {
			return true
		}
	}
	return false
}

// ConnectionHasWrite reports whether the connection offers side-effect capabilities.
func (s *Service) ConnectionHasWrite(c Connection) bool {
	return hasWrite(s.manifests[c.Provider], c.GrantedCapabilities)
}

// PutGrant creates/updates a grant (D1: connecting is not granting). A grant
// with write capabilities needs a second human only when the organization has
// more than one admin (owner decision 5): it is stored as pending_second_approval
// until another admin approves it.
func (s *Service) PutGrant(ctx context.Context, org, connID, agentID string, in GrantInput, actor string) (Grant, error) {
	c, err := s.Store.GetConnection(ctx, org, connID)
	if err != nil {
		return Grant{}, err
	}
	if c.Status == StatusRevoked {
		return Grant{}, fmt.Errorf("%w: connection is revoked", ErrConflict)
	}
	m := s.manifests[c.Provider]
	caps := dedupe(in.Capabilities)
	if len(caps) == 0 {
		return Grant{}, invalid("capabilities are required")
	}
	for _, cp := range caps {
		if IsForbiddenCapability(cp) {
			return Grant{}, invalid("agents_cannot_approve: capability %q cannot be granted to an agent", cp)
		}
		if !capNameRe.MatchString(cp) {
			return Grant{}, invalid("capability %q cannot be granted to an agent", cp)
		}
		if _, ok := m.Capability(cp); !ok {
			return Grant{}, invalid("capability %q is not offered by %s", cp, c.Provider)
		}
		if !slices.Contains(c.GrantedCapabilities, cp) {
			return Grant{}, fmt.Errorf("%w: %s", ErrInvalid, CodeScopeNotGranted+": the connection does not hold "+cp)
		}
	}
	maxRisk := in.MaxRisk
	if maxRisk == "" {
		maxRisk = "low"
		for _, cp := range caps {
			if RiskRank(m.Capabilities[cp].Risk) > RiskRank(maxRisk) {
				maxRisk = m.Capabilities[cp].Risk
			}
		}
	}
	if maxRisk != "low" && maxRisk != "medium" && maxRisk != "high" {
		return Grant{}, invalid("max_risk must be low, medium or high")
	}
	for _, cp := range caps {
		if RiskRank(m.Capabilities[cp].Risk) > RiskRank(maxRisk) {
			return Grant{}, invalid("max_risk %s is below the risk of %s", maxRisk, cp)
		}
	}
	profile := in.RedactionProfile
	switch profile {
	case "":
		profile = "strict" // owner decision 6
	case "strict", "standard":
	case "none_admin_only":
		if !in.AllowNoRedaction {
			return Grant{}, invalid("none_admin_only requires an owner")
		}
	default:
		return Grant{}, invalid("unknown redaction_profile")
	}
	if err := validateLimits(in.Limits); err != nil {
		return Grant{}, err
	}
	scope, err := narrowScope(c.ResourceScope, in.ResourceScope)
	if err != nil {
		return Grant{}, err
	}
	lim, err := narrowLimits(c.Limits, in.Limits)
	if err != nil {
		return Grant{}, err
	}
	if in.AutonomyOverride != "" && !slices.Contains([]string{"suggest", "approve_each", "rules", "autonomous"}, in.AutonomyOverride) {
		return Grant{}, invalid("unknown autonomy_override")
	}

	now := s.Now().UTC()
	before := []string{}
	g := Grant{ID: uuid.NewString(), OrgID: org, ConnectionID: connID, AgentID: agentID, CreatedAt: now, ValidFrom: now}
	if prev, err := s.Store.GetGrant(ctx, org, connID, agentID); err == nil {
		g.ID, g.CreatedAt, g.GrantedBy, g.ApprovedBy, g.IsDefault = prev.ID, prev.CreatedAt, prev.GrantedBy, prev.ApprovedBy, prev.IsDefault
		before = prev.Capabilities
		g.Alias = prev.Alias
	} else {
		g.IsDefault = true
	}
	g.Capabilities, g.ResourceScope, g.Constraints, g.MaxRisk, g.Limits = caps, scope, in.Constraints, maxRisk, lim
	g.RedactionProfile, g.ValidUntil, g.AutonomyOverride = profile, in.ValidUntil, in.AutonomyOverride
	if in.IsDefault != nil {
		g.IsDefault = *in.IsDefault
	}
	if in.Alias != "" {
		g.Alias = slug(in.Alias)
	} else if g.Alias == "" {
		g.Alias = slug(c.Label)
	}
	g.GrantedBy, g.RequestedBy, g.ApprovedBy = actor, actor, ""
	g.Status = GrantActive
	if hasWrite(m, caps) && s.AdminCount(ctx, org) > 1 {
		g.Status = GrantPendingApproval // maker-checker: a different human must approve
	}
	if err := s.Store.UpsertGrant(ctx, g); err != nil {
		return Grant{}, err
	}
	s.Audit(ctx, org, actor, "connection.grant_changed", "connection", connID,
		map[string]any{"agent_id": agentID, "before": before, "after": caps, "status": g.Status})
	s.Emit(ctx, org, "connection.grant_changed", map[string]any{"connection_id": connID, "agent_id": agentID, "before": before, "after": caps, "by": actor, "status": g.Status})
	return g, nil
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if s == "" {
		return "connection"
	}
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// ApproveGrant activates a pending write grant. The approver must be a
// different human than the one who granted.
func (s *Service) ApproveGrant(ctx context.Context, org, connID, agentID, approver string) (Grant, error) {
	g, err := s.Store.GetGrant(ctx, org, connID, agentID)
	if err != nil {
		return Grant{}, err
	}
	if g.Status != GrantPendingApproval {
		return Grant{}, fmt.Errorf("%w: grant is %s", ErrConflict, g.Status)
	}
	if approver == "" || approver == g.GrantedBy {
		return Grant{}, ErrNeedsSecondApprover
	}
	g.Status, g.ApprovedBy = GrantActive, approver
	if err := s.Store.UpsertGrant(ctx, g); err != nil {
		return Grant{}, err
	}
	s.Audit(ctx, org, approver, "connection.grant_approved", "connection", connID, map[string]any{"agent_id": agentID, "granted_by": g.GrantedBy})
	s.Emit(ctx, org, "connection.grant_changed", map[string]any{"connection_id": connID, "agent_id": agentID, "before": []string{}, "after": g.Capabilities, "by": approver, "status": g.Status})
	return g, nil
}

// RevokeGrant removes an agent's access.
func (s *Service) RevokeGrant(ctx context.Context, org, connID, agentID, actor string) error {
	g, err := s.Store.GetGrant(ctx, org, connID, agentID)
	if err != nil {
		return err
	}
	now := s.Now().UTC()
	g.Status, g.RevokedAt, g.RevokedBy = GrantRevoked, &now, actor
	if err := s.Store.UpsertGrant(ctx, g); err != nil {
		return err
	}
	s.Audit(ctx, org, actor, "connection.grant_changed", "connection", connID, map[string]any{"agent_id": agentID, "before": g.Capabilities, "after": []string{}})
	s.Emit(ctx, org, "connection.grant_changed", map[string]any{"connection_id": connID, "agent_id": agentID, "before": g.Capabilities, "after": []string{}, "by": actor})
	return nil
}

// Grants lists the grants of a connection (revoked ones omitted).
func (s *Service) Grants(ctx context.Context, org, connID string) ([]Grant, error) {
	if _, err := s.Store.GetConnection(ctx, org, connID); err != nil {
		return nil, err
	}
	gs, err := s.Store.ListGrantsByConnection(ctx, org, connID)
	return liveGrants(gs), err
}

// AgentAccess is the inverse view: what one agent can reach.
type AgentAccess struct {
	Connection Connection `json:"connection"`
	Grant      Grant      `json:"grant"`
}

// AgentConnections lists the connections an agent holds grants on.
func (s *Service) AgentConnections(ctx context.Context, org, agentID string) ([]AgentAccess, error) {
	gs, err := s.Store.ListGrantsByAgent(ctx, org, agentID)
	if err != nil {
		return nil, err
	}
	out := []AgentAccess{}
	for _, g := range liveGrants(gs) {
		c, err := s.Store.GetConnection(ctx, org, g.ConnectionID)
		if err != nil {
			continue
		}
		out = append(out, AgentAccess{Connection: s.view(ctx, c), Grant: g})
	}
	return out, nil
}

func liveGrants(gs []Grant) []Grant {
	out := []Grant{}
	for _, g := range gs {
		if g.Status != GrantRevoked {
			out = append(out, g)
		}
	}
	return out
}

// narrowScope returns the grant scope, which may only NARROW the connection's.
func narrowScope(conn, grant ResourceScope) (ResourceScope, error) {
	out := grant
	if len(conn.Labels) > 0 {
		if len(grant.Labels) == 0 {
			out.Labels = conn.Labels
		}
		for _, l := range out.Labels {
			if !slices.Contains(conn.Labels, l) {
				return out, invalid("grant label %q is outside the connection's resource scope", l)
			}
		}
	}
	out.ExcludeLabels = dedupe(append(append([]string{}, conn.ExcludeLabels...), grant.ExcludeLabels...))
	if conn.MaxAgeDays > 0 {
		if grant.MaxAgeDays == 0 || grant.MaxAgeDays > conn.MaxAgeDays {
			if grant.MaxAgeDays > conn.MaxAgeDays {
				return out, invalid("grant max_age_days exceeds the connection's")
			}
			out.MaxAgeDays = conn.MaxAgeDays
		}
	}
	return out, nil
}

// narrowLimits makes a grant's limits <= the connection's; 0 inherits.
func narrowLimits(conn, g Limits) (Limits, error) {
	pick := func(name string, c, v int64) (int64, error) {
		switch {
		case v == 0:
			return c, nil
		case c > 0 && v > c:
			return 0, invalid("grant limit %s (%d) exceeds the connection's (%d)", name, v, c)
		}
		return v, nil
	}
	var err error
	out := g
	var v int64
	if v, err = pick("per_minute", int64(conn.PerMinute), int64(g.PerMinute)); err != nil {
		return out, err
	}
	out.PerMinute = int(v)
	if v, err = pick("per_hour", int64(conn.PerHour), int64(g.PerHour)); err != nil {
		return out, err
	}
	out.PerHour = int(v)
	if v, err = pick("per_day", int64(conn.PerDay), int64(g.PerDay)); err != nil {
		return out, err
	}
	out.PerDay = int(v)
	if v, err = pick("write_per_day", int64(conn.WritePerDay), int64(g.WritePerDay)); err != nil {
		return out, err
	}
	out.WritePerDay = int(v)
	if v, err = pick("max_items_per_call", int64(conn.MaxItemsPerCall), int64(g.MaxItemsPerCall)); err != nil {
		return out, err
	}
	out.MaxItemsPerCall = int(v)
	if out.MaxBytesPerDay, err = pick("max_bytes_per_day", conn.MaxBytesPerDay, g.MaxBytesPerDay); err != nil {
		return out, err
	}
	if g.MonthlyBudgetUSD == 0 {
		out.MonthlyBudgetUSD = conn.MonthlyBudgetUSD
	} else if conn.MonthlyBudgetUSD > 0 && g.MonthlyBudgetUSD > conn.MonthlyBudgetUSD {
		return out, invalid("grant monthly_budget_usd exceeds the connection's")
	}
	out.OnExceed = "deny"
	return out, nil
}

// ---- usage ----

var emitGap = 250 * time.Millisecond

// RecordUsage appends a usage row (metadata only), touches last_used_at and
// emits a coalesced connection.usage event.
func (s *Service) RecordUsage(ctx context.Context, u Usage) {
	if u.ID == "" {
		u.ID = uuid.NewString()
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = s.Now().UTC()
	}
	ctx = context.WithoutCancel(ctx)
	if err := s.Store.AddUsage(ctx, u); err != nil {
		return
	}
	if u.Decision == "allowed" && (u.Status == "succeeded" || u.Status == "sent") {
		if c, err := s.Store.GetConnection(ctx, u.OrgID, u.ConnectionID); err == nil {
			t := u.CreatedAt
			c.LastUsedAt = &t
			_ = s.Store.UpdateConnection(ctx, c)
		}
	}
	s.emitMu.Lock()
	last := s.lastEmit[u.ConnectionID]
	ok := u.CreatedAt.Sub(last) >= emitGap || u.Decision != "allowed"
	if ok {
		s.lastEmit[u.ConnectionID] = u.CreatedAt
	}
	s.emitMu.Unlock()
	if ok {
		s.Emit(ctx, u.OrgID, "connection.usage", map[string]any{"connection_id": u.ConnectionID, "agent_id": u.AgentID,
			"tool": u.Tool, "action": u.Action, "decision": u.Decision, "status": u.Status})
	}
}

// Usage lists usage rows.
func (s *Service) Usage(ctx context.Context, org string, f UsageFilter) ([]Usage, error) {
	if f.ConnectionID != "" {
		if _, err := s.Store.GetConnection(ctx, org, f.ConnectionID); err != nil {
			return nil, err
		}
	}
	us, err := s.Store.ListUsage(ctx, org, f)
	if us == nil {
		us = []Usage{}
	}
	return us, err
}

// UsageSummary aggregates the last `period` ("day" default, "week", "month").
func (s *Service) UsageSummary(ctx context.Context, org, connID, period string) (UsageSummary, error) {
	d := 24 * time.Hour
	switch period {
	case "week":
		d = 7 * 24 * time.Hour
	case "month":
		d = 30 * 24 * time.Hour
	default:
		period = "day"
	}
	from := s.Now().Add(-d)
	rows, err := s.Usage(ctx, org, UsageFilter{ConnectionID: connID, From: &from, Limit: 500})
	sum := UsageSummary{Period: period}
	for _, u := range rows {
		switch {
		case u.Decision == "denied":
			sum.Denied++
		case u.Decision == "needs_approval":
			sum.Approval++
		case isWrite(u.Capability):
			sum.Writes++
		default:
			sum.Reads++
		}
		sum.CostUSD += u.CostUSD
	}
	return sum, err
}

// LimitCheck evaluates rate and volume limits for (connection, agent) using the
// usage projection. It returns a deny code ("" = ok).
func (s *Service) LimitCheck(ctx context.Context, org string, c Connection, g Grant, write bool) (string, error) {
	now := s.Now()
	type win struct {
		since time.Time
		max   int
	}
	eff := func(a, b int) int {
		if a > 0 && (b == 0 || a < b) {
			return a
		}
		return b
	}
	wins := []win{
		{now.Add(-time.Minute), eff(g.Limits.PerMinute, c.Limits.PerMinute)},
		{now.Add(-time.Hour), eff(g.Limits.PerHour, c.Limits.PerHour)},
		{now.Add(-24 * time.Hour), eff(g.Limits.PerDay, c.Limits.PerDay)},
	}
	for _, w := range wins {
		if w.max <= 0 {
			continue
		}
		n, _, err := s.Store.CountUsage(ctx, org, c.ID, g.AgentID, w.since, false)
		if err != nil {
			return "", err
		}
		if n >= w.max {
			return CodeRateLimit, nil
		}
	}
	if write {
		if max := eff(g.Limits.WritePerDay, c.Limits.WritePerDay); max > 0 {
			n, _, err := s.Store.CountUsage(ctx, org, c.ID, g.AgentID, now.Add(-24*time.Hour), true)
			if err != nil {
				return "", err
			}
			if n >= max {
				return CodeRateLimit, nil
			}
		}
	}
	if maxB := c.Limits.MaxBytesPerDay; maxB > 0 {
		_, b, err := s.Store.CountUsage(ctx, org, c.ID, "", now.Add(-24*time.Hour), false)
		if err != nil {
			return "", err
		}
		if b >= maxB {
			return CodeBudget, nil
		}
	}
	if bud := c.Limits.MonthlyBudgetUSD; bud > 0 {
		cost, err := s.Store.SumCost(ctx, org, c.ID, now.Add(-30*24*time.Hour))
		if err != nil {
			return "", err
		}
		if cost >= bud {
			return CodeBudget, nil
		}
	}
	return "", nil
}
