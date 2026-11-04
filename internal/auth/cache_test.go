package auth

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/company/sluice/internal/store"
)

func TestMemoryCacheRoundTrip(t *testing.T) {
	c := NewMemoryCache()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	c.SetClock(func() time.Time { return now })
	p := &Principal{Token: store.Token{TokenHash: "abc"}, Employee: store.Employee{Name: "Alice"}}

	if err := c.Set(context.Background(), "k", p, time.Minute); err != nil {
		t.Fatal(err)
	}
	got, err := c.Get(context.Background(), "k")
	if err != nil || got.Employee.Name != "Alice" {
		t.Fatalf("got %+v err=%v", got, err)
	}

	now = now.Add(2 * time.Minute)
	if _, err := c.Get(context.Background(), "k"); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("expired entry got %v", err)
	}
}

func TestMemoryCacheDelete(t *testing.T) {
	c := NewMemoryCache()
	c.Set(context.Background(), "k", &Principal{}, time.Minute)
	if err := c.Delete(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), "k"); !errors.Is(err, ErrCacheMiss) {
		t.Fatal(err)
	}
}

func TestRedisCacheRoundTrip(t *testing.T) {
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

	c := NewRedisCache(client)
	key := "test-principal-" + uuid.NewString()
	p := &Principal{Token: store.Token{TokenHash: "abc"}, Employee: store.Employee{Name: "Alice"}}
	if err := c.Set(ctx, key, p, time.Minute); err != nil {
		t.Fatal(err)
	}
	got, err := c.Get(ctx, key)
	if err != nil || got.Employee.Name != "Alice" || got.Token.TokenHash != "abc" {
		t.Fatalf("got %+v err=%v", got, err)
	}
	if err := c.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, key); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("after delete got %v", err)
	}
}
