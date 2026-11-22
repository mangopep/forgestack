package playground

import (
	"context"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// This file is the ENTIRE observability surface for cleanup: it reuses
// internal/platform/telemetry's already-configured global OTel
// TracerProvider (zero-config safe: spans are always created and
// sampled locally, exported nowhere unless telemetry.Setup was given an
// endpoint -- this package never calls telemetry.Setup itself, exactly
// like it never constructs a live CloudClient; that remains the
// caller's job, see cmd/playground/main.go) and Prometheus's registry
// pattern (NewMetrics(reg), mirroring internal/platform/telemetry.
// NewMetrics exactly -- a fresh registry per call so repeated test
// calls never panic on duplicate registration).
//
// No new monitoring stack, no new dependency, no new background
// service: tracer := otel.Tracer(...) and promauto.With(reg) are both
// already-vendored, already-used-elsewhere patterns.
//
// FAILURE ISOLATION, THE CORE RULE EVERYTHING BELOW OBEYS: telemetry
// must never determine whether cleanup succeeds, and an unavailable
// collector must never block cleanup. Every function in this file is
// therefore: (a) safe to call with a nil *Metrics (a no-op, not a
// panic) -- Controller.Metrics defaults to nil, so a Controller that
// never opts in behaves with zero observability-related behavior
// change; (b) non-blocking -- Prometheus counters/gauges are in-memory
// increments, and OTel spans are created/ended synchronously but their
// actual network export (if any) happens on the SDK's own background
// batching goroutine, never on this call's own goroutine; (c) never
// itself returns an error that could be mistaken for a cleanup failure
// -- every function here returns nothing, or an error ONLY from
// UpdateUnresolvedGauge's own read of persisted state (ListEnvironments),
// which a caller must treat exactly like any other observability-only
// failure: log it, never let it affect cleanup.
// tracerName identifies this package's spans. Deliberately NOT cached
// in a package-level `var tracer = otel.Tracer(tracerName)`: the OTel
// Go SDK's global TracerProvider delegates an already-created Tracer
// to a real provider only ONCE (via a sync.Once under the hood) --
// calling otel.SetTracerProvider a second time (e.g. a second test in
// the same process installing its own recorder) updates the global
// pointer but never re-delegates a Tracer handle obtained before that
// point. Calling otel.Tracer(tracerName) fresh on every startSpan call
// instead looks up whatever TracerProvider is CURRENTLY installed every
// time, which is what both production (telemetry.Setup called once,
// early) and repeated test setup/teardown (multiple
// SetTracerProvider calls across tests) actually need.
const tracerName = "forgestack/internal/playground"

// Metrics holds this package's Prometheus collectors. Every label is a
// fixed, bounded enum value (a domain name, an outcome name, a
// notification kind) -- NEVER an environment ID, bucket name, account
// ID, or any other unbounded or sensitive value, which would make
// Prometheus's own storage grow unboundedly with cardinality and could
// leak an identifier into a metrics scrape. Environment-level
// correlation belongs on spans/logs (see logCleanupEvent), never on a
// metric label.
type Metrics struct {
	// CleanupAttemptsTotal counts every cleanup attempt outcome, by
	// domain ("local", "docker", "cloud") and outcome ("verified_clean",
	// "unverified", "incomplete", "attention_required").
	CleanupAttemptsTotal *prometheus.CounterVec
	// AttentionRequiredTotal counts every time an environment newly
	// escalates to StatusCleanupAttentionRequired -- the one status a
	// human must act on (see ResetCloudCleanupAttempts).
	AttentionRequiredTotal prometheus.Counter
	// NotificationTotal counts every SendCleanupNotification outcome, by
	// kind (verified_clean/cleanup_incomplete/attention_required) and
	// outcome (delivered/failed/skipped_claimed).
	NotificationTotal *prometheus.CounterVec
	// ClaimRecoveryTotal counts every time claimNotification reclaims an
	// abandoned (lease-expired) claim -- see notification.go's
	// notificationClaimLease. A nonzero rate of this is itself worth
	// alerting on: it means something (a crash, a stuck notifier) is
	// preventing notifications from finalizing normally.
	ClaimRecoveryTotal prometheus.Counter
	// UnresolvedEnvironments is a GAUGE -- Set, never Add/Inc -- holding
	// the CURRENT count of environments whose persisted status is not
	// yet a terminal success (see unresolvedCleanupStatuses). It is
	// always recomputed from ListEnvironments (UpdateUnresolvedGauge),
	// never incremented/decremented in place, so it can never drift from
	// actual persisted state across a crash, a restart, or a manual
	// ResetCloudCleanupAttempts -- the next call simply recounts from
	// scratch.
	UnresolvedEnvironments prometheus.Gauge
}

// NewMetrics registers this package's collectors on reg and returns
// them. Pass the returned *Metrics as Controller.Metrics to opt in;
// leave it nil (the default) for zero observability-related behavior
// change, exactly like Worker.Notifier's own opt-in design.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	factory := promauto.With(reg)
	return &Metrics{
		CleanupAttemptsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "cleanup_attempts_total",
			Help: "Total cleanup attempts, labeled by domain and outcome.",
		}, []string{"domain", "outcome"}),
		AttentionRequiredTotal: factory.NewCounter(prometheus.CounterOpts{
			Name: "cleanup_attention_required_total",
			Help: "Total times an environment newly escalated to cleanup_attention_required.",
		}),
		NotificationTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "cleanup_notification_total",
			Help: "Total cleanup notification delivery attempts, labeled by kind and outcome.",
		}, []string{"kind", "outcome"}),
		ClaimRecoveryTotal: factory.NewCounter(prometheus.CounterOpts{
			Name: "cleanup_claim_recovery_total",
			Help: "Total times an abandoned (lease-expired) notification claim was reclaimed.",
		}),
		UnresolvedEnvironments: factory.NewGauge(prometheus.GaugeOpts{
			Name: "cleanup_unresolved_environments",
			Help: "Current count of environments not yet in a terminal verified_clean state, recomputed from persisted state on every update.",
		}),
	}
}

