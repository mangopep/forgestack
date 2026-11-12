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
	"github.com/mangopep/forgestack/internal/platform/middleware"
)

// New assembles the full application handler.
func New() http.Handler {
	repo := repository.NewInMemory()
	svc := service.New(repo, nil)

	var requestCount atomic.Uint64
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "crushly-api"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready", "storage": "in-memory"})
	})
	mux.HandleFunc("GET /metrics-demo", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"requests_seen": requestCount.Load(), "note": "demo counter; real metrics in observability lab"})
	})

	mux.Handle("GET /api/v1/posts", posttransport.NewListHandler(svc))
	mux.Handle("POST /api/v1/posts", middleware.MaxBytes(4096)(posttransport.NewCreateHandler(svc)))

	return middleware.RequestLogging(&requestCount)(mux)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
