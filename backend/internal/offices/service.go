package offices

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Config wires the service. The functions read the rest of the organization (org chart, members, agents).
type Config struct {
	Store       Store
	Departments func(ctx context.Context, org string) ([]Dept, error)
	// Placements: user id -> department id (the org chart); AgentDepts: agent id -> department id.
	Placements func(ctx context.Context, org string) (map[string]string, error)
	AgentDepts func(ctx context.Context, org string) (map[string]string, error)
	People     func(ctx context.Context, org string) ([]Person, error)
	Agents     func(ctx context.Context, org string) ([]AgentRef, error)
	// CleanNote cleans the text of a note (the /recordar cleaner): "" refuses it. nil = trim and cap.
	CleanNote func(text string) string
	Audit     func(ctx context.Context, org, actor, action, id string, details map[string]any)
	Now       func() time.Time
	// TTL of the cached model (default 10 s). Every write through the service drops it at once.
	TTL time.Duration
}

// Service is the offices model and its changes.
type Service struct {
	cfg   Config
	mu    sync.Mutex
	cache map[string]cached
	wmu   sync.Mutex // serializes the writes (check then write)
}

type cached struct {
	at time.Time
	m  *Model
}

// New builds the service.
func New(cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 10 * time.Second
	}
	return &Service{cfg: cfg, cache: map[string]cached{}}
}

func (s *Service) now() time.Time { return s.cfg.Now().UTC() }

func (s *Service) invalidate(org string) {
	s.mu.Lock()
	delete(s.cache, org)
	s.mu.Unlock()
}

func (s *Service) audit(ctx context.Context, org string, v Viewer, action, id string, d map[string]any) {
	if s.cfg.Audit != nil {
		s.cfg.Audit(ctx, org, v.UserID, action, id, d)
	}
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrInvalid, fmt.Sprintf(format, a...))
}

func forbidden(what string) error { return fmt.Errorf("%w: %s", domain.ErrForbidden, what) }

// Model returns the computed model of the organization (cached).
func (s *Service) Model(ctx context.Context, org string) (*Model, error) {
	s.mu.Lock()
	if c, ok := s.cache[org]; ok && time.Since(c.at) < s.cfg.TTL {
		s.mu.Unlock()
		return c.m, nil
	}
	s.mu.Unlock()
	m, err := s.build(ctx, org)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.cache[org] = cached{at: time.Now(), m: m}
	s.mu.Unlock()
	return m, nil
}

func (s *Service) build(ctx context.Context, org string) (*Model, error) {
	var (
		depts  []Dept
		adept  = map[string]string{}
		people []Person
		agents []AgentRef
		err    error
	)
	if s.cfg.Departments != nil {
		if depts, err = s.cfg.Departments(ctx, org); err != nil {
			return nil, err
		}
	}
	if s.cfg.AgentDepts != nil {
		if adept, err = s.cfg.AgentDepts(ctx, org); err != nil {
			return nil, err
		}
	}
	if s.cfg.People != nil {
		if people, err = s.cfg.People(ctx, org); err != nil {
			return nil, err
		}
	}
	if s.cfg.Agents != nil {
		if agents, err = s.cfg.Agents(ctx, org); err != nil {
			return nil, err
		}
	}
	rows, err := s.cfg.Store.ListOffices(ctx, org)
	if err != nil {
		return nil, err
	}
	members, err := s.cfg.Store.ListMembers(ctx, org)
	if err != nil {
		return nil, err
	}
	access, err := s.cfg.Store.ListAgentAccess(ctx, org)
	if err != nil {
		return nil, err
	}
	return buildModel(depts, adept, people, agents, rows, members, access), nil
}

// fresh rebuilds the model without the cache (writes check against the current state).
func (s *Service) fresh(ctx context.Context, org string) (*Model, error) {
	s.invalidate(org)
	return s.Model(ctx, org)
}

func (s *Service) officeAfter(ctx context.Context, org, id string) (Office, error) {
	m, err := s.fresh(ctx, org)
	if err != nil {
		return Office{}, err
	}
	o, ok := m.Office(id)
	if !ok {
		return Office{}, fmt.Errorf("%w: office %s", domain.ErrNotFound, id)
	}
	return o, nil
}

func validVisibility(v string) (string, error) {
	switch v {
	case "":
		return Open, nil // open by default (equipos-oficinas.md §10.2)
	case Open, Private:
		return v, nil
	}
	return "", invalid("visibility must be open or private")
}

