package server

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/company/api-key/internal/config"
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