// recordCleanupAttempt increments CleanupAttemptsTotal{domain,outcome}.
// Safe to call with a nil m.
func recordCleanupAttempt(m *Metrics, domain, outcome string) {
	if m == nil {
		return
	}
	m.CleanupAttemptsTotal.WithLabelValues(domain, outcome).Inc()
}

// recordAttentionRequired increments AttentionRequiredTotal. Safe to
// call with a nil m.
func recordAttentionRequired(m *Metrics) {
	if m == nil {
		return
	}
	m.AttentionRequiredTotal.Inc()
}

// recordNotification increments NotificationTotal{kind,outcome}. Safe
// to call with a nil m.
func recordNotification(m *Metrics, kind NotificationKind, outcome string) {
	if m == nil {
		return
	}
	m.NotificationTotal.WithLabelValues(string(kind), outcome).Inc()
}

// recordClaimRecovery increments ClaimRecoveryTotal. Safe to call with
// a nil m.
func recordClaimRecovery(m *Metrics) {
	if m == nil {
		return
	}
	m.ClaimRecoveryTotal.Inc()
}

// unresolvedCleanupStatuses is every Environment.Status this package
// considers "not yet a terminal success" for UpdateUnresolvedGauge's
// count -- deliberately NOT reusing needsCleanup (controller.go)
// unchanged, because needsCleanup answers "will an automatic pass
// retry this," which EXCLUDES StatusCleanupAttentionRequired on
// purpose (see needsCleanup's own doc comment) -- but that status is
// exactly the one this gauge most needs to surface, since it is the
// one requiring a human. This set is therefore needsCleanup's own set
// PLUS StatusCleanupAttentionRequired, reusing the same status
// constants (lifecycle.go), never inventing a parallel vocabulary.
func unresolvedCleanupStatuses(status string) bool {
	return needsCleanup(status) || status == StatusCleanupAttentionRequired
}

