package playground

import (
	"context"
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
