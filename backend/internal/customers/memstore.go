package customers

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// MemStore keeps customers in memory (demo mode without a database, tests).
type MemStore struct {
	mu        sync.Mutex
	customers map[string]map[string]Customer   // org -> id -> customer
	assigns   map[string]map[string]Assignment // org -> id -> clone
	links     map[string]map[string]Link       // org -> kind|ref -> link
}

// NewMemStore builds an empty store.
func NewMemStore() *MemStore {
	return &MemStore{customers: map[string]map[string]Customer{}, assigns: map[string]map[string]Assignment{}, links: map[string]map[string]Link{}}
}

var _ Store = (*MemStore)(nil)

func notFound(what string) error { return fmt.Errorf("%w: %s", ErrNotFound, what) }

func (m *MemStore) nameTaken(org, id, name string) bool {
	for _, c := range m.customers[org] {
		if c.ID != id && !c.Archived && strings.EqualFold(c.Name, name) {
			return true
		}
	}
	return false
}

func (m *MemStore) CreateCustomer(_ context.Context, c Customer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.customers[c.OrgID] == nil {
		m.customers[c.OrgID] = map[string]Customer{}
	}
	if _, ok := m.customers[c.OrgID][c.ID]; ok || m.nameTaken(c.OrgID, c.ID, c.Name) {
		return fmt.Errorf("%w: a customer with that name already exists", ErrConflict)
	}
	m.customers[c.OrgID][c.ID] = c
	return nil
}

func (m *MemStore) UpdateCustomer(_ context.Context, c Customer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.customers[c.OrgID][c.ID]; !ok {
		return notFound("customer")
	}
	if !c.Archived && m.nameTaken(c.OrgID, c.ID, c.Name) {
		return fmt.Errorf("%w: a customer with that name already exists", ErrConflict)
	}
	m.customers[c.OrgID][c.ID] = c
	return nil
}

func (m *MemStore) GetCustomer(_ context.Context, org, id string) (Customer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.customers[org][id]
	if !ok {
		return Customer{}, notFound("customer")
	}
	return c, nil
}

func (m *MemStore) ListCustomers(_ context.Context, org string, includeArchived bool) ([]Customer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Customer{}
	for _, c := range m.customers[org] {
		if includeArchived || !c.Archived {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name); a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *MemStore) AddAssignment(_ context.Context, org string, a Assignment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.assigns[org] == nil {
		m.assigns[org] = map[string]Assignment{}
	}
	for _, x := range m.assigns[org] {
		if x.CustomerID == a.CustomerID && x.AgentID == a.AgentID {
			return fmt.Errorf("%w: the agent already works for this customer", ErrConflict)
		}
	}
	m.assigns[org][a.ID] = a
	return nil
}

func (m *MemStore) DeleteAssignment(_ context.Context, org, customerID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.assigns[org][id]
	if !ok || a.CustomerID != customerID {
		return notFound("assignment")
	}
	delete(m.assigns[org], id)
	return nil
}

func (m *MemStore) Assignments(_ context.Context, org, customerID string) ([]Assignment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Assignment{}
	for _, a := range m.assigns[org] {
		if customerID == "" || a.CustomerID == customerID {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func linkKey(kind, ref string) string { return kind + "|" + ref }

func (m *MemStore) AddLink(_ context.Context, org string, l Link) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.links[org] == nil {
		m.links[org] = map[string]Link{}
	}
	if _, ok := m.links[org][linkKey(l.Kind, l.RefID)]; ok {
		return fmt.Errorf("%w: already scoped to a customer", ErrConflict)
	}
	m.links[org][linkKey(l.Kind, l.RefID)] = l
	return nil
}

func (m *MemStore) DeleteLink(_ context.Context, org, kind, refID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.links[org][linkKey(kind, refID)]; !ok {
		return notFound("link")
	}
	delete(m.links[org], linkKey(kind, refID))
	return nil
}

func (m *MemStore) LinkOf(_ context.Context, org, kind, refID string) (Link, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.links[org][linkKey(kind, refID)]
	if !ok {
		return Link{}, notFound("link")
	}
	return l, nil
}

func (m *MemStore) Links(_ context.Context, org, customerID, kind string, limit int) ([]Link, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Link{}
	for _, l := range m.links[org] {
		if l.Kind == kind && (customerID == "" || l.CustomerID == customerID) {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].RefID < out[j].RefID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
