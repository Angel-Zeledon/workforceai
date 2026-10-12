package rewards

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Store persists the wallet, the ledger and the cosmetic state of an
// organization. Every method is scoped by org (the Postgres implementation
// also enforces it with Row-Level Security, migration 530).
type Store interface {
	// Earn appends an earn entry and credits the wallet in one atomic step.
	// It is idempotent on e.IdemKey: a repeat changes nothing and returns the
	// stored entry with applied=false.
	Earn(ctx context.Context, org string, e Entry) (stored Entry, w Wallet, applied bool, err error)
	// Purchase debits the wallet, appends the spend entry and records the item
	// as owned in one atomic step. Concurrency safe: parallel purchases of one
	// organization are serialized and the balance never goes below zero.
	// Idempotent on e.IdemKey (replay=true returns the original result);
	// ErrAlreadyOwned when the item is owned under another key; ErrInsufficient
	// when the balance is too small.
	Purchase(ctx context.Context, org string, e Entry, o Owned) (stored Entry, w Wallet, replay bool, err error)

	Wallet(ctx context.Context, org string) (Wallet, error)
	// Ledger returns entries newest first, those with seq < before when before>0.
	Ledger(ctx context.Context, org string, limit int, before int64) ([]Entry, error)
	// EarnedByAgent sums the earned coins per agent (display only).
	EarnedByAgent(ctx context.Context, org string) (map[string]int64, error)

	Inventory(ctx context.Context, org string) ([]Owned, error)
	// SetEquipped puts item in the slot of the agent ("" empties the slot).
	SetEquipped(ctx context.Context, org, agent, slot, item string) error
	Equipment(ctx context.Context, org string) ([]Equipped, error)
	// SetPlaced puts item in the office slot ("" empties it).
	SetPlaced(ctx context.Context, org, slot, item string) error
	Office(ctx context.Context, org string) ([]Placed, error)
}

type memOrg struct {
	wallet  Wallet
	ledger  []Entry
	byKey   map[string]int // idem key -> index in ledger
	owned   map[string]Owned
	equip   map[[2]string]string // agent,slot -> item
	office  map[string]string
	perAgnt map[string]int64
}

// MemStore is the in-memory Store (no DATABASE_URL, tests).
type MemStore struct {
	mu   sync.Mutex
	orgs map[string]*memOrg
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore { return &MemStore{orgs: map[string]*memOrg{}} }

var _ Store = (*MemStore)(nil)

func (m *MemStore) org(id string) *memOrg {
	o := m.orgs[id]
	if o == nil {
		o = &memOrg{byKey: map[string]int{}, owned: map[string]Owned{}, equip: map[[2]string]string{}, office: map[string]string{}, perAgnt: map[string]int64{}}
		m.orgs[id] = o
	}
	return o
}

func (o *memOrg) append(e Entry) Entry {
	e.ID, e.Seq = uuid.NewString(), int64(len(o.ledger)+1)
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	e.BalanceAfter = o.wallet.Balance
	o.byKey[e.IdemKey] = len(o.ledger)
	o.ledger = append(o.ledger, e)
	return e
}

// Earn implements Store.
func (m *MemStore) Earn(_ context.Context, org string, e Entry) (Entry, Wallet, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.org(org)
	if i, ok := o.byKey[e.IdemKey]; ok {
		return o.ledger[i], o.wallet, false, nil
	}
	o.wallet.Balance += e.Amount
	o.wallet.EarnedTotal += e.Amount
	if e.AgentID != "" {
		o.perAgnt[e.AgentID] += e.Amount
	}
	return o.append(e), o.wallet, true, nil
}

// Purchase implements Store.
func (m *MemStore) Purchase(_ context.Context, org string, e Entry, ow Owned) (Entry, Wallet, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.org(org)
	if i, ok := o.byKey[e.IdemKey]; ok {
		return o.ledger[i], o.wallet, true, nil
	}
	if _, has := o.owned[ow.ItemID]; has {
		return Entry{}, o.wallet, false, ErrAlreadyOwned
	}
	if o.wallet.Balance < e.Amount {
		return Entry{}, o.wallet, false, ErrInsufficient
	}
	o.wallet.Balance -= e.Amount
	o.wallet.SpentTotal += e.Amount
	if ow.PurchasedAt.IsZero() {
		ow.PurchasedAt = time.Now().UTC()
	}
	o.owned[ow.ItemID] = ow
	return o.append(e), o.wallet, false, nil
}

// Wallet implements Store.
func (m *MemStore) Wallet(_ context.Context, org string) (Wallet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.org(org).wallet, nil
}

// Ledger implements Store.
func (m *MemStore) Ledger(_ context.Context, org string, limit int, before int64) ([]Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.org(org)
	out := []Entry{}
	for i := len(o.ledger) - 1; i >= 0 && len(out) < limit; i-- {
		if before > 0 && o.ledger[i].Seq >= before {
			continue
		}
		out = append(out, o.ledger[i])
	}
	return out, nil
}

// EarnedByAgent implements Store.
func (m *MemStore) EarnedByAgent(_ context.Context, org string) (map[string]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]int64{}
	for k, v := range m.org(org).perAgnt {
		out[k] = v
	}
	return out, nil
}

// Inventory implements Store.
func (m *MemStore) Inventory(_ context.Context, org string) ([]Owned, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Owned{}
	for _, v := range m.org(org).owned {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ItemID < out[j].ItemID })
	return out, nil
}

// SetEquipped implements Store.
func (m *MemStore) SetEquipped(_ context.Context, org, agent, slot, item string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.org(org)
	if item == "" {
		delete(o.equip, [2]string{agent, slot})
	} else {
		o.equip[[2]string{agent, slot}] = item
	}
	return nil
}

// Equipment implements Store.
func (m *MemStore) Equipment(_ context.Context, org string) ([]Equipped, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Equipped{}
	for k, v := range m.org(org).equip {
		out = append(out, Equipped{AgentID: k[0], Slot: k[1], ItemID: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AgentID != out[j].AgentID {
			return out[i].AgentID < out[j].AgentID
		}
		return out[i].Slot < out[j].Slot
	})
	return out, nil
}

// SetPlaced implements Store.
func (m *MemStore) SetPlaced(_ context.Context, org, slot, item string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.org(org)
	if item == "" {
		delete(o.office, slot)
	} else {
		o.office[slot] = item
	}
	return nil
}

// Office implements Store.
func (m *MemStore) Office(_ context.Context, org string) ([]Placed, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Placed{}
	for k, v := range m.org(org).office {
		out = append(out, Placed{Slot: k, ItemID: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out, nil
}
