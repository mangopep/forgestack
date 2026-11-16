package integration

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mangopep/forgestack/internal/platform/router"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestMetricsEndpointExposesRealPrometheusOutput(t *testing.T) {
	handler := router.New()

	for i := 0; i < 2; i++ {
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, httptest.NewRequest("GET", "/healthz", nil))
		if r.Code != 200 {
			t.Fatalf("healthz request %d: want 200, got %d", i, r.Code)
		}
	}

	r := httptest.NewRecorder()
	handler.ServeHTTP(r, httptest.NewRequest("GET", "/metrics", nil))
	if r.Code != 200 {
		t.Fatalf("GET /metrics: want 200, got %d", r.Code)
	}
	body := r.Body.String()
	if !strings.Contains(body, `http_requests_total{method="GET",route="GET /healthz",status="200"} 2`) {
		t.Errorf("expected a real Prometheus counter line for the two /healthz requests, got:\n%s", body)
	}
	if !strings.Contains(body, "http_request_duration_seconds") {
		t.Errorf("expected a real Prometheus histogram for request duration, got:\n%s", body)
	}
	// /metrics itself must not have counted /metrics -- it's read after the
	// assertions above are formed from the response, so this just confirms
	// the exposition format is well-formed Prometheus text, not that it's
	// empty or malformed.
	if !strings.HasPrefix(body, "# HELP") {
		t.Errorf("expected standard Prometheus text exposition format starting with '# HELP', got:\n%.80s", body)
	}
}

// TestRealRequestProducesLogsMetricsAndATrace is F6's "demonstrate a real
// request moving through the service, producing structured logs, metrics,
// and an exported trace" requirement, run against the actual assembled
// router.New() handler (not a hand-built test mux) and real middleware
// composition -- only the trace *exporter* is swapped for an in-memory one
// so the test doesn't require a running OTLP collector.
func TestRealRequestProducesLogsMetricsAndATrace(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	var logs bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	handler := router.New()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/posts", nil))
	if rec.Code != 200 {
		t.Fatalf("GET /api/v1/posts: want 200, got %d", rec.Code)
	}

	// 1. Structured log, correlated to the trace.
	var logged map[string]any
	if err := json.Unmarshal(logs.Bytes(), &logged); err != nil {
		t.Fatalf("expected one JSON log line, got %q: %v", logs.String(), err)
	}
	traceID, _ := logged["trace_id"].(string)
	if len(traceID) != 32 {
		t.Fatalf("expected a 32-hex-char trace_id in the log line, got %+v", logged["trace_id"])
	}
	if logged["request_id"] == "" || logged["request_id"] == nil {
		t.Error("expected request_id in the log line alongside trace_id")
	}

	// 2. Metrics, scraped from the same assembled handler.
	metricsRec := httptest.NewRecorder()
	handler.ServeHTTP(metricsRec, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(metricsRec.Body.String(), `route="GET /api/v1/posts"`) {
		t.Errorf("expected /metrics to show the GET /api/v1/posts route, got:\n%s", metricsRec.Body.String())
	}

	// 3. Exported trace -- correlated to the same trace_id as the log line.
	// (sr also recorded a second span for the /metrics scrape just above;
	// find the one for the actual request under test by name.)
	var postsSpan sdktrace.ReadOnlySpan
	for _, s := range sr.Ended() {
		if s.Name() == "GET /api/v1/posts" {
			postsSpan = s
		}
	}
	if postsSpan == nil {
		t.Fatalf("expected an ended span named %q, got: %v", "GET /api/v1/posts", sr.Ended())
	}
	if got := postsSpan.SpanContext().TraceID().String(); got != traceID {
		t.Errorf("span trace ID %s does not match the log line's trace_id %s -- log/trace correlation is broken", got, traceID)
	}
}
