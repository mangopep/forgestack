package playground

import (
	"context"
	"testing"
	"time"
)

// TestReconcileCleanupNotifications_RecoversAfterSimulatedCrash is the
// permanent regression test for the second independent audit's P1
// finding: a notification for StatusCleanupAttentionRequired must not
// be permanently lost just because the process that produced that
// status crashed before ever attempting to notify about it.
//
// This directly reproduces the audit's confirmed repro: drive an
// environment to cleanup_attention_required via CleanupDue, then
// simulate a crash by doing NOTHING further (no notification attempt
// at all -- as if the process died right after CleanupDue returned).
// Before this fix, no mechanism in this package could ever notice this
// environment again, because needsCleanup permanently excludes
// cleanup_attention_required from CleanupDue's own results. After this
// fix, a fresh Controller/process calling ReconcileCleanupNotifications
// -- which scans ListEnvironments directly, never CleanupDue's results
// -- finds it and delivers the notification.
func setupEscalatedEnvironmentForCrashTest(t *testing.T) (root string, env Environment, fake *FakeCloudClient) {
	t.Helper()
	root = t.TempDir()
	m := NewManager()
	var err error
	env, err = m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "forgestack-escalates"}
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake = NewFakeCloudClient()
	fake.SeedBucket(ref, cloudTags(env.ID, "alice"), 0, 0, 0)
	fake.RemoveErr = errCloudPermanentlyDenied

	c := Controller{
		Manager:                 m,
		Now:                     func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:                   noSleep,
		MaxCleanupAttempts:      1,
		Cloud:                   fake,
		MaxCloudCleanupAttempts: 1,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	results, err := c.CleanupDue(context.Background(), root)
	if err == nil {
		t.Fatal("expected this pass to escalate with a non-nil error")
	}
	if len(results) != 1 || results[0].Environment.Status != StatusCleanupAttentionRequired {
		t.Fatalf("expected escalation this pass, got %+v", results)
	}
	return root, env, fake
}

func TestReconcileCleanupNotifications_RecoversAfterSimulatedCrash(t *testing.T) {
	root, env, fake := setupEscalatedEnvironmentForCrashTest(t)

	// SIMULATE A CRASH: nothing further happens. No report generated,
	// no notification attempted -- exactly as if the process died right
	// here, before ever reaching a notification step.
	recsBefore, _ := LoadNotificationRecords(root, env.ID)
	if len(recsBefore) != 0 {
		t.Fatalf("test setup error: expected no notification record yet, got %+v", recsBefore)
	}

	// "Restart": a brand-new Controller/process, no shared memory,
	// calls ReconcileCleanupNotifications directly -- NOT CleanupDue,
	// proving recovery does not depend on CleanupDue's own transient
	// result list at all.
	fresh := Controller{
		Manager:                 NewManager(),
		Now:                     func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:                   noSleep,
		MaxCleanupAttempts:      1,
		Cloud:                   fake,
		MaxCloudCleanupAttempts: 1,
	}
	notifier := &FakeNotifier{}
	reports, err := fresh.ReconcileCleanupNotifications(context.Background(), root, notifier, env.ExpiresAt.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || reports[0].EnvironmentID != env.ID {
		t.Fatalf("expected ReconcileCleanupNotifications to find and report on the escalated environment, got %+v", reports)
	}
	assertExactlyOneAttentionRequiredDelivered(t, root, env.ID, notifier)
}

func assertExactlyOneAttentionRequiredDelivered(t *testing.T, root, envID string, notifier *FakeNotifier) {
	t.Helper()
	if got := notifier.Count(); got != 1 {
		t.Fatalf("expected exactly one notification delivered for the previously-unnotified escalation, got %d", got)
	}
	if notifier.Received()[0].Kind != NotificationAttentionRequired {
		t.Fatalf("expected an attention_required notification, got %q", notifier.Received()[0].Kind)
	}
	recsAfter, err := LoadNotificationRecords(root, envID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recsAfter) != 1 || recsAfter[0].Status != notificationStatusDelivered {
		t.Fatalf("expected a durable delivered record, got %+v", recsAfter)
	}
}

// TestReconcileCleanupNotifications_RecoversVerifiedCleanToo is the
// same crash-recovery proof for StatusVerifiedClean -- the other
// status needsCleanup permanently excludes from CleanupDue's results.
func TestReconcileCleanupNotifications_RecoversVerifiedCleanToo(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := Controller{
		Manager:            m,
		Now:                func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:              noSleep,
		MaxCleanupAttempts: 1,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	results, err := c.CleanupDue(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Environment.Status != StatusVerifiedClean {
		t.Fatalf("expected verified_clean this pass, got %+v", results)
	}

	// Crash simulated: no notification attempted.
	notifier := &FakeNotifier{}
	fresh := Controller{Manager: NewManager(), Now: func() time.Time { return env.ExpiresAt.Add(time.Millisecond) }}
	reports, err := fresh.ReconcileCleanupNotifications(context.Background(), root, notifier, env.ExpiresAt.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || reports[0].Safety != SafetyVerifiedClean {
		t.Fatalf("expected a recovered verified_clean report, got %+v", reports)
	}
	if got := notifier.Count(); got != 1 {
		t.Fatalf("expected exactly one notification delivered, got %d", got)
	}
}

// TestReconcileCleanupNotifications_SkipsAlreadyDeliveredAndNonTerminal
// proves ReconcileCleanupNotifications is safe to call repeatedly
// (every Worker pass, in practice): it never re-notifies an already-
// delivered event, and never notifies an environment still mid-flight
// (e.g. active, cleanup_pending).
func TestReconcileCleanupNotifications_SkipsAlreadyDeliveredAndNonTerminal(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	done, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Create(root, "bob", "playground", time.Hour, nil) // still active, not due
	if err != nil {
		t.Fatal(err)
	}

	c := Controller{
		Manager:            m,
		Now:                func() time.Time { return done.ExpiresAt.Add(time.Millisecond) },
		Sleep:              noSleep,
		MaxCleanupAttempts: 1,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CleanupDue(context.Background(), root); err != nil {
		t.Fatal(err)
	}

	notifier := &FakeNotifier{}
	now := done.ExpiresAt.Add(time.Millisecond)
	if _, err := c.ReconcileCleanupNotifications(context.Background(), root, notifier, now); err != nil {
		t.Fatal(err)
	}
	if got := notifier.Count(); got != 1 {
		t.Fatalf("expected exactly one notification (the done environment only), got %d", got)
	}

	// A second pass (simulating the next Worker tick) must not re-notify.
	if _, err := c.ReconcileCleanupNotifications(context.Background(), root, notifier, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := notifier.Count(); got != 1 {
		t.Fatalf("expected still exactly one notification after a second reconcile pass, got %d", got)
	}
}
