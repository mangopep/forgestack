package playground

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Permanent regression test for the MEDIUM audit finding: a concurrent
// cleanup race (one caller's remove attempt fails while another's
// succeeds) could leave a STALE cleanup_unverified on disk for a
// resource that was, in fact, already gone -- because the final
// downgrade persist ran outside the per-environment lock. The fix moves
// the entire additive-domain decide-and-persist cycle inside one lock
// acquisition (verifyAdditionalDomains, controller.go). This test
// reproduces the exact deterministic interleaving the audit used and
// confirms the stale write no longer occurs.
func TestP2_ConcurrentCleanupNeverLeavesAStaleUnverifiedForAnAlreadyRemovedResource(t *testing.T) {
	for iter := 0; iter < 20; iter++ {
		root := t.TempDir()
		m := NewManager()
		env, err := m.Create(root, "alice", "playground", time.Second, nil)
		if err != nil {
			t.Fatal(err)
		}
		ref := testRef("forgestack-p2-stale-write")
		if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
			t.Fatal(err)
		}
		fake := NewFakeCloudClient()
		fake.SeedBucket(ref, cloudTags(env.ID, "alice"), 0, 0, 0)
		// The first Remove call to actually reach the fake fails
		// (whichever goroutine's cleanupOne acquires the per-environment
		// lock first); the second succeeds.
		fake.RemoveFailuresRemaining = 1

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

		var wg sync.WaitGroup
		for g := 0; g < 2; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = c.CleanupDue(context.Background(), root)
			}()
		}
		wg.Wait()

		envs, err := ListEnvironments(root)
		if err != nil {
			t.Fatal(err)
		}
		finalStatus := envs[0].Status
		actuallyPresent := fake.Has("forgestack-p2-stale-write")

		if finalStatus == StatusVerifiedClean && actuallyPresent {
			t.Fatalf("iter %d: CRITICAL regression -- verified_clean reported while the resource is still present", iter)
		}
		if finalStatus == StatusCleanupUnverified && !actuallyPresent {
			t.Fatalf("iter %d: REGRESSION of the audited P2 finding -- on-disk status is a stale cleanup_unverified for a resource that is actually already gone", iter)
		}
		if finalStatus != StatusVerifiedClean {
			t.Fatalf("iter %d: expected the single-lock fix to always converge to verified_clean once the resource is actually gone within this one pass, got %q (present=%v)", iter, finalStatus, actuallyPresent)
		}
	}
}

// Same check for the dangerous direction across many random concurrent
// interleavings (not just the deterministic one-fails-one-succeeds
// case), as a broader stress confirmation.
func TestP2_ConcurrentCloudCleanupNeverReportsCleanWhileResourceRemains(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		root := t.TempDir()
		m := NewManager()
		env, err := m.Create(root, "alice", "playground", time.Second, nil)
		if err != nil {
			t.Fatal(err)
		}
		ref := testRef("forgestack-p2-stress")
		if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
			t.Fatal(err)
		}
		fake := NewFakeCloudClient()
		fake.SeedBucket(ref, cloudTags(env.ID, "alice"), 0, 0, 0)

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

		var wg sync.WaitGroup
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = c.CleanupDue(context.Background(), root)
			}()
		}
		wg.Wait()

		envs, err := ListEnvironments(root)
		if err != nil {
			t.Fatal(err)
		}
		if envs[0].Status == StatusVerifiedClean && fake.Has("forgestack-p2-stress") {
			t.Fatalf("iter %d: CRITICAL -- verified_clean reported while present", iter)
		}
	}
}

// TestP2_CorruptedTrackedFileCannotAuthorizeUnrelatedDeletion is the
// permanent regression test for the audit's confirmed POSITIVE security
// finding: a tampered cloud-resources.json naming an unrelated, real
// bucket (never carrying this environment's tag) must never cause its
// removal, because removal is driven by the live tag query (Present),
// never by the Tracked list's contents directly.
func TestP2_CorruptedTrackedFileCannotAuthorizeUnrelatedDeletion(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}

	dir := must(environmentDir(root, env.ID))
	unrelated := CloudResourceRef{Kind: CloudS3Bucket, Account: "999999999999", Region: "us-east-1", Ref: "someone-elses-production-bucket"}
	if err := writeTrackedCloudResources(dir, []CloudResourceRef{unrelated}); err != nil {
		t.Fatal(err)
	}

	fake := NewFakeCloudClient()
	fake.SeedBucket(unrelated, cloudTags("env-dddddddddddddddd", "someone-else"), 0, 0, 0)

	if _, err := RemoveTrackedCloudResources(context.Background(), fake, root, env.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	if !fake.HasRef(unrelated) {
		t.Fatal("SECURITY REGRESSION: an unrelated bucket named only in a tampered local tracked-resources file was removed")
	}
}

