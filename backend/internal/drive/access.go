package drive

import (
	"context"
	"strings"
)

// Folder access of each agent (docs/superpowers/specs/2026-11-03-agentes-ubican-archivos-y-accesos-design.md, part 3):
// three levels per folder, inherited downwards, and the most specific rule wins.
//
//	Ninguno (none)  the agent does not see the folder: not listed, read, searched (keyword, meaning, memory, recent files)
//	Leer (read)     list, read and search; writing there asks a person (as today in /Shared)
//	Editar (edit)   read, and write / edit / move there without approval (like its desk)
//
// Defaults (no rule): Edit on its desk (/Agents/<me>, never changeable) and on the folders of the projects it works on,
// Read on /Shared; everything else Ninguno. A rule is a row of drive_grants (mode none|read|edit); an older "read"
// grant keeps meaning what it meant, so existing agents keep what they see today. A rule beats a default only when it is
// at least as deep as the default's root (a rule on /Projects does not undo the agent's own project; one on
// /Projects/<it>/Final does). Every tool call re-reads the rules (scopeOf), never only at the start of a task.
const (
	AccessNone    = "none"
	AccessRead    = "read"
	AccessEdit    = "edit"
	AccessDefault = "default" // SetAgentAccess: remove the rule, back to the default

	SourceDefault = "default"
	SourceManual  = "manual"
)

// Zones produced by access rules.
const (
	ZoneDenied      = "denied"       // a Ninguno rule
	ZoneGrantedEdit = "granted_edit" // an Editar rule
)

// ValidAccess reports whether level is one a rule may carry.
func ValidAccess(level string) bool {
	return level == AccessNone || level == AccessRead || level == AccessEdit
}

func grantMode(m string) string {
	if ValidAccess(m) {
		return m
	}
	return AccessRead // rows written before the levels existed
}

// InScope tells whether path is inside a search scope: nil = everything; otherwise the deepest entry that covers the
// path decides, where "!<path>" excludes a subtree and any other entry includes one. The SQL stores apply the same rule.
func InScope(under []string, p string) bool {
	if under == nil {
		return true
	}
	best, in := -1, false
	lp := strings.ToLower(p)
	for _, u := range under {
		neg := strings.HasPrefix(u, "!")
		r := strings.ToLower(strings.TrimPrefix(u, "!"))
		if r == "" || !IsUnder(lp, r) || len(r) < best || len(r) == best && !neg {
			continue
		}
		best, in = len(r), !neg
	}
	return in
}

// splitScope separates the included roots from the excluded subtrees ("!" removed).
func splitScope(under []string) (pos, neg []string) {
	for _, u := range under {
		if strings.HasPrefix(u, "!") {
			neg = append(neg, u[1:])
		} else {
			pos = append(pos, u)
		}
	}
	return pos, neg
}

func depthOf(p string) int {
	t := strings.Trim(p, "/")
	if t == "" {
		return 0
	}
	return strings.Count(t, "/") + 1
}

// deepestGrant is the most specific rule covering p (nil when none does).
func (sc scope) deepestGrant(p string) *Grant {
	var best *Grant
	for i := range sc.rules {
		g := &sc.rules[i]
		if IsUnder(p, g.Path) && (best == nil || depthOf(g.Path) > depthOf(best.Path)) {
			best = g
		}
	}
	return best
}

// ruleAt is the rule set exactly on p ("" when there is none).
func (sc scope) ruleAt(p string) string {
	for _, g := range sc.rules {
		if strings.EqualFold(g.Path, p) {
			return grantMode(g.Mode)
		}
	}
	return ""
}

// withRules narrows a list of default roots by the agent's rules: a Ninguno on a root drops it, a Ninguno below an
// included root becomes an exclusion, Leer/Editar rules add their folder.
func (sc scope) withRules(roots []string) []string {
	var out []string
	for _, r := range roots {
		if sc.ruleAt(r) != AccessNone {
			out = append(out, r)
		}
	}
	for _, g := range sc.rules {
		switch grantMode(g.Mode) {
		case AccessNone:
			if InScope(out, g.Path) {
				out = append(out, "!"+g.Path)
			}
		default:
			if !containsFold(out, g.Path) {
				out = append(out, g.Path)
			}
		}
	}
	return out
}

