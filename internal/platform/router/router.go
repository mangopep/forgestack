// Package router is the application's composition root: it wires modules
// (currently just posts) and platform-level cross-cutting handlers
// (health, readiness, demo metrics) into one http.Handler. This file is
// the source of truth for the docgen tool's generated API reference —
// every route registration lives here, not scattered across modules.
package router

import (
	"encoding/json"
	"net/http"
	"sync/atomic"

	"github.com/mangopep/forgestack/internal/modules/posts/repository"
	"github.com/mangopep/forgestack/internal/modules/posts/service"
	posttransport "github.com/mangopep/forgestack/internal/modules/posts/transport"
	"github.com/mangopep/forgestack/internal/platform/buildinfo"
	"github.com/mangopep/forgestack/internal/platform/middleware"
	"github.com/mangopep/forgestack/internal/platform/telemetry"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// New assembles the full application handler. Each call gets its own
// Prometheus registry (rather than sharing the global default one), so
// calling New() more than once -- every test in this package does -- never
// panics on duplicate metric registration.
func New() http.Handler {
	repo := repository.NewInMemory()
	svc := service.New(repo, nil)
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
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready", "storage": "in-memory"})
	})
	mux.HandleFunc("GET /metrics-demo", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"requests_seen": requestCount.Load(), "note": "demo counter kept for backward compatibility; GET /metrics below is the real Prometheus endpoint"})
	})
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))

	mux.Handle("GET /api/v1/posts", posttransport.NewListHandler(svc))
	mux.Handle("POST /api/v1/posts", middleware.MaxBytes(4096)(posttransport.NewCreateHandler(svc)))

	handler := middleware.RequestLogging(&requestCount)(mux)
	handler = middleware.Telemetry(metrics)(handler)
	return middleware.CorrelationID(handler)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
