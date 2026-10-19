package presence

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Registry is the presence directory shared by every replica of the server. Each replica keeps the live state of
// its own sockets (positions, throttle, proximity) and publishes WHO is online here, with a TTL, so a replica that
// dies leaves no ghosts and a snapshot taken on any replica lists everyone. The events themselves already travel
// through the events bus (Redis), so the other replicas' avatars keep moving live; the registry only fixes the
// snapshot and the "last socket of the person left" decision. A nil registry (no Redis) keeps the hub per replica.
type Registry interface {
	// Put creates or refreshes the entry of person p held by replica (and renews its TTL).
	Put(ctx context.Context, org, replica string, p Person) error
	// Delete removes the entry.
	Delete(ctx context.Context, org, replica, user string) error
	// List returns the live (not expired) entries of the organization, from every replica.
	List(ctx context.Context, org string) ([]Remote, error)
}

// Remote is a registry entry.
type Remote struct {
	Replica string
	Person  Person
}

// ---- in-memory registry (tests, and several hubs in one process) ----

// MemoryRegistry implements Registry in process, with the same TTL rule as the Redis one.
type MemoryRegistry struct {
	TTL time.Duration // default 30s
	Now func() time.Time

	mu   sync.Mutex
	rows map[string]memRow // org|replica|user
}

type memRow struct {
	r   Remote
	exp time.Time
}

func (m *MemoryRegistry) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *MemoryRegistry) ttl() time.Duration {
	if m.TTL > 0 {
		return m.TTL
	}
	return 30 * time.Second
}

func (m *MemoryRegistry) Put(_ context.Context, org, replica string, p Person) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows == nil {
		m.rows = map[string]memRow{}
	}
	m.rows[org+"|"+replica+"|"+p.UserID] = memRow{r: Remote{Replica: replica, Person: p}, exp: m.now().Add(m.ttl())}
	return nil
}

func (m *MemoryRegistry) Delete(_ context.Context, org, replica, user string) error {
	m.mu.Lock()
	delete(m.rows, org+"|"+replica+"|"+user)
	m.mu.Unlock()
	return nil
}

func (m *MemoryRegistry) List(_ context.Context, org string) ([]Remote, error) {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Remote
	for k, row := range m.rows {
		if !strings.HasPrefix(k, org+"|") {
			continue
		}
		if !now.Before(row.exp) {
			delete(m.rows, k)
			continue
		}
		out = append(out, row.r)
	}
	return out, nil
}

// ---- Redis registry ----

// RedisRegistry keeps one hash per organization: field "replica|user", value {"exp": unix seconds, "p": Person}.
// The expiry lives in the value (hash fields have no TTL in older Redis); the key itself expires too, so an
// abandoned organization disappears. It only needs Eval, so it takes the Scripter the rest of the server already has.
type RedisRegistry struct {
	R      goredis.Scripter
	Prefix string        // default "aiworkforce:presence:"
	TTL    time.Duration // default 30s
	Now    func() time.Time
}

const (
	luaPut  = `redis.call('HSET', KEYS[1], ARGV[1], ARGV[2]); redis.call('EXPIRE', KEYS[1], ARGV[3]); return 1`
	luaDel  = `return redis.call('HDEL', KEYS[1], ARGV[1])`
	luaList = `return redis.call('HGETALL', KEYS[1])`
)

type redisRow struct {
	Exp int64  `json:"exp"`
	P   Person `json:"p"`
}

func (r *RedisRegistry) key(org string) string {
	p := r.Prefix
	if p == "" {
		p = "aiworkforce:presence:"
	}
	return p + org
}

func (r *RedisRegistry) ttl() time.Duration {
	if r.TTL > 0 {
		return r.TTL
	}
	return 30 * time.Second
}

func (r *RedisRegistry) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *RedisRegistry) Put(ctx context.Context, org, replica string, p Person) error {
	raw, err := json.Marshal(redisRow{Exp: r.now().Add(r.ttl()).Unix(), P: p})
	if err != nil {
		return err
	}
	// the key outlives a few TTLs so a briefly silent replica does not wipe the others' fields
	secs := int(r.ttl().Seconds()) * 4
	return r.R.Eval(ctx, luaPut, []string{r.key(org)}, replica+"|"+p.UserID, string(raw), secs).Err()
}

func (r *RedisRegistry) Delete(ctx context.Context, org, replica, user string) error {
	return r.R.Eval(ctx, luaDel, []string{r.key(org)}, replica+"|"+user).Err()
}

func (r *RedisRegistry) List(ctx context.Context, org string) ([]Remote, error) {
	flat, err := r.R.Eval(ctx, luaList, []string{r.key(org)}).StringSlice()
	if err != nil {
		return nil, err
	}
	return parseRegistryRows(flat, r.now()), nil
}

// parseRegistryRows turns an HGETALL reply (field, value, field, value...) into live entries. Corrupt or expired
// rows are skipped.
func parseRegistryRows(flat []string, now time.Time) []Remote {
	var out []Remote
	for i := 0; i+1 < len(flat); i += 2 {
		replica, _, ok := strings.Cut(flat[i], "|")
		if !ok {
			continue
		}
		var row redisRow
		if json.Unmarshal([]byte(flat[i+1]), &row) != nil || row.P.UserID == "" {
			continue
		}
		if row.Exp <= now.Unix() {
			continue
		}
		out = append(out, Remote{Replica: replica, Person: row.P})
	}
	return out
}
