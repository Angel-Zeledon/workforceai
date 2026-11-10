package skills

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/offices"
)

// Skills of a department. A skill with an OfficeID belongs to that department:
// only its agents can have it, and whoever cannot see the department's work
// (a private office they are not a member of, administrators included) does not
// see the skill at all: it answers as if it did not exist.
//
// Without an offices model (an organization without departments, the open
// demo) nothing is filtered or restricted.

// OfficeModel returns the computed offices model of an organization.
type OfficeModel func(ctx context.Context, org string) (*offices.Model, error)

// officeSource holds the model function; set once at wiring time.
type officeSource struct {
	mu sync.RWMutex
	fn OfficeModel
}

// SetOffices wires the offices model (the offices service starts after the
// library). nil = no departments.
func (s *Service) SetOffices(fn func(ctx context.Context, org string) (*offices.Model, error)) {
	s.offices.mu.Lock()
	s.offices.fn = fn
	s.offices.mu.Unlock()
}

// offView is what the library knows of the departments for one call: the model,
// nothing (no offices service: the open demo, no filtering by design) or a
// failure (the model could not be built: the skills of departments stay closed).
type offView struct {
	m      *offices.Model
	failed bool
}

func (s *Service) model(ctx context.Context, org string) offView {
	s.offices.mu.RLock()
	fn := s.offices.fn
	s.offices.mu.RUnlock()
	if fn == nil {
		return offView{}
	}
	m, err := fn(ctx, org)
	if err != nil || m == nil {
		return offView{failed: true}
	}
	return offView{m: m}
}

func viewerOf(a Actor) offices.Viewer { return offices.Viewer{UserID: a.ID, Admin: a.Admin} }

// canSee: the actor may know that the skill exists. System actors (the runtime)
// are not people and are not filtered here; assignment keeps them in line.
func canSee(v offView, a Actor, sk Skill) bool {
	if sk.OfficeID == "" || a.System || a.Open {
		return true
	}
	if v.failed {
		return false
	}
	m := v.m
	if m == nil {
		return true
	}
	o, ok := m.Office(sk.OfficeID)
	if !ok {
		return a.Admin // the department was closed: only administrators still find its skills
	}
	return o.Visibility == offices.Open || m.IsMember(a.ID, sk.OfficeID)
}

// canWrite: who may create, edit or delete a skill of the department.
func canWrite(v offView, a Actor, office string) bool {
	if office == "" || a.System || a.Open {
		return true
	}
	if v.failed {
		return false
	}
	m := v.m
	if m == nil {
		return true
	}
	if _, ok := m.Office(office); !ok {
		return a.Admin
	}
	return a.Admin || m.IsLead(a.ID, office)
}

// visible loads a skill and answers "not found" when the actor cannot see it.
func (s *Service) visible(ctx context.Context, org string, a Actor, id string) (Skill, error) {
	sk, err := s.skill(ctx, org, id)
	if err != nil {
		return sk, err
	}
	if m := s.model(ctx, org); !canSee(m, a, sk) {
		return Skill{}, notFound("skill not found")
	}
	return sk, nil
}

// writable is visible plus the right to change it.
func (s *Service) writable(ctx context.Context, org string, a Actor, id string) (Skill, error) {
	sk, err := s.skill(ctx, org, id)
	if err != nil {
		return sk, err
	}
	m := s.model(ctx, org)
	if !canSee(m, a, sk) {
		return Skill{}, notFound("skill not found")
	}
	if sk.OfficeID != "" && !canWrite(m, a, sk.OfficeID) {
		return Skill{}, forbidden("office_forbidden", "only the responsible of the department or an administrator can change its skills")
	}
	return sk, nil
}

// withOfficeName fills the display name of the department.
func withOfficeName(v offView, sk Skill) Skill {
	if sk.OfficeID == "" || v.m == nil {
		return sk
	}
	if o, ok := v.m.Office(sk.OfficeID); ok {
		sk.OfficeName = o.Name
	}
	return sk
}

// officeFor validates the department of a new skill ("" and General = the company).
func (s *Service) officeFor(ctx context.Context, org string, a Actor, office string) (string, error) {
	if office == "" || office == offices.GeneralID {
		return "", nil
	}
	v := s.model(ctx, org)
	if v.failed {
		return "", forbidden("office_unavailable", "the departments cannot be read right now; try again")
	}
	m := v.m
	if m == nil {
		return "", invalid("office_invalid", "this organization has no departments")
	}
	if _, ok := m.Office(office); !ok {
		return "", invalid("office_invalid", "unknown department")
	}
	if !canSee(v, a, Skill{OfficeID: office}) {
		return "", forbidden("office_forbidden", "you cannot add skills to a department you cannot see")
	}
	if !canWrite(v, a, office) {
		return "", forbidden("office_forbidden", "only the responsible of the department or an administrator can add its skills")
	}
	return office, nil
}

// skillDir is the drive folder of a version. A company skill lives in the shared
// folder; a department's skill lives inside the department's own sealed folder
// (/Offices/<office>/Skills/<name>/vN), so whoever cannot read that folder cannot
// read the skill's files through the drive either.
func (s *Service) skillDir(ctx context.Context, org, office, name string, n int) (string, error) {
	if office == "" {
		return VersionDir(name, n), nil
	}
	ctx = application.WithOrg(ctx, org)
	f, err := s.cfg.Drive.OfficeFolder(ctx, office)
	if errors.Is(err, domain.ErrNotFound) {
		display := office
		if v := s.model(ctx, org); v.m != nil {
			if o, ok := v.m.Office(office); ok {
				display = o.Name
			}
		}
		f, err = s.cfg.Drive.EnsureOfficeFolder(ctx, office, display, nil)
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/Skills/%s/v%d", f.Path, name, n), nil
}
