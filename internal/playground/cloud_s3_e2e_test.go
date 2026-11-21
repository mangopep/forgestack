package playground

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// This file is the full offline S3Adapter-to-telemetry end-to-end test:
// it is the ONE test in this package that drives the complete cloud
// resource lifecycle (create -> track -> expire -> cleanup -> verify
// deletion -> report -> notify -> metrics/traces/logs) through the REAL
// S3Adapter (cloud_s3_adapter.go), never FakeCloudClient, so a bug that
// exists only in S3Adapter's own translation of S3's API shapes into
// this package's CloudClient contract -- but happens to not exist in
// FakeCloudClient -- cannot hide behind the (much larger) existing
// FakeCloudClient-based test suite. Every call still goes through
// FakeS3API (cloud_s3_fake_test.go): no AWS credentials, no network
// call, no real AWS endpoint of any kind.

// TestS3AdapterEndToEnd_FullLifecycleThroughTelemetry is the positive
// path: a tracked S3 bucket (with object versions and an in-progress
// multipart upload, so emptying actually has work to do) is cleaned up
// through the real S3Adapter, independently verified gone, reported,
// notified, and the whole pass is observed end-to-end via Prometheus
// metrics, an OTel span tree, and a structured log line.
func TestS3AdapterEndToEnd_FullLifecycleThroughTelemetry(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "e2e-bucket"}
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}

	api := NewFakeS3API()
	api.SeedBucket(ref.Ref, ref.Account, ref.Region, cloudTags(env.ID, "alice"))
	api.AddObjectVersion(ref.Ref, "report.txt", "v1", true)
	api.AddObjectVersion(ref.Ref, "report.txt", "v0", false)
	api.AddDeleteMarker(ref.Ref, "deleted.txt", "v1", true)
	api.AddMultipartUpload(ref.Ref, "big-upload.bin", "upload-1")
	adapter := NewS3Adapter(api, ref.Account, ref.Region)

	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg)
	spans := withSpanRecorder(t)
	logs := withJSONLog(t)
	notifier := &FakeNotifier{}

	c := Controller{
		Manager:                 m,
		Now:                     func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:                   noSleep,
		MaxCleanupAttempts:      1,
		Cloud:                   adapter,
		MaxCloudCleanupAttempts: 3,
		Metrics:                 metrics,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	cleanEnv := runAndVerifyFullLifecycleCleanup(t, c, api, adapter, root, env.ID, ref)
	reportAndNotifyFullLifecycle(t, c, root, cleanEnv, notifier, metrics)

	// Metrics: a real cleanup attempt against the cloud domain, through
	// S3Adapter, must be counted exactly like a FakeCloudClient-backed
	// one would be -- the Controller is unaware which implementation is
	// behind c.Cloud.
	assertFullLifecycleMetrics(t, reg, metrics, root)
	assertFullLifecycleSpans(t, spans, env.ID)
	assertFullLifecycleLogs(t, logs, env.ID)
}

