// Command migrate applies the embedded goose migrations and exits. It exists
// so production upgrades can use a privileged migration connection without
// giving the long-running gateway that privilege.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/company/api-key/internal/store/postgres"
)

func main() {
	url := os.Getenv("MIGRATE_DATABASE_URL")
	if url == "" {
		url = os.Getenv("DATABASE_URL")
	}
	if url == "" {
		fmt.Fprintln(os.Stderr, "migrate: MIGRATE_DATABASE_URL or DATABASE_URL is required")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	store, err := postgres.Open(ctx, url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	if err := postgres.Migrate(ctx, store.Pool()); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("migrate: migrations applied")
}
