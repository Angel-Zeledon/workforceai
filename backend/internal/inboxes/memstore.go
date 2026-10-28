package inboxes

import (
	"context"
	"sort"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// MemStore is the in-memory Store (tests and the open demo without Postgres).
type MemStore struct {
	mu      sync.Mutex
	inboxes map[string]map[string]Inbox // org -> id -> inbox
	pushes  map[string][]Push           // org|inbox -> pushes (oldest first)
}

var _ Store = (*MemStore)(nil)

// NewMemStore builds an empty store.
func NewMemStore() *MemStore {
	return &MemStore{inboxes: map[string]map[string]Inbox{}, pushes: map[string][]Push{}}
}

func cloneInbox(i Inbox) Inbox {
	i.Allowed = append([]string(nil), i.Allowed...)
	return i
}

func clonePush(p Push) Push {
	p.Files = append([]PushedFile(nil), p.Files...)
	return p
}

func (m *MemStore) CreateInbox(_ context.Context, org string, i Inbox) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, byID := range m.inboxes {
		for _, e := range byID {
			if e.TokenHash == i.TokenHash {
				return domain.ErrConflict
			}
		}
	}
	if m.inboxes[org] == nil {
		m.inboxes[org] = map[string]Inbox{}
	}
	if _, ok := m.inboxes[org][i.ID]; ok {
		return domain.ErrConflict
	}
	m.inboxes[org][i.ID] = cloneInbox(i)
	return nil
}

func (m *MemStore) UpdateInbox(_ context.Context, org string, i Inbox) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.inboxes[org][i.ID]
	if !ok {
		return domain.ErrNotFound
	}
	cur.Name, cur.Allowed, cur.MaxBytes, cur.RatePerMin, cur.UpdatedAt = i.Name, append([]string(nil), i.Allowed...), i.MaxBytes, i.RatePerMin, i.UpdatedAt
	m.inboxes[org][i.ID] = cur
	return nil
}

func (m *MemStore) GetInbox(_ context.Context, org, id string) (Inbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.inboxes[org][id]
	if !ok {
		return Inbox{}, domain.ErrNotFound
	}
	return cloneInbox(i), nil
}

func (m *MemStore) ListInboxes(_ context.Context, org string) ([]Inbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Inbox{}
	for _, i := range m.inboxes[org] {
		out = append(out, cloneInbox(i))
	}
	sort.Slice(out, func(a, b int) bool {
		if !out[a].CreatedAt.Equal(out[b].CreatedAt) {
			return out[a].CreatedAt.After(out[b].CreatedAt)
		}
		return out[a].ID > out[b].ID
	})
	return out, nil
}

func (m *MemStore) SetToken(_ context.Context, org, id, hash, hint string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.inboxes[org][id]
	if !ok {
		return domain.ErrNotFound
	}
	i.TokenHash, i.TokenHint, i.RotatedAt, i.UpdatedAt = hash, hint, &at, at
	m.inboxes[org][id] = i
	return nil
}

func (m *MemStore) RevokeInbox(_ context.Context, org, id, by string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.inboxes[org][id]
	if !ok {
		return domain.ErrNotFound
	}
	if i.RevokedAt == nil {
		i.RevokedAt, i.RevokedBy = &at, by
		m.inboxes[org][id] = i
	}
	return nil
}

func (m *MemStore) InboxByHash(_ context.Context, hash string) (string, Inbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for org, byID := range m.inboxes {
		for _, i := range byID {
			if i.TokenHash == hash {
				return org, cloneInbox(i), nil
			}
		}
	}
	return "", Inbox{}, domain.ErrNotFound
}

func pushKey(org, inboxID string) string { return org + "|" + inboxID }

func (m *MemStore) ReservePush(_ context.Context, org string, p Push) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.inboxes[org][p.InboxID]; !ok {
		return domain.ErrNotFound
	}
	k := pushKey(org, p.InboxID)
	if p.IdemKey != "" {
		for _, e := range m.pushes[k] {
			if e.IdemKey == p.IdemKey {
				return domain.ErrConflict
			}
		}
	}
	p.Status = PushPending
	m.pushes[k] = append(m.pushes[k], clonePush(p))
	return nil
}

func (m *MemStore) CompletePush(_ context.Context, org string, p Push) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := pushKey(org, p.InboxID)
	for n, e := range m.pushes[k] {
		if e.ID == p.ID {
			p.Status = PushDone
			m.pushes[k][n] = clonePush(p)
			i := m.inboxes[org][p.InboxID]
			at := p.ReceivedAt
			i.LastPushAt = &at
			i.PushCount++
			m.inboxes[org][p.InboxID] = i
			return nil
		}
	}
	return domain.ErrNotFound
}

func (m *MemStore) ReleasePush(_ context.Context, org, inboxID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := pushKey(org, inboxID)
	for n, e := range m.pushes[k] {
		if e.ID == id && e.Status == PushPending {
			m.pushes[k] = append(m.pushes[k][:n], m.pushes[k][n+1:]...)
			return nil
		}
	}
	return nil
}

func (m *MemStore) PushByKey(_ context.Context, org, inboxID, idemKey string) (Push, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if idemKey == "" {
		return Push{}, domain.ErrNotFound
	}
	for _, e := range m.pushes[pushKey(org, inboxID)] {
		if e.IdemKey == idemKey {
			return clonePush(e), nil
		}
	}
	return Push{}, domain.ErrNotFound
}

func (m *MemStore) Pushes(_ context.Context, org, inboxID string, limit int) ([]Push, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	all := m.pushes[pushKey(org, inboxID)]
	out := []Push{}
	for n := len(all) - 1; n >= 0 && (limit <= 0 || len(out) < limit); n-- {
		if all[n].Status == PushDone {
			out = append(out, clonePush(all[n]))
		}
	}
	return out, nil
}
