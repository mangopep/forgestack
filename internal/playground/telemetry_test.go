package playground

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// withSpanRecorder installs a fresh in-memory OTel TracerProvider as the
// global provider for the duration of the test, restoring whatever
// provider was installed before (never leaking state into other tests,
// and never requiring a real collector -- the entire point of this
// package's zero-config tracing design). Returns the recorder so the
// test can inspect every span this package's startSpan calls produced.
func withSpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	return rec
}

// withJSONLog redirects slog.Default() to a JSON handler writing into
// buf for the duration of the test, restoring the previous default
// logger afterward -- the same pattern
// internal/platform/middleware_test.go already uses.
func withJSONLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func counterVecValue(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, metric := range f.GetMetric() {
			got := map[string]string{}
			for _, lp := range metric.GetLabel() {
				got[lp.GetName()] = lp.GetValue()
			}
			match := true
			for k, v := range labels {
				if got[k] != v {
					match = false
					break
				}
			}
			if match {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func gaugeValue(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() == name && len(f.GetMetric()) > 0 {
			return f.GetMetric()[0].GetGauge().GetValue()
		}
	}
	return 0
}

func TestNewMetrics_RegistersAllCollectorsAndNeverPanicsOnRepeatedCall(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("NewMetrics on a fresh registry should never panic, got: %v", r)
		}
	}()
	reg1 := prometheus.NewRegistry()
	m := NewMetrics(reg1)
	m.CleanupAttemptsTotal.WithLabelValues("local", "verified_clean").Inc()
	m.AttentionRequiredTotal.Inc()
	m.NotificationTotal.WithLabelValues("verified_clean", "delivered").Inc()
	m.ClaimRecoveryTotal.Inc()
	m.UnresolvedEnvironments.Set(3)

	families, err := reg1.Gather()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range families {
		names[f.GetName()] = true
	}
	for _, want := range []string{
		"cleanup_attempts_total", "cleanup_attention_required_total",
		"cleanup_notification_total", "cleanup_claim_recovery_total",
		"cleanup_unresolved_environments",
	} {
		if !names[want] {
			t.Errorf("expected registered metric family %q, got %v", want, names)
		}
	}

	_ = NewMetrics(prometheus.NewRegistry()) // a second, independent registry must never panic
}

// TestMetricsHelpers_NeverPanicOnNilMetrics is the permanent regression
// test for requirement 4 ("telemetry must never determine whether
// cleanup succeeds"): every record* helper must be a safe no-op when
// Controller.Metrics is nil (the default, zero-config state).
func TestMetricsHelpers_NeverPanicOnNilMetrics(t *testing.T) {
	recordCleanupAttempt(nil, "local", "verified_clean")
	recordAttentionRequired(nil)
	recordNotification(nil, NotificationVerifiedClean, "delivered")
	recordClaimRecovery(nil)
	if _, err := UpdateUnresolvedGauge(nil, t.TempDir()); err != nil {
		t.Fatalf("UpdateUnresolvedGauge with a nil *Metrics should still succeed, got: %v", err)
	}
}

// TestCleanupAttemptsTotal_CorrectForEveryOutcome drives the real
// Controller through local-only success, a tracked-but-unconfigured
// domain (unverified), and cloud escalation (attention_required), and
// asserts cleanup_attempts_total/cleanup_attention_required_total carry
// the exact right labels/counts for each -- requirement: "correct
// metrics for every cleanup outcome."
func TestCleanupAttemptsTotal_CorrectForEveryOutcome(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg)

	t.Run("local_only_verified_clean", func(t *testing.T) {
		assertLocalOnlyVerifiedCleanMetric(t, reg, metrics)
	})
	t.Run("cloud_escalates_to_attention_required", func(t *testing.T) {
		assertCloudEscalationMetrics(t, reg, metrics)
	})
	t.Run("missing_docker_client_unverified", func(t *testing.T) {
		assertMissingDockerClientMetric(t, reg, metrics)
	})
}

