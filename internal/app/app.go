// Package app assembles the gateway's runtime dependencies. Both the
// production entrypoint and the end-to-end tests build the same object graph
// through New.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/company/api-key/internal/adminapi"
	"github.com/company/api-key/internal/adminauth"
	"github.com/company/api-key/internal/alert"
	"github.com/company/api-key/internal/audit"
	"github.com/company/api-key/internal/auth"
	"github.com/company/api-key/internal/config"
	"github.com/company/api-key/internal/crypto"
	"github.com/company/api-key/internal/dlp"
	"github.com/company/api-key/internal/gateway"
	"github.com/company/api-key/internal/metrics"
	"github.com/company/api-key/internal/netguard"
	"github.com/company/api-key/internal/proxy"
	"github.com/company/api-key/internal/quota"
	"github.com/company/api-key/internal/ratelimit"
	"github.com/company/api-key/internal/server"
	"github.com/company/api-key/internal/store"
	"github.com/company/api-key/internal/store/postgres"
)

const (
	redisPingTimeout = 2 * time.Second
	refreshTimeout   = 10 * time.Second
)

// Options overrides production defaults for tests and local development.
type Options struct {
	// DisableRedis forces the in-memory cache and rate limiter.
	DisableRedis bool
	// UpstreamClient replaces the guarded upstream HTTP client.
	UpstreamClient *http.Client
	// AlertMailer replaces SMTP-loaded alert delivery.
	AlertMailer alert.Mailer
	// AuditQueueSize and AuditFlushInterval tune the audit writer.
	AuditQueueSize     int
	AuditFlushInterval time.Duration
}

// App owns every long-lived component so shutdown is deterministic.
type App struct {
	Stores *postgres.Store
	Server *server.Server

	resolver  *dynamicResolver
	dlpEngine *dlp.Engine

	writer      *audit.Writer
	alerts      *alert.Worker
	redis       *redis.Client
	maintenance *postgres.Store

	cancel    context.CancelFunc
	closeOnce sync.Once
	closeErr  error
}

