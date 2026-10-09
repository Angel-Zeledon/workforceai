package application

import (
	"context"
	"sync"
	"time"
)

// spendTTL bounds how stale the cached org spend may get when ANOTHER process
// writes costs to the same database. In a single process the counter is exact
// (every cost goes through Budget.Record) and the TTL only re-syncs it.
const spendTTL = 15 * time.Second

// spendCounter caches the org-wide spend (SUM of requests.cost_usd) so the hot
// paths (every runtime attempt, every reservation, every warning check) do not
// run an aggregate query. It is incremented by Budget.Record right after the
// cost reaches the store and re-synced from the store every spendTTL.
type spendCounter struct {
	mu    sync.Mutex
	byOrg map[string]*orgSpend
	adds  uint64 // bumped by every add: detects an add racing with a re-sync
}

type orgSpend struct {
	used   float64
	synced time.Time
}

func (c *spendCounter) add(org string, usd float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.adds++
	if e := c.byOrg[org]; e != nil {
		e.used += usd
	}
}

func (c *spendCounter) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byOrg = nil
	c.adds++
}

// OrgSpent returns the organization's total spend, from the incremental counter
// when it is fresh, else from the store (which also re-seeds the counter).
func (b *Budget) OrgSpent(ctx context.Context, org string) (float64, error) {
	c := &b.spend
	c.mu.Lock()
	if e := c.byOrg[org]; e != nil && time.Since(e.synced) < spendTTL {
		used := e.used
		c.mu.Unlock()
		return used, nil
	}
	gen := c.adds
	c.mu.Unlock()

	used, err := b.store.OrgCost(ctx, org)
	if err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.adds != gen {
		// A cost was recorded while querying: the query may or may not include
		// it. Do not seed the cache from an ambiguous value; the next call retries.
		return used, nil
	}
	if c.byOrg == nil {
		c.byOrg = map[string]*orgSpend{}
	}
	c.byOrg[org] = &orgSpend{used: used, synced: time.Now()}
	return used, nil
}
