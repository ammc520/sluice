package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsEndpointExposesRequestCounter(t *testing.T) {
	r := New()
	r.IncRequest(200, "none")

	rr := httptest.NewRecorder()
	r.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "gateway_requests_total") {
		t.Fatalf("missing request counter:\n%s", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "gateway_requests_total{dlp_action=\"none\",status=\"200\"}") == false {
		t.Fatalf("missing labelled request counter:\n%s", rr.Body.String())
	}
}

func TestMetricsLabelsStayBounded(t *testing.T) {
	r := New()
	r.IncRequest(200, "none")
	r.IncRequest(403, "block")
	r.IncRequest(500, "unexpected-action")
	r.IncDLPHit("phone", "mask")
	r.IncDLPHit("", "mask")
	r.ObserveLatency(12.5)
	r.SetAuditQueueDepth(7)
	r.SetSpoolFiles(2)

	rr := httptest.NewRecorder()
	r.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	for _, want := range []string{
		`gateway_requests_total{dlp_action="none",status="200"} 1`,
		`gateway_requests_total{dlp_action="block",status="403"} 1`,
		`gateway_requests_total{dlp_action="other",status="500"} 1`,
		`gateway_dlp_hits_total{action="mask",rule="phone"} 1`,
		`gateway_dlp_hits_total{action="mask",rule="unknown"} 1`,
		`gateway_request_duration_ms_count 1`,
		"audit_queue_depth 7",
		"audit_spool_files 2",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q:\n%s", want, body)
		}
	}

	// The metric surface must never carry unbounded per-request dimensions.
	for _, forbidden := range []string{"employee", "request_id", "token_id", "path="} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("high-cardinality label %q exposed:\n%s", forbidden, body)
		}
	}
}

func TestNilRegistryIsSafe(t *testing.T) {
	var r *Registry
	r.IncRequest(200, "none")
	r.ObserveLatency(1)
	r.IncDLPHit("phone", "mask")
	r.SetAuditQueueDepth(0)
	r.SetSpoolFiles(0)
	// Handler must remain usable even when metrics are disabled.
	if r.Handler() == nil {
		t.Fatal("nil registry returned nil handler")
	}
}
