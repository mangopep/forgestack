package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthz(t *testing.T) {
	r := httptest.NewRecorder()
	New().ServeHTTP(r, httptest.NewRequest("GET", "/healthz", nil))
	if r.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", r.Code)
	}
	if r.Header().Get("X-Request-Id") == "" {
		t.Error("expected a correlation ID to be echoed on the response")
	}
}

func TestReadyz(t *testing.T) {
	r := httptest.NewRecorder()
	New().ServeHTTP(r, httptest.NewRequest("GET", "/readyz", nil))
	if r.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", r.Code)
	}
}

func TestMetricsEndpointRecordsRealRequests(t *testing.T) {
	handler := New()

	r := httptest.NewRecorder()
	handler.ServeHTTP(r, httptest.NewRequest("GET", "/healthz", nil))
	if r.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", r.Code)
	}

	r = httptest.NewRecorder()
	handler.ServeHTTP(r, httptest.NewRequest("GET", "/metrics", nil))
	if r.Code != http.StatusOK {
		t.Fatalf("GET /metrics: status = %d, want 200", r.Code)
	}
	if !strings.Contains(r.Body.String(), `http_requests_total{method="GET",route="GET /healthz",status="200"} 1`) {
		t.Errorf("expected a real Prometheus counter line for the /healthz request, got:\n%s", r.Body.String())
	}
}
