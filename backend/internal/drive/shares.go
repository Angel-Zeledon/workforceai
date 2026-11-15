package drive

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Shares between departments (D4, docs/superpowers/plans/2026-11-15-d4-entre-departamentos.md): a department shares a
// file or a folder of its drive folder (/Offices/<owner>/...) with another department, read only. The request lives
// in its own table (not in the approvals inbox, which decides by agent): a responsible of the owner decides, and if
// the person asking is one, the share is born active. Rules:
//
//	what      a file or folder strictly inside /Offices/<owner>/ (never the office folder itself)
//	to whom   another office that is not General and not the owner
//	who sees  the members of the target office read the path and everything under it (never edit); an agent of the
//	          target office reads it too, always narrowed by the person it works for (ZoneSharedIn)
//	revoke    rejected and revoked shares open nothing: the access is read from the store on every call

// Share statuses.
const (
	ShareStatusPending  = "pending"
	ShareStatusActive   = "active"
	ShareStatusRejected = "rejected"
	ShareStatusRevoked  = "revoked"
)

// Share kinds.
const (
	ShareKindFile   = "file"
	ShareKindFolder = "folder"
)

// Share decisions.
const (
	ShareApprove = "approve"
	ShareReject  = "reject"
)

// Share is a request to share a path of an office with another office, and what became of it. CanDecide and CanRevoke
// are filled for the person who asks (they are not stored).
type Share struct {
	ID             string     `json:"id"`
	Path           string     `json:"path"`
	Kind           string     `json:"kind"`
	FileID         string     `json:"file_id"` // the id of the file, or of the folder when Kind is folder
	Name           string     `json:"name"`
	OwnerOfficeID  string     `json:"owner_office_id"`
	TargetOfficeID string     `json:"target_office_id"`
	Status         string     `json:"status"`
	RequestedBy    string     `json:"requested_by"`
	RequestedAt    time.Time  `json:"requested_at"`
	DecidedBy      string     `json:"decided_by,omitempty"`
	DecidedAt      *time.Time `json:"decided_at,omitempty"`
	CanDecide      bool       `json:"can_decide"`
	CanRevoke      bool       `json:"can_revoke"`
}

// ShareQuery filters ListShares; an empty field does not filter.
type ShareQuery struct {
	OwnerOfficeID  string
	TargetOfficeID string
	Statuses       []string
}

// ShareStore is implemented by the stores that keep shares (MemStore and the Postgres DriveStore). It is optional:
// a Store without it simply has no shares.
type ShareStore interface {
	CreateShare(ctx context.Context, org string, s Share) error
	ShareByID(ctx context.Context, org, id string) (Share, error)
	// ListShares returns the shares matching q, newest request first.
	ListShares(ctx context.Context, org string, q ShareQuery) ([]Share, error)
	// MoveShare sets the status (and who decided, when) only if the share is still in one of from
	// (compare-and-swap): domain.ErrConflict otherwise, domain.ErrNotFound for an unknown id.
	MoveShare(ctx context.Context, org, id string, from []string, to, by string, at time.Time) error
}

// OfficeRoster is what the drive needs to know about an office to decide who may do what with its shares.
type OfficeRoster struct {
	Known   bool // the office exists (General included)
	General bool
	Leads   []string // the responsibles that are also members
	Members []string
}

// ShareOffices is the adapter over the offices (implemented in cmd/server). It must fail closed: an error is never
// membership.
type ShareOffices interface {
	OfficeRoster(ctx context.Context, org, officeID string) (OfficeRoster, error)
}

// SetShareOffices plugs the adapter after construction (offices are built after the drive). Without it, shares are
// not available.
func (s *Service) SetShareOffices(o ShareOffices) { s.cfg.ShareOffices = o }

func (r OfficeRoster) isMember(user string) bool { return user != "" && slices.Contains(r.Members, user) }

// canDecide: a responsible of the office, or any member when it has none.
func (r OfficeRoster) canDecide(user string) bool {
	if !r.isMember(user) {
		return false
	}
	return len(r.Leads) == 0 || slices.Contains(r.Leads, user)
}

// pathCovers reports whether p is sharePath or lies under it (names compare without regard to case).
func pathCovers(sharePath, p string) bool {
	return IsUnder(strings.ToLower(p), strings.ToLower(sharePath))
}

