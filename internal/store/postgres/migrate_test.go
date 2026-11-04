package postgres

import (
	"context"
	"testing"

	"github.com/company/sluice/internal/testutil"
)

func TestMigrationsUpDownUp(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	ctx := context.Background()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal("second up failed:", err)
	}
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM tokens").Scan(&n); err != nil {
		t.Fatal(err)
	}
}
