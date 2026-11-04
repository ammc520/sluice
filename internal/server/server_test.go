package server

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/company/sluice/internal/config"
	"github.com/company/sluice/internal/metrics"
)

func TestHealthz(t *testing.T) {
	s := New(&config.Config{}, Dependencies{Ready: func(context.Context) error { return nil }})
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/healthz", nil))
	if rr.Code != 200 {
		t.Fatalf("got %d", rr.Code)
	}
}

func TestReadyzFailsClosed(t *testing.T) {
	s := New(&config.Config{}, Dependencies{Ready: func(context.Context) error { return errors.New("db down") }})
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/readyz", nil))
	if rr.Code != 503 {
		t.Fatalf("got %d", rr.Code)
	}
}

func TestReadyzOK(t *testing.T) {
	s := New(&config.Config{}, Dependencies{Ready: func(context.Context) error { return nil }})
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/readyz", nil))
	if rr.Code != 200 {
		t.Fatalf("got %d", rr.Code)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	registry := metrics.New()
	registry.IncRequest(200, "none")
	s := New(&config.Config{}, Dependencies{Metrics: registry})

	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/metrics", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "gateway_requests_total") {
		t.Fatalf("got %d %s", rr.Code, rr.Body.String())
	}
}
