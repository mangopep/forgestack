package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"__MODULE_PATH__/internal/platform/telemetry"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// withTestTracerProvider installs an in-memory span recorder as the global
// tracer provider for the duration of a test and restores the previous one
// after, so tests can run in parallel with the package's other tests
// without leaking global state.
func withTestTracerProvider(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	return sr
}

func counterTotal(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var total float64
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, metric := range f.GetMetric() {
			total += metric.GetCounter().GetValue()
		}
	}
	return total
}

func TestTelemetryCreatesASpanNamedForTheMatchedRoute(t *testing.T) {
	sr := withTestTracerProvider(t)
	reg := prometheus.NewRegistry()
	m := telemetry.NewMetrics(reg)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	Telemetry(m)(mux).ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected exactly one ended span, got %d", len(spans))
	}
	if got := spans[0].Name(); got != "GET /healthz" {
		t.Errorf("span name = %q, want the matched route pattern %q", got, "GET /healthz")
	}
	if got := counterTotal(t, reg, "http_requests_total"); got != 1 {
		t.Errorf("http_requests_total = %v, want 1", got)
	}
	if got := counterTotal(t, reg, "http_request_errors_total"); got != 0 {
		t.Errorf("http_request_errors_total = %v, want 0 for a 200 response", got)
	}
}

func TestTelemetryRecordsErrorsAndSpanStatusForFailedRequests(t *testing.T) {
	sr := withTestTracerProvider(t)
	reg := prometheus.NewRegistry()
	m := telemetry.NewMetrics(reg)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /boom", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	rec := httptest.NewRecorder()
	Telemetry(m)(mux).ServeHTTP(rec, httptest.NewRequest("GET", "/boom", nil))

	if got := counterTotal(t, reg, "http_request_errors_total"); got != 1 {
		t.Errorf("http_request_errors_total = %v, want 1 for a 500 response", got)
	}
	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected exactly one ended span, got %d", len(spans))
	}
	if got := spans[0].Status().Code; got != codes.Error {
		t.Errorf("span status = %v, want codes.Error for a 500 response", got)
	}
}

func TestTelemetryFallsBackToRawPathWhenNoRouteMatches(t *testing.T) {
	sr := withTestTracerProvider(t)
	reg := prometheus.NewRegistry()
	m := telemetry.NewMetrics(reg)

	// An empty, unmatched mux never sets r.Pattern, exercising the
	// route-label fallback to r.URL.Path (still correct, just a wider
	// label than a registered pattern would give).
	mux := http.NewServeMux()

	rec := httptest.NewRecorder()
	Telemetry(m)(mux).ServeHTTP(rec, httptest.NewRequest("GET", "/no-such-route", nil))

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected exactly one ended span, got %d", len(spans))
	}
	if got := spans[0].Name(); got != "/no-such-route" {
		t.Errorf("span name = %q, want the raw path fallback %q", got, "/no-such-route")
	}
}

func TestRequestLoggingIncludesTraceIDWhenASpanIsPresent(t *testing.T) {
	withTestTracerProvider(t)
	reg := prometheus.NewRegistry()
	m := telemetry.NewMetrics(reg)

	var buf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	var counter atomic.Uint64
	// Same composition order as router.New(): CorrelationID wraps
	// Telemetry wraps RequestLogging wraps the mux, so the span Telemetry
	// starts is on the context RequestLogging reads from afterward.
	handler := CorrelationID(Telemetry(m)(RequestLogging(&counter)(mux)))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))

	var logged map[string]any
	if err := json.Unmarshal(buf.Bytes(), &logged); err != nil {
		t.Fatalf("expected one JSON log line, got %q: %v", buf.String(), err)
	}
	traceID, ok := logged["trace_id"].(string)
	if !ok || len(traceID) != 32 {
		t.Errorf("expected a 32-hex-char trace_id in the log line, got %+v", logged["trace_id"])
	}
	if logged["request_id"] == "" || logged["request_id"] == nil {
		t.Error("expected request_id to still be present alongside trace_id")
	}
}

func TestTelemetryContinuesAnInboundTraceparentHeader(t *testing.T) {
	sr := withTestTracerProvider(t)
	reg := prometheus.NewRegistry()
	m := telemetry.NewMetrics(reg)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	req := httptest.NewRequest("GET", "/healthz", nil)
	req.Header.Set("traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")

	Telemetry(m)(mux).ServeHTTP(httptest.NewRecorder(), req)

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected exactly one ended span, got %d", len(spans))
	}
	if got := spans[0].SpanContext().TraceID().String(); got != traceID {
		t.Errorf("expected the span to continue the inbound trace %s, got %s", traceID, got)
	}
}

func TestRequestLoggingOmitsTraceIDWithoutASpan(t *testing.T) {
	var buf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	var counter atomic.Uint64
	handler := RequestLogging(&counter)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/healthz", nil))

	var logged map[string]any
	if err := json.Unmarshal(buf.Bytes(), &logged); err != nil {
		t.Fatalf("expected one JSON log line, got %q: %v", buf.String(), err)
	}
	if _, present := logged["trace_id"]; present {
		t.Errorf("expected no trace_id field when no span was ever started, got %+v", logged["trace_id"])
	}
}

func TestCorrelationIDGeneratesWhenAbsent(t *testing.T) {
	var seen string
	handler := CorrelationID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestID(r.Context())
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if seen == "" {
		t.Fatal("expected a generated request ID in the handler's context, got empty string")
	}
	if got := rec.Header().Get(RequestIDHeader); got != seen {
		t.Errorf("response header %s = %q, want it to match the context value %q", RequestIDHeader, got, seen)
	}
}

func TestCorrelationIDReusesInboundValue(t *testing.T) {
	const inbound = "caller-supplied-trace-id"
	var seen string
	handler := CorrelationID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestID(r.Context())
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(RequestIDHeader, inbound)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if seen != inbound {
		t.Errorf("expected the inbound request ID to be reused, got %q, want %q", seen, inbound)
	}
	if got := rec.Header().Get(RequestIDHeader); got != inbound {
		t.Errorf("response header %s = %q, want %q (echoed back)", RequestIDHeader, got, inbound)
	}
}

func TestRequestIDWithoutMiddlewareReturnsEmpty(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	if got := RequestID(req.Context()); got != "" {
		t.Errorf("expected empty string for a context never touched by CorrelationID, got %q", got)
	}
}

func TestTwoRequestsGetDifferentGeneratedIDs(t *testing.T) {
	var first, second string
	handler := CorrelationID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if first == "" {
			first = RequestID(r.Context())
			return
		}
		second = RequestID(r.Context())
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	if first == "" || second == "" {
		t.Fatal("expected both requests to get a non-empty generated ID")
	}
	if first == second {
		t.Error("expected two separate requests with no inbound header to get different generated IDs, got the same one twice")
	}
}
