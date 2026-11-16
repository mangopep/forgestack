// Package telemetry configures this service's OpenTelemetry tracing and
// Prometheus metrics. Both work with zero configuration and no running
// collector: traces are created and sampled locally but exported nowhere
// until OTEL_EXPORTER_OTLP_ENDPOINT is set, and metrics are always served
// locally from /metrics regardless of any collector's presence. Neither
// path ever attempts a network call unless an endpoint is explicitly
// configured, so a developer who hasn't started the observability stack
// (see observability/README.md) sees no errors, timeouts, or retry noise.
package telemetry

import (
	"context"
	"fmt"
	"net/url"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// EndpointEnvVar is the standard OpenTelemetry env var this package reads to
// decide whether to export spans anywhere. Its absence is not an error --
// it is the supported "no collector present" configuration.
const EndpointEnvVar = "OTEL_EXPORTER_OTLP_ENDPOINT"

// Shutdown flushes any buffered spans and releases exporter resources. It
// must be called before process exit (e.g. deferred in main) or buffered
// spans can be lost; it is itself safe to call even if Setup used no
// exporter.
type Shutdown func(context.Context) error

// Setup installs the global OpenTelemetry tracer provider and a W3C
// tracecontext+baggage propagator (so an inbound traceparent header
// continues an existing trace, and this service's own outbound calls, if
// any, would propagate it further). If endpoint is empty, spans are still
// created and sampled -- callers and tests see real span/trace IDs -- but
// nothing is exported over the network.
func Setup(ctx context.Context, serviceName, serviceVersion, endpoint string) (Shutdown, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(serviceName),
		semconv.ServiceVersion(serviceVersion),
	))
	if err != nil {
		return nil, fmt.Errorf("build telemetry resource: %w", err)
	}

	opts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
	if endpoint != "" {
		// otlptracehttp.New itself only logs (via its internal logger) and
		// falls back to defaults on a malformed endpoint URL rather than
		// returning an error -- validate explicitly so a typo in
		// OTEL_EXPORTER_OTLP_ENDPOINT fails loudly here, not silently later
		// by exporting nowhere.
		u, parseErr := url.Parse(endpoint)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid %s %q: %w", EndpointEnvVar, endpoint, parseErr)
		}
		// otlptracehttp.WithEndpointURL takes the URL literally: a bare
		// "http://host:port" (the standard OTEL_EXPORTER_OTLP_ENDPOINT
		// form, with no /v1/traces suffix) becomes a request to "/", which
		// 404s against a real collector -- confirmed against a live
		// OpenTelemetry Collector while building this. WithEndpoint (host
		// only, no scheme) is the option that appends the correct default
		// signal path, so build the exporter from the parsed host instead.
		httpOpts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(u.Host)}
		if u.Scheme != "https" {
			httpOpts = append(httpOpts, otlptracehttp.WithInsecure())
		}
		exp, err := otlptracehttp.New(ctx, httpOpts...)
		if err != nil {
			return nil, fmt.Errorf("create OTLP trace exporter for %q: %w", endpoint, err)
		}
		opts = append(opts, sdktrace.WithBatcher(exp))
	}

	tp := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// SetupFromEnv is Setup using the standard OTEL_EXPORTER_OTLP_ENDPOINT env
// var, for callers (main.go) that want the conventional zero-config path.
func SetupFromEnv(ctx context.Context, serviceName, serviceVersion string) (Shutdown, error) {
	return Setup(ctx, serviceName, serviceVersion, os.Getenv(EndpointEnvVar))
}

// Metrics holds the Prometheus collectors this service exports. Every label
// value comes from the server's own matched route pattern and the recorded
// status code -- never from request/response bodies, query strings, or
// headers -- so a label can never carry a secret, token, or PII.
type Metrics struct {
	RequestsTotal   *prometheus.CounterVec
	ErrorsTotal     *prometheus.CounterVec
	RequestDuration *prometheus.HistogramVec
}

// NewMetrics registers this service's HTTP metrics on reg and returns them.
// A fresh *prometheus.Registry per call (rather than the global default
// registry) keeps repeated calls -- e.g. once per test -- from panicking on
// duplicate registration.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	factory := promauto.With(reg)
	return &Metrics{
		RequestsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests handled, labeled by method, route and status.",
		}, []string{"method", "route", "status"}),
		ErrorsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "http_request_errors_total",
			Help: "Total HTTP requests that completed with a 4xx or 5xx status, labeled by method, route and status.",
		}, []string{"method", "route", "status"}),
		RequestDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds, labeled by method and route.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
	}
}
