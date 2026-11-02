package auth

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrCacheMiss indicates that a principal is not cached.
var ErrCacheMiss = errors.New("auth: cache miss")

// Cache stores authenticated principals keyed by token hash.
type Cache interface {
	Get(ctx context.Context, key string) (*Principal, error)
	Set(ctx context.Context, key string, p *Principal, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

type memoryEntry struct {
	principal *Principal
	expiresAt time.Time
}

// MemoryCache is an in-process principal cache.
type MemoryCache struct {
	mu    sync.Mutex
	items map[string]memoryEntry
	clock func() time.Time
}

// NewMemoryCache creates an empty in-process cache.
func NewMemoryCache() *MemoryCache {
	return &MemoryCache{items: map[string]memoryEntry{}, clock: time.Now}
}

// SetClock overrides the clock for tests.
func (c *MemoryCache) SetClock(clock func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if clock == nil {
		clock = time.Now
	}
	c.clock = clock
}

func (c *MemoryCache) Get(_ context.Context, key string) (*Principal, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[key]
	if !ok {
		return nil, ErrCacheMiss
	}
	if !c.clock().Before(entry.expiresAt) {
		delete(c.items, key)
		return nil, ErrCacheMiss
	}
	return entry.principal, nil
}

func (c *MemoryCache) Set(_ context.Context, key string, p *Principal, ttl time.Duration) error {
	if p == nil || ttl <= 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = memoryEntry{principal: p, expiresAt: c.clock().Add(ttl)}
	return nil
}

func (c *MemoryCache) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, key)
	return nil
}

// RedisCache shares principals between gateway instances.
type RedisCache struct {
	client *redis.Client
}

// NewRedisCache creates a Redis-backed principal cache.
func NewRedisCache(client *redis.Client) Cache {
	return &RedisCache{client: client}
}

func (c *RedisCache) Get(ctx context.Context, key string) (*Principal, error) {
	raw, err := c.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrCacheMiss
	}
	if err != nil {
		return nil, err
	}
	var p Principal
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (c *RedisCache) Set(ctx context.Context, key string, p *Principal, ttl time.Duration) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, key, raw, ttl).Err()
}

func (c *RedisCache) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, key).Err()
}