func assertLocalOnlyVerifiedCleanMetric(t *testing.T, reg *prometheus.Registry, metrics *Metrics) {
	t.Helper()
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := Controller{Manager: m, Now: func() time.Time { return env.ExpiresAt.Add(time.Millisecond) }, Sleep: noSleep, MaxCleanupAttempts: 1, Metrics: metrics}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CleanupDue(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if got := counterVecValue(t, reg, "cleanup_attempts_total", map[string]string{"domain": "local", "outcome": StatusVerifiedClean}); got != 1 {
		t.Errorf("expected local/verified_clean=1, got %v", got)
	}
}

func assertCloudEscalationMetrics(t *testing.T, reg *prometheus.Registry, metrics *Metrics) {
	t.Helper()
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "bob", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-telemetry-escalate")
	if err := m.TrackCloudResource(root, env.ID, "bob", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, cloudTags(env.ID, "bob"), 0, 0, 0)
	fake.RemoveErr = errCloudPermanentlyDenied
	c := Controller{
		Manager: m, Now: func() time.Time { return env.ExpiresAt.Add(time.Millisecond) }, Sleep: noSleep,
		MaxCleanupAttempts: 1, Cloud: fake, MaxCloudCleanupAttempts: 1, Metrics: metrics,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CleanupDue(context.Background(), root); err == nil {
		t.Fatal("expected escalation to report a non-nil error")
	}
	if got := counterVecValue(t, reg, "cleanup_attempts_total", map[string]string{"domain": "cloud", "outcome": "attention_required"}); got != 1 {
		t.Errorf("expected cloud/attention_required=1, got %v", got)
	}
	if got := counterVecValue(t, reg, "cleanup_attention_required_total", nil); got != 1 {
		t.Errorf("expected cleanup_attention_required_total=1, got %v", got)
	}
}

func assertMissingDockerClientMetric(t *testing.T, reg *prometheus.Registry, metrics *Metrics) {
	t.Helper()
	// Missing-domain safety bypass fix -- tracked docker resource, no
	// Docker client configured -> "unverified".
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "carol", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.TrackDockerResource(root, env.ID, "carol", TrackedDockerResource{Kind: DockerContainer, Ref: "x"}); err != nil {
		t.Fatal(err)
	}
	cloudFake := NewFakeCloudClient()
	c := Controller{Manager: m, Now: func() time.Time { return env.ExpiresAt.Add(time.Millisecond) }, Sleep: noSleep, MaxCleanupAttempts: 1, Cloud: cloudFake, Metrics: metrics}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CleanupDue(context.Background(), root); err == nil {
		t.Fatal("expected an error: tracked docker resource with no client configured")
	}
	if got := counterVecValue(t, reg, "cleanup_attempts_total", map[string]string{"domain": "docker", "outcome": "unverified"}); got != 1 {
		t.Errorf("expected docker/unverified=1, got %v", got)
	}
}

// TestUnresolvedGauge_AccurateAfterRecoveryAndRestart proves the gauge
// is always a fresh recount from persisted state (Set, never
// incremented), so it can never drift across a crash/restart or a
// manual ResetCloudCleanupAttempts -- requirement: "current unresolved
// count remains accurate after recovery and restart."
func TestUnresolvedGauge_AccurateAfterRecoveryAndRestart(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	// Two environments that will need cleanup, one that is already
	// fully resolved (product, never touched).
	envA, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	envB, err := m.Create(root, "bob", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(root, "carol", "product", 0, nil); err != nil {
		t.Fatal(err)
	}

	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg)
	c := Controller{Manager: m, Now: func() time.Time { return envA.ExpiresAt.Add(time.Millisecond) }, Sleep: noSleep, MaxCleanupAttempts: 1, Metrics: metrics}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}

	// Before any cleanup pass: both playgrounds are "expired" (unresolved).
	if _, err := UpdateUnresolvedGauge(metrics, root); err != nil {
		t.Fatal(err)
	}
	if got := gaugeValue(t, reg, "cleanup_unresolved_environments"); got != 2 {
		t.Fatalf("expected 2 unresolved before cleanup, got %v", got)
	}

	// Resolve both via a real cleanup pass.
	if _, err := c.CleanupDue(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if got := gaugeValue(t, reg, "cleanup_unresolved_environments"); got != 0 {
		t.Fatalf("expected 0 unresolved after cleanup resolved both, got %v", got)
	}

	// "Restart": a brand-new Controller/Metrics/registry recomputing
	// from scratch must see the SAME accurate count, never a stale or
	// incrementally-drifted one.
	freshReg := prometheus.NewRegistry()
	freshMetrics := NewMetrics(freshReg)
	if _, err := UpdateUnresolvedGauge(freshMetrics, root); err != nil {
		t.Fatal(err)
	}
	if got := gaugeValue(t, freshReg, "cleanup_unresolved_environments"); got != 0 {
		t.Fatalf("expected a fresh recount after restart to also be 0, got %v", got)
	}

	_ = envB // referenced only for clarity of the two-environment setup
}

// TestTraceCorrelation_SpansNestAcrossCleanupStages proves requirement
// 6 ("correlate environment ID, cleanup attempt and notification
// result"): CleanupDue's pass span is the parent of cleanupOne's span,
// which is the parent of verify_cloud_domain's span, and all three
// share the same trace ID -- the join key a human would use in Tempo/
// Grafana to see one environment's entire cleanup story.
func TestTraceCorrelation_SpansNestAcrossCleanupStages(t *testing.T) {
	rec := withSpanRecorder(t)

	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-trace-correlation")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, cloudTags(env.ID, "alice"), 0, 0, 0)
	c := Controller{Manager: m, Now: func() time.Time { return env.ExpiresAt.Add(time.Millisecond) }, Sleep: noSleep, MaxCleanupAttempts: 1, Cloud: fake, MaxCloudCleanupAttempts: 3}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CleanupDue(context.Background(), root); err != nil {
		t.Fatal(err)
	}

	spans := rec.Ended()
	byName := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range spans {
		byName[s.Name()] = s
	}
	pass, ok := byName["playground.cleanup_due_pass"]
	if !ok {
		t.Fatal("expected a playground.cleanup_due_pass span")
	}
	one, ok := byName["playground.cleanup_one"]
	if !ok {
		t.Fatal("expected a playground.cleanup_one span")
	}
	cloudDomain, ok := byName["playground.verify_cloud_domain"]
	if !ok {
		t.Fatal("expected a playground.verify_cloud_domain span")
	}

	if one.Parent().SpanID() != pass.SpanContext().SpanID() {
		t.Error("expected cleanup_one's parent to be the cleanup_due_pass span")
	}
	if cloudDomain.Parent().SpanID() != one.SpanContext().SpanID() {
		t.Error("expected verify_cloud_domain's parent to be the cleanup_one span")
	}
	if pass.SpanContext().TraceID() != one.SpanContext().TraceID() || one.SpanContext().TraceID() != cloudDomain.SpanContext().TraceID() {
		t.Error("expected all three spans to share one trace ID")
	}
	assertSpanHasStringAttr(t, one, "environment_id", env.ID)
}

// assertSpanHasStringAttr fails the test unless span carries a string
// attribute key=value.
func assertSpanHasStringAttr(t *testing.T, span sdktrace.ReadOnlySpan, key, value string) {
	t.Helper()
	for _, attr := range span.Attributes() {
		if string(attr.Key) == key && attr.Value.AsString() == value {
			return
		}
	}
	t.Errorf("expected span %q to carry attribute %s=%s, got %+v", span.Name(), key, value, span.Attributes())
}

// TestTraceCorrelation_NotificationSpanSharesEnvironmentID proves
// notification delivery spans also carry environment_id, letting a
// human correlate a cleanup attempt with its eventual notification.
func TestTraceCorrelation_NotificationSpanSharesEnvironmentID(t *testing.T) {
	rec := withSpanRecorder(t)

	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := Controller{Manager: m, Now: func() time.Time { return env.ExpiresAt.Add(time.Millisecond) }, Sleep: noSleep, MaxCleanupAttempts: 1}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CleanupDue(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	notifier := &FakeNotifier{}
	if _, err := c.ReconcileCleanupNotifications(context.Background(), root, notifier, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	for _, s := range rec.Ended() {
		if s.Name() != "playground.send_notification" {
			continue
		}
		for _, attr := range s.Attributes() {
			if string(attr.Key) == "environment_id" && attr.Value.AsString() == env.ID {
				return // found it
			}
		}
		t.Fatalf("send_notification span missing environment_id=%s attribute: %+v", env.ID, s.Attributes())
	}
	t.Fatal("expected a playground.send_notification span")
}

// TestStructuredLogFields_PresentAndCorrelated proves cleanup_event log
// lines carry environment_id, status, domain, verification_mode, and
// (when a span is active) trace_id.
func TestStructuredLogFields_PresentAndCorrelated(t *testing.T) {
	withSpanRecorder(t)
	buf := withJSONLog(t)

	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := Controller{Manager: m, Now: func() time.Time { return env.ExpiresAt.Add(time.Millisecond) }, Sleep: noSleep, MaxCleanupAttempts: 1}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CleanupDue(context.Background(), root); err != nil {
		t.Fatal(err)
	}

	found := false
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("expected every log line to be valid JSON, got %q: %v", line, err)
		}
		if entry["msg"] != "cleanup_event" {
			continue
		}
		found = true
		for _, field := range []string{"environment_id", "status", "domain", "attempt", "verification_mode", "trace_id"} {
			if _, ok := entry[field]; !ok {
				t.Errorf("expected cleanup_event log line to carry field %q, got %v", field, entry)
			}
		}
		if entry["environment_id"] != env.ID {
			t.Errorf("expected environment_id=%s, got %v", env.ID, entry["environment_id"])
		}
	}
	if !found {
		t.Fatal("expected at least one cleanup_event log line")
	}
}

// TestStructuredLogFields_NeverLeakSensitiveData is the permanent
// regression test for requirement 7: no cleanup/notification log line
// may ever contain a claim token, raw tracked-resource JSON content, or
// anything that looks like a credential.
func TestStructuredLogFields_NeverLeakSensitiveData(t *testing.T) {
	buf := withJSONLog(t)

	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-sensitive-check")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, cloudTags(env.ID, "alice"), 0, 0, 0)
	c := Controller{Manager: m, Now: func() time.Time { return env.ExpiresAt.Add(time.Millisecond) }, Sleep: noSleep, MaxCleanupAttempts: 1, Cloud: fake, MaxCloudCleanupAttempts: 3}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CleanupDue(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	notifier := &FakeNotifier{}
	if _, err := c.ReconcileCleanupNotifications(context.Background(), root, notifier, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	for _, forbidden := range []string{"claim_token", "ClaimToken", ref.Account, "\"tags\":"} {
		if strings.Contains(output, forbidden) {
			t.Errorf("SECURITY REGRESSION: log output contains forbidden substring %q\nfull output:\n%s", forbidden, output)
		}
	}
}

// TestTelemetry_MissingCollectorsDoNotInterruptCleanup proves
// requirement 4 end to end: with NO span recorder/exporter and NO
// Metrics configured at all (the global OTel no-op default, exactly
// what every process that never calls telemetry.Setup has), a full
// cleanup+notification cycle still completes and reaches
// verified_clean. This is already implicitly proven by every other
// test in this package (none of them configure telemetry), but this
// test makes the claim explicit and permanent.
func TestTelemetry_MissingCollectorsDoNotInterruptCleanup(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := Controller{Manager: m, Now: func() time.Time { return env.ExpiresAt.Add(time.Millisecond) }, Sleep: noSleep, MaxCleanupAttempts: 1} // Metrics nil, no span recorder installed
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	results, err := c.CleanupDue(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Environment.Status != StatusVerifiedClean {
		t.Fatalf("expected cleanup to succeed normally with zero telemetry configured, got %+v", results)
	}
	notifier := &FakeNotifier{}
	if _, err := c.ReconcileCleanupNotifications(context.Background(), root, notifier, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if notifier.Count() != 1 {
		t.Fatalf("expected the notification to still be delivered normally, got %d", notifier.Count())
	}
}

// TestClaimRecoveryMetric_IncrementsOnlyOnAbandonedClaimReclaim proves
// cleanup_claim_recovery_total fires exactly when an abandoned claim is
// reclaimed, never on an ordinary fresh claim or delivery.
func TestClaimRecoveryMetric_IncrementsOnlyOnAbandonedClaimReclaim(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg)

	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	n := Notification{ID: notificationID(env.ID, NotificationVerifiedClean), EnvironmentID: env.ID, Kind: NotificationVerifiedClean, CreatedAt: now}
	if _, _, claimed, _, err := claimNotification(root, n, now); err != nil || !claimed {
		t.Fatalf("test setup: expected a fresh claim, claimed=%v err=%v", claimed, err)
	}
	if got := counterVecValue(t, reg, "cleanup_claim_recovery_total", nil); got != 0 {
		t.Fatalf("expected zero claim recoveries before any lease expiry, got %v", got)
	}

	report := reportWithOutcome(env.ID, StatusVerifiedClean, SafetyVerifiedClean, now)
	notifier := &FakeNotifier{}
	later := now.Add(notificationClaimLease + time.Minute)
	if _, err := SendCleanupNotification(context.Background(), root, report, notifier, later, metrics); err != nil {
		t.Fatal(err)
	}
	if got := counterVecValue(t, reg, "cleanup_claim_recovery_total", nil); got != 1 {
		t.Fatalf("expected exactly one claim recovery after reclaiming the abandoned claim, got %v", got)
	}

	// A second, ordinary delivery for a DIFFERENT environment must not
	// increment claim recovery at all.
	env2, err := m.Create(root, "bob", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	report2 := reportWithOutcome(env2.ID, StatusVerifiedClean, SafetyVerifiedClean, now)
	if _, err := SendCleanupNotification(context.Background(), root, report2, notifier, now, metrics); err != nil {
		t.Fatal(err)
	}
	if got := counterVecValue(t, reg, "cleanup_claim_recovery_total", nil); got != 1 {
		t.Fatalf("expected claim recovery to stay at 1 after an unrelated ordinary delivery, got %v", got)
	}
}
