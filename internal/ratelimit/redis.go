package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisBucketScript = `
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local now = tonumber(ARGV[2])
local ttl = tonumber(ARGV[3])
local data = redis.call('HMGET', key, 'tokens', 'ts')
local tokens = tonumber(data[1])
local ts = tonumber(data[2])
if tokens == nil then
  tokens = limit
  ts = now
end
local elapsed = math.max(0, now - ts) / 1000.0
local rate = limit / 60.0
tokens = math.min(limit, tokens + elapsed * rate)
local allowed = 0
local retry = 0
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
else
  retry = math.ceil((1 - tokens) / rate * 1000)
end
redis.call('HSET', key, 'tokens', tokens, 'ts', now)
redis.call('EXPIRE', key, ttl)
return {allowed, retry}
`

var bucketScript = redis.NewScript(redisBucketScript)

// Redis is a distributed token bucket backed by Redis.
type Redis struct {
	client *redis.Client
	clock  func() time.Time
}

// NewRedis creates a Redis limiter.
func NewRedis(client *redis.Client) *Redis {
	return &Redis{client: client, clock: time.Now}
}

// SetClock replaces the clock, for tests.
func (r *Redis) SetClock(clock func() time.Time) { r.clock = clock }

func (r *Redis) Allow(ctx context.Context, key string, limit int) (bool, time.Duration, error) {
	if limit <= 0 {
		limit = 60
	}
	res, err := bucketScript.Run(ctx, r.client, []string{"rl:" + key},
		limit, r.clock().UnixMilli(), 120).Slice()
	if err != nil {
		return false, 0, fmt.Errorf("ratelimit redis: %w", err)
	}
	if len(res) != 2 {
		return false, 0, fmt.Errorf("ratelimit redis: unexpected script result")
	}
	allowed, _ := res[0].(int64)
	retryMS, _ := res[1].(int64)
	if allowed == 1 {
		return true, 0, nil
	}
	return false, time.Duration(retryMS) * time.Millisecond, nil
}
