package offices

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"

	"aiworkforce/backend/internal/domain"
)

// MemStore is the in-memory Store (tests and the open demo without a database).
type MemStore struct {
	mu      sync.Mutex
	offices map[string]map[string]OfficeRow
	members map[string]map[string]MemberRow // key office|user
	access  map[string]map[string]AgentAccess
	notes   map[string][]Note
	deleted map[string]map[string]bool
}

// NewMemStore returns an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{offices: map[string]map[string]OfficeRow{}, members: map[string]map[string]MemberRow{},
		access: map[string]map[string]AgentAccess{}, notes: map[string][]Note{}, deleted: map[string]map[string]bool{}}
}

func sub[T any](m map[string]map[string]T, org string) map[string]T {
	if m[org] == nil {
		m[org] = map[string]T{}
	}
	return m[org]
}

func (m *MemStore) ListOffices(_ context.Context, org string) ([]OfficeRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]OfficeRow, 0)
	for _, o := range sub(m.offices, org) {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].DepartmentID < out[j].DepartmentID
	})
	return out, nil
}

func (m *MemStore) PutOffice(_ context.Context, org string, o OfficeRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	sub(m.offices, org)[o.DepartmentID] = o
	return nil
}

func (m *MemStore) DeleteOffice(_ context.Context, org, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := sub(m.offices, org)[id]; !ok {
		return fmt.Errorf("%w: office %s", domain.ErrNotFound, id)
	}
	delete(sub(m.offices, org), id)
	for k, r := range sub(m.members, org) {
		if r.OfficeID == id {
			delete(sub(m.members, org), k)
		}
	}
	return nil
}

func (m *MemStore) ListMembers(_ context.Context, org string) ([]MemberRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]MemberRow, 0)
	for _, r := range sub(m.members, org) {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OfficeID != out[j].OfficeID {
			return out[i].OfficeID < out[j].OfficeID
		}
		if !out[i].AddedAt.Equal(out[j].AddedAt) {
			return out[i].AddedAt.Before(out[j].AddedAt)
		}
		return out[i].UserID < out[j].UserID
	})
	return out, nil
}

func (m *MemStore) PutMember(_ context.Context, org string, r MemberRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	sub(m.members, org)[r.OfficeID+"|"+r.UserID] = r
	return nil
}

func (m *MemStore) DeleteMember(_ context.Context, org, office, user string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := office + "|" + user
	if _, ok := sub(m.members, org)[k]; !ok {
		return fmt.Errorf("%w: member %s", domain.ErrNotFound, user)
	}
	delete(sub(m.members, org), k)
	return nil
}

func (m *MemStore) ListAgentAccess(_ context.Context, org string) ([]AgentAccess, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AgentAccess, 0)
	for _, a := range sub(m.access, org) {
		a.Shares = slices.Clone(a.Shares)
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	return out, nil
}

func (m *MemStore) PutAgentAccess(_ context.Context, org string, a AgentAccess) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a.Shares = slices.Clone(a.Shares)
	sub(m.access, org)[a.AgentID] = a
	return nil
}

func (m *MemStore) DeleteAgentAccess(_ context.Context, org, agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(sub(m.access, org), agentID)
	return nil
}

func (m *MemStore) ListNotes(_ context.Context, org, scope, ref string) ([]Note, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Note, 0)
	for _, n := range m.notes[org] {
		if sub(m.deleted, org)[n.ID] || scope != "" && n.Scope != scope || ref != "" && n.OwnerRef != ref {
			continue
		}
		out = append(out, n)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *MemStore) PutNote(_ context.Context, org string, n Note) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notes[org] = append(m.notes[org], n)
	return nil
}

func (m *MemStore) DeleteNote(_ context.Context, org, id, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, n := range m.notes[org] {
		if n.ID == id && !sub(m.deleted, org)[id] {
			sub(m.deleted, org)[id] = true
			return nil
		}
	}
	return fmt.Errorf("%w: note %s", domain.ErrNotFound, id)
}
