package artifacts

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"

	"aiworkforce/backend/internal/domain"
)

// Stored is an artifact as persisted: the head meta and content.
type Stored struct {
	Meta    Meta
	Content json.RawMessage
}

// Store persists artifacts, their immutable versions and the links between
// them. Every method is scoped by org id (the Postgres implementation adds
// Row-Level Security, migration 280). Writers are serialized per artifact by the
// service; Commit is nevertheless a compare-and-swap on the head version.
type Store interface {
	// Create inserts an artifact with its first version.
	Create(ctx context.Context, org string, a Stored, v Version) error
	Get(ctx context.Context, org, id string) (Stored, error)
	// List returns the meta of every artifact of the organization, newest update first.
	List(ctx context.Context, org string) ([]Meta, error)
	// Commit appends version v and replaces the head, only when the stored head
	// version is expectedHead; otherwise it returns domain.ErrConflict.
	Commit(ctx context.Context, org string, a Stored, v Version, expectedHead int) error
	// UpdateMeta changes title, status, attachments, progress, project fields...
	// but never the head version, content, size or last author.
	UpdateMeta(ctx context.Context, org string, m Meta) error
	GetVersion(ctx context.Context, org, id string, n int) (Version, error)
	ListVersions(ctx context.Context, org, id string) ([]VersionInfo, error)

	// ReplaceAutoLinks swaps the links derived from the content of `from`
	// (keeping their synced version when the target stays the same).
	ReplaceAutoLinks(ctx context.Context, org, from string, links []Link) error
	AddLink(ctx context.Context, org string, l Link) error
	// ListLinks returns the links where id is source or target.
	ListLinks(ctx context.Context, org, id string) ([]Link, error)
	// ListAllLinks returns every link of the organization (project workspaces).
	ListAllLinks(ctx context.Context, org string) ([]Link, error)
	SetSyncedVersion(ctx context.Context, org, linkID string, version int) error
}

func cloneStored(a Stored) Stored {
	b, _ := json.Marshal(a.Meta)
	var m Meta
	_ = json.Unmarshal(b, &m)
	return Stored{Meta: m, Content: slices.Clone(a.Content)}
}

func cloneVersion(v Version) Version {
	v.Content = slices.Clone(v.Content)
	return v
}

// MemStore is the in-memory Store (demo mode and tests).
type MemStore struct {
	mu       sync.Mutex
	items    map[string]Stored // org|id
	versions map[string][]Version
	links    map[string][]Link // org
}

func NewMemStore() *MemStore {
	return &MemStore{items: map[string]Stored{}, versions: map[string][]Version{}, links: map[string][]Link{}}
}

var _ Store = (*MemStore)(nil)

func key(org, id string) string { return org + "|" + id }

func (m *MemStore) Create(_ context.Context, org string, a Stored, v Version) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[key(org, a.Meta.ID)]; ok {
		return domain.ErrConflict
	}
	m.items[key(org, a.Meta.ID)] = cloneStored(a)
	m.versions[key(org, a.Meta.ID)] = []Version{cloneVersion(v)}
	return nil
}

func (m *MemStore) Get(_ context.Context, org, id string) (Stored, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.items[key(org, id)]
	if !ok {
		return Stored{}, domain.ErrNotFound
	}
	return cloneStored(a), nil
}

func (m *MemStore) List(_ context.Context, org string) ([]Meta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Meta{}
	for k, a := range m.items {
		if strings.HasPrefix(k, org+"|") {
			out = append(out, cloneStored(a).Meta)
		}
	}
	slices.SortFunc(out, func(a, b Meta) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	return out, nil
}

func (m *MemStore) Commit(_ context.Context, org string, a Stored, v Version, expectedHead int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.items[key(org, a.Meta.ID)]
	if !ok {
		return domain.ErrNotFound
	}
	if cur.Meta.HeadVersion != expectedHead {
		return domain.ErrConflict
	}
	m.items[key(org, a.Meta.ID)] = cloneStored(a)
	m.versions[key(org, a.Meta.ID)] = append(m.versions[key(org, a.Meta.ID)], cloneVersion(v))
	return nil
}

func (m *MemStore) UpdateMeta(_ context.Context, org string, meta Meta) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.items[key(org, meta.ID)]
	if !ok {
		return domain.ErrNotFound
	}
	head, size, last := cur.Meta.HeadVersion, cur.Meta.SizeBytes, cur.Meta.LastAuthor
	meta.HeadVersion, meta.SizeBytes, meta.LastAuthor = head, size, last
	m.items[key(org, meta.ID)] = cloneStored(Stored{Meta: meta, Content: cur.Content})
	return nil
}

func (m *MemStore) GetVersion(_ context.Context, org, id string, n int) (Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range m.versions[key(org, id)] {
		if v.Version == n {
			return cloneVersion(v), nil
		}
	}
	return Version{}, domain.ErrNotFound
}

func (m *MemStore) ListVersions(_ context.Context, org, id string) ([]VersionInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	vs := m.versions[key(org, id)]
	out := make([]VersionInfo, 0, len(vs))
	for i := len(vs) - 1; i >= 0; i-- { // newest first
		out = append(out, vs[i].VersionInfo)
	}
	return out, nil
}

func (m *MemStore) ReplaceAutoLinks(_ context.Context, org, from string, links []Link) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	old := map[string]Link{}
	var keep []Link
	for _, l := range m.links[org] {
		if l.From == from && l.Auto {
			old[l.To+"|"+l.Relation] = l
			continue
		}
		keep = append(keep, l)
	}
	for _, l := range links {
		if o, ok := old[l.To+"|"+l.Relation]; ok {
			l.SyncedVersion = o.SyncedVersion
		}
		keep = append(keep, l)
	}
	m.links[org] = keep
	return nil
}

func (m *MemStore) AddLink(_ context.Context, org string, l Link) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.links[org] {
		if x.From == l.From && x.To == l.To && x.Relation == l.Relation && !x.Auto && !l.Auto {
			return domain.ErrConflict
		}
	}
	m.links[org] = append(m.links[org], l)
	return nil
}

func (m *MemStore) ListLinks(_ context.Context, org, id string) ([]Link, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Link
	for _, l := range m.links[org] {
		if l.From == id || l.To == id {
			out = append(out, l)
		}
	}
	return out, nil
}

func (m *MemStore) ListAllLinks(_ context.Context, org string) ([]Link, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.links[org]), nil
}

func (m *MemStore) SetSyncedVersion(_ context.Context, org, linkID string, version int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.links[org] {
		if m.links[org][i].ID == linkID {
			m.links[org][i].SyncedVersion = version
		}
	}
	return nil
}
