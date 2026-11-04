package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/company/api-key/internal/alert"
	"github.com/company/api-key/internal/audit"
	"github.com/company/api-key/internal/auth"
	"github.com/company/api-key/internal/dlp"
	"github.com/company/api-key/internal/metrics"
	"github.com/company/api-key/internal/proxy"
	"github.com/company/api-key/internal/quota"
	"github.com/company/api-key/internal/store"
)

// Authenticator resolves a company token to a principal.
type Authenticator interface {
	Authenticate(*http.Request) (*auth.Principal, error)
}

// Limiter enforces a per-minute request limit for a key.
type Limiter interface {
	Allow(context.Context, string, int) (bool, time.Duration, error)
}

// Quota reserves and settles the employee's shared quota around an upstream
// call. The token is only the credential (and may carry an optional cap).
type Quota interface {
	Reserve(context.Context, uuid.UUID, *store.Token, quota.Endpoint, int64) (store.Reservation, error)
	Settle(context.Context, store.Reservation, int64) error
}

// DLP inspects and optionally rewrites a request body.
type DLP interface {
	Inspect([]byte) (dlp.Result, error)
}

// Resolver selects the upstream provider for a path and model.
type Resolver interface {
	Resolve(path, model string) (proxy.Target, error)
}

// Upstream forwards non-streaming and streaming requests.
type Upstream interface {
	Do(context.Context, http.ResponseWriter, proxy.Request) (proxy.Result, error)
	Stream(context.Context, http.ResponseWriter, func(), proxy.Request) (proxy.Result, error)
}

// Audit accepts non-blocking audit events.
type Audit interface {
	Enqueue(audit.Event) bool
}

// AlertSink accepts non-blocking DLP alerts.
type AlertSink interface {
	Enqueue(context.Context, alert.Alert) bool
}

// HandlerDeps are the gateway pipeline's dependencies. Optional fields have
// production-safe defaults; required fields are checked per request.
type HandlerDeps struct {
	Auth      Authenticator
	Limiter   Limiter
	Quota     Quota
	DLP       DLP
	Resolver  Resolver
	Upstream  Upstream
	Audit     Audit
	AlertSink AlertSink
	Metrics   *metrics.Registry

	// MaxBodyBytes limits request bodies. Zero uses 2 MiB.
	MaxBodyBytes int64
	// TrustedProxies lists CIDRs whose X-Forwarded-For header is trusted when
	// deriving the audit client IP. Empty trusts only the direct peer.
	TrustedProxies []string
	Logger         *slog.Logger
	Clock          func() time.Time
}
