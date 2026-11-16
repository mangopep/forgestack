// Package middleware holds cross-cutting net/http middleware shared across
// modules: request body size limiting, correlation IDs, structured request
// logging, and OpenTelemetry tracing + Prometheus metrics.
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"__MODULE_PATH__/internal/platform/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
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
// metrics endpoint); it is not a replacement for real metrics. If next (or
// an outer layer such as Telemetry) has already attached an OpenTelemetry
// span to the request context, the log line also carries trace_id so a log
// line and a trace can be correlated in Grafana/Loki -- never any header,
// body, or token value.
func RequestLogging(counter *atomic.Uint64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			counter.Add(1)
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			attrs := []any{
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", RequestID(r.Context()),
			}
			if sc := trace.SpanContextFromContext(r.Context()); sc.HasTraceID() {
				attrs = append(attrs, "trace_id", sc.TraceID().String())
			}
			slog.Info("http_request", attrs...)
		})
	}
}

// inboundPropagator extracts a W3C traceparent/baggage header pair. It is a
// package-local value, not the global otel.GetTextMapPropagator(), so
// inbound trace continuation works the instant this middleware runs --
// it never silently degrades to a no-op just because telemetry.Setup
// (which also sets the *global* propagator, for any future outbound-call
// instrumentation) happened not to run first, e.g. in a test or a caller
// that assembles the router without calling Setup.
var inboundPropagator = propagation.NewCompositeTextMapPropagator(
	propagation.TraceContext{}, propagation.Baggage{},
)

// Telemetry wraps next with OpenTelemetry span creation (extracting any
// inbound W3C traceparent so a client-supplied trace continues rather than
// starting a new one) and Prometheus request/error/latency recording.
// Telemetry must sit directly outside the final mux (optionally with only
// request-logging-style middleware that forwards the same *http.Request
// pointer in between) so that r.Pattern -- set by http.ServeMux once it
// matches a route -- is visible here afterward; without it, the route label
// falls back to the raw path, which is still correct but loses the
// low-cardinality guarantee a registered pattern gives for path-parameter
// routes.
func Telemetry(m *telemetry.Metrics) func(http.Handler) http.Handler {
	tracer := otel.Tracer("__SERVICE_NAME__/http")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := inboundPropagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			ctx, span := tracer.Start(ctx, r.Method+" "+r.URL.Path, trace.WithSpanKind(trace.SpanKindServer))
			defer span.End()

			req := r.WithContext(ctx)
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(rec, req)
			duration := time.Since(start)

			route := req.Pattern
			if route == "" {
				route = r.URL.Path
			}
			status := strconv.Itoa(rec.status)

			span.SetName(route)
			span.SetAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.route", route),
				attribute.Int("http.status_code", rec.status),
			)
			if rec.status >= 500 {
				span.SetStatus(codes.Error, fmt.Sprintf("http %d", rec.status))
			}

			m.RequestsTotal.WithLabelValues(r.Method, route, status).Inc()
			m.RequestDuration.WithLabelValues(r.Method, route).Observe(duration.Seconds())
			if rec.status >= 400 {
				m.ErrorsTotal.WithLabelValues(r.Method, route, status).Inc()
			}
		})
	}
}