// runAndVerifyFullLifecycleCleanup runs ExpireDue/CleanupDue through c
// and independently re-verifies absence via a fresh CloudReconcile call
// (never trusting Remove's own return value alone), returning the
// resulting Environment for the caller's report/notify step. Factored
// out of TestS3AdapterEndToEnd_FullLifecycleThroughTelemetry purely to
// keep that test's own cyclomatic complexity readable.
func runAndVerifyFullLifecycleCleanup(t *testing.T, c Controller, api *FakeS3API, adapter *S3Adapter, root, environmentID string, ref CloudResourceRef) Environment {
	t.Helper()
	results, err := c.CleanupDue(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Environment.Status != StatusVerifiedClean {
		t.Fatalf("expected verified_clean through the real S3Adapter, got %+v", results)
	}

	// Independently verify absence -- not merely trusting Remove's
	// return value: the bucket, every object/delete-marker, and the
	// multipart upload must all actually be gone from FakeS3API's own
	// state, re-checked via a fresh call path (CloudReconcile), not via
	// S3Adapter's internal bookkeeping.
	if api.HasBucket(ref.Ref) {
		t.Fatal("expected the S3 bucket to actually be gone from the backing store")
	}
	rec, err := CloudReconcile(context.Background(), adapter, root, environmentID)
	if err != nil {
		t.Fatalf("independent post-cleanup reconcile failed: %v", err)
	}
	if len(rec.Present) != 0 {
		t.Fatalf("expected independent reconciliation to confirm nothing present, got %+v", rec.Present)
	}
	return results[0].Environment
}

// reportAndNotifyFullLifecycle persists a fresh CleanupReport and sends
// its notification, exactly like ReconcileCleanupNotifications does --
// exercising cleanup_report.go and notification.go through the real
// S3Adapter's VerificationMode, not a hand-built report. Factored out of
// TestS3AdapterEndToEnd_FullLifecycleThroughTelemetry purely to keep
// that test's own cyclomatic complexity readable.
func reportAndNotifyFullLifecycle(t *testing.T, c Controller, root string, env Environment, notifier *FakeNotifier, metrics *Metrics) {
	t.Helper()
	report, err := c.GenerateCleanupReport(context.Background(), root, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.CloudVerification != VerificationSimulated {
		t.Fatalf("expected S3Adapter-backed cleanup to still report VerificationSimulated (FakeS3API, never VerificationLive), got %q", report.CloudVerification)
	}
	if !report.Simulated {
		t.Fatal("expected Simulated=true -- this report must never be presentable as real AWS-verified evidence")
	}
	if report.Safety != SafetyVerifiedClean {
		t.Fatalf("expected SafetyVerifiedClean, got %q", report.Safety)
	}
	if err := PersistCleanupReport(root, report); err != nil {
		t.Fatal(err)
	}
	sent, err := SendCleanupNotification(context.Background(), root, report, notifier, c.Now(), metrics)
	if err != nil || !sent {
		t.Fatalf("expected the verified_clean notification to be sent, sent=%v err=%v", sent, err)
	}
	if notifier.Count() != 1 {
		t.Fatalf("expected exactly one notification delivered, got %d", notifier.Count())
	}
}

// assertFullLifecycleMetrics is
// TestS3AdapterEndToEnd_FullLifecycleThroughTelemetry's metrics
// assertions, factored out purely to keep that test's own cyclomatic
// complexity readable -- behavior is unchanged from having it inline.
func assertFullLifecycleMetrics(t *testing.T, reg *prometheus.Registry, metrics *Metrics, root string) {
	t.Helper()
	if got := counterVecValue(t, reg, "cleanup_attempts_total", map[string]string{"domain": "cloud", "outcome": StatusVerifiedClean}); got != 1 {
		t.Errorf("expected cleanup_attempts_total{domain=cloud,outcome=verified_clean}=1, got %v", got)
	}
	if got := counterVecValue(t, reg, "cleanup_notification_total", map[string]string{"kind": string(NotificationVerifiedClean), "outcome": "delivered"}); got != 1 {
		t.Errorf("expected cleanup_notification_total{kind=verified_clean,outcome=delivered}=1, got %v", got)
	}
	if _, err := UpdateUnresolvedGauge(metrics, root); err != nil {
		t.Fatal(err)
	}
	if got := gaugeValue(t, reg, "cleanup_unresolved_environments"); got != 0 {
		t.Errorf("expected cleanup_unresolved_environments=0 once the only environment is verified_clean, got %v", got)
	}
}

// assertFullLifecycleSpans checks that both the cleanup pass and the
// notification send produced a span carrying environmentID -- proving
// the real S3Adapter path is instrumented identically to the fake-
// client path telemetry_test.go already covers.
func assertFullLifecycleSpans(t *testing.T, spans *tracetest.SpanRecorder, environmentID string) {
	t.Helper()
	var sawCleanupSpan, sawNotificationSpan bool
	for _, sp := range spans.Ended() {
		for _, kv := range sp.Attributes() {
			if string(kv.Key) == "environment_id" && kv.Value.AsString() == environmentID {
				switch sp.Name() {
				case "playground.send_notification":
					sawNotificationSpan = true
				default:
					sawCleanupSpan = true
				}
			}
		}
	}
	if !sawCleanupSpan {
		t.Error("expected at least one cleanup-related span carrying this environment's ID")
	}
	if !sawNotificationSpan {
		t.Error("expected a playground.send_notification span carrying this environment's ID")
	}
}

// assertFullLifecycleLogs checks that at least one cleanup_event line
// and one cleanup_notification_event line, both correlated to
// environmentID, were emitted -- never silently skipped just because
// the real S3Adapter (rather than FakeCloudClient) was behind the call.
func assertFullLifecycleLogs(t *testing.T, logs *bytes.Buffer, environmentID string) {
	t.Helper()
	logged := logs.String()
	if !strings.Contains(logged, `"cleanup_event"`) || !strings.Contains(logged, environmentID) {
		t.Errorf("expected a cleanup_event log line correlated to %s, got: %s", environmentID, logged)
	}
	if !strings.Contains(logged, `"cleanup_notification_event"`) {
		t.Errorf("expected a cleanup_notification_event log line, got: %s", logged)
	}
}

// TestS3AdapterEndToEnd_NegativeCases exercises every failure mode the
// task's investigation named, through the real S3Adapter, and proves
// the single invariant that matters for all of them: NONE can ever
// produce StatusVerifiedClean, a SafetyVerifiedClean report, or a
// verified_clean notification while the real bucket (per FakeS3API's
// own state) is still present or was never independently confirmed
// absent.
func TestS3AdapterEndToEnd_NegativeCases(t *testing.T) {
	type setup struct {
		name       string
		build      func(t *testing.T, root string, env Environment, ref CloudResourceRef) Controller
		wantStatus string // "" means "never reaches a terminal status at all" is not checked further than below
	}

	cases := []setup{
		{
			name: "ownership_mismatch_resource_never_removed",
			build: func(t *testing.T, root string, env Environment, ref CloudResourceRef) Controller {
				api := NewFakeS3API()
				// Tagged with THIS environment's ID (so ListByTag/
				// CloudReconcile still surfaces it as Present) but a
				// DIFFERENT owner tag -- VerifyOwnership's exact-match
				// re-check (RemoveTrackedCloudResources) must refuse to
				// remove it on the strength of a stale/partial tag match.
				api.SeedBucket(ref.Ref, ref.Account, ref.Region, cloudTags(env.ID, "mallory"))
				adapter := NewS3Adapter(api, ref.Account, ref.Region)
				return Controller{Cloud: adapter, MaxCloudCleanupAttempts: 3}
			},
			wantStatus: StatusCleanupUnverified,
		},
		{
			name: "missing_or_invalid_aws_identity_access_denied",
			build: func(t *testing.T, root string, env Environment, ref CloudResourceRef) Controller {
				api := NewFakeS3API()
				// Bucket really belongs to a DIFFERENT account than the
				// adapter is configured with -- ExpectedBucketOwner
				// causes every call to fail AccessDenied, simulating an
				// adapter running with the wrong/invalid AWS identity.
				api.SeedBucket(ref.Ref, "999999999999", ref.Region, cloudTags(env.ID, "alice"))
				adapter := NewS3Adapter(api, ref.Account, ref.Region)
				return Controller{Cloud: adapter, MaxCloudCleanupAttempts: 3}
			},
			wantStatus: StatusCleanupUnverified,
		},
		{
			name: "partial_object_deletion_never_reports_success",
			build: func(t *testing.T, root string, env Environment, ref CloudResourceRef) Controller {
				api := NewFakeS3API()
				api.SeedBucket(ref.Ref, ref.Account, ref.Region, cloudTags(env.ID, "alice"))
				api.AddObjectVersion(ref.Ref, "stuck.txt", "v1", true)
				api.DeleteObjectsPartialFailures = map[string]bool{"stuck.txt/v1": true}
				adapter := NewS3Adapter(api, ref.Account, ref.Region)
				return Controller{Cloud: adapter, MaxCloudCleanupAttempts: 3}
			},
			wantStatus: StatusCleanupUnverified,
		},
		{
			name: "credential_expiry_on_verification_never_reports_success",
			build: func(t *testing.T, root string, env Environment, ref CloudResourceRef) Controller {
				api := NewFakeS3API()
				api.SeedBucket(ref.Ref, ref.Account, ref.Region, cloudTags(env.ID, "alice"))
				api.GetBucketTaggingErr = map[string]error{
					ref.Ref: apiErr("ExpiredToken", "simulated expired credentials"),
				}
				adapter := NewS3Adapter(api, ref.Account, ref.Region)
				return Controller{Cloud: adapter, MaxCloudCleanupAttempts: 3}
			},
			wantStatus: StatusCleanupUnverified,
		},
		{
			name: "verification_unavailable_on_listing_never_reports_success",
			build: func(t *testing.T, root string, env Environment, ref CloudResourceRef) Controller {
				api := NewFakeS3API()
				api.SeedBucket(ref.Ref, ref.Account, ref.Region, cloudTags(env.ID, "alice"))
				api.ListObjectVersionsErr = apiErr("InternalError", "simulated unavailable S3 backend")
				adapter := NewS3Adapter(api, ref.Account, ref.Region)
				return Controller{Cloud: adapter, MaxCloudCleanupAttempts: 3}
			},
			wantStatus: StatusCleanupUnverified,
		},
		{
			name: "missing_client_never_reports_success",
			build: func(t *testing.T, root string, env Environment, ref CloudResourceRef) Controller {
				// No Cloud client configured at all -- the tracked
				// resource must remain unverifiable, never silently
				// treated as absent. Lands on StatusCleanupPending
				// (checkUnconfiguredDomain, lifecycle.go) rather than
				// StatusCleanupUnverified -- a distinct status from an
				// ordinary failed removal attempt, specifically meaning
				// "needs a Controller with this client configured," but
				// equally never verified_clean.
				return Controller{MaxCloudCleanupAttempts: 3}
			},
			wantStatus: StatusCleanupPending,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			m := NewManager()
			env, err := m.Create(root, "alice", "playground", time.Second, nil)
			if err != nil {
				t.Fatal(err)
			}
			ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "negative-" + tc.name}
			if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
				t.Fatal(err)
			}

			c := tc.build(t, root, env, ref)
			c.Manager = m
			c.Now = func() time.Time { return env.ExpiresAt.Add(time.Millisecond) }
			c.Sleep = noSleep
			c.MaxCleanupAttempts = 1

			if _, err := c.ExpireDue(root); err != nil {
				t.Fatal(err)
			}
			results, cleanupErr := c.CleanupDue(context.Background(), root)
			if cleanupErr == nil {
				t.Fatal("expected this failure mode to produce a non-nil CleanupDue error")
			}
			if len(results) != 1 || results[0].Environment.Status != tc.wantStatus {
				t.Fatalf("expected status %q, got %+v", tc.wantStatus, results)
			}

			report, err := c.GenerateCleanupReport(context.Background(), root, results[0].Environment, cleanupErr)
			if err != nil {
				t.Fatal(err)
			}
			if report.Safety == SafetyVerifiedClean {
				t.Fatalf("CRITICAL: %s produced a SafetyVerifiedClean report despite the resource never being independently confirmed absent: %+v", tc.name, report)
			}

			notifier := &FakeNotifier{}
			sent, err := SendCleanupNotification(context.Background(), root, report, notifier, c.Now(), nil)
			_ = err // a cleanup_incomplete notification may legitimately succeed; what matters is its Kind/Outcome below
			if sent {
				for _, n := range notifier.Received() {
					if n.Kind == NotificationVerifiedClean {
						t.Fatalf("CRITICAL: %s delivered a false verified_clean notification", tc.name)
					}
				}
			}
		})
	}
}

