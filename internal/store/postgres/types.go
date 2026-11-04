package postgres

import "github.com/jackc/pgx/v5/pgxpool"

// The store types below share a connection pool but expose separate method
// sets, matching the domain interfaces that reuse method names.
type (
	EmployeeStore   struct{ pool *pgxpool.Pool }
	TokenStore      struct{ pool *pgxpool.Pool }
	ProviderStore   struct{ pool *pgxpool.Pool }
	RouteStore      struct{ pool *pgxpool.Pool }
	DLPRuleStore    struct{ pool *pgxpool.Pool }
	AuditStore      struct{ pool *pgxpool.Pool }
	AdminStore      struct{ pool *pgxpool.Pool }
	SettingsStore   struct{ pool *pgxpool.Pool }
	DepartmentStore struct{ pool *pgxpool.Pool }
	DashboardStore  struct{ pool *pgxpool.Pool }

	EmployeeAccountStore struct{ pool *pgxpool.Pool }
	TokenRequestStore    struct{ pool *pgxpool.Pool }
	EmployeeQuotaStore   struct{ pool *pgxpool.Pool }
	EmployeeAuditStore   struct{ pool *pgxpool.Pool }
)
