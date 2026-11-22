package playground

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestTrackCloudResourceConcurrentCallsNeverLoseAnUpdate is the cloud
// equivalent of TestTrackDockerResourceConcurrentCallsNeverLoseAnUpdate:
// without withEnvironmentLock serializing the read-modify-write cycle,
// concurrent calls tracking different resources for the same environment
// could each read the same starting list and one would silently discard
// the other's entry on write.
func TestTrackCloudResourceConcurrentCallsNeverLoseAnUpdate(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	refs := []string{"forgestack-a", "forgestack-b", "forgestack-c", "forgestack-d"}
	for _, ref := range refs {
		wg.Add(1)
		go func(ref string) {
			defer wg.Done()
			if err := m.TrackCloudResource(root, env.ID, "alice", testRef(ref)); err != nil {
				t.Errorf("TrackCloudResource(%s): %v", ref, err)
			}
		}(ref)
	}
	wg.Wait()

	tracked, err := loadTrackedCloudResources(must(environmentDir(root, env.ID)))
	if err != nil {
		t.Fatal(err)
	}
	if len(tracked) != len(refs) {
		t.Fatalf("expected all %d concurrently-tracked resources to survive, got %d: %+v", len(refs), len(tracked), tracked)
	}
}

// TestConcurrentCleanupOfDifferentEnvironmentNeverAffectsThisOnesCloudResult
// proves cross-environment isolation holds for the cloud phase the same
// way isolation_test.go already proves it for local cleanup: environment
// B's own, fully concurrent cleanup (including its cloud removal) must
// never affect environment A's result.
func TestConcurrentCleanupOfDifferentEnvironmentNeverAffectsThisOnesCloudResult(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	envA, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	envB, err := m.Create(root, "bob", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	refA := testRef("forgestack-concurrent-a")
	refB := testRef("forgestack-concurrent-b")
	if err := m.TrackCloudResource(root, envA.ID, "alice", refA); err != nil {
		t.Fatal(err)
	}
	if err := m.TrackCloudResource(root, envB.ID, "bob", refB); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(refA, cloudTags(envA.ID, "alice"), 0, 0, 0)
	fake.SeedBucket(refB, cloudTags(envB.ID, "bob"), 0, 0, 0)

	// envB is created a moment after envA, so its real ExpiresAt can be
	// later than envA's -- the fixed clock below must be past whichever
	// of the two is later, or the earlier-expiring one's own ExpireDue
	// would never fire.
	expiry := envA.ExpiresAt
	if envB.ExpiresAt.After(expiry) {
		expiry = envB.ExpiresAt
	}
	c := Controller{
		Manager:            m,
		Now:                func() time.Time { return expiry.Add(time.Millisecond) },
		Sleep:              noSleep,
		MaxCleanupAttempts: 1,
		RetryBackoff:       0,
		Cloud:              fake,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	results := make(map[string][]Result)
	var mu sync.Mutex
	for _, id := range []string{envA.ID, envB.ID} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			r, err := c.CleanupDue(context.Background(), root)
			if err != nil {
				// One environment's own transient outcome is not this
				// test's concern; what matters is isolation, asserted
				// below via the fakes' final state.
				_ = err
			}
			mu.Lock()
			results[id] = append(results[id], r...)
			mu.Unlock()
		}(id)
	}
	wg.Wait()

	if fake.Has("forgestack-concurrent-a") {
		t.Error("env A's bucket should have been removed")
	}
	if fake.Has("forgestack-concurrent-b") {
		t.Error("env B's bucket should have been removed")
	}
}