func (s *Service) shareStore() (ShareStore, ShareOffices, error) {
	st, ok := s.cfg.Store.(ShareStore)
	if !ok || s.cfg.ShareOffices == nil {
		return nil, nil, errf(domain.ErrNotFound, "shares_unavailable", "sharing between departments is not available")
	}
	return st, s.cfg.ShareOffices, nil
}

// rosters caches OfficeRoster lookups for one call.
type rosters struct {
	ctx  context.Context
	org  string
	src  ShareOffices
	seen map[string]OfficeRoster
}

func newRosters(ctx context.Context, org string, src ShareOffices) *rosters {
	return &rosters{ctx: ctx, org: org, src: src, seen: map[string]OfficeRoster{}}
}

func (r *rosters) get(officeID string) (OfficeRoster, error) {
	if ro, ok := r.seen[officeID]; ok {
		return ro, nil
	}
	ro, err := r.src.OfficeRoster(r.ctx, r.org, officeID)
	if err != nil {
		return OfficeRoster{}, err
	}
	r.seen[officeID] = ro
	return ro, nil
}

// view fills what the viewer may do with a share.
func (r *rosters) view(sh Share, viewer string) Share {
	sh.CanDecide, sh.CanRevoke = false, false
	if sh.Status != ShareStatusPending && sh.Status != ShareStatusActive {
		return sh
	}
	ro, err := r.get(sh.OwnerOfficeID)
	if err != nil {
		return sh // unknown: nothing allowed
	}
	decider := ro.canDecide(viewer)
	sh.CanDecide = decider && sh.Status == ShareStatusPending
	sh.CanRevoke = decider || viewer != "" && viewer == sh.RequestedBy
	return sh
}

// RequestShare asks to share path (a file or folder strictly inside /Offices/<owner>/) with the target office. The
// caller has already checked that the person may edit path. A responsible of the owner office shares at once; anyone
// else leaves a pending request.
func (s *Service) RequestShare(ctx context.Context, userID, path, targetOfficeID string) (Share, error) {
	st, src, err := s.shareStore()
	if err != nil {
		return Share{}, err
	}
	org := s.org(ctx)
	if err := s.gate(ctx, org); err != nil {
		return Share{}, err
	}
	segs, err := Segments(path)
	if err != nil {
		return Share{}, err
	}
	if len(segs) < 3 || !s.isOfficesRoot(ctx, org, segs[0]) {
		return Share{}, invalid("not_in_office", "only a file or folder inside a department's folder can be shared")
	}
	officeFolder, err := s.cfg.Store.FolderByPath(ctx, org, JoinPath(RootOffices, segs[1]))
	if err != nil || officeFolder.RefID == "" {
		return Share{}, notFound("folder not found")
	}
	owner := officeFolder.RefID
	rs := newRosters(ctx, org, src)
	ownerRoster, err := rs.get(owner)
	if err != nil || !ownerRoster.isMember(userID) {
		return Share{}, notFound("folder not found") // fails closed; the person must belong to the owner
	}
	// what is being shared: a file or a folder (canonical path and name)
	var kind, name, canon, fid string
	if f, ferr := s.FileByPath(ctx, path); ferr == nil {
		kind, name, canon, fid = ShareKindFile, f.Name, f.Path, f.ID
	} else if fo, derr := s.cfg.Store.FolderByPath(ctx, org, path); derr == nil {
		kind, name, canon, fid = ShareKindFolder, fo.Name, fo.Path, fo.ID
	} else if errors.Is(derr, domain.ErrNotFound) {
		return Share{}, notFound("folder not found")
	} else {
		return Share{}, derr
	}
	target, err := rs.get(targetOfficeID)
	if err != nil || !target.Known {
		return Share{}, notFound("department not found")
	}
	switch {
	case target.General:
		return Share{}, invalid("general_office", "a folder cannot be shared with General: it is the whole company")
	case targetOfficeID == owner:
		return Share{}, invalid("same_office", "choose another department")
	}
	live, err := st.ListShares(ctx, org, ShareQuery{OwnerOfficeID: owner, TargetOfficeID: targetOfficeID, Statuses: []string{ShareStatusPending, ShareStatusActive}})
	if err != nil {
		return Share{}, err
	}
	for _, l := range live {
		if strings.EqualFold(l.Path, canon) {
			return Share{}, conflict("already_shared", "this is already shared with that department or waiting for approval")
		}
	}
	now := s.now()
	sh := Share{ID: newID("shr_"), Path: canon, Kind: kind, FileID: fid, Name: name, OwnerOfficeID: owner, TargetOfficeID: targetOfficeID,
		Status: ShareStatusPending, RequestedBy: userID, RequestedAt: now}
	if ownerRoster.canDecide(userID) {
		sh.Status, sh.DecidedBy, sh.DecidedAt = ShareStatusActive, userID, &now
	}
	if err := st.CreateShare(ctx, org, sh); err != nil {
		return Share{}, mapStore(err, "share")
	}
	s.audit(ctx, org, Human(userID), "drive.share.requested", "share", sh.ID, map[string]any{"path": sh.Path, "owner_office_id": owner, "target_office_id": targetOfficeID, "status": sh.Status})
	return rs.view(sh, userID), nil
}