func containsFold(l []string, s string) bool {
	for _, x := range l {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// AccessEntry is one line of "Carpetas que puede ver".
type AccessEntry struct {
	Path    string `json:"path"`
	Level   string `json:"level"`
	Source  string `json:"source"` // default | manual
	GrantID string `json:"grant_id,omitempty"`
	// Fixed: the agent's own desk, which is always Edit.
	Fixed bool `json:"fixed,omitempty"`
}

// AgentAccess lists the folders of an agent with their level: the defaults first (desk, /Shared, its projects), then
// the rules people set. A rule on exactly a default folder replaces that default line.
func (a *AgentFS) AgentAccess(ctx context.Context, agentID string) ([]AccessEntry, error) {
	sc, err := a.scopeOf(ctx, agentID)
	if err != nil {
		return nil, err
	}
	org := a.Svc.org(ctx)
	desk := JoinPath(RootAgents, agentID)
	out := []AccessEntry{{Path: desk, Level: AccessEdit, Source: SourceDefault, Fixed: true}}
	defaults := []AccessEntry{{Path: "/" + RootShared, Level: AccessRead, Source: SourceDefault}}
	for _, p := range a.projectRoots(ctx, org, sc) {
		defaults = append(defaults, AccessEntry{Path: p, Level: AccessEdit, Source: SourceDefault})
	}
	for _, d := range defaults {
		if sc.ruleAt(d.Path) == "" {
			out = append(out, d)
		}
	}
	for _, g := range sc.rules {
		out = append(out, AccessEntry{Path: g.Path, Level: grantMode(g.Mode), Source: SourceManual, GrantID: g.ID})
	}
	return out, nil
}

// AccessAt is the effective level of an agent on a path (outside any customer scope) and whether a rule or a default
// decided it.
func (a *AgentFS) AccessAt(ctx context.Context, agentID, p string) (level, source string, err error) {
	c, err := CleanPath(p)
	if err != nil {
		return "", "", err
	}
	sc, err := a.scopeOf(ctx, agentID)
	if err != nil {
		return "", "", err
	}
	switch a.zone(ctx, a.Svc.org(ctx), sc, c) {
	case ZoneDesk, ZoneProject:
		return AccessEdit, SourceDefault, nil
	case ZoneShared:
		return AccessRead, SourceDefault, nil
	case ZoneGranted:
		return AccessRead, SourceManual, nil
	case ZoneGrantedEdit:
		return AccessEdit, SourceManual, nil
	case ZoneDenied:
		return AccessNone, SourceManual, nil
	}
	return AccessNone, SourceDefault, nil
}

// projectRoots are the folders of the projects the agent works on.
func (a *AgentFS) projectRoots(ctx context.Context, org string, sc scope) []string {
	var out []string
	if len(sc.projects) == 0 {
		return out
	}
	top, err := a.Svc.cfg.Store.FolderByPath(ctx, org, "/"+RootProjects)
	if err != nil {
		return out
	}
	kids, err := a.Svc.cfg.Store.ChildFolders(ctx, org, top.ID)
	if err != nil {
		return out
	}
	for _, k := range kids {
		if sc.projects[k.RefID] {
			out = append(out, k.Path)
		}
	}
	return out
}

// SetAgentAccess sets the level of an agent on a folder (none | read | edit), or removes the rule (default). It
// replaces the active rule on exactly that folder and returns the level it had ("" = no rule). Only people change
// access (the API also checks who: admins and the head of the agent's team); every change is audited.
func (s *Service) SetAgentAccess(ctx context.Context, by Principal, agentID, path, level string) (string, error) {
	org := s.org(ctx)
	if err := checkPrincipal(by); err != nil || by.IsAgent() {
		return "", forbidden("forbidden", "only people change folder access")
	}
	if level != AccessDefault && !ValidAccess(level) {
		return "", invalid("level_invalid", "level must be none, read, edit or default")
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || len(agentID) > 80 {
		return "", invalid("agent_invalid", "an agent id is required")
	}
	c, err := CleanPath(path)
	if err != nil || c == "/" {
		return "", invalid("path_invalid", "access needs a folder path below /")
	}
	if IsUnder(c, JoinPath(RootAgents, agentID)) {
		return "", invalid("own_desk", "the agent's own folder is always Edit")
	}
	if err := s.gate(ctx, org); err != nil {
		return "", err
	}
	gs, err := s.cfg.Store.Grants(ctx, org, agentID)
	if err != nil {
		return "", err
	}
	prev, ids := "", []string{}
	for _, g := range gs {
		if strings.EqualFold(g.Path, c) {
			prev, ids = grantMode(g.Mode), append(ids, g.ID)
		}
	}
	if prev == level || prev == "" && level == AccessDefault {
		return prev, nil // nothing changes
	}
	for _, gid := range ids {
		if err := s.cfg.Store.RevokeGrant(ctx, org, gid, s.now()); err != nil {
			return "", mapStore(err, "grant")
		}
	}
	id := ""
	if level != AccessDefault {
		g := Grant{ID: newID("grt_"), AgentID: agentID, Path: c, Mode: level, CreatedBy: by, CreatedAt: s.now()}
		if err := s.cfg.Store.AddGrant(ctx, org, g); err != nil {
			return "", err
		}
		id = g.ID
	}
	from := prev
	if from == "" {
		from = AccessDefault
	}
	s.audit(ctx, org, by, "drive.access.changed", "grant", id, map[string]any{"agent_id": agentID, "path": c, "from": from, "to": level})
	return prev, nil
}
