package router

import (
	"net/http"
	"net/http/httptest"
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
