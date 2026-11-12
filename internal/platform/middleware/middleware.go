// Package middleware holds cross-cutting net/http middleware shared across
// modules: request body size limiting and structured request logging.
package middleware

import (
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

// MaxBytes wraps next so its request body cannot exceed limit bytes. It
// must wrap the final handler directly (not be applied inside a
// go-kit DecodeRequestFunc), since only here is http.ResponseWriter
// available for http.MaxBytesReader to use if the limit is exceeded.
func MaxBytes(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// RequestLogging wraps next with structured request logging and increments
// counter once per request. counter is exposed by callers (e.g. a demo
// metrics endpoint); it is not a replacement for real metrics.
func RequestLogging(counter *atomic.Uint64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			counter.Add(1)
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			slog.Info("http_request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}
