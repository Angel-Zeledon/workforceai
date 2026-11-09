package drive

import "context"

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
	if err != nil || len(segs) == 0 || segs[0] != RootOffices {
		return AccessEdit
	}
	if len(segs) == 1 {
		return AccessRead
	}
	if lvl, ok := memo[segs[1]]; ok {
		return lvl
	}
	lvl := s.officeLevel(ctx, userID, segs[1])
	memo[segs[1]] = lvl
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
