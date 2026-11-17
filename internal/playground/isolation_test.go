package playground

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestConcurrentCreateDuringCleanupNeverCausesFalseUnverified is the
// permanent regression test for the sibling-inventory false positive:
// this environment's own cleanup genuinely succeeds, but a brand-new,
// unrelated sibling environment is Created concurrently, deterministically
// landed (via channel synchronization, not sleeps) in the exact window
// between this Cleanup's before/after root-directory snapshots. The
// result must still be VERIFIED_CLEAN -- a concurrent Create for a
// different, independently-generated ID is not collateral damage.
func TestConcurrentCreateDuringCleanupNeverCausesFalseUnverified(t *testing.T) {
	root := t.TempDir()
	base := NewManager()
	env, err := base.Create(root, "alice", "playground", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}

	removed := make(chan struct{})
	proceed := make(chan struct{})
	m := Manager{removeAll: func(path string) error {
		err := base.removeAll(path)
		close(removed)
		<-proceed
		return err
	}}

	var wg sync.WaitGroup
	var result Result
	var cleanupErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		result, cleanupErr = m.Cleanup(root, env.ID, "alice")
	}()

	<-removed
	if _, err := base.Create(root, "bob", "playground", time.Hour, nil); err != nil {
		t.Fatal(err)
	}
	close(proceed)
	wg.Wait()

	if cleanupErr != nil {
		t.Fatalf("expected no error, got %v", cleanupErr)
	}
	if result.Environment.Status != StatusVerifiedClean {
		t.Fatalf("a concurrent Create of an unrelated environment must never affect this one's cleanup result, got status=%q", result.Environment.Status)
	}
}

// TestConcurrentCleanupOfDifferentEnvironmentNeverAffectsThisOnesResult
// covers the "deleting another environment" half of item 3: environment
// B's own Cleanup -- running fully concurrently, synchronized to overlap
// via the same removeAll hook on both sides -- must not affect A's
// result. (Cleanup never removes an environment's own top-level
// directory, only its resources subdirectory, so B's cleanup should not
// even touch the root-level listing A's sibling check inspects; this
// confirms that empirically rather than by inspection alone.)
func TestConcurrentCleanupOfDifferentEnvironmentNeverAffectsThisOnesResult(t *testing.T) {
	root := t.TempDir()
	base := NewManager()
	envA, err := base.Create(root, "alice", "playground", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	envB, err := base.Create(root, "bob", "playground", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}

	aReady := make(chan struct{})
	bReady := make(chan struct{})
	managerA := Manager{removeAll: func(path string) error {
		close(aReady)
		<-bReady
		return base.removeAll(path)
	}}
	managerB := Manager{removeAll: func(path string) error {
		close(bReady)
		<-aReady
		return base.removeAll(path)
	}}

	var wg sync.WaitGroup
	var resultA, resultB Result
	var errA, errB error
	wg.Add(2)
	go func() {
		defer wg.Done()
		resultA, errA = managerA.Cleanup(root, envA.ID, "alice")
	}()
	go func() {
		defer wg.Done()
		resultB, errB = managerB.Cleanup(root, envB.ID, "bob")
	}()
	wg.Wait()

	if errA != nil || resultA.Environment.Status != StatusVerifiedClean {
		t.Fatalf("A: expected VERIFIED_CLEAN unaffected by B's concurrent cleanup, got status=%q err=%v", resultA.Environment.Status, errA)
	}
	if errB != nil || resultB.Environment.Status != StatusVerifiedClean {
		t.Fatalf("B: expected VERIFIED_CLEAN unaffected by A's concurrent cleanup, got status=%q err=%v", resultB.Environment.Status, errB)
	}
}

// TestConcurrentTrackDockerResourceOnDifferentEnvironmentNeverAffectsThisOnesResult
// covers the "tracking another environment" half of item 3: tracking a
// Docker resource against environment B, concurrently with environment
// A's cleanup, must not affect A's result. TrackDockerResource only ever
// writes inside its own environment's directory (docker-resources.json),
// never at the root level, so this should already be safe; confirmed
// empirically.
func TestConcurrentTrackDockerResourceOnDifferentEnvironmentNeverAffectsThisOnesResult(t *testing.T) {
	root := t.TempDir()
	base := NewManager()
	envA, err := base.Create(root, "alice", "playground", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	envB, err := base.Create(root, "bob", "playground", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}

	removed := make(chan struct{})
	proceed := make(chan struct{})
	managerA := Manager{removeAll: func(path string) error {
		err := base.removeAll(path)
		close(removed)
		<-proceed
		return err
	}}

	var wg sync.WaitGroup
	var resultA Result
	var errA error
	wg.Add(1)
	go func() {
		defer wg.Done()
		resultA, errA = managerA.Cleanup(root, envA.ID, "alice")
	}()

	<-removed
	if err := base.TrackDockerResource(root, envB.ID, "bob", TrackedDockerResource{Kind: DockerContainer, Ref: "unrelated-container"}); err != nil {
		t.Fatal(err)
	}
	close(proceed)
	wg.Wait()

	if errA != nil || resultA.Environment.Status != StatusVerifiedClean {
		t.Fatalf("expected A's cleanup to be unaffected by concurrently tracking a resource on B, got status=%q err=%v", resultA.Environment.Status, errA)
	}
}

// TestPriorSiblingsStillPresentStillDetectsRealCollateralDamage is the
// negative-test counterpart that proves the ownership-scoped relaxation
// did not weaken verification: a sibling that existed before and is
// genuinely gone (or changed type) afterward must still be caught, and
// only a brand-new addition is tolerated.
func TestPriorSiblingsStillPresentStillDetectsRealCollateralDamage(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "kept"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "removed-collaterally"), 0700); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}

	// A brand-new sibling appearing is tolerated.
	if err := os.Mkdir(filepath.Join(root, "brand-new"), 0700); err != nil {
		t.Fatal(err)
	}
	afterWithAddition, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if !priorSiblingsStillPresent(before, afterWithAddition, "nonexistent-id") {
		t.Fatal("expected a brand-new sibling to be tolerated")
	}

	// A prior sibling going missing must still be caught.
	if err := os.RemoveAll(filepath.Join(root, "removed-collaterally")); err != nil {
		t.Fatal(err)
	}
	afterWithRemoval, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if priorSiblingsStillPresent(before, afterWithRemoval, "nonexistent-id") {
		t.Fatal("expected a prior sibling going missing to be detected, not tolerated")
	}
}
