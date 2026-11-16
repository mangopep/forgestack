package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"go.opentelemetry.io/otel"
)

func TestSetupWithNoEndpointSucceedsAndNeverExports(t *testing.T) {
	shutdown, err := Setup(context.Background(), "test-service", "0.0.0", "")
	if err != nil {
		t.Fatalf("Setup with no endpoint should succeed (no collector required): %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown of a no-exporter provider should succeed, got: %v", err)
	}
}

func TestSetupWithMalformedEndpointFailsClosed(t *testing.T) {
	// A scheme-less, unparsable endpoint must be rejected at Setup time with
	// a clear error, not silently ignored or panicking later mid-request.
	_, err := Setup(context.Background(), "test-service", "0.0.0", "\x7f not a url")
	if err == nil {
		t.Fatal("expected Setup to reject a malformed OTLP endpoint")
	}
}

// TestSetupPostsToTheCorrectOTLPPath is a regression test for a real bug
// found by actually running this against a live OpenTelemetry Collector
// (see observability/README.md): giving the standard bare
// "http://host:port" form of OTEL_EXPORTER_OTLP_ENDPOINT (no /v1/traces
// suffix) to otlptracehttp.WithEndpointURL makes it POST to "/", which a
// real collector answers with 404 -- exports silently failed every time.
// Setup must build the exporter so the default "/v1/traces" path is
// appended instead.
func TestSetupPostsToTheCorrectOTLPPath(t *testing.T) {
	var gotPath string
	var gotRequests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	shutdown, err := Setup(context.Background(), "test-service", "0.0.0", srv.URL)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	_, span := otel.Tracer("test").Start(context.Background(), "test-span")
	span.End()
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown (which flushes the exporter): %v", err)
	}

	if gotRequests.Load() == 0 {
		t.Fatal("expected the exporter to make at least one request to the test collector, got none")
	}
	if gotPath != "/v1/traces" {
		t.Errorf("exporter posted to path %q, want the standard OTLP HTTP traces path %q", gotPath, "/v1/traces")
	}
}

func TestSetupFromEnvWithoutEndpointVarSucceeds(t *testing.T) {
	t.Setenv(EndpointEnvVar, "")
	shutdown, err := SetupFromEnv(context.Background(), "test-service", "0.0.0")
	if err != nil {
		t.Fatalf("SetupFromEnv with no env var set should succeed: %v", err)
	}
	_ = shutdown(context.Background())
}

func TestNewMetricsRegistersDistinctCollectors(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	m.RequestsTotal.WithLabelValues("GET", "/healthz", "200").Inc()
	m.ErrorsTotal.WithLabelValues("GET", "/missing", "404").Inc()
	m.RequestDuration.WithLabelValues("GET", "/healthz").Observe(0.01)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	names := map[string]bool{}
	for _, f := range families {
		names[f.GetName()] = true
	}
	for _, want := range []string{"http_requests_total", "http_request_errors_total", "http_request_duration_seconds"} {
		if !names[want] {
			t.Errorf("expected registered metric family %q, got families: %v", want, names)
		}
	}
}

func TestNewMetricsCalledTwiceOnSeparateRegistriesDoesNotPanic(t *testing.T) {
	// Mirrors how router.New() is called once per test in this repo's
	// integration suite: each call must get its own registry, never the
	// shared global default one, or the second call panics on duplicate
	// registration.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("NewMetrics on a fresh registry should never panic, got: %v", r)
		}
	}()
	_ = NewMetrics(prometheus.NewRegistry())
	_ = NewMetrics(prometheus.NewRegistry())
}

func counterValue(t *testing.T, reg *prometheus.Registry, name string) float64 {
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

func TestCounterValueHelperSumsAcrossLabelCombinations(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	m.RequestsTotal.WithLabelValues("GET", "/a", "200").Inc()
	m.RequestsTotal.WithLabelValues("GET", "/b", "200").Inc()
	if got := counterValue(t, reg, "http_requests_total"); got != 2 {
		t.Fatalf("expected summed counter value 2, got %v", got)
	}
}

// Sanity-check the dto import actually matches what client_golang's Gather
// returns, so a future client_golang major bump that changes this shape
// fails this test instead of silently losing coverage.
func TestGatherReturnsExpectedProtoShape(t *testing.T) {
	reg := prometheus.NewRegistry()
	NewMetrics(reg).RequestsTotal.WithLabelValues("GET", "/x", "200").Inc()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var found *dto.MetricFamily
	for _, f := range families {
		if f.GetName() == "http_requests_total" {
			found = f
		}
	}
	if found == nil || found.GetType() != dto.MetricType_COUNTER {
		t.Fatalf("expected a COUNTER family named http_requests_total, got %+v", found)
	}
}
