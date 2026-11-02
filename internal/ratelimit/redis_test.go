package ratelimit

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestRedisLimiterAllowsThenRejects(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(opts)
	defer client.Close()
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("redis unavailable: %v", err)
	}

	l := NewRedis(client)
	key := "test-" + uuid.NewString()
	if ok, _, err := l.Allow(ctx, key, 2); !ok || err != nil {
		t.Fatalf("first ok=%v err=%v", ok, err)
	}
	if ok, _, err := l.Allow(ctx, key, 2); !ok || err != nil {
		t.Fatalf("second ok=%v err=%v", ok, err)
	}
	ok, retry, err := l.Allow(ctx, key, 2)
	if err != nil || ok || retry <= 0 || retry > time.Minute {
		t.Fatalf("third ok=%v retry=%v err=%v", ok, retry, err)
	}
}
