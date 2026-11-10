package skills

import (
	"context"
	"sync"

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

// model returns the offices model or nil when there is none (or it fails to
// build: the library then behaves as without departments, never as closed).
func (s *Service) model(ctx context.Context, org string) *offices.Model {
	s.offices.mu.RLock()
	fn := s.offices.fn
	s.offices.mu.RUnlock()
	if fn == nil {
		return nil
	}
	m, err := fn(ctx, org)
	if err != nil {
		return nil
	}
	return m
}

func viewerOf(a Actor) offices.Viewer { return offices.Viewer{UserID: a.ID, Admin: a.Admin} }

// canSee: the actor may know that the skill exists. System actors (the runtime)
// are not people and are not filtered here; assignment keeps them in line.
func canSee(m *offices.Model, a Actor, sk Skill) bool {
	if sk.OfficeID == "" || m == nil || a.System || a.Open {
		return true
	}
	o, ok := m.Office(sk.OfficeID)
	if !ok {
		return a.Admin // the department was closed: only administrators still find its skills
	}
	return o.Visibility == offices.Open || m.IsMember(a.ID, sk.OfficeID)
}

// canWrite: who may create, edit or delete a skill of the department.
func canWrite(m *offices.Model, a Actor, office string) bool {
	if office == "" || m == nil || a.System || a.Open {
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
func withOfficeName(m *offices.Model, sk Skill) Skill {
	if sk.OfficeID == "" || m == nil {
		return sk
	}
	if o, ok := m.Office(sk.OfficeID); ok {
		sk.OfficeName = o.Name
	}
	return sk
}

// officeFor validates the department of a new skill ("" and General = the company).
func (s *Service) officeFor(ctx context.Context, org string, a Actor, office string) (string, error) {
	if office == "" || office == offices.GeneralID {
		return "", nil
	}
	m := s.model(ctx, org)
	if m == nil {
		return "", invalid("office_invalid", "this organization has no departments")
	}
	if _, ok := m.Office(office); !ok {
		return "", invalid("office_invalid", "unknown department")
	}
	if !canSee(m, a, Skill{OfficeID: office}) {
		return "", forbidden("office_forbidden", "you cannot add skills to a department you cannot see")
	}
	if !canWrite(m, a, office) {
		return "", forbidden("office_forbidden", "only the responsible of the department or an administrator can add its skills")
	}
	return office, nil
}
