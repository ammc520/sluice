package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/company/api-key/internal/config"
	"github.com/company/api-key/internal/metrics"
)

// Dependencies are the runtime dependencies the server needs.
type Dependencies struct {
	Ready   func(context.Context) error
	Logger  *slog.Logger
	Metrics *metrics.Registry
	// Gateway handles every /v1/* request. Nil leaves the route unmounted.
	Gateway gin.HandlerFunc
	// RegisterAdmin mounts the admin API under /admin/api. Nil leaves the
	// admin surface unmounted.
	RegisterAdmin func(*gin.RouterGroup)
}

// Server wraps the Gin engine and the underlying HTTP server.
type Server struct {
	cfg    *config.Config
	deps   Dependencies
	engine *gin.Engine
	http   *http.Server
}

// New builds the HTTP server with health endpoints.
func New(cfg *config.Config, deps Dependencies) *Server {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Metrics == nil {
		deps.Metrics = metrics.New()
	}
	engine := gin.New()
	engine.Use(gin.Recovery())
	// Only proxies explicitly listed in TRUSTED_PROXIES may influence the
	// client IP. A nil list trusts the direct peer only.
	_ = engine.SetTrustedProxies(cfg.TrustedProxies)

	s := &Server{cfg: cfg, deps: deps, engine: engine}
	s.routes()
	s.http = &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           engine,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return s
}

func (s *Server) routes() {
	if s.deps.Gateway != nil {
		s.engine.Any("/v1/*path", s.deps.Gateway)
	}
	if s.deps.RegisterAdmin != nil {
		s.deps.RegisterAdmin(s.engine.Group("/admin/api"))
	}

	s.engine.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	// Caddy rejects /metrics on the public hostname; this endpoint is for
	// in-network scraping only.
	s.engine.GET("/metrics", gin.WrapH(s.deps.Metrics.Handler()))
	s.engine.GET("/readyz", func(c *gin.Context) {
		if s.deps.Ready != nil {
			ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
			defer cancel()
			if err := s.deps.Ready(ctx); err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready"})
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})
}

// Engine exposes the Gin engine so later wiring can mount routes.
func (s *Server) Engine() *gin.Engine {
	return s.engine
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.engine
}

// Run serves until the context is cancelled or SIGINT/SIGTERM arrives.
func (s *Server) Run(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		s.deps.Logger.Info("gateway listening", "addr", s.cfg.ListenAddr)
		if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.deps.Logger.Info("shutting down gateway")
	return s.http.Shutdown(shutdownCtx)
}
