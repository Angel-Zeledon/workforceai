package skills

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Store persists metadata. Every call is scoped by organization (RLS in
// Postgres). The bytes of the files are NOT here: they are in the drive.
type Store interface {
	// CreateSkill inserts the skill and its first version. domain.ErrConflict
	// when a live skill already has the name.
	CreateSkill(ctx context.Context, org string, s Skill, v Version) error
	// GetSkill returns a live (not deleted) skill; domain.ErrNotFound otherwise.
	GetSkill(ctx context.Context, org, id string) (Skill, error)
	// SkillByName returns the skill with the name, deleted ones included
	// (DeletedAt set), so a deleted skill can be brought back by creating it again.
	SkillByName(ctx context.Context, org, name string) (Skill, error)
	// ListSkills lists live skills (Agents = direct assignments).
	ListSkills(ctx context.Context, org string) ([]Skill, error)
	// CountSkills counts every skill row, deleted ones included (starter seeding).
	CountSkills(ctx context.Context, org string) (int, error)
	// AddVersion appends v and writes s (status, current/active version...)
	// only if the stored current version is still expected (compare-and-swap).
	AddVersion(ctx context.Context, org string, expected int, s Skill, v Version) error
	// UpdateSkill writes the mutable fields of s (status, review fields, active version).
	UpdateSkill(ctx context.Context, org string, s Skill) error
	// DeleteSkill soft-deletes the skill and removes its assignments.
	DeleteSkill(ctx context.Context, org, id string, at time.Time) error
	Versions(ctx context.Context, org, skillID string) ([]Version, error)
	VersionOf(ctx context.Context, org, skillID string, n int) (Version, error)
	// Assign is idempotent.
	Assign(ctx context.Context, org string, a Assignment) error
	Unassign(ctx context.Context, org, agentID, skillID string) error
	AssignmentsOfAgent(ctx context.Context, org, agentID string) ([]Assignment, error)
	AssignmentsOfSkill(ctx context.Context, org, skillID string) ([]Assignment, error)
}

// MemStore is the in-memory Store (tests and the database-less demo).
type MemStore struct {
	mu       sync.Mutex
	skills   map[string]map[string]Skill     // org -> id -> skill
	versions map[string]map[string][]Version // org -> id -> versions
	assigns  map[string][]Assignment         // org -> assignments
}

// NewMemStore returns an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{skills: map[string]map[string]Skill{}, versions: map[string]map[string][]Version{}, assigns: map[string][]Assignment{}}
}

var _ Store = (*MemStore)(nil)

func (m *MemStore) init(org string) {
	if m.skills[org] == nil {
		m.skills[org] = map[string]Skill{}
		m.versions[org] = map[string][]Version{}
	}
}

func (m *MemStore) CreateSkill(_ context.Context, org string, s Skill, v Version) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init(org)
	for _, o := range m.skills[org] {
		if o.DeletedAt == nil && strings.EqualFold(o.Name, s.Name) {
			return domain.ErrConflict
		}
	}
	m.skills[org][s.ID] = s
	m.versions[org][s.ID] = []Version{v}
	return nil
}

func (m *MemStore) GetSkill(_ context.Context, org, id string) (Skill, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.skills[org][id]
	if !ok || s.DeletedAt != nil {
		return Skill{}, domain.ErrNotFound
	}
	return s, nil
}

func (m *MemStore) SkillByName(_ context.Context, org, name string) (Skill, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var found *Skill
	for _, s := range m.skills[org] {
		if strings.EqualFold(s.Name, name) {
			s := s
			if found == nil || (found.DeletedAt != nil && s.DeletedAt == nil) {
				found = &s
			}
		}
	}
	if found == nil {
		return Skill{}, domain.ErrNotFound
	}
	return *found, nil
}

func (m *MemStore) ListSkills(_ context.Context, org string) ([]Skill, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Skill{}
	for _, s := range m.skills[org] {
		if s.DeletedAt != nil {
			continue
		}
		for _, a := range m.assigns[org] {
			if a.SkillID == s.ID {
				s.Agents++
			}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *MemStore) CountSkills(_ context.Context, org string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.skills[org]), nil
}

func (m *MemStore) AddVersion(_ context.Context, org string, expected int, s Skill, v Version) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.skills[org][s.ID]
	if !ok {
		return domain.ErrNotFound
	}
	if cur.CurrentVersion != expected {
		return domain.ErrConflict
	}
	m.skills[org][s.ID] = s
	m.versions[org][s.ID] = append(m.versions[org][s.ID], v)
	return nil
}

func (m *MemStore) UpdateSkill(_ context.Context, org string, s Skill) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.skills[org][s.ID]; !ok {
		return domain.ErrNotFound
	}
	m.skills[org][s.ID] = s
	return nil
}

func (m *MemStore) DeleteSkill(_ context.Context, org, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.skills[org][id]
	if !ok || s.DeletedAt != nil {
		return domain.ErrNotFound
	}
	s.DeletedAt = &at
	m.skills[org][id] = s
	keep := m.assigns[org][:0:0]
	for _, a := range m.assigns[org] {
		if a.SkillID != id {
			keep = append(keep, a)
		}
	}
	m.assigns[org] = keep
	return nil
}

func (m *MemStore) Versions(_ context.Context, org, id string) ([]Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Version{}, m.versions[org][id]...), nil
}

func (m *MemStore) VersionOf(_ context.Context, org, id string, n int) (Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range m.versions[org][id] {
		if v.Version == n {
			return v, nil
		}
	}
	return Version{}, domain.ErrNotFound
}

func (m *MemStore) Assign(_ context.Context, org string, a Assignment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range m.assigns[org] {
		if o.AgentID == a.AgentID && o.SkillID == a.SkillID {
			return nil
		}
	}
	m.assigns[org] = append(m.assigns[org], a)
	return nil
}

func (m *MemStore) Unassign(_ context.Context, org, agentID, skillID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	keep := m.assigns[org][:0:0]
	for _, a := range m.assigns[org] {
		if !(a.AgentID == agentID && a.SkillID == skillID) {
			keep = append(keep, a)
		}
	}
	m.assigns[org] = keep
	return nil
}

func (m *MemStore) AssignmentsOfAgent(_ context.Context, org, agentID string) ([]Assignment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Assignment{}
	for _, a := range m.assigns[org] {
		if a.AgentID == agentID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *MemStore) AssignmentsOfSkill(_ context.Context, org, skillID string) ([]Assignment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Assignment{}
	for _, a := range m.assigns[org] {
		if a.SkillID == skillID {
			out = append(out, a)
		}
	}
	return out, nil
}
