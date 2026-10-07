// Package redis implements event pub/sub and task locks on Redis.
package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"aiworkforce/backend/internal/domain"
)

const channel = "aiworkforce:events"

// Client wraps a go-redis client.
type Client struct{ rdb *goredis.Client }

func New(ctx context.Context, url string) (*Client, error) {
	opt, err := goredis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}
	c := &Client{rdb: goredis.NewClient(opt)}
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := c.rdb.Ping(pctx).Err(); err != nil {
		_ = c.rdb.Close()
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return c, nil
}

func (c *Client) Close() error { return c.rdb.Close() }

// Scripter exposes the underlying client (used by the auth rate limiter).
func (c *Client) Scripter() goredis.Scripter { return c.rdb }

// Publish sends an event frame to every backend instance.
func (c *Client) Publish(ctx context.Context, e domain.Event) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return c.rdb.Publish(ctx, channel, raw).Err()
}

// Subscribe returns raw frames until ctx is cancelled. go-redis reconnects automatically.
func (c *Client) Subscribe(ctx context.Context) <-chan []byte {
	sub := c.rdb.Subscribe(ctx, channel)
	out := make(chan []byte, 256)
	go func() {
		defer close(out)
		defer sub.Close()
		for msg := range sub.Channel() {
			select {
			case out <- []byte(msg.Payload):
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

var unlockScript = goredis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`)

// Lock implements application.Locker with SET NX PX.
func (c *Client) Lock(ctx context.Context, key string, ttl time.Duration) (func(), bool, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return nil, false, err
	}
	token := hex.EncodeToString(buf)
	key = "aiworkforce:lock:" + key
	ok, err := c.rdb.SetNX(ctx, key, token, ttl).Result()
	if err != nil || !ok {
		return nil, false, err
	}
	return func() {
		uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = unlockScript.Run(uctx, c.rdb, []string{key}, token).Err()
	}, true, nil
}
