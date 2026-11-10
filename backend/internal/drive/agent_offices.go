package drive

import (
	"context"
	"errors"
	"strings"

	"aiworkforce/backend/internal/application"
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
//	nobody        the platform itself (a scheduled run, the system: application.IsSystemActor) leaves the agent its
//	              office, but it writes nothing into another office nor creates one (other_office: refused).
//	foreign       any other actor (a token, a signed link, Slack, another agent) acts for someone the drive cannot
//	              tell: inside /Offices the agent gets nothing for it (requesterOf).
//
// An agent of General, or a drive without an AgentOffices adapter, has no office folder. Paths are recognized like the
// store resolves them (Service.isOfficesRoot), so /offices/legal/... or /OFFİCES/legal/... evades nothing.

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

// requesterOf is who a call works for: a person (application.PersonID), or nobody. foreign marks a requester that is
// neither a person nor the platform itself (a token, a signed link, Slack, another agent...): it acts for someone the
// drive cannot tell, so inside /Offices the agent gets nothing for it, not even its own office (fails closed). Only
// the platform (application.IsSystemActor: a schedule, the system, no requester at all) leaves the agent its office.
func requesterOf(ctx context.Context) (person string, foreign bool) {
	r := strings.TrimSpace(requesterFrom(ctx))
	if p := application.PersonID(r); p != "" {
		return p, false
	}
	return "", !application.IsSystemActor(r)
}

// ZonePersonDenied: inside /Offices, the person the call works for has no access. Nothing there is read, listed or
// written for them, and no approval can change that (it is not the agent's access that is missing).
const ZonePersonDenied = "person_denied"

const (
	denyPersonNoAccess = "person_no_access"
	personNoAccessMsg  = "the person you work for has no access to this office folder, so you cannot use it for them; tell them so (giving you access would not help)"
)

func personDeny() Plan { return deny(denyPersonNoAccess, personNoAccessMsg) }

// denyOtherOffice: with nobody to work for (a scheduled or system run) the agent works with its office only (spec §4.1):
// it writes nothing into another office's folder, nor creates a new one under /Offices. Refused outright, not sent for
// approval: no person's access stands behind it. A rule (drive_grants) that opens that office changes the zone first.
const denyOtherOffice = "other_office"

func otherOfficeDeny() Plan {
	return deny(denyOtherOffice, "this is another office's folder: working on your own you write only in your office, your desk, your projects or /Shared")
}

// otherOfficeRefused reports whether a write into zone z is refused because it lands in another office with nobody
// to work for (with a person, personLimit already narrowed the zone to what they may do).
func (sc scope) otherOfficeRefused(z string) bool { return sc.person == "" && z == ZoneOtherOffice }

// personLimit narrows a zone inside /Offices to what the person the call works for may do there; a foreign requester
// (requesterOf) gets nothing there.
func (a *AgentFS) personLimit(ctx context.Context, org string, sc scope, p, z string) string {
	if sc.person == "" && !sc.foreign || z == ZoneDenied || !a.Svc.underOffices(ctx, org, p) {
		return z
	}
	if sc.foreign {
		return ZonePersonDenied
	}
	switch a.Svc.PersonAccess(ctx, sc.person, p) {
	case AccessNone:
		return ZonePersonDenied
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
	if sc.foreign { // a requester the drive cannot tell: no office at all
		out := make([]string, 0, len(roots)+1)
		for _, r := range roots {
			if strings.HasPrefix(r, "!") || !a.Svc.underOffices(ctx, org, r) {
				out = append(out, r)
			}
		}
		if InScope(out, "/"+RootOffices) {
			out = append(out, "!/"+RootOffices)
		}
		return out
	}
	if sc.person == "" || a.Svc.cfg.People == nil {
		return roots
	}
	memo := map[string]string{}
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		if !strings.HasPrefix(r, "!") && depthOf(r) >= 2 && a.Svc.underOffices(ctx, org, r) && a.Svc.personAccess(ctx, sc.person, r, memo) == AccessNone {
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