// TestP2_TrackCloudResourceRejectsBlankAccount/Region/Ref are the
// permanent regression tests for the audit's confirmed MEDIUM finding
// (blank fields were previously accepted, producing permanent incorrect
// drift reports).
func TestP2_TrackCloudResourceRejectsBlankAccount(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "", Region: "ap-south-1", Ref: "forgestack-bucket"}
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err == nil {
		t.Fatal("expected a blank Account to be rejected")
	}
}

func TestP2_TrackCloudResourceRejectsBlankRegion(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "", Ref: "forgestack-bucket"}
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err == nil {
		t.Fatal("expected a blank Region to be rejected")
	}
}

func TestP2_TrackCloudResourceRejectsBlankRef(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: ""}
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err == nil {
		t.Fatal("expected a blank Ref to be rejected")
	}
}

// Cross-account and cross-region negative tests: two buckets with the
// identical Ref (bucket name) but different Account, or different
// Region, must never be confused with each other by FakeCloudClient or
// by reconciliation's identity matching.
func TestP2_SameNamedBucketsInDifferentAccountsOrRegionsAreNeverConfused(t *testing.T) {
	cases := []struct {
		name          string
		accountA      string
		accountB      string
		regionA       string
		regionB       string
		confusionNote string
	}{
		{
			name:          "cross-account",
			accountA:      "111111111111",
			accountB:      "222222222222",
			regionA:       "ap-south-1",
			regionB:       "ap-south-1",
			confusionNote: "CROSS-ACCOUNT CONFUSION",
		},
		{
			name:          "cross-region",
			accountA:      "111111111111",
			accountB:      "111111111111",
			regionA:       "ap-south-1",
			regionB:       "us-east-1",
			confusionNote: "CROSS-REGION CONFUSION",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			m := NewManager()
			envA, err := m.Create(root, "alice", "playground", time.Minute, nil)
			if err != nil {
				t.Fatal(err)
			}
			envB, err := m.Create(root, "bob", "playground", time.Minute, nil)
			if err != nil {
				t.Fatal(err)
			}

			refA := CloudResourceRef{Kind: CloudS3Bucket, Account: tc.accountA, Region: tc.regionA, Ref: "shared-bucket-name"}
			refB := CloudResourceRef{Kind: CloudS3Bucket, Account: tc.accountB, Region: tc.regionB, Ref: "shared-bucket-name"}
			if err := m.TrackCloudResource(root, envA.ID, "alice", refA); err != nil {
				t.Fatal(err)
			}
			if err := m.TrackCloudResource(root, envB.ID, "bob", refB); err != nil {
				t.Fatal(err)
			}

			fake := NewFakeCloudClient()
			fake.SeedBucket(refA, cloudTags(envA.ID, "alice"), 0, 0, 0)
			fake.SeedBucket(refB, cloudTags(envB.ID, "bob"), 0, 0, 0)

			if _, err := RemoveTrackedCloudResources(context.Background(), fake, root, envA.ID, "alice"); err != nil {
				t.Fatal(err)
			}
			if fake.HasRef(refA) {
				t.Error("env A's bucket should have been removed")
			}
			if !fake.HasRef(refB) {
				t.Fatalf("%s: env B's same-named bucket must survive env A's cleanup untouched", tc.confusionNote)
			}
		})
	}
}

// TestP2_OwnershipMismatchNeverAuthorizesDeletion is a permanent
// regression test consolidating the audit's #4 requirement: neither a
// missing managed-by tag, nor a drifted owner tag, can ever lead to
// removal, across repeated concurrent attempts (not just a single call).
func TestP2_OwnershipMismatchNeverAuthorizesDeletionUnderConcurrentPressure(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-mismatch-pressure")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	// Carries the right environment-id tag (so it's Present under the
	// tag query) but the WRONG owner -- simulating a tagging mistake or
	// a resource that coincidentally shares this environment's ID tag.
	fake.SeedBucket(ref, map[string]string{CloudTagEnvironmentID: env.ID, CloudTagOwner: "not-alice", CloudTagManagedBy: CloudManagedByValue}, 0, 0, 0)

	var wg sync.WaitGroup
	var errCount atomic.Int32
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := RemoveTrackedCloudResources(context.Background(), fake, root, env.ID, "alice"); err != nil {
				errCount.Add(1)
			}
		}()
	}
	wg.Wait()

	if errCount.Load() != 8 {
		t.Errorf("expected all 8 concurrent attempts to be rejected by the ownership mismatch, got %d rejections", errCount.Load())
	}
	if !fake.HasRef(ref) {
		t.Fatal("SECURITY REGRESSION: a resource with a mismatched owner tag was removed")
	}
}

