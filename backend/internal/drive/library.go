package drive

import (
	"context"
	"strings"
)

// The library of a position (docs/architecture/positions.md). A position of a department may carry a library: the
// folders its agents search and read (fs.search, fs.read, fs.list, fs.recent and the long-term memory, which share
// readableRoots and zoneAll). A library only acts on what the agent has by default from its department, never beyond:
//
//	its office   /Offices/<its office>/...        default Edit; the library narrows it (and may lower it to Read)
//	/Shared      /Shared/...                      default Read; the library narrows it (and may raise it to Edit)
//	shared in    what another office shares with  default Read; the library narrows it (never Edit: read only)
//	             the agent's office
//
// Everything else stays exactly as it was: the agent's desk (always Edit), its projects (Edit), the rules people set
// for the agent by hand (drive_grants, which still win when at least as deep as the library entry they meet), the
// customer isolation of a customer-scoped task, and the limit of the person the call works for (personLimit, applied
// after). No library (no position, or a position without folders) = today's behaviour.
//
// Inside the base, a path no library entry covers, or covered by an exclusion (level none), is ZonePositionOut: not
// listed, read, searched or recalled. A read of such a file answers the agent "no such file" (the agent does not
// learn it exists) and leaves the real reason in the audit (drive.read_outside_position).

// LibraryRule is one entry of a position's library (level none | read | edit).
type LibraryRule struct {
	Path  string
	Level string
}

// Library is the library of the agent's position. No rules = no library.
type Library struct {
	PositionID   string
	PositionName string
	Rules        []LibraryRule
}

// AgentLibraries tells the library of an agent's position. Implemented by internal/positions; it must fail closed
// (an error makes the agent's scope unknown, hence nothing readable).
type AgentLibraries interface {
	AgentLibrary(ctx context.Context, org, agentID string) (Library, error)
}

// SetAgentLibraries plugs the positions after construction. Call it before serving.
func (s *Service) SetAgentLibraries(l AgentLibraries) { s.cfg.AgentLibraries = l }

// ZonePositionOut: inside the agent's base, outside the library of its position.
const ZonePositionOut = "position_out"

// SourcePosition marks an access line that comes from the agent's position.
const SourcePosition = "position"

// denyOutsidePosition is the internal reason (audit) of a read outside the library; the agent is told not_found.
const denyOutsidePosition = "outside_position"

// libraryBase: the zones a library acts on.
func libraryBase(z string) bool { return z == ZoneShared || z == ZoneOffice || z == ZoneSharedIn }

// loadLibrary fills the library of the agent's position. Rules outside the agent's base (a share that was revoked,
// a stale path) are dropped; the library still narrows (dropping a rule never widens).
func (a *AgentFS) loadLibrary(ctx context.Context, org string, sc *scope) error {
	src := a.Svc.cfg.AgentLibraries
	if src == nil {
		return nil
	}
	lib, err := src.AgentLibrary(ctx, org, sc.agent)
	if err != nil {
		return err
	}
	if len(lib.Rules) == 0 {
		return nil
	}
	sc.libOn, sc.position, sc.positionName = true, lib.PositionID, lib.PositionName
	for _, r := range lib.Rules {
		c, err := CleanPath(r.Path)
		if err != nil || c == "/" || !ValidAccess(r.Level) {
			continue
		}
		if z, _ := a.baseZone(ctx, org, *sc, c); libraryBase(z) {
			sc.lib = append(sc.lib, LibraryRule{Path: c, Level: r.Level})
		}
	}
	return nil
}

// deepestLib is the most specific library entry covering p (nil when none does).
func (sc scope) deepestLib(p string) *LibraryRule {
	var best *LibraryRule
	lp := strings.ToLower(p)
	for i := range sc.lib {
		r := &sc.lib[i]
		if IsUnder(lp, strings.ToLower(r.Path)) && (best == nil || depthOf(r.Path) > depthOf(best.Path)) {
			best = r
		}
	}
	return best
}

