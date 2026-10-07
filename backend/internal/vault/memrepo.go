package vault

import (
	"context"
	"fmt"
	"sync"
)

// MemRepo is an in-memory Repo (tests, simulation without Postgres).
type MemRepo struct {
	mu    sync.Mutex
	keys  map[string]DataKey // org/version
	creds []Record
}

// NewMemRepo creates an empty repo.
func NewMemRepo() *MemRepo { return &MemRepo{keys: map[string]DataKey{}} }

func kk(org string, v int) string { return fmt.Sprintf("%s/%d", org, v) }

func (m *MemRepo) ActiveDataKey(_ context.Context, org string) (DataKey, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best DataKey
	for _, k := range m.keys {
		if k.Org == org && k.Status == "active" && k.Version > best.Version {
			best = k
		}
	}
	return best, best.Version > 0, nil
}

func (m *MemRepo) GetDataKey(_ context.Context, org string, v int) (DataKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[kk(org, v)]
	if !ok {
		return DataKey{}, ErrNotFound
	}
	return k, nil
}

func (m *MemRepo) ListDataKeys(_ context.Context, org string) ([]DataKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []DataKey
	for _, k := range m.keys {
		if k.Org == org {
			out = append(out, k)
		}
	}
	return out, nil
}

func (m *MemRepo) PutDataKey(_ context.Context, k DataKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keys[kk(k.Org, k.Version)] = k
	return nil
}

func (m *MemRepo) UpdateDataKey(ctx context.Context, k DataKey) error { return m.PutDataKey(ctx, k) }

func (m *MemRepo) PutCredential(_ context.Context, r Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.creds = append(m.creds, r)
	return nil
}

func (m *MemRepo) ActiveCredential(_ context.Context, org, conn string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.creds) - 1; i >= 0; i-- {
		c := m.creds[i]
		if c.Org == org && c.ConnID == conn && c.Status == StatusActive {
			return c, nil
		}
	}
	for _, c := range m.creds {
		if c.Org == org && c.ConnID == conn && c.Status == StatusDestroyed {
			return Record{}, ErrDestroyed
		}
	}
	return Record{}, ErrNotFound
}

func (m *MemRepo) RetireActive(_ context.Context, org, conn string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.creds {
		if c := &m.creds[i]; c.Org == org && c.ConnID == conn && c.Status == StatusActive {
			c.Status = StatusRetired
		}
	}
	return nil
}

func (m *MemRepo) DestroyAll(_ context.Context, org, conn string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.creds {
		if c := &m.creds[i]; c.Org == org && c.ConnID == conn {
			c.Status, c.Ciphertext, c.Nonce = StatusDestroyed, nil, nil
		}
	}
	return nil
}

// RawCiphertexts exposes stored ciphertext (tests: prove no plaintext at rest).
func (m *MemRepo) RawCiphertexts() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out [][]byte
	for _, c := range m.creds {
		out = append(out, append([]byte(nil), c.Ciphertext...))
	}
	return out
}

// Swap exchanges the stored ciphertexts of two records (tests: AAD binding).
func (m *MemRepo) Swap(i, j int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.creds[i].Ciphertext, m.creds[j].Ciphertext = m.creds[j].Ciphertext, m.creds[i].Ciphertext
	m.creds[i].Nonce, m.creds[j].Nonce = m.creds[j].Nonce, m.creds[i].Nonce
}
