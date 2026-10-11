package projects

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"

	"aiworkforce/backend/internal/domain"
)

// Store persists projects and the custom templates of an organization. Every
// method is scoped by org id (the Postgres implementation also enforces it with
// Row-Level Security, migration 270).
type Store interface {
	// Put upserts a project by (org, id).
	Put(ctx context.Context, r Record) error
	Get(ctx context.Context, org, id string) (Record, error)
	// List returns the projects of the organization, newest first.
	List(ctx context.Context, org string) ([]Record, error)
	// ByRequest finds the project launched as the given orchestrator request.
	ByRequest(ctx context.Context, org, requestID string) (Record, error)

	PutTemplate(ctx context.Context, org string, t Template) error
	ListTemplates(ctx context.Context, org string) ([]Template, error)
	// GetTemplate returns one organization template (domain.ErrNotFound when absent).
	GetTemplate(ctx context.Context, org, id string) (Template, error)
	// DeleteTemplate removes an organization template and its history.
	DeleteTemplate(ctx context.Context, org, id string) error
	// PutTemplateVersion appends a snapshot to the history of a template (idempotent by version).
	PutTemplateVersion(ctx context.Context, org string, v TemplateVersion) error
	// ListTemplateVersions returns the history of a template, newest first.
	ListTemplateVersions(ctx context.Context, org, id string) ([]TemplateVersion, error)
}

func cloneRecord(r Record) Record {
	b, _ := json.Marshal(r)
	var out Record
	_ = json.Unmarshal(b, &out)
	return out
}

// MemStore is the in-memory Store (demo mode and tests).
type MemStore struct {
	mu   sync.Mutex
	recs map[string]Record // org|id
	tpls map[string][]Template
	vers map[string][]TemplateVersion // org|template id
	chg  *MemChangeStore
}

func NewMemStore() *MemStore {
	return &MemStore{recs: map[string]Record{}, tpls: map[string][]Template{}, vers: map[string][]TemplateVersion{}}
}

var _ Store = (*MemStore)(nil)

// MemStore also keeps the plan changes (Q2).
var _ ChangeStore = (*MemStore)(nil)

func (m *MemStore) PutChange(ctx context.Context, c PlanChange) error {
	return m.changes().PutChange(ctx, c)
}
func (m *MemStore) GetChange(ctx context.Context, org, project, id string) (PlanChange, error) {
	return m.changes().GetChange(ctx, org, project, id)
}
func (m *MemStore) ListChanges(ctx context.Context, org, project string) ([]PlanChange, error) {
	return m.changes().ListChanges(ctx, org, project)
}

func (m *MemStore) changes() *MemChangeStore {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.chg == nil {
		m.chg = NewMemChangeStore()
	}
	return m.chg
}

func (m *MemStore) Put(_ context.Context, r Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recs[r.OrgID+"|"+r.ID] = cloneRecord(r)
	return nil
}

func (m *MemStore) Get(_ context.Context, org, id string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.recs[org+"|"+id]
	if !ok {
		return Record{}, domain.ErrNotFound
	}
	return cloneRecord(r), nil
}

func (m *MemStore) List(_ context.Context, org string) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Record{}
	for k, r := range m.recs {
		if strings.HasPrefix(k, org+"|") {
			out = append(out, cloneRecord(r))
		}
	}
	slices.SortFunc(out, func(a, b Record) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out, nil
}

func (m *MemStore) ByRequest(_ context.Context, org, requestID string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, r := range m.recs {
		if strings.HasPrefix(k, org+"|") && r.RequestID == requestID && requestID != "" {
			return cloneRecord(r), nil
		}
	}
	return Record{}, domain.ErrNotFound
}

func (m *MemStore) PutTemplate(_ context.Context, org string, t Template) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.tpls[org]
	for i := range list {
		if list[i].ID == t.ID {
			list[i] = cloneTemplate(t)
			return nil
		}
	}
	m.tpls[org] = append(list, cloneTemplate(t))
	return nil
}

func (m *MemStore) ListTemplates(_ context.Context, org string) ([]Template, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Template, 0, len(m.tpls[org]))
	for _, t := range m.tpls[org] {
		out = append(out, cloneTemplate(t))
	}
	return out, nil
}

func (m *MemStore) GetTemplate(_ context.Context, org, id string) (Template, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tpls[org] {
		if t.ID == id {
			return cloneTemplate(t), nil
		}
	}
	return Template{}, domain.ErrNotFound
}

func (m *MemStore) DeleteTemplate(_ context.Context, org, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.tpls[org]
	i := slices.IndexFunc(list, func(t Template) bool { return t.ID == id })
	if i < 0 {
		return domain.ErrNotFound
	}
	m.tpls[org] = slices.Delete(slices.Clone(list), i, i+1)
	delete(m.vers, org+"|"+id)
	return nil
}

func (m *MemStore) PutTemplateVersion(_ context.Context, org string, v TemplateVersion) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := org + "|" + v.TemplateID
	for _, e := range m.vers[k] {
		if e.Version == v.Version {
			return nil
		}
	}
	m.vers[k] = append(m.vers[k], cloneVersion(v))
	return nil
}

func (m *MemStore) ListTemplateVersions(_ context.Context, org, id string) ([]TemplateVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]TemplateVersion, 0, len(m.vers[org+"|"+id]))
	for _, v := range m.vers[org+"|"+id] {
		out = append(out, cloneVersion(v))
	}
	slices.SortFunc(out, func(a, b TemplateVersion) int { return b.Version - a.Version })
	return out, nil
}

func cloneTemplate(t Template) Template {
	b, _ := json.Marshal(t)
	var out Template
	_ = json.Unmarshal(b, &out)
	return out
}

func cloneVersion(v TemplateVersion) TemplateVersion {
	b, _ := json.Marshal(v)
	var out TemplateVersion
	_ = json.Unmarshal(b, &out)
	return out
}
