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
)

// New assembles the full handler: health/readiness plus whatever routes
// you add below, wrapped in correlation-ID propagation and structured
// request logging.
func New() http.Handler {
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

	// Add your own routes here, e.g.:
	// mux.Handle("GET /api/v1/widgets", middleware.MaxBytes(4096)(someHandler))

	return middleware.CorrelationID(middleware.RequestLogging(&requestCount)(mux))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