// New opens PostgreSQL, applies migrations, bootstraps the first
// administrator, and starts the background workers. The supplied context is
// the lifetime for those workers; Close must still be called.
func New(ctx context.Context, cfg *config.Config, logger *slog.Logger, opts Options) (*App, error) {
	if cfg == nil {
		return nil, errors.New("app: configuration is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if logger == nil {
		logger = slog.Default()
	}

	cipher, err := crypto.NewCipher(cfg.MasterKey, cfg.MasterKeyPrevious)
	if err != nil {
		return nil, fmt.Errorf("app: master key: %w", err)
	}

	stores, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("app: open database: %w", err)
	}
	cleanup := func() { stores.Close() }
	ok := false
	defer func() {
		if !ok {
			cleanup()
		}
	}()

	if err := postgres.Migrate(ctx, stores.Pool()); err != nil {
		return nil, fmt.Errorf("app: migrate database: %w", err)
	}

	created, err := adminauth.EnsureBootstrapAdmin(ctx, stores.Admins, cfg.BootstrapAdminUsername, cfg.BootstrapAdminPassword)
	if err != nil {
		return nil, fmt.Errorf("app: bootstrap administrator: %w", err)
	}
	if created {
		logger.Info("created bootstrap administrator", "username", cfg.BootstrapAdminUsername)
	} else if cfg.BootstrapAdminPassword == "" {
		if admins, listErr := stores.Admins.List(ctx); listErr == nil && len(admins) == 0 {
			logger.Warn("no administrator account exists; set BOOTSTRAP_ADMIN_PASSWORD on first start")
		}
	}

	metricsRegistry := metrics.New()

	redisClient := (*redis.Client)(nil)
	if !opts.DisableRedis && cfg.RedisURL != "" {
		redisClient = openRedis(ctx, cfg.RedisURL, logger)
		if redisClient != nil {
			defer func() {
				if !ok {
					_ = redisClient.Close()
				}
			}()
		}
	}

	tokenCache := auth.Cache(auth.NewMemoryCache())
	limiter := ratelimit.Limiter(ratelimit.NewMemory())
	if redisClient != nil {
		tokenCache = auth.NewRedisCache(redisClient)
		limiter = ratelimit.NewFallback(ratelimit.NewRedis(redisClient), ratelimit.NewMemory())
	}

	now := time.Now
	authSvc := auth.New(stores.Tokens, stores.Employees, tokenCache, cfg.TrustedProxies, now)
	adminSvc := adminauth.New(stores.Admins, limiter, now, time.Duration(cfg.AdminSessionTTLHours)*time.Hour)
	quotaSvc := quota.New(stores.Tokens, cfg.DefaultReserveTokens, now)

	if err := dlp.EnsureBuiltinRules(ctx, stores.DLPRules); err != nil {
		return nil, fmt.Errorf("app: ensure builtin DLP rules: %w", err)
	}
	enabledRules, err := stores.DLPRules.ListEnabled(ctx)
	if err != nil {
		return nil, fmt.Errorf("app: load DLP rules: %w", err)
	}
	engine, ruleErrs := dlp.NewEngine(enabledRules)
	for _, ruleErr := range ruleErrs {
		logger.Warn("skipped invalid DLP rule", "error", ruleErr)
	}

	resolver := &dynamicResolver{}
	if err := resolver.Reload(ctx, stores.Providers, stores.Routes); err != nil {
		return nil, fmt.Errorf("app: load model routes: %w", err)
	}

	upstreamClient := opts.UpstreamClient
	if upstreamClient == nil {
		upstreamClient = netguard.NewClient(0, 120*time.Second, netguardOptions(cfg)...)
	}
	proxySvc := proxy.New(proxy.Config{
		Client:       upstreamClient,
		Cipher:       cipher,
		CaptureLimit: cfg.CaptureLimit,
	})

	writer := audit.NewWriter(audit.WriterConfig{
		Store:         stores.Audit,
		Cipher:        cipher,
		QueueSize:     opts.AuditQueueSize,
		FlushInterval: opts.AuditFlushInterval,
		SpoolDir:      cfg.AuditSpoolDir,
		Logger:        logger,
		Metrics:       metricsRegistry,
	})

	alertMailer := opts.AlertMailer
	alertLoader := func(ctx context.Context) (alert.Mailer, error) {
		if alertMailer != nil {
			return alertMailer, nil
		}
		smtpCfg, err := alert.LoadSMTP(ctx, stores.Settings, cipher)
		if err != nil {
			return nil, err
		}
		return alert.NewMailer(smtpCfg), nil
	}
	alerts := alert.NewWorker(alertLoader, 0, 0, now)

	maintenance, err := openMaintenance(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer func() {
		if !ok && maintenance != nil {
			maintenance.Close()
		}
	}()

	// Workers outlive a cancelled startup/signal context so shutdown can still
	// flush pending audit events and alert emails. Close explicitly cancels.
	lifetimeCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	writer.Start(lifetimeCtx)
	alerts.Start(lifetimeCtx)
	if maintenance != nil {
		retention := audit.NewRetentionFunc(maintenance.Audit, func(ctx context.Context) int {
			return auditRetentionDays(ctx, stores.Settings, cfg.AuditRetentionDays)
		}, now)
		retention.SetLogger(logger)
		go retention.Run(lifetimeCtx)
	}

	a := &App{
		Stores:      stores,
		resolver:    resolver,
		dlpEngine:   engine,
		writer:      writer,
		alerts:      alerts,
		redis:       redisClient,
		maintenance: maintenance,
		cancel:      cancel,
	}

	refreshResolver := func(ctx context.Context) {
		ctx, done := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
		defer done()
		if err := resolver.Reload(ctx, stores.Providers, stores.Routes); err != nil {
			logger.Error("reload model routes", "error", err)
		}
	}
	refreshDLP := func(ctx context.Context) {
		ctx, done := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
		defer done()
		rules, err := stores.DLPRules.ListEnabled(ctx)
		if err != nil {
			logger.Error("reload DLP rules", "error", err)
			return
		}
		for _, ruleErr := range engine.Reload(rules) {
			logger.Warn("skipped invalid DLP rule", "error", ruleErr)
		}
	}

	testClient := netguard.NewClient(10*time.Second, 10*time.Second, netguardOptions(cfg)...)
	authHandler := adminapi.NewAuthHandler(adminSvc, cfg.CookieSecure)
	adminUsersHandler := adminapi.NewAdminUsersHandler(stores.Admins, cfg.TrustedProxies)
	auditHandler := adminapi.NewAuditHandler(stores.Audit, stores.Admins, cipher, now, cfg.TrustedProxies)
	dashboardHandler := adminapi.NewDashboardHandler(stores.Audit, stores.Dashboard, now)
	departmentHandler := adminapi.NewDepartmentHandler(stores.Departments)
	dlpRuleHandler := adminapi.NewDLPRuleHandler(stores.DLPRules, refreshDLP)
	dlpTestHandler := adminapi.NewDLPTestHandler(stores.DLPRules)
	employeeHandler := adminapi.NewEmployeeHandler(stores.Employees)
	providerHandler := adminapi.NewProviderHandler(stores.Providers, stores.Routes, cipher, testClient, refreshResolver)
	routeHandler := adminapi.NewRouteHandler(stores.Routes, stores.Providers, refreshResolver)
	settingsHandler := adminapi.NewSettingsHandler(stores.Settings, stores.Admins, cipher, func(cfg alert.SMTPConfig) alert.Mailer {
		return alert.NewMailer(cfg)
	}, now, cfg.TrustedProxies)
	tokenHandler := adminapi.NewTokenHandler(stores.Tokens, stores.Employees, now)

	gatewayHandler := gateway.New(gateway.HandlerDeps{
		Auth:           authSvc,
		Limiter:        limiter,
		Quota:          quotaSvc,
		DLP:            engine,
		Resolver:       resolver,
		Upstream:       proxySvc,
		Audit:          writer,
		AlertSink:      alerts,
		Metrics:        metricsRegistry,
		MaxBodyBytes:   cfg.RequestBodyLimit,
		TrustedProxies: cfg.TrustedProxies,
		Logger:         logger,
		Clock:          now,
	})

	srv := server.New(cfg, server.Dependencies{
		Ready: func(ctx context.Context) error {
			return stores.Pool().Ping(ctx)
		},
		Logger:  logger,
		Metrics: metricsRegistry,
		Gateway: gatewayHandler.Handle,
		RegisterAdmin: func(group *gin.RouterGroup) {
			registerAdminRoutes(group, adminSvc, routeSet{
				auth:        authHandler,
				adminUsers:  adminUsersHandler,
				audit:       auditHandler,
				dashboard:   dashboardHandler,
				dlpRules:    dlpRuleHandler,
				dlpTest:     dlpTestHandler,
				employees:   employeeHandler,
				departments: departmentHandler,
				providers:   providerHandler,
				routes:      routeHandler,
				settings:    settingsHandler,
				tokens:      tokenHandler,
			})
		},
	})
	a.Server = srv

	ok = true
	return a, nil
}

// Close flushes audit events, stops workers, and releases database
// connections. It is safe to call multiple times.
func (a *App) Close(ctx context.Context) error {
	if a == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	a.closeOnce.Do(func() {
		var errs []error
		// Close the workers before cancelling so their queues drain.
		if a.writer != nil {
			if err := a.writer.Close(ctx); err != nil {
				errs = append(errs, err)
			}
		}
		if a.alerts != nil {
			if err := a.alerts.Close(ctx); err != nil {
				errs = append(errs, err)
			}
		}
		if a.cancel != nil {
			a.cancel()
		}
		if a.redis != nil {
			if err := a.redis.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if a.maintenance != nil {
			a.maintenance.Close()
		}
		if a.Stores != nil {
			a.Stores.Close()
		}
		a.closeErr = errors.Join(errs...)
	})
	return a.closeErr
}

// ReloadDLP re-reads enabled rules. It is used by tests that mutate rules
// directly rather than through the admin API.
func (a *App) ReloadDLP(ctx context.Context) error {
	if a == nil {
		return errors.New("app: nil app")
	}
	rules, err := a.Stores.DLPRules.ListEnabled(ctx)
	if err != nil {
		return err
	}
	if errs := a.dlpEngine.Reload(rules); len(errs) > 0 {
		return errs[0]
	}
	return nil
}

func openRedis(ctx context.Context, rawURL string, logger *slog.Logger) *redis.Client {
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		logger.Warn("invalid REDIS_URL; using in-memory cache and limiter", "error", err)
		return nil
	}
	client := redis.NewClient(opts)
	pingCtx, cancel := context.WithTimeout(ctx, redisPingTimeout)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		logger.Warn("Redis unavailable at startup; using in-memory cache and limiter", "error", err)
		_ = client.Close()
		return nil
	}
	return client
}