// DecideShare approves or rejects a pending request. 404: the share is unknown or the person is not a member of the
// owner office; domain.ErrForbidden (not_decider): a member who is not a responsible; 409 not_pending.
func (s *Service) DecideShare(ctx context.Context, userID, id, decision string) (Share, error) {
	st, src, err := s.shareStore()
	if err != nil {
		return Share{}, err
	}
	org := s.org(ctx)
	if decision != ShareApprove && decision != ShareReject {
		return Share{}, invalid("invalid_decision", "decision must be approve or reject")
	}
	sh, err := st.ShareByID(ctx, org, id)
	if err != nil {
		return Share{}, mapStore(err, "share")
	}
	rs := newRosters(ctx, org, src)
	ro, err := rs.get(sh.OwnerOfficeID)
	if err != nil || !ro.isMember(userID) {
		return Share{}, notFound("share not found")
	}
	if !ro.canDecide(userID) {
		return Share{}, forbidden("not_decider", "only a responsible of the department can decide")
	}
	if err := s.gate(ctx, org); err != nil {
		return Share{}, err
	}
	to := ShareStatusActive
	if decision == ShareReject {
		to = ShareStatusRejected
	}
	now := s.now()
	if err := st.MoveShare(ctx, org, id, []string{ShareStatusPending}, to, userID, now); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return Share{}, conflict("not_pending", "this request was already decided")
		}
		return Share{}, mapStore(err, "share")
	}
	sh.Status, sh.DecidedBy, sh.DecidedAt = to, userID, &now
	s.audit(ctx, org, Human(userID), "drive.share.decided", "share", sh.ID, map[string]any{"path": sh.Path, "decision": decision, "target_office_id": sh.TargetOfficeID})
	return rs.view(sh, userID), nil
}

// RevokeShare ends a pending or active share. The person who asked and the deciders of the owner office may.
func (s *Service) RevokeShare(ctx context.Context, userID, id string) error {
	st, src, err := s.shareStore()
	if err != nil {
		return err
	}
	org := s.org(ctx)
	sh, err := st.ShareByID(ctx, org, id)
	if err != nil {
		return mapStore(err, "share")
	}
	rs := newRosters(ctx, org, src)
	ro, err := rs.get(sh.OwnerOfficeID)
	if err != nil {
		return notFound("share not found")
	}
	if !ro.canDecide(userID) && !(userID != "" && userID == sh.RequestedBy) {
		if ro.isMember(userID) {
			return forbidden("not_decider", "only who asked or a responsible of the department can stop sharing")
		}
		return notFound("share not found")
	}
	if err := s.gate(ctx, org); err != nil {
		return err
	}
	now := s.now()
	if err := st.MoveShare(ctx, org, id, []string{ShareStatusPending, ShareStatusActive}, ShareStatusRevoked, userID, now); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return conflict("not_active", "this share already ended")
		}
		return mapStore(err, "share")
	}
	s.audit(ctx, org, Human(userID), "drive.share.revoked", "share", sh.ID, map[string]any{"path": sh.Path, "target_office_id": sh.TargetOfficeID})
	return nil
}

// Directions of OfficeShares.
const (
	ShareOut = "out"
	ShareIn  = "in"
)

