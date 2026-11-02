package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/company/api-key/internal/store"
	"github.com/company/api-key/migrations"
)

// Store aggregates the PostgreSQL-backed persistence implementations.
// The sub-stores share one connection pool but expose distinct method sets,
// because the domain interfaces reuse method names (Create/Get/Update/List)
// with different types, which a single Go type cannot implement.
type Store struct {
	pool      *pgxpool.Pool
	Employees *EmployeeStore
	Tokens    *TokenStore
	Providers *ProviderStore
	Routes    *RouteStore
	DLPRules  *DLPRuleStore
	Audit     *AuditStore
	Admins    *AdminStore
	Settings  *SettingsStore
}

func newStore(pool *pgxpool.Pool) *Store {
	return &Store{
		pool:      pool,
		Employees: &EmployeeStore{pool: pool},
		Tokens:    &TokenStore{pool: pool},
		Providers: &ProviderStore{pool: pool},
		Routes:    &RouteStore{pool: pool},
		DLPRules:  &DLPRuleStore{pool: pool},
		Audit:     &AuditStore{pool: pool},
		Admins:    &AdminStore{pool: pool},
		Settings:  &SettingsStore{pool: pool},
	}
}

// Open connects to PostgreSQL and verifies connectivity.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return newStore(pool), nil
}

// Pool exposes the underlying pool, mainly for readiness checks.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Close releases all connections.
func (s *Store) Close() { s.pool.Close() }

// Migrate applies embedded goose migrations.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	db, err := sql.Open("pgx", pool.Config().ConnString())
	if err != nil {
		return fmt.Errorf("open migration connection: %w", err)
	}
	defer db.Close()

	goose.SetBaseFS(migrations.FS())
	goose.SetDialect("postgres")
	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return store.ErrConflict
	}
	return err
}