// applyPosition narrows a base zone by the library. It returns the zone and the depth a rule set by hand must reach
// to override it (the deeper of the default's root and the library entry that decided).
func (sc scope) applyPosition(z string, depth int, p string) (string, int) {
	if !sc.libOn || !libraryBase(z) {
		return z, depth
	}
	r := sc.deepestLib(p)
	if r == nil {
		return ZonePositionOut, depth
	}
	if d := depthOf(r.Path); d > depth {
		depth = d
	}
	switch r.Level {
	case AccessEdit:
		switch z {
		case ZoneSharedIn:
			return z, depth // shared for reading: never written (the positions service refuses it too)
		case ZoneOffice:
			return z, depth
		}
		return ZoneGrantedEdit, depth
	case AccessRead:
		if z == ZoneOffice {
			return ZoneGranted, depth // reads its office there; writing asks a person
		}
		return z, depth
	}
	return ZonePositionOut, depth
}

// narrowRoots replaces the default roots a library acts on (/Shared, its office, what is shared with it) with the
// library: its included folders and its exclusions. A customer-scoped task keeps only the library under /Shared (an
// office folder is not the customer's). Exclusions where a person set a rule by hand are left to that rule.
func (a *AgentFS) narrowRoots(ctx context.Context, org string, sc scope, roots []string) []string {
	if !sc.libOn {
		return roots
	}
	out := make([]string, 0, len(roots)+len(sc.lib))
	for _, r := range roots {
		if strings.HasPrefix(r, "!") {
			out = append(out, r)
			continue
		}
		if strings.EqualFold(r, "/"+RootShared) || sc.office != "" && strings.EqualFold(r, sc.office) || containsFold(sc.sharedIn, r) {
			continue // a default root the library replaces
		}
		out = append(out, r)
	}
	for _, l := range sc.lib {
		if l.Level == AccessNone {
			continue
		}
		if sc.customer != "" {
			if z, _ := a.baseZone(ctx, org, sc, l.Path); z != ZoneShared {
				continue
			}
		}
		if !containsFold(out, l.Path) {
			out = append(out, l.Path)
		}
	}
	for _, l := range sc.lib {
		if l.Level == AccessNone && sc.ruleAt(l.Path) == "" && InScope(out, l.Path) {
			out = append(out, "!"+l.Path)
		}
	}
	return out
}

// libraryEntries are the "Lo que puede consultar" lines of the library (a line where a person set a rule by hand is
// shown by that rule instead).
func (sc scope) libraryEntries() []AccessEntry {
	var out []AccessEntry
	for _, l := range sc.lib {
		if sc.ruleAt(l.Path) == "" {
			out = append(out, AccessEntry{Path: l.Path, Level: l.Level, Source: SourcePosition, Position: sc.positionName})
		}
	}
	return out
}

// auditOutside leaves the internal reason of a refusal by the library (the agent was told not_found).
func (a *AgentFS) auditOutside(ctx context.Context, agentID, action, path string) {
	sc, err := a.scopeOf(ctx, agentID)
	if err != nil {
		return
	}
	a.Svc.audit(ctx, a.Svc.org(ctx), AgentP(agentID), "drive.read_outside_position", "file", path, map[string]any{
		"agent_id": agentID, "action": action, "path": path, "position_id": sc.position, "position": sc.positionName, "reason": denyOutsidePosition})
}

// outsideDeny answers like a missing file or folder (the agent does not learn it exists); Run audits the reason.
func outsideDeny(msg string) Plan {
	pl := deny("not_found", msg)
	pl.outside = true
	return pl
}

// SharedInPaths lists what other departments share with a department (active shares): the read-only part of its
// bounds for a position's library.
func (s *Service) SharedInPaths(ctx context.Context, officeID string) ([]string, error) {
	return s.sharedInPaths(ctx, s.org(ctx), officeID)
}