// TestS3AdapterEndToEnd_CrashAndRestartRecoversThroughRealAdapter proves
// the existing restart-recovery guarantee
// (TestCloudCleanup_AttemptStateSurvivesSimulatedRestart) holds when the
// CloudClient behind it is the real S3Adapter, not FakeCloudClient: a
// brand-new Controller/Manager, sharing no memory with the first,
// resumes the attempt count purely from persisted state and still
// reaches the correct terminal outcome.
func TestS3AdapterEndToEnd_CrashAndRestartRecoversThroughRealAdapter(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "crash-restart-bucket"}
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	api := NewFakeS3API()
	api.SeedBucket(ref.Ref, ref.Account, ref.Region, cloudTags(env.ID, "alice"))
	api.DeleteBucketFailuresRemaining = 1 << 30 // throttled forever until "fixed" below
	adapter := NewS3Adapter(api, ref.Account, ref.Region)

	now := env.ExpiresAt.Add(time.Millisecond)
	c := Controller{
		Manager:                 m,
		Now:                     func() time.Time { return now },
		Sleep:                   noSleep,
		MaxCleanupAttempts:      1,
		Cloud:                   adapter,
		MaxCloudCleanupAttempts: 3,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CleanupDue(context.Background(), root); err == nil {
		t.Fatal("expected the first (throttled) pass to fail")
	}

	// "Crash": a fresh Controller/Manager/adapter -- no shared Go-level
	// memory with the process above -- but wrapping the SAME api
	// (fakeS3Bucket's state, including DeleteBucketFailuresRemaining),
	// exactly like a real restart: THIS process's in-memory state is
	// gone, but AWS itself (and whatever is throttling it) has no idea
	// this process restarted. Only cloud-cleanup-state.json on disk
	// carries the attempt count across the "restart."
	freshAdapter := NewS3Adapter(api, ref.Account, ref.Region)
	fresh := Controller{
		Manager:                 NewManager(),
		Now:                     func() time.Time { return now },
		Sleep:                   noSleep,
		MaxCleanupAttempts:      1,
		Cloud:                   freshAdapter,
		MaxCloudCleanupAttempts: 3,
	}
	if _, err := fresh.CleanupDue(context.Background(), root); err == nil {
		t.Fatal("expected the second pass (attempt 2 of 3, picked up from persisted state) to still fail")
	}

	// Throttling "clears" (simulating the real-world fix) -- the third
	// attempt, from yet another fresh process, must succeed.
	api.DeleteBucketFailuresRemaining = 0
	secondFresh := Controller{
		Manager:                 NewManager(),
		Now:                     func() time.Time { return now },
		Sleep:                   noSleep,
		MaxCleanupAttempts:      1,
		Cloud:                   freshAdapter,
		MaxCloudCleanupAttempts: 3,
	}
	results, err := secondFresh.CleanupDue(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Environment.Status != StatusVerifiedClean {
		t.Fatalf("expected the third, post-recovery attempt to succeed, got %+v", results)
	}
	if api.HasBucket(ref.Ref) {
		t.Fatal("expected the bucket to actually be gone after the recovered attempt")
	}
}
