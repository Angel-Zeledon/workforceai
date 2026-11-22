package officetemplates

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/domain"
)

// Store keeps the templates imported into each organization (Postgres: office_templates, migration 826, RLS).
type Store interface {
	PutTemplate(ctx context.Context, org string, s Stored) error
	// ListTemplates returns the organization's templates, newest first.
	ListTemplates(ctx context.Context, org string) ([]Stored, error)
	GetTemplate(ctx context.Context, org, id string) (Stored, error)
	DeleteTemplate(ctx context.Context, org, id string) error
}

// Library is the set of imported templates of the organizations.
type Library struct {
	Store Store
	Now   func() time.Time
	mu    sync.Mutex
}

func (l *Library) now() time.Time {
	if l.Now != nil {
		return l.Now().UTC()
	}
	return time.Now().UTC()
}

// Save keeps a validated template. The same content imported again returns the copy already kept (created=false).
func (l *Library) Save(ctx context.Context, org, actor string, t Template) (Stored, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fp := Fingerprint(t)
	all, err := l.Store.ListTemplates(ctx, org)
	if err != nil {
		return Stored{}, false, err
	}
	for _, s := range all {
		if s.Fingerprint == fp {
			return s, false, nil
		}
	}
	if len(all) >= MaxStoredPerOrg {
		return Stored{}, false, fmt.Errorf("%w: at most %d imported templates; delete one first", domain.ErrConflict, MaxStoredPerOrg)
	}
	s := Stored{ID: "otpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16], Name: t.Name, Kind: t.Kind, Fingerprint: fp,
		Template: t, CreatedBy: actor, CreatedAt: l.now()}
	if err := l.Store.PutTemplate(ctx, org, s); err != nil {
		return Stored{}, false, err
	}
	return s, true, nil
}

// MemStore is the in-memory Store (tests and the demo without Postgres).
type MemStore struct {
	mu sync.Mutex
	m  map[string]map[string]Stored
}

// NewMemStore returns an empty MemStore.
func NewMemStore() *MemStore { return &MemStore{m: map[string]map[string]Stored{}} }

func (s *MemStore) PutTemplate(_ context.Context, org string, t Stored) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m[org] == nil {
		s.m[org] = map[string]Stored{}
	}
	s.m[org][t.ID] = t
	return nil
}

func (s *MemStore) ListTemplates(_ context.Context, org string) ([]Stored, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Stored{}
	for _, t := range s.m[org] {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s *MemStore) GetTemplate(_ context.Context, org, id string) (Stored, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.m[org][id]
	if !ok {
		return Stored{}, fmt.Errorf("%w: template %s", domain.ErrNotFound, id)
	}
	return t, nil
}

func (s *MemStore) DeleteTemplate(_ context.Context, org, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[org][id]; !ok {
		return fmt.Errorf("%w: template %s", domain.ErrNotFound, id)
	}
	delete(s.m[org], id)
	return nil
}