// auditRetentionDays reads the administrator-configured window, falling back
// to the environment default. The value is re-read on every retention run.
func auditRetentionDays(ctx context.Context, settings store.SettingsStore, fallback int) int {
	raw, err := settings.Get(ctx, "audit")
	if err != nil {
		return fallback
	}
	var saved struct {
		RetentionDays int `json:"retention_days"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil || saved.RetentionDays <= 0 {
		return fallback
	}
	return saved.RetentionDays
}

func openMaintenance(ctx context.Context, cfg *config.Config) (*postgres.Store, error) {
	if cfg.AuditMaintenanceDatabaseURL == "" {
		return nil, nil
	}
	store, err := postgres.Open(ctx, cfg.AuditMaintenanceDatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("app: open audit maintenance database: %w", err)
	}
	return store, nil
}

func netguardOptions(cfg *config.Config) []netguard.Option {
	if cfg.AllowPrivateUpstreams {
		return []netguard.Option{netguard.WithPrivateUpstreams()}
	}
	return nil
}

// dynamicResolver atomically swaps the provider/route snapshot after admin
// mutations. Resolve stays on the request path and only takes a read lock.
type dynamicResolver struct {
	mu      sync.RWMutex
	current *proxy.Resolver
}

func (r *dynamicResolver) Reload(ctx context.Context, providers store.ProviderStore, routes store.RouteStore) error {
	providerList, err := providers.List(ctx)
	if err != nil {
		return err
	}
	routeList, err := routes.ListEnabled(ctx)
	if err != nil {
		return err
	}
	next := proxy.NewResolver(providerList, routeList)
	r.mu.Lock()
	r.current = next
	r.mu.Unlock()
	return nil
}

func (r *dynamicResolver) Resolve(path, model string) (proxy.Target, error) {
	r.mu.RLock()
	current := r.current
	r.mu.RUnlock()
	if current == nil {
		return proxy.Target{}, proxy.ErrNoRoute
	}
	return current.Resolve(path, model)
}

type routeSet struct {
	auth        *adminapi.AuthHandler
	adminUsers  *adminapi.AdminUsersHandler
	audit       *adminapi.AuditHandler
	dashboard   *adminapi.DashboardHandler
	dlpRules    *adminapi.DLPRuleHandler
	dlpTest     *adminapi.DLPTestHandler
	employees   *adminapi.EmployeeHandler
	departments *adminapi.DepartmentHandler
	providers   *adminapi.ProviderHandler
	routes      *adminapi.RouteHandler
	settings    *adminapi.SettingsHandler
	tokens      *adminapi.TokenHandler
}

func registerAdminRoutes(group *gin.RouterGroup, adminSvc *adminauth.Service, h routeSet) {
	group.POST("/auth/login", h.auth.Login)

	authed := group.Group("")
	authed.Use(adminauth.RequireSession(adminSvc))
	authed.POST("/auth/logout", adminauth.RequireCSRF(), h.auth.Logout)
	authed.GET("/auth/me", h.auth.Me)

	// Every authenticated role may read dashboard and operational data.
	authed.GET("/dashboard/overview", h.dashboard.Overview)
	authed.GET("/dashboard/trend", h.dashboard.Trend)
	authed.GET("/dashboard/top-users", h.dashboard.TopUsers)
	authed.GET("/dashboard/dlp", h.dashboard.DLP)
	authed.GET("/dashboard/summary", h.dashboard.Summary)
	authed.GET("/employees", h.employees.List)
	authed.GET("/departments", h.departments.List)
	authed.GET("/tokens", h.tokens.List)
	authed.GET("/providers", h.providers.List)
	// Model discovery decrypts the stored provider key and calls upstream, so
	// it is an operator action rather than a read-only audit operation.
	authed.GET("/providers/:id/models", adminauth.RequireRole("operator"), h.providers.Models)
	authed.GET("/model-routes", h.routes.List)
	authed.GET("/dlp-rules", h.dlpRules.List)
	authed.GET("/audit", h.audit.List)
	authed.GET("/audit/:id", h.audit.Detail)

	// Mutations require CSRF and at least operator. Super admins pass
	// RequireRole("operator") as well.
	write := authed.Group("")
	write.Use(adminauth.RequireCSRF(), adminauth.RequireRole("operator"))
	write.POST("/employees", h.employees.Create)
	write.PUT("/employees/:id", h.employees.Update)
	write.POST("/tokens", h.tokens.Create)
	write.PUT("/tokens/:id", h.tokens.Update)
	write.POST("/tokens/:id/revoke", h.tokens.Revoke)
	write.POST("/tokens/:id/rotate", h.tokens.Rotate)
	write.POST("/providers", h.providers.Create)
	write.PUT("/providers/:id", h.providers.Update)
	write.DELETE("/providers/:id", h.providers.Delete)
	write.POST("/providers/:id/test", h.providers.Test)
	write.POST("/model-routes", h.routes.Create)
	write.DELETE("/model-routes/:id", h.routes.Delete)
	write.POST("/dlp-rules", h.dlpRules.Create)
	write.PUT("/dlp-rules/:id", h.dlpRules.Update)
	write.DELETE("/dlp-rules/:id", h.dlpRules.Delete)
	write.POST("/dlp-rules/test", h.dlpTest.Test)

	// Sensitive surfaces are limited to super administrators. The handlers
	// re-check the role as defense in depth.
	super := authed.Group("")
	super.Use(adminauth.RequireCSRF(), adminauth.RequireRole("super_admin"))
	super.POST("/audit/:id/reveal", h.audit.Reveal)
	super.GET("/admin-users", h.adminUsers.List)
	super.POST("/admin-users", h.adminUsers.Create)
	super.PUT("/admin-users/:id", h.adminUsers.Update)
	super.POST("/admin-users/:id/reset-password", h.adminUsers.ResetPassword)
	super.GET("/settings/smtp", h.settings.GetSMTP)
	super.PUT("/settings/smtp", h.settings.PutSMTP)
	super.POST("/settings/smtp/test", h.settings.TestSMTP)
	super.GET("/settings/audit", h.settings.GetAuditSettings)
	super.PUT("/settings/audit", h.settings.PutAuditSettings)
}