// TestOverlappingCleanupDuePassesDoNotDoubleConsumeCloudAttemptBudget is
// the permanent regression test for the P2 "retry concurrency" finding
// from the second independent audit: two CleanupDue passes for the same
// not-yet-exhausted environment that overlap in real time (e.g. a
// scheduled Worker pass overlapping an operator-triggered `cleanup-due`)
// are already serialized by withEnvironmentLock -- never concurrent,
// never corrupting state -- but without a backoff gate, each one still
// made its own real removal attempt and consumed its own unit of the
// bounded MaxCloudCleanupAttempts budget, burning it roughly twice as
// fast as MaxCloudCleanupAttempts alone would suggest. The fix is
// NewController's new nonzero default CloudRetryBackoff (see
// defaultCloudRetryBackoff, cloud_retry.go, and CloudRetryBackoff's doc
// comment, controller.go): the second, lock-serialized call re-reads the
// attempt state the first call just wrote and finds itself still inside
// the backoff window, so it skips the CloudClient entirely for this
// round.
func TestOverlappingCleanupDuePassesDoNotDoubleConsumeCloudAttemptBudget(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-overlap-budget")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, cloudTags(env.ID, "alice"), 0, 0, 0)
	fake.RemoveErr = errCloudPermanentlyDenied

	// A single fixed instant for the whole test: the worst case for the
	// backoff gate (no real wall-clock time elapses between the two
	// overlapping passes at all), and fully deterministic -- this test
	// does not rely on goroutine scheduling order to pass.
	fixedNow := env.ExpiresAt.Add(time.Millisecond)
	c := Controller{
		Manager:                 m,
		Now:                     func() time.Time { return fixedNow },
		Sleep:                   noSleep,
		MaxCleanupAttempts:      1,
		Cloud:                   fake,
		MaxCloudCleanupAttempts: 3,
		CloudRetryBackoff:       defaultCloudRetryBackoff, // matches NewController's production default
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.CleanupDue(context.Background(), root)
		}()
	}
	wg.Wait()

	if got := fake.CallCount("Remove"); got != 1 {
		t.Fatalf("expected exactly one real Remove call across both overlapping passes (backoff should have gated the second), got %d", got)
	}
	dir := must(environmentDir(root, env.ID))
	state, err := loadCloudCleanupAttemptState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if state.Attempts != 1 {
		t.Fatalf("expected exactly one attempt consumed from the bounded budget, got %d", state.Attempts)
	}
	envs, err := ListEnvironments(root)
	if err != nil {
		t.Fatal(err)
	}
	if envs[0].Status != StatusCleanupUnverified {
		t.Fatalf("expected cleanup_unverified (one of three attempts consumed, not escalated), got %q", envs[0].Status)
	}
}

// TestOverlappingCleanupDuePassesWithoutBackoffDoDoubleConsumeBudget
// documents, as an explicit and deliberate characterization (not a
// silent gap), the remaining behavior of a Controller literal that
// leaves CloudRetryBackoff at its Go zero value -- every deterministic
// test in this package outside this file does exactly that, on purpose,
// to get "attempt on every due pass" determinism. Such a Controller gets
// no protection from the double-consumption this file's sibling test
// closes for NewController's production default: two overlapping passes
// really do consume two units of the bounded budget. This is still
// always SAFE (never corrupts state, never produces a false
// verified_clean -- see TestP2_ConcurrentCloudCleanupNeverReportsCleanWhileResourceRemains),
// just faster-exhausting than MaxCloudCleanupAttempts alone would
// suggest if a caller builds a Controller literal for production use
// without setting CloudRetryBackoff. Callers building a Controller
// literal (rather than NewController) for anything other than a
// deterministic test should set CloudRetryBackoff explicitly.
func TestOverlappingCleanupDuePassesWithoutBackoffDoDoubleConsumeBudget(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-overlap-no-backoff")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, cloudTags(env.ID, "alice"), 0, 0, 0)
	fake.RemoveErr = errCloudPermanentlyDenied

	fixedNow := env.ExpiresAt.Add(time.Millisecond)
	c := Controller{
		Manager:                 m,
		Now:                     func() time.Time { return fixedNow },
		Sleep:                   noSleep,
		MaxCleanupAttempts:      1,
		Cloud:                   fake,
		MaxCloudCleanupAttempts: 3,
		// CloudRetryBackoff left at its zero value, deliberately.
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.CleanupDue(context.Background(), root)
		}()
	}
	wg.Wait()

	if got := fake.CallCount("Remove"); got != 2 {
		t.Fatalf("expected exactly two real Remove calls (documented remaining behavior with no backoff configured), got %d", got)
	}
	// Still safe: no corruption, no false clean, no error from reading
	// the state file after two rapid increments.
	dir := must(environmentDir(root, env.ID))
	state, err := loadCloudCleanupAttemptState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if state.Attempts != 2 {
		t.Fatalf("expected both attempts to be durably recorded, got %d", state.Attempts)
	}
}
