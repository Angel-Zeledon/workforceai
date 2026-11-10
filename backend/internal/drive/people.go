package drive

import (
	"context"
	"errors"
	"strings"

	"aiworkforce/backend/internal/domain"
)

// PeopleAccess tells what a person may do inside an office's folder: AccessEdit, AccessRead or AccessNone.
// Implementations must fail closed (an error or an unknown office is never access).
type PeopleAccess interface {
	OfficeAccess(ctx context.Context, org, userID, officeID string) (string, error)
}

// SetPeople plugs the adapter after construction (offices are built after the drive). Call it before serving.
func (s *Service) SetPeople(p PeopleAccess) { s.cfg.People = p }

// PersonAccess is what a person may do at path. Only /Offices/<office>/... is restricted: /Offices itself is
// readable, and everything else (and a drive without an adapter) is edit, as before.
func (s *Service) PersonAccess(ctx context.Context, userID, path string) string {
	return s.personAccess(ctx, userID, path, map[string]string{})
}

func (s *Service) personAccess(ctx context.Context, userID, path string, memo map[string]string) string {
	if s.cfg.People == nil {
		return AccessEdit
	}
	segs, err := Segments(path)
	if err != nil || len(segs) == 0 || !s.isOfficesRoot(ctx, s.org(ctx), segs[0]) {
		return AccessEdit
	}
	if len(segs) == 1 {
		return AccessRead
	}
	key := strings.ToLower(segs[1]) // the store resolves paths without regard to case
	if lvl, ok := memo[key]; ok {
		return lvl
	}
	lvl := s.officeLevel(ctx, userID, segs[1])
	memo[key] = lvl
	return lvl
}

func (s *Service) officeLevel(ctx context.Context, userID, name string) string {
	org := s.org(ctx)
	f, err := s.cfg.Store.FolderByPath(ctx, org, JoinPath(RootOffices, name))
	if err != nil || f.RefID == "" {
		return AccessNone
	}
	lvl, err := s.cfg.People.OfficeAccess(ctx, org, userID, f.RefID)
	if err != nil || !ValidAccess(lvl) {
		return AccessNone
	}
	return lvl
}

// VisibleTo reports, per path, whether the person may see it at all. Each office folder is resolved once per call.
func (s *Service) VisibleTo(ctx context.Context, userID string, paths []string) []bool {
	memo := map[string]string{}
	out := make([]bool, len(paths))
	for i, p := range paths {
		out[i] = s.personAccess(ctx, userID, p, memo) != AccessNone
	}
	return out
}

// InOfficeFolder reports whether path names an office's folder or something inside it (/Offices/<office>/...), under
// any spelling the store resolves there. /Offices itself is not an office's folder.
func (s *Service) InOfficeFolder(ctx context.Context, path string) bool {
	segs, err := Segments(path)
	return err == nil && len(segs) >= 2 && s.isOfficesRoot(ctx, s.org(ctx), segs[0])
}

// isOfficesRoot reports whether a top-level segment names /Offices. Go compares names with EqualFold, but Postgres
// resolves paths with lower() under the database collation, which may fold more ("OFFİCES", with a dotted capital
// İ, lowers to "offices"): a segment that is not one of the well-known roots in ASCII is asked to the store, which
// answers with the folder it would resolve to. One query, only for such a segment; a store error counts as /Offices
// (fails closed: inside /Offices nothing is granted by default).
func (s *Service) isOfficesRoot(ctx context.Context, org, seg string) bool {
	if strings.EqualFold(seg, RootOffices) {
		return true
	}
	for _, r := range Roots {
		if strings.EqualFold(seg, r) {
			return false
		}
	}
	f, err := s.cfg.Store.FolderByPath(ctx, org, "/"+seg)
	if errors.Is(err, domain.ErrNotFound) {
		return false
	}
	return err != nil || f.RootKind == rootKind(RootOffices)
}

// underOffices reports whether a path lies under /Offices (any spelling the store resolves there).
func (s *Service) underOffices(ctx context.Context, org, p string) bool {
	segs, err := Segments(p)
	return err == nil && len(segs) > 0 && s.isOfficesRoot(ctx, org, segs[0])
}

// OfficeMembers is optionally implemented by the PeopleAccess adapter: the user ids of the members of an office.
type OfficeMembers interface {
	OfficeMembers(ctx context.Context, org, officeID string) ([]string, error)
}

// SealedAudience tells who may learn that something happened at path (a live event naming it). sealed is false for a
// path outside an office's folder (anyone in the organization). Inside /Offices/<office>, members are the members of
// that office; nil when they cannot be told (no adapter, an unknown office, an error, nobody): the caller must then
// not name the path at all.
func (s *Service) SealedAudience(ctx context.Context, path string) (members []string, sealed bool) {
	if s.cfg.People == nil {
		return nil, false // no office folders are sealed (PersonAccess: edit everywhere)
	}
	org := s.org(ctx)
	segs, err := Segments(path)
	if err != nil || len(segs) < 2 || !s.isOfficesRoot(ctx, org, segs[0]) {
		return nil, false
	}
	om, ok := s.cfg.People.(OfficeMembers)
	if !ok {
		return nil, true
	}
	f, err := s.cfg.Store.FolderByPath(ctx, org, JoinPath(RootOffices, segs[1]))
	if err != nil || f.RefID == "" {
		return nil, true
	}
	ids, err := om.OfficeMembers(ctx, org, f.RefID)
	if err != nil || len(ids) == 0 {
		return nil, true
	}
	return ids, true
}
