// Package metrics exposes the gateway's Prometheus metrics. Labels are
// deliberately bounded: no employee, token, request ID, path, or other
// per-request identifier is ever attached to a metric.
package metrics

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry owns the gateway's metric collectors and their private registry.
// The zero value is not usable; call New. A nil *Registry is a safe no-op so
// callers can leave metrics disabled in tests.
type Registry struct {
	registry *prometheus.Registry

	requests        *prometheus.CounterVec
	requestDuration prometheus.Histogram
	dlpHits         *prometheus.CounterVec
	auditQueueDepth prometheus.Gauge
	auditSpoolFiles prometheus.Gauge
}

var latencyBuckets = []float64{
	5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000,
}

// New creates a registry with the gateway metrics and the standard Go
// runtime/process collectors.
func New() *Registry {
	r := &Registry{
		registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_requests_total",
			Help: "Gateway requests by HTTP status and DLP action.",
		}, []string{"status", "dlp_action"}),
		requestDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "gateway_request_duration_ms",
			Help:    "Gateway request latency in milliseconds, including streaming responses.",
			Buckets: latencyBuckets,
		}),
		dlpHits: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_dlp_hits_total",
			Help: "DLP rule matches by rule and action.",
		}, []string{"rule", "action"}),
		auditQueueDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "audit_queue_depth",
			Help: "Audit events waiting in the in-process queue.",
		}),
		auditSpoolFiles: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "audit_spool_files",
			Help: "Audit spool files currently on disk.",
		}),
	}
	r.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		r.requests,
		r.requestDuration,
		r.dlpHits,
		r.auditQueueDepth,
		r.auditSpoolFiles,
	)
	return r
}

// Handler serves the registry in Prometheus text format.
func (r *Registry) Handler() http.Handler {
	if r == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "metrics disabled", http.StatusServiceUnavailable)
		})
	}
	return promhttp.HandlerFor(r.registry, promhttp.HandlerOpts{
		ErrorHandling: promhttp.ContinueOnError,
	})
}

// IncRequest records one completed request. dlpAction is normalized to a
// small fixed set so a future caller cannot accidentally create unbounded
// label values.
func (r *Registry) IncRequest(status int, dlpAction string) {
	if r == nil {
		return
	}
	r.requests.WithLabelValues(statusLabel(status), actionLabel(dlpAction)).Inc()
}

// ObserveLatency records total request latency in milliseconds.
func (r *Registry) ObserveLatency(ms float64) {
	if r == nil || ms < 0 {
		return
	}
	r.requestDuration.Observe(ms)
}

// IncDLPHit records one DLP rule match.
func (r *Registry) IncDLPHit(rule, action string) {
	if r == nil {
		return
	}
	r.dlpHits.WithLabelValues(ruleLabel(rule), actionLabel(action)).Inc()
}

// SetAuditQueueDepth records the current audit queue length.
func (r *Registry) SetAuditQueueDepth(n int) {
	if r == nil || n < 0 {
		return
	}
	r.auditQueueDepth.Set(float64(n))
}

// SetSpoolFiles records the number of audit spool files on disk.
func (r *Registry) SetSpoolFiles(n int) {
	if r == nil || n < 0 {
		return
	}
	r.auditSpoolFiles.Set(float64(n))
}

func statusLabel(status int) string {
	if status < 100 || status > 599 {
		return "invalid"
	}
	return strconv.Itoa(status)
}

func actionLabel(action string) string {
	switch action {
	case "", "none":
		return "none"
	case "block", "mask", "audit":
		return action
	default:
		return "other"
	}
}

func ruleLabel(rule string) string {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return "unknown"
	}
	// Rule names come from the database. Bound the label value even if an
	// operator pastes an oversized name.
	runes := []rune(rule)
	if len(runes) > 64 {
		rule = string(runes[:64])
	}
	return rule
}
