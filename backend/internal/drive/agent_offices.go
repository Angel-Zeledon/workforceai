package drive

import (
	"context"
	"errors"
	"strings"

	"aiworkforce/backend/internal/domain"
)

// Agents and office folders (D1, carpetas de departamento):
//
//	its office    an agent of an office works in /Offices/<its office> like on its desk: reads and writes without
//	              approval (ZoneOffice, a default at depth 2: a Ninguno rule below it still closes a subfolder).
//	other offices closed by default (ZoneOtherOff); only a rule (drive_grants) opens one, and the API lets a person
//	              create that rule only when the person has that access.
//	the person    when the call works for a person (WithRequester), the agent never goes beyond that person inside
//	              /Offices: none → closed (also for its own office), read → reads, writes ask a human.
//
// An agent of General, or a drive without an AgentOffices adapter, has no office folder. Paths are compared without
// regard to case, like the store resolves them, so /offices/legal/... evades nothing.

// AgentOffices tells the office an agent belongs to ("" = General or none). Implemented in cmd/server over the
// offices model; it must fail closed (an error makes the agent's scope unknown, hence nothing readable).
type AgentOffices interface {
	AgentOffice(ctx context.Context, org, agentID string) (string, error)
}

// SetAgentOffices plugs the adapter after construction (offices are built after the drive). Call it before serving.
// AgentFS.Offices, when set, takes precedence.
func (s *Service) SetAgentOffices(o AgentOffices) { s.cfg.AgentOffices = o }

// RequesterFrom is the person ctx works for (WithRequester), "" when none.
func RequesterFrom(ctx context.Context) string { return requesterFrom(ctx) }

// officeFolderOf is the folder path of the agent's office ("" = none: General, no adapter or no folder yet).
func (a *AgentFS) officeFolderOf(ctx context.Context, org, agentID string) (string, error) {
	src := a.Offices
	if src == nil {
		src = a.Svc.cfg.AgentOffices
	}
	if src == nil {
		return "", nil
	}
	id, err := src.AgentOffice(ctx, org, agentID)
	if err != nil || id == "" {
		return "", err
	}
	f, err := a.Svc.OfficeFolder(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return f.Path, nil
}

// personRequester is the person a call works for; actors that are not people (a schedule, the system, the
// single-user mode) are nobody.
func personRequester(ctx context.Context) string {
	switch p := strings.TrimSpace(requesterFrom(ctx)); p {
	case "user", "system", "scheduler":
		return ""
	default:
		return p
	}
}

func underOffices(p string) bool {
	segs, err := Segments(p)
	return err == nil && len(segs) > 0 && strings.EqualFold(segs[0], RootOffices)
}

// personLimit narrows a zone inside /Offices to what the person the call works for may do there.
func (a *AgentFS) personLimit(ctx context.Context, sc scope, p, z string) string {
	if sc.person == "" || z == ZoneDenied || !underOffices(p) {
		return z
	}
	switch a.Svc.PersonAccess(ctx, sc.person, p) {
	case AccessNone:
		return ZoneDenied
	case AccessRead:
		if autoWrite(z) {
			return ZoneGranted // the person only reads there: so does the agent, writes ask a human
		}
	}
	return z
}

// personRoots narrows readable roots to the person the call works for: a root inside an office the person cannot see
// is dropped, and a root above several offices (/Offices or /) excludes those offices.
func (a *AgentFS) personRoots(ctx context.Context, org string, sc scope, roots []string) []string {
	if sc.person == "" || a.Svc.cfg.People == nil {
		return roots
	}
	memo := map[string]string{}
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		if !strings.HasPrefix(r, "!") && depthOf(r) >= 2 && underOffices(r) && a.Svc.personAccess(ctx, sc.person, r, memo) == AccessNone {
			continue
		}
		out = append(out, r)
	}
	top, err := a.Svc.cfg.Store.FolderByPath(ctx, org, "/"+RootOffices)
	if errors.Is(err, domain.ErrNotFound) {
		return out // no offices yet
	}
	var kids []Folder
	if err == nil {
		kids, err = a.Svc.cfg.Store.ChildFolders(ctx, org, top.ID)
	}
	if err != nil {
		return []string{} // unknown: nothing readable rather than an office the person may not see
	}
	for _, k := range kids {
		if InScope(out, k.Path) && a.Svc.personAccess(ctx, sc.person, k.Path, memo) == AccessNone {
			out = append(out, "!"+k.Path)
		}
	}
	return out
}
