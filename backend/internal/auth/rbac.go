package auth

// Role is a membership role inside one organization.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
	RoleViewer Role = "viewer"
)

// Permission is a granular capability, "<resource>:<action>".
type Permission string

const (
	PermOrgManage         Permission = "org:manage"
	PermOrgDelete         Permission = "org:delete"
	PermBillingManage     Permission = "billing:manage"
	PermMembersRead       Permission = "members:read"
	PermMembersInvite     Permission = "members:invite"
	PermMembersManage     Permission = "members:manage"
	PermAgentsRead        Permission = "agents:read"
	PermAgentsWrite       Permission = "agents:write"
	PermTasksRead         Permission = "tasks:read"
	PermTasksCreate       Permission = "tasks:create"
	PermTasksManage       Permission = "tasks:manage"
	PermRequestsRead      Permission = "requests:read"
	PermRequestsCreate    Permission = "requests:create"
	PermConversationsRead Permission = "conversations:read"
	PermConversationsPost Permission = "conversations:write"
	PermApprovalsRead     Permission = "approvals:read"
	PermApprovalsDecide   Permission = "approvals:decide"
	PermReportsRead       Permission = "reports:read"
	PermMemoriesRead      Permission = "memories:read"
	PermMemoriesWrite     Permission = "memories:write"
	PermActivityRead      Permission = "activity:read"
	PermMetricsRead       Permission = "metrics:read"
	PermAuditRead         Permission = "audit:read"
	// PermPolicyManage edits the approval rules (thresholds, double approval,
	// limits). Owner only: whoever can relax the rules must not be able to
	// approve the actions they govern as a mere admin. Agents never hold it.
	PermPolicyManage Permission = "policy:manage"

	// Connections and controls (docs/architecture/integrations-credentials.md
	// sections 10 and 14). Agents never hold any of these.
	PermConnectionsRead       Permission = "connections:read"
	PermConnectionsManage     Permission = "connections:manage"
	PermConnectionsGrant      Permission = "connections:grant"
	PermConnectionsGrantWrite Permission = "connections:grant:write"
	PermConnectionsRevoke     Permission = "connections:revoke"
	PermConnectionsUsageRead  Permission = "connections:usage:read"
	PermControlsPause         Permission = "controls:pause"
	PermControlsKillSwitch    Permission = "controls:killswitch"
	// PermControlsRelease lifts the kill switch / read-only mode: owner only.
	PermControlsRelease Permission = "controls:release"
)

func set(ps ...Permission) map[Permission]struct{} {
	m := make(map[Permission]struct{}, len(ps))
	for _, p := range ps {
		m[p] = struct{}{}
	}
	return m
}

func union(base map[Permission]struct{}, ps ...Permission) map[Permission]struct{} {
	m := make(map[Permission]struct{}, len(base)+len(ps))
	for k := range base {
		m[k] = struct{}{}
	}
	for _, p := range ps {
		m[p] = struct{}{}
	}
	return m
}

// Each role is a strict superset of the one below it.
var (
	viewerPerms = set(
		PermMembersRead, PermAgentsRead, PermTasksRead, PermRequestsRead,
		PermConversationsRead, PermApprovalsRead, PermReportsRead,
		PermMemoriesRead, PermActivityRead, PermMetricsRead,
	)
	memberPerms = union(viewerPerms,
		PermRequestsCreate, PermTasksCreate, PermConversationsPost, PermMemoriesWrite,
		PermConnectionsRead,
	)
	adminPerms = union(memberPerms,
		PermOrgManage, PermMembersInvite, PermMembersManage, PermAgentsWrite,
		PermTasksManage, PermApprovalsDecide, PermAuditRead,
		PermConnectionsManage, PermConnectionsGrant, PermConnectionsGrantWrite, PermConnectionsRevoke,
		PermConnectionsUsageRead, PermControlsPause, PermControlsKillSwitch,
	)
	ownerPerms = union(adminPerms, PermOrgDelete, PermBillingManage, PermControlsRelease, PermPolicyManage)

	rolePerms = map[Role]map[Permission]struct{}{
		RoleViewer: viewerPerms,
		RoleMember: memberPerms,
		RoleAdmin:  adminPerms,
		RoleOwner:  ownerPerms,
	}
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool { _, ok := rolePerms[r]; return ok }

// Rank orders roles; unknown roles rank 0 (no privileges).
func (r Role) Rank() int {
	switch r {
	case RoleOwner:
		return 4
	case RoleAdmin:
		return 3
	case RoleMember:
		return 2
	case RoleViewer:
		return 1
	}
	return 0
}

// Permissions returns a copy of the role's permissions.
func (r Role) Permissions() []Permission {
	out := make([]Permission, 0, len(rolePerms[r]))
	for p := range rolePerms[r] {
		out = append(out, p)
	}
	return out
}

// Has reports whether the role grants perm. Unknown roles grant nothing.
func (r Role) Has(perm Permission) bool {
	_, ok := rolePerms[r][perm]
	return ok
}

// CanAssign reports whether an actor with role `actor` may grant `target`.
// Owners can grant anything; admins only member/viewer; others nothing. No one
// can grant a role above their own, which prevents privilege escalation.
func CanAssign(actor, target Role) bool {
	if !target.Valid() {
		return false
	}
	switch actor {
	case RoleOwner:
		return true
	case RoleAdmin:
		return target.Rank() < RoleAdmin.Rank()
	}
	return false
}

// CanManage reports whether `actor` may change or remove a member currently
// holding `current`. Owners manage everyone; admins manage only members below
// admin; nobody else manages anyone.
func CanManage(actor, current Role) bool {
	switch actor {
	case RoleOwner:
		return current.Valid()
	case RoleAdmin:
		return current.Valid() && current.Rank() < RoleAdmin.Rank()
	}
	return false
}
