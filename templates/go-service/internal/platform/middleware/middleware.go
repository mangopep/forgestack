// Package middleware holds cross-cutting net/http middleware shared across
// modules: request body size limiting, correlation IDs and structured
// request logging.
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

// RequestIDHeader is the header name used to propagate a correlation ID
// across the request/response boundary. This name is a cross-language
// contract (see docs/platform/CONTRACTS.md) — the Python reference service
// uses the identical header name so a trace can be followed across both.
const RequestIDHeader = "X-Request-Id"

type contextKey int

const requestIDKey contextKey = 0

// RequestID returns the correlation ID attached to ctx by the
// CorrelationID middleware, or "" if none is present (e.g. in a test that
// doesn't go through the middleware chain).
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// CorrelationID ensures every request carries a correlation ID: it reuses
// an inbound RequestIDHeader value if the caller already supplied one
// (so a request can be traced across multiple services), or generates a
// new random one otherwise. The ID is attached to the request context (via
// RequestID) and echoed back on the response so a client can correlate its
// own logs with the server's.
func CorrelationID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set(RequestIDHeader, id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// newRequestID generates a random 16-byte hex ID. Not a UUID (no need for
// the extra format/dependency to satisfy "a unique-enough trace token"),
// but collision-improbable for this service's traffic volume.
func newRequestID() string {
	buf := make([]byte, 16)
	// crypto/rand.Read on the standard library's default reader never
	// returns an error in practice (it would mean the OS entropy source
	// itself failed) — but a zero-value ID would still be a valid,
	// if-degraded, token, so this path intentionally doesn't panic or
	// fail the request over it.
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

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
				"request_id", RequestID(r.Context()),
			)
		})
	}
}
