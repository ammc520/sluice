package testutil

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/company/sluice/migrations"
)

const testLockKey int64 = 0x6170692d6b6579 // "api-key"

var tables = []string{
	"settings",
	"employee_audit_logs",
	"token_requests",
	"employee_sessions",
	"employee_accounts",
	"admin_audit_logs",
	"admin_sessions",
	"admin_users",
	"audit_logs",
	"dlp_rules",
	"model_routes",
	"upstream_providers",
	"employee_quotas",
	"tokens",
	"employees",
	"departments",
}

// OpenTestDB connects to TEST_DATABASE_URL, migrates, and truncates all tables.
// The test is skipped when TEST_DATABASE_URL is not set.
func OpenTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping test db: %v", err)
	}

	// go test runs package binaries in parallel, and all packages share the
	// same TEST_DATABASE_URL. Serialize DB tests with an advisory lock so one
	// package cannot truncate tables under another.
	lockConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire test lock connection: %v", err)
	}
	if _, err := lockConn.Exec(ctx, "SELECT pg_advisory_lock($1)", testLockKey); err != nil {
		lockConn.Release()
		t.Fatalf("acquire test lock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = lockConn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", testLockKey)
		lockConn.Release()
	})

	if err := migrate(ctx, url); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	truncate(ctx, t, pool)
	return pool
}

func migrate(ctx context.Context, url string) error {
	db, err := sql.Open("pgx", url)
	if err != nil {
		return err
	}
	defer db.Close()
	goose.SetBaseFS(migrations.FS())
	goose.SetDialect("postgres")
	return goose.UpContext(ctx, db, ".")
}

func truncate(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, "TRUNCATE "+join(tables)+" RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func join(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
