package playground

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Permanent regression tests for the HIGH audit finding: cloud cleanup
// previously retried a permanently-failing resource forever, on every
// CleanupDue/worker pass, with no bound and no human-visible escalation.

func TestCloudCleanup_EscalatesToAttentionRequiredAfterExhaustingAttempts(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-permanent-failure")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, cloudTags(env.ID, "alice"), 0, 0, 0)
	fake.RemoveErr = errCloudPermanentlyDenied // never clears -- every attempt fails

	c := Controller{
		Manager:                 m,
		Now:                     func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:                   noSleep,
		MaxCleanupAttempts:      1,
		Cloud:                   fake,
		MaxCloudCleanupAttempts: 3,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= 2; i++ {
		results, err := c.CleanupDue(context.Background(), root)
		if err == nil {
			t.Fatalf("pass %d: expected an error while attempts remain", i)
		}
		if len(results) != 1 || results[0].Environment.Status != StatusCleanupUnverified {
			t.Fatalf("pass %d: expected cleanup_unverified while under the attempt bound, got %+v", i, results)
		}
	}

	// Third pass pushes Attempts to 3 == MaxCloudCleanupAttempts -> escalate.
	results, err := c.CleanupDue(context.Background(), root)
	if err == nil {
		t.Fatal("expected an error on the escalating pass")
	}
	if len(results) != 1 || results[0].Environment.Status != StatusCleanupAttentionRequired {
		t.Fatalf("expected escalation to cleanup_attention_required after exhausting attempts, got %+v", results)
	}
	if !fake.Has("forgestack-permanent-failure") {
		t.Error("the resource must remain tracked and present -- never silently discarded")
	}

	// A further pass must not even be attempted: needsCleanup excludes
	// the escalated status, so CleanupDue should skip it entirely --
	// confirmed by the API call count not increasing.
	before := fake.CallCount("Remove")
	results, err = c.CleanupDue(context.Background(), root)
	if err != nil {
		t.Fatalf("expected no error -- the escalated environment should simply be skipped, got %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected zero environments touched once escalated, got %+v", results)
	}
	if after := fake.CallCount("Remove"); after != before {
		t.Errorf("expected no additional Remove call after escalation (avoid unnecessary repeated API calls), before=%d after=%d", before, after)
	}
}

var errCloudPermanentlyDenied = cloudPermanentlyDeniedError{}

type cloudPermanentlyDeniedError struct{}

func (cloudPermanentlyDeniedError) Error() string { return "simulated permanent permission denial" }

func TestCloudCleanup_BackoffPreventsAnUnnecessaryImmediateRetryAPICall(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-backoff")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, cloudTags(env.ID, "alice"), 0, 0, 0)
	fake.RemoveErr = errCloudPermanentlyDenied

	clock := env.ExpiresAt.Add(time.Millisecond)
	c := Controller{
		Manager:                 m,
		Now:                     func() time.Time { return clock },
		Sleep:                   noSleep,
		MaxCleanupAttempts:      1,
		Cloud:                   fake,
		MaxCloudCleanupAttempts: 10,
		CloudRetryBackoff:       time.Minute,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}

	if _, err := c.CleanupDue(context.Background(), root); err == nil {
		t.Fatal("expected the first pass to fail and consume one attempt")
	}
	firstAttemptCalls := fake.CallCount("Remove")
	if firstAttemptCalls != 1 {
		t.Fatalf("expected exactly one Remove call after the first pass, got %d", firstAttemptCalls)
	}

	// A second pass immediately afterward (clock unchanged) must be
	// gated by the backoff -- no additional Remove call.
	if _, err := c.CleanupDue(context.Background(), root); err == nil {
		t.Fatal("expected the backoff-gated pass to still report failure (resources remain unverified)")
	}
	if got := fake.CallCount("Remove"); got != firstAttemptCalls {
		t.Errorf("expected the backoff to prevent an unnecessary immediate retry API call, Remove calls: before=%d after=%d", firstAttemptCalls, got)
	}

	// Advance the clock past the backoff window -- the next pass must
	// attempt again.
	clock = clock.Add(2 * time.Minute)
	if _, err := c.CleanupDue(context.Background(), root); err == nil {
		t.Fatal("expected the post-backoff pass to still fail (RemoveErr is permanent) and consume a second attempt")
	}
	if got := fake.CallCount("Remove"); got != firstAttemptCalls+1 {
		t.Errorf("expected exactly one more Remove call once the backoff window passed, got %d (was %d)", got, firstAttemptCalls)
	}
}

func TestCloudCleanup_AttemptStateSurvivesSimulatedRestart(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-restart-attempts")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, cloudTags(env.ID, "alice"), 0, 0, 0)
	fake.RemoveErr = errCloudPermanentlyDenied

	now := env.ExpiresAt.Add(time.Millisecond)
	c := Controller{
		Manager:                 m,
		Now:                     func() time.Time { return now },
		Sleep:                   noSleep,
		MaxCleanupAttempts:      1,
		Cloud:                   fake,
		MaxCloudCleanupAttempts: 3,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CleanupDue(context.Background(), root); err == nil {
		t.Fatal("expected the first pass to fail")
	}

	// "Restart": a fresh Controller/Manager, no shared memory, reads
	// only persisted state -- must pick up the attempt count at 1, not
	// reset to 0.
	fresh := Controller{
		Manager:                 NewManager(),
		Now:                     func() time.Time { return now },
		Sleep:                   noSleep,
		MaxCleanupAttempts:      1,
		Cloud:                   fake,
		MaxCloudCleanupAttempts: 3,
	}
	if _, err := fresh.CleanupDue(context.Background(), root); err == nil {
		t.Fatal("expected the second pass (attempt 2 of 3) to still fail")
	}
	if _, err := fresh.CleanupDue(context.Background(), root); err == nil {
		t.Fatal("expected the third pass (attempt 3 of 3) to escalate, reporting a non-nil error")
	}
	envs, err := ListEnvironments(root)
	if err != nil {
		t.Fatal(err)
	}
	if envs[0].Status != StatusCleanupAttentionRequired {
		t.Fatalf("expected escalation after exactly 3 attempts tracked correctly across simulated restarts, got %q", envs[0].Status)
	}
}

func TestResetCloudCleanupAttempts_AllowsManualRecoveryAfterEscalation(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-manual-recovery")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
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
	if _, err := c.CleanupDue(context.Background(), root); err == nil {
		t.Fatal("expected the first and only permitted attempt to fail and escalate")
	}
	envs, err := ListEnvironments(root)
	if err != nil {
		t.Fatal(err)
	}
	if envs[0].Status != StatusCleanupAttentionRequired {
		t.Fatalf("expected escalation, got %q", envs[0].Status)
	}

	// Human fixes the underlying problem (simulated: clear RemoveErr)
	// and explicitly invokes the separate recovery path.
	fake.RemoveErr = nil
	if err := ResetCloudCleanupAttempts(root, env.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	envs, err = ListEnvironments(root)
	if err != nil {
		t.Fatal(err)
	}
	if envs[0].Status != StatusCleanupUnverified {
		t.Fatalf("expected ResetCloudCleanupAttempts to move the status back to cleanup_unverified (re-entering the automatic retry set), got %q", envs[0].Status)
	}

	results, err := c.CleanupDue(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Environment.Status != StatusVerifiedClean {
		t.Fatalf("expected the next automatic pass to succeed with a fresh attempt budget, got %+v", results)
	}
	if fake.Has("forgestack-manual-recovery") {
		t.Error("the bucket should have been removed")
	}
}

func TestResetCloudCleanupAttempts_RejectsWrongOwner(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := ResetCloudCleanupAttempts(root, env.ID, "mallory"); err == nil {
		t.Fatal("expected ResetCloudCleanupAttempts to reject a non-owning caller")
	}
}

// TestCloudCleanup_SuccessClearsAttemptHistory proves a resource that
// eventually succeeds (after some earlier transient failures, none of
// which exhausted the bound) leaves no stale attempt-count residue
// behind for a human to be confused by later.
func TestCloudCleanup_SuccessClearsAttemptHistory(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-clears-history")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, cloudTags(env.ID, "alice"), 0, 0, 0)
	fake.RemoveFailuresRemaining = 2

	c := Controller{
		Manager:                 m,
		Now:                     func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:                   noSleep,
		MaxCleanupAttempts:      1,
		Cloud:                   fake,
		MaxCloudCleanupAttempts: 5,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := c.CleanupDue(context.Background(), root); err == nil {
			t.Fatalf("pass %d: expected a transient failure", i)
		}
	}
	results, err := c.CleanupDue(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Environment.Status != StatusVerifiedClean {
		t.Fatalf("expected eventual success, got %+v", results)
	}

	dir := must(environmentDir(root, env.ID))
	state, err := loadCloudCleanupAttemptState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if state.Attempts != 0 {
		t.Errorf("expected attempt history to be cleared on success, got %+v", state)
	}
}

// Permanent regression tests for the repeat-escalation notification bug:
// notificationID used to be keyed only by (environmentID, kind), with no
// generation component, so a SECOND escalation to
// StatusCleanupAttentionRequired after a ResetCloudCleanupAttempts reset
// reused the FIRST escalation's already-"delivered" notification ID and
// was silently suppressed. The fix threads a persisted, durable episode
// counter (cloudCleanupAttemptState.Episode) into the key, bumped only
// by ResetCloudCleanupAttempts.

// TestResetCloudCleanupAttempts_SecondEscalationGetsItsOwnNotification is
// the core repeat-escalation regression test: escalate, notify, reset,
// escalate again, notify again -- the second notification must actually
// be delivered, as a DISTINCT record, with the first escalation's
// delivered record untouched (preserved audit history).
func TestResetCloudCleanupAttempts_SecondEscalationGetsItsOwnNotification(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-repeat-escalation")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, cloudTags(env.ID, "alice"), 0, 0, 0)
	fake.RemoveErr = errCloudPermanentlyDenied

	now := env.ExpiresAt.Add(time.Millisecond)
	c := Controller{
		Manager:                 m,
		Now:                     func() time.Time { return now },
		Sleep:                   noSleep,
		MaxCleanupAttempts:      1,
		Cloud:                   fake,
		MaxCloudCleanupAttempts: 1,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CleanupDue(context.Background(), root); err == nil {
		t.Fatal("expected the first (and only permitted) attempt to fail and escalate")
	}
	envs, err := ListEnvironments(root)
	if err != nil {
		t.Fatal(err)
	}
	if envs[0].Status != StatusCleanupAttentionRequired {
		t.Fatalf("expected first escalation, got %q", envs[0].Status)
	}

	notifier := &FakeNotifier{}
	notifyEscalationAndRequireDelivered(t, c, root, envs[0], notifier, now, 0, 1)

	// Human resets -- this must bump the episode, not just clear attempts.
	if err := ResetCloudCleanupAttempts(root, env.ID, "alice"); err != nil {
		t.Fatal(err)
	}

	// Escalate a SECOND time (fake.RemoveErr is still permanent, and the
	// budget is 1 again after reset).
	if _, err := c.CleanupDue(context.Background(), root); err == nil {
		t.Fatal("expected the second escalation attempt to fail and escalate again")
	}
	envs, err = ListEnvironments(root)
	if err != nil {
		t.Fatal(err)
	}
	if envs[0].Status != StatusCleanupAttentionRequired {
		t.Fatalf("expected second escalation, got %q", envs[0].Status)
	}
	notifyEscalationAndRequireDelivered(t, c, root, envs[0], notifier, now, 1, 2)

	assertPreservedDeliveredAuditRecords(t, root, env.ID, 0, 1)
}

// notifyEscalationAndRequireDelivered generates env's current cleanup
// report, asserts it carries wantEpisode, sends its notification, and
// asserts notifier's cumulative delivery count is wantTotalDeliveries --
// the repeated core of the repeat-escalation regression test, factored
// out so it can be called once per escalation without duplicating all
// of its assertions inline.
func notifyEscalationAndRequireDelivered(t *testing.T, c Controller, root string, env Environment, notifier *FakeNotifier, now time.Time, wantEpisode, wantTotalDeliveries int) {
	t.Helper()
	report, err := c.GenerateCleanupReport(context.Background(), root, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.CloudEscalationEpisode != wantEpisode {
		t.Fatalf("expected episode %d, got %d", wantEpisode, report.CloudEscalationEpisode)
	}
	sent, err := SendCleanupNotification(context.Background(), root, report, notifier, now, nil)
	if err != nil || !sent {
		t.Fatalf("expected escalation episode %d's notification to be sent, sent=%v err=%v", wantEpisode, sent, err)
	}
	if notifier.Count() != wantTotalDeliveries {
		t.Fatalf("expected %d cumulative deliveries after episode %d, got %d", wantTotalDeliveries, wantEpisode, notifier.Count())
	}
}

// assertPreservedDeliveredAuditRecords checks that every episode in
// episodes has its own durable, delivered NotificationRecord for id --
// proving an earlier episode's record is never overwritten or lost once
// a later one is created.
func assertPreservedDeliveredAuditRecords(t *testing.T, root, id string, episodes ...int) {
	t.Helper()
	records, err := LoadNotificationRecords(root, id)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := map[string]bool{}
	for _, ep := range episodes {
		wantIDs[notificationID(id, NotificationAttentionRequired, ep)] = false
	}
	for _, rec := range records {
		if _, ok := wantIDs[rec.ID]; ok {
			if rec.Status != notificationStatusDelivered {
				t.Errorf("expected record %s to be delivered, got status %q", rec.ID, rec.Status)
			}
			wantIDs[rec.ID] = true
		}
	}
	for id, found := range wantIDs {
		if !found {
			t.Errorf("expected a preserved, delivered audit record for %s", id)
		}
	}
}

// TestReconcileCleanupNotifications_RecoversSecondEscalationAfterRestart
// is the restart-recovery regression test: a completely fresh Controller
// (simulating a new process with no in-memory state) must independently
// derive the CURRENT escalation episode from persisted state and still
// deliver the second escalation's notification via
// ReconcileCleanupNotifications -- the same crash-recovery path Worker
// uses, exercising hasDeliveredNotification's episode-aware pre-check.
func TestReconcileCleanupNotifications_RecoversSecondEscalationAfterRestart(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-restart-escalation")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, cloudTags(env.ID, "alice"), 0, 0, 0)
	fake.RemoveErr = errCloudPermanentlyDenied

	now := env.ExpiresAt.Add(time.Millisecond)
	c := Controller{
		Manager:                 m,
		Now:                     func() time.Time { return now },
		Sleep:                   noSleep,
		MaxCleanupAttempts:      1,
		Cloud:                   fake,
		MaxCloudCleanupAttempts: 1,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CleanupDue(context.Background(), root); err == nil {
		t.Fatal("expected the first attempt to fail and escalate")
	}

	notifier := &FakeNotifier{}
	if _, err := c.ReconcileCleanupNotifications(context.Background(), root, notifier, now); err != nil {
		t.Fatal(err)
	}
	if notifier.Count() != 1 {
		t.Fatalf("expected exactly one delivery after the first escalation, got %d", notifier.Count())
	}

	if err := ResetCloudCleanupAttempts(root, env.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CleanupDue(context.Background(), root); err == nil {
		t.Fatal("expected the second attempt to fail and escalate again")
	}

	// Simulate a restart: a brand-new Controller/Manager/Notifier, no
	// shared memory with anything above -- only persisted state on disk.
	freshNotifier := &FakeNotifier{}
	fresh := Controller{Manager: NewManager()}
	if _, err := fresh.ReconcileCleanupNotifications(context.Background(), root, freshNotifier, now); err != nil {
		t.Fatal(err)
	}
	if freshNotifier.Count() != 1 {
		t.Fatalf("expected the fresh process to independently deliver the second escalation's notification exactly once, got %d", freshNotifier.Count())
	}
	got := freshNotifier.Received()[0]
	if got.ID != notificationID(env.ID, NotificationAttentionRequired, 1) {
		t.Errorf("expected the recovered notification to carry the second escalation's episode-1 ID, got %q", got.ID)
	}

	// Calling it again must not re-deliver anything -- idempotent.
	if _, err := fresh.ReconcileCleanupNotifications(context.Background(), root, freshNotifier, now); err != nil {
		t.Fatal(err)
	}
	if freshNotifier.Count() != 1 {
		t.Fatalf("expected no additional delivery on a repeated reconcile call, got %d", freshNotifier.Count())
	}
}

// TestSendCleanupNotification_AttentionRequiredWithinSameEpisodeIsNeverResent
// is the duplicate-suppression regression test: multiple calls for the
// SAME escalation episode (no reset in between) must never deliver more
// than once, exactly like every other notification kind already
// guarantees -- the episode-aware key must not accidentally weaken
// within-episode idempotency.
func TestSendCleanupNotification_AttentionRequiredWithinSameEpisodeIsNeverResent(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	report := reportWithOutcome(env.ID, StatusCleanupAttentionRequired, SafetyActionRequired, now)
	report.CloudEscalationEpisode = 3 // arbitrary non-zero episode
	notifier := &FakeNotifier{}

	for i := 0; i < 5; i++ {
		sent, err := SendCleanupNotification(context.Background(), root, report, notifier, now.Add(time.Duration(i)*time.Minute), nil)
		if err != nil || !sent {
			t.Fatalf("call %d: expected sent=true err=nil, got sent=%v err=%v", i, sent, err)
		}
	}
	if notifier.Count() != 1 {
		t.Fatalf("expected exactly one real delivery across five repeated calls for the same episode, got %d", notifier.Count())
	}
}

// TestSendCleanupNotification_ConcurrentAttentionRequiredCallsNeverDoubleDeliver
// is the concurrent-delivery regression test for the episode-aware key:
// many goroutines racing to send the SAME escalation's notification must
// still result in exactly one real delivery, exercising the existing
// claim/lease machinery (notification.go) with a non-zero,
// attention_required-specific ID.
func TestSendCleanupNotification_ConcurrentAttentionRequiredCallsNeverDoubleDeliver(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	report := reportWithOutcome(env.ID, StatusCleanupAttentionRequired, SafetyActionRequired, now)
	report.CloudEscalationEpisode = 2
	notifier := &FakeNotifier{}

	var wg sync.WaitGroup
	results := make([]bool, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sent, err := SendCleanupNotification(context.Background(), root, report, notifier, now, nil)
			if err != nil {
				t.Errorf("goroutine %d: unexpected error: %v", i, err)
				return
			}
			results[i] = sent
		}(g)
	}
	wg.Wait()

	if notifier.Count() != 1 {
		t.Fatalf("expected exactly one real delivery across 8 concurrent callers, got %d", notifier.Count())
	}
	records, err := LoadNotificationRecords(root, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	idx := findRecord(records, notificationID(env.ID, NotificationAttentionRequired, 2))
	if idx < 0 || records[idx].Status != notificationStatusDelivered {
		t.Fatalf("expected a single delivered record for episode 2, got %+v", records)
	}
}