// Enable turns a department into an office (admins only). Its members are the people placed in the department and
// its head, who becomes the responsible.
func (s *Service) Enable(ctx context.Context, org string, v Viewer, deptID, visibility string) (Office, error) {
	if deptID == GeneralID {
		return Office{}, invalid("General is always an office")
	}
	vis, err := validVisibility(visibility)
	if err != nil {
		return Office{}, err
	}
	if !v.Admin {
		return Office{}, forbidden("only an admin creates offices")
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	var dept *Dept
	if s.cfg.Departments != nil {
		ds, err := s.cfg.Departments(ctx, org)
		if err != nil {
			return Office{}, err
		}
		for i := range ds {
			if ds[i].ID == deptID {
				dept = &ds[i]
			}
		}
	}
	if dept == nil {
		return Office{}, fmt.Errorf("%w: office %s", domain.ErrNotFound, deptID)
	}
	m, err := s.fresh(ctx, org)
	if err != nil {
		return Office{}, err
	}
	if _, ok := m.Office(deptID); ok {
		return Office{}, fmt.Errorf("%w: %s is already an office", domain.ErrConflict, dept.Name)
	}
	now := s.now()
	if err := s.cfg.Store.PutOffice(ctx, org, OfficeRow{DepartmentID: deptID, Visibility: vis, CreatedBy: v.UserID, CreatedAt: now, UpdatedAt: now}); err != nil {
		return Office{}, err
	}
	seed := map[string]string{}
	if s.cfg.Placements != nil {
		if place, err := s.cfg.Placements(ctx, org); err == nil {
			for u, d := range place {
				if d == deptID {
					seed[u] = RoleMember
				}
			}
		}
	}
	if dept.HeadUser != "" {
		seed[dept.HeadUser] = RoleLead
	}
	for u, role := range seed {
		if _, ok := m.Person(u); !ok {
			continue
		}
		if err := s.cfg.Store.PutMember(ctx, org, MemberRow{OfficeID: deptID, UserID: u, Role: role, AddedBy: v.UserID, AddedAt: now}); err != nil {
			return Office{}, err
		}
	}
	s.audit(ctx, org, v, "office.enabled", deptID, map[string]any{"name": dept.Name, "visibility": vis, "members": len(seed)})
	return s.officeAfter(ctx, org, deptID)
}

// Disable turns an office back into a plain department (admins only). An office that still owns agents cannot
// be closed: they are passed to another office first (equipos-colaboracion.md §4).
func (s *Service) Disable(ctx context.Context, org string, v Viewer, id string) error {
	if id == GeneralID {
		return invalid("General cannot be closed")
	}
	if !v.Admin {
		return forbidden("only an admin closes offices")
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	m, err := s.fresh(ctx, org)
	if err != nil {
		return err
	}
	if _, ok := m.Office(id); !ok {
		return fmt.Errorf("%w: office %s", domain.ErrNotFound, id)
	}
	for _, row := range m.rows {
		if row.OwnerKind == OwnerOffice && row.OwnerID == id {
			return fmt.Errorf("%w: the office still owns agents; pass them to another office first", domain.ErrConflict)
		}
	}
	if err := s.cfg.Store.DeleteOffice(ctx, org, id); err != nil {
		return err
	}
	s.invalidate(org)
	s.audit(ctx, org, v, "office.disabled", id, nil)
	return nil
}

// SetVisibility opens or closes an office (its responsible or an admin).
func (s *Service) SetVisibility(ctx context.Context, org string, v Viewer, id, visibility string) (Office, error) {
	if id == GeneralID {
		return Office{}, invalid("General is always open")
	}
	vis, err := validVisibility(visibility)
	if err != nil {
		return Office{}, err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	m, err := s.fresh(ctx, org)
	if err != nil {
		return Office{}, err
	}
	o, ok := m.Office(id)
	if !ok {
		return Office{}, fmt.Errorf("%w: office %s", domain.ErrNotFound, id)
	}
	if !m.CanManage(v, id) {
		return Office{}, forbidden("only its responsible or an admin changes an office")
	}
	if err := s.cfg.Store.PutOffice(ctx, org, OfficeRow{DepartmentID: id, Visibility: vis, CreatedBy: "", CreatedAt: o.CreatedAt, UpdatedAt: s.now()}); err != nil {
		return Office{}, err
	}
	s.audit(ctx, org, v, "office.visibility", id, map[string]any{"from": o.Visibility, "to": vis})
	return s.officeAfter(ctx, org, id)
}

// AddMember adds a person to an office or changes their role (its responsible or an admin).
func (s *Service) AddMember(ctx context.Context, org string, v Viewer, id, user, role string) (Office, error) {
	if id == GeneralID {
		return Office{}, invalid("everybody is a member of General")
	}
	if role == "" {
		role = RoleMember
	}
	if role != RoleMember && role != RoleLead {
		return Office{}, invalid("role must be lead or member")
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	m, err := s.fresh(ctx, org)
	if err != nil {
		return Office{}, err
	}
	if _, ok := m.Office(id); !ok {
		return Office{}, fmt.Errorf("%w: office %s", domain.ErrNotFound, id)
	}
	if !m.CanManage(v, id) {
		return Office{}, forbidden("only its responsible or an admin adds members")
	}
	if _, ok := m.Person(user); !ok {
		return Office{}, invalid("%s is not a member of the organization", user)
	}
	if err := s.cfg.Store.PutMember(ctx, org, MemberRow{OfficeID: id, UserID: user, Role: role, AddedBy: v.UserID, AddedAt: s.now()}); err != nil {
		return Office{}, err
	}
	s.audit(ctx, org, v, "office.member_set", id, map[string]any{"user": user, "role": role})
	return s.officeAfter(ctx, org, id)
}

// RemoveMember takes a person out of an office (its responsible, an admin, or the person themself).
func (s *Service) RemoveMember(ctx context.Context, org string, v Viewer, id, user string) (Office, error) {
	if id == GeneralID {
		return Office{}, invalid("everybody is a member of General")
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	m, err := s.fresh(ctx, org)
	if err != nil {
		return Office{}, err
	}
	if _, ok := m.Office(id); !ok {
		return Office{}, fmt.Errorf("%w: office %s", domain.ErrNotFound, id)
	}
	if !m.CanManage(v, id) && v.UserID != user {
		return Office{}, forbidden("only its responsible or an admin removes members")
	}
	if err := s.cfg.Store.DeleteMember(ctx, org, id, user); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return Office{}, err
	}
	s.audit(ctx, org, v, "office.member_removed", id, map[string]any{"user": user})
	return s.officeAfter(ctx, org, id)
}