// UpdateUnresolvedGauge recomputes m.UnresolvedEnvironments from
// ListEnvironments (root) -- the exact same read-only, persisted-state
// scan ExpireDue/CleanupDue/ReconcileCleanupNotifications already use --
// and Sets (never Add/Inc) the gauge to that fresh count. Safe to call
// with a nil m (a no-op returning the count anyway, so a caller can
// still use the return value for a structured log line without a
// Metrics instance). The returned error is an OBSERVABILITY-ONLY
// failure (ListEnvironments itself erroring) -- callers must log it and
// continue, never treat it as a cleanup failure.
func UpdateUnresolvedGauge(m *Metrics, root string) (int, error) {
	envs, err := ListEnvironments(root)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, env := range envs {
		if unresolvedCleanupStatuses(env.Status) {
			count++
		}
	}
	if m != nil {
		m.UnresolvedEnvironments.Set(float64(count))
	}
	return count, nil
}

// startSpan starts a child span named "playground."+name with attrs --
// every attribute here must be a fixed, bounded enum value or a single
// identifier meant for correlation (environment_id), never a raw
// resource identifier, bucket name, or free-text error dump. Always
// succeeds, even with zero collector configured: see this file's own
// top-of-file doc comment.
func startSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	return otel.Tracer(tracerName).Start(ctx, "playground."+name, oteltrace.WithAttributes(attrs...))
}

// traceIDAttr returns the "trace_id" slog attribute for ctx's current
// span, if any -- the same correlation convention
// internal/platform/middleware.RequestLogging already uses -- or nil
// (omit entirely) if ctx carries no active span/trace.
func traceIDAttr(ctx context.Context) []any {
	sc := oteltrace.SpanContextFromContext(ctx)
	if !sc.HasTraceID() {
		return nil
	}
	return []any{"trace_id", sc.TraceID().String()}
}

// logCleanupEvent emits ONE structured slog line for a cleanup/
// notification decision, via slog.Default() -- never a separate logger
// instance, so it respects whatever handler (JSON, text, or none) the
// hosting process has already configured, exactly like
// internal/platform/middleware.RequestLogging does. Fields are
// environment_id, status, domain, attempt, failure_category,
// verification_mode, and trace_id (if ctx carries a span) -- NEVER a
// claim token, raw tracked-resource JSON, credential, or any other
// sensitive payload; see TestLogCleanupEvent_NeverLogsSensitiveFields.
//
// attempt and failureCategory are 0/"" when not applicable (e.g. a
// clean local-only pass with nothing to report) -- slog simply omits a
// zero-value int/empty string from nothing special, so callers pass
// them unconditionally rather than building a variadic list themselves.
func logCleanupEvent(ctx context.Context, environmentID, status, domain string, attempt int, failureCategory string, verificationMode VerificationMode) {
	attrs := []any{
		"environment_id", environmentID,
		"status", status,
		"domain", domain,
		"attempt", attempt,
		"failure_category", failureCategory,
		"verification_mode", string(verificationMode),
	}
	attrs = append(attrs, traceIDAttr(ctx)...)
	slog.Default().Info("cleanup_event", attrs...)
}

// logNotificationEvent is logCleanupEvent's notification-delivery
// equivalent.
func logNotificationEvent(ctx context.Context, environmentID string, kind NotificationKind, outcome string, attempts int) {
	attrs := []any{
		"environment_id", environmentID,
		"notification_kind", string(kind),
		"notification_outcome", outcome,
		"attempt", attempts,
	}
	attrs = append(attrs, traceIDAttr(ctx)...)
	slog.Default().Info("cleanup_notification_event", attrs...)
}