// OfficeShares lists the shares of an office for one of its members: "out" the requests that leave it (any status in
// statuses, all when empty), "in" the active shares that reach it. A person who is not a member gets 404.
func (s *Service) OfficeShares(ctx context.Context, userID, officeID, dir string, statuses []string) ([]Share, error) {
	st, src, err := s.shareStore()
	if err != nil {
		return nil, err
	}
	org := s.org(ctx)
	if dir != ShareOut && dir != ShareIn {
		return nil, invalid("invalid_dir", "dir must be out or in")
	}
	for _, v := range statuses {
		if !slices.Contains([]string{ShareStatusPending, ShareStatusActive, ShareStatusRejected, ShareStatusRevoked}, v) {
			return nil, invalid("invalid_status", "unknown status %q", v)
		}
	}
	rs := newRosters(ctx, org, src)
	ro, err := rs.get(officeID)
	if err != nil || !ro.Known || !ro.isMember(userID) {
		return nil, notFound("department not found")
	}
	q := ShareQuery{OwnerOfficeID: officeID, Statuses: statuses}
	if dir == ShareIn {
		q = ShareQuery{TargetOfficeID: officeID, Statuses: []string{ShareStatusActive}}
	}
	list, err := st.ListShares(ctx, org, q)
	if err != nil {
		return nil, err
	}
	out := make([]Share, 0, len(list))
	for _, sh := range list {
		out = append(out, rs.view(sh, userID))
	}
	return out, nil
}

// SharesAt lists the pending and active shares of path: the ones made on it and the ones made on a folder above it
// ("who can see this"). The caller has already checked that the person may read path.
func (s *Service) SharesAt(ctx context.Context, userID, path string) ([]Share, error) {
	st, src, err := s.shareStore()
	if err != nil {
		return nil, err
	}
	org := s.org(ctx)
	out := []Share{}
	segs, err := Segments(path)
	if err != nil || len(segs) < 3 || !s.isOfficesRoot(ctx, org, segs[0]) {
		return out, nil // only paths inside a department's folder are shared
	}
	officeFolder, err := s.cfg.Store.FolderByPath(ctx, org, JoinPath(RootOffices, segs[1]))
	if err != nil || officeFolder.RefID == "" {
		return out, nil
	}
	list, err := st.ListShares(ctx, org, ShareQuery{OwnerOfficeID: officeFolder.RefID, Statuses: []string{ShareStatusPending, ShareStatusActive}})
	if err != nil {
		return nil, err
	}
	rs := newRosters(ctx, org, src)
	for _, sh := range list {
		if pathCovers(sh.Path, path) {
			out = append(out, rs.view(sh, userID))
		}
	}
	return out, nil
}

// sharedRead reports whether an active share of the owner office gives userID read on path: the person belongs to the
// target office. Any error is no access.
func (s *Service) sharedRead(ctx context.Context, org, userID, ownerOfficeID, path string, m *accessMemo) bool {
	st, ok := s.cfg.Store.(ShareStore)
	if !ok || s.cfg.ShareOffices == nil || userID == "" {
		return false
	}
	list, ok := m.shares[ownerOfficeID]
	if !ok {
		var err error
		if list, err = st.ListShares(ctx, org, ShareQuery{OwnerOfficeID: ownerOfficeID, Statuses: []string{ShareStatusActive}}); err != nil {
			list = nil
		}
		m.shares[ownerOfficeID] = list
	}
	for _, sh := range list {
		if !pathCovers(sh.Path, path) {
			continue
		}
		key := sh.TargetOfficeID + "|" + userID
		member, ok := m.member[key]
		if !ok {
			ro, err := s.cfg.ShareOffices.OfficeRoster(ctx, org, sh.TargetOfficeID)
			member = err == nil && ro.isMember(userID)
			m.member[key] = member
		}
		if member {
			return true
		}
	}
	return false
}

// sharedInPaths are the paths other offices share with officeID (active shares), for an agent of that office. Paths
// whose owner office has no folder (any more) are left out. An error is returned so the caller fails closed.
func (s *Service) sharedInPaths(ctx context.Context, org, officeID string) ([]string, error) {
	st, ok := s.cfg.Store.(ShareStore)
	if !ok || officeID == "" {
		return nil, nil
	}
	list, err := st.ListShares(ctx, org, ShareQuery{TargetOfficeID: officeID, Statuses: []string{ShareStatusActive}})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, sh := range list {
		of, err := s.OfficeFolder(ctx, sh.OwnerOfficeID)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if sh.Path != of.Path && pathCovers(of.Path, sh.Path) {
			out = append(out, sh.Path)
		}
	}
	sort.Strings(out)
	return out, nil
}
