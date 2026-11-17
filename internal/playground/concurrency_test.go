package playground

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSecondConcurrentCleanupNeverRegressesAVerifiedCleanEnvironment is the
// regression test for the stale-write race: environment A's cleanup is
// held, via its real removeAll, inside the per-environment lock (proving
// it is the current holder); a second, fully concurrent Cleanup call for
// the exact same environment is launched while the first still holds the
// lock, using a Manager whose removeAll always fails for a reason
// unrelated to real file state (a stand-in for "a transient error the
// first attempt didn't hit"). Before this package's locking was added,
// forcing this exact interleaving made the second call's stale failure
// overwrite the first call's true VERIFIED_CLEAN on disk. With the lock,
// the second call cannot even perform its own ownership/resource read
// until the first fully finishes, so by the time it does, the resources
// are genuinely already gone -- its "always fails" removeAll is never
// invoked at all, and it independently arrives at the same true
// VERIFIED_CLEAN rather than overwriting it with a stale failure.
func TestSecondConcurrentCleanupNeverRegressesAVerifiedCleanEnvironment(t *testing.T) {
	root := t.TempDir()
	base := NewManager()
	env, err := base.Create(root, "alice", "playground", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}

	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	var secondRemoveAllCalled atomic.Bool

	first := Manager{removeAll: func(path string) error {
		close(firstStarted)
		<-firstRelease
		return base.removeAll(path)
	}}
	second := Manager{removeAll: func(string) error {
		secondRemoveAllCalled.Store(true)
		return errors.New("simulated failure uncorrelated with real file state -- must never be reached")
	}}

	var wg sync.WaitGroup
	var resultFirst, resultSecond Result
	var errFirst, errSecond error

	wg.Add(1)
	go func() {
		defer wg.Done()
		resultFirst, errFirst = first.Cleanup(root, env.ID, "alice")
	}()
	<-firstStarted // first now holds the per-environment lock and is blocked inside its real removeAll

	wg.Add(1)
	go func() {
		defer wg.Done()
		// Blocks acquiring the same lock until firstRelease fires below --
		// deterministic because `first` already holds it.
		resultSecond, errSecond = second.Cleanup(root, env.ID, "alice")
	}()

	close(firstRelease)
	wg.Wait()

	if errFirst != nil || resultFirst.Environment.Status != StatusVerifiedClean {
		t.Fatalf("first attempt: expected success, got status=%q err=%v", resultFirst.Environment.Status, errFirst)
	}
	if errSecond != nil || resultSecond.Environment.Status != StatusVerifiedClean {
		t.Fatalf("second attempt: expected it to independently reach VERIFIED_CLEAN rather than report a stale failure, got status=%q err=%v", resultSecond.Environment.Status, errSecond)
	}
	if secondRemoveAllCalled.Load() {
		t.Fatal("second attempt's removeAll was invoked despite the first already having removed the resources under the same lock -- the read-then-act decision was not serialized")
	}

	envs, err := ListEnvironments(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 1 || envs[0].Status != StatusVerifiedClean {
		t.Fatalf("final persisted status = %+v, want exactly one environment at %q", envs, StatusVerifiedClean)
	}
}

// TestControllerCleanupDueConcurrentCallersConvergeWithoutCorruption stress
// tests the same property at the Controller level, through the public
// CleanupDue entry point two independent Controller values (standing in
// for two separate processes, sharing nothing but the root directory) call
// concurrently and repeatedly, with a consistently-failing local Manager
// for one of them -- the asymmetric-failure shape item 6 asks for. Run
// with -race, this also catches any unsynchronized shared-memory access
// the lock's introduction might have missed.
func TestControllerCleanupDueConcurrentCallersConvergeWithoutCorruption(t *testing.T) {
	for iter := 0; iter < 25; iter++ {
		root := t.TempDir()
		base := NewManager()
		env, err := base.Create(root, "alice", "playground", time.Second, nil)
		if err != nil {
			t.Fatal(err)
		}

		var failingCalls atomic.Int32
		flaky := Manager{removeAll: func(path string) error {
			// Fails on its first couple of invocations regardless of
			// which caller made them, then starts succeeding -- an
			// asymmetric, not-tied-to-real-state failure pattern.
			if failingCalls.Add(1) <= 2 {
				return errors.New("simulated asymmetric transient failure")
			}
			return base.removeAll(path)
		}}

		ctrlReliable := Controller{
			Manager:            base,
			Now:                func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
			Sleep:              func(time.Duration) {},
			MaxCleanupAttempts: 3,
		}
		ctrlFlaky := Controller{
			Manager:            flaky,
			Now:                func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
			Sleep:              func(time.Duration) {},
			MaxCleanupAttempts: 3,
		}

		if _, err := ctrlReliable.ExpireDue(root); err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		for _, c := range []Controller{ctrlReliable, ctrlFlaky, ctrlReliable, ctrlFlaky} {
			wg.Add(1)
			go func(c Controller) {
				defer wg.Done()
				_, _ = c.CleanupDue(context.Background(), root)
			}(c)
		}
		wg.Wait()

		envs, err := ListEnvironments(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(envs) != 1 {
			t.Fatalf("iter %d: expected exactly one environment, got %+v", iter, envs)
		}
		status := envs[0].Status
		if status != StatusVerifiedClean && status != StatusCleanupIncomplete {
			t.Fatalf("iter %d: unexpected final status %q (must be a legitimate outcome, never something else)", iter, status)
		}
	}
}

// TestTrackDockerResourceConcurrentCallsNeverLoseAnUpdate proves the
// read-modify-write in TrackDockerResource is also serialized: two
// goroutines each track a *different* resource for the same environment
// at the same time. Without the per-environment lock, both could load the
// same starting list, each append their own entry, and whichever write
// landed last would silently discard the other's -- a lost update, not
// merely something that reconverges on its own the way Cleanup's race
// does.
func TestTrackDockerResourceConcurrentCallsNeverLoseAnUpdate(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	refs := []string{"container-a", "container-b", "container-c", "container-d"}
	for _, ref := range refs {
		wg.Add(1)
		go func(ref string) {
			defer wg.Done()
			if err := m.TrackDockerResource(root, env.ID, "alice", TrackedDockerResource{Kind: DockerContainer, Ref: ref}); err != nil {
				t.Errorf("TrackDockerResource(%s): %v", ref, err)
			}
		}(ref)
	}
	wg.Wait()

	tracked, err := loadTrackedDockerResources(must(environmentDir(root, env.ID)))
	if err != nil {
		t.Fatal(err)
	}
	if len(tracked) != len(refs) {
		t.Fatalf("expected all %d concurrently-tracked resources to survive, got %d: %+v", len(refs), len(tracked), tracked)
	}
}

func must(s string, err error) string {
	if err != nil {
		panic(err)
	}
	return s
}
