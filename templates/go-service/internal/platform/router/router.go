// Package router assembles this service's HTTP handler tree. Add your own
// routes alongside /healthz and /readyz — this file is the one place new
// endpoints get registered.
package router

import (
	"encoding/json"
	"net/http"
	"sync/atomic"

	"__MODULE_PATH__/internal/platform/buildinfo"
	"__MODULE_PATH__/internal/platform/middleware"
	"__MODULE_PATH__/internal/platform/telemetry"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// New assembles the full handler: health/readiness/metrics plus whatever
// routes you add below, wrapped in correlation-ID propagation, OpenTelemetry
// tracing, Prometheus metrics, and structured request logging. Each call
// gets its own Prometheus registry (rather than sharing the global default
// one), so calling New() more than once -- every test in this package does
// -- never panics on duplicate metric registration.
func New() http.Handler {
	reg := prometheus.NewRegistry()
	metrics := telemetry.NewMetrics(reg)

	var requestCount atomic.Uint64
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"service": buildinfo.Name,
			"version": buildinfo.Version,
		})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		// Replace with a real readiness check once this service has a
		// dependency worth checking (a database connection, a downstream
		// service, ...) — "ready" today just means "the process started."
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /metrics-demo", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"requests_seen": requestCount.Load(), "note": "demo counter kept for backward compatibility; GET /metrics below is the real Prometheus endpoint"})
	})
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))

	// Add your own routes here, e.g.:
	// mux.Handle("GET /api/v1/widgets", middleware.MaxBytes(4096)(someHandler))

	handler := middleware.RequestLogging(&requestCount)(mux)
	handler = middleware.Telemetry(metrics)(handler)
	return middleware.CorrelationID(handler)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
