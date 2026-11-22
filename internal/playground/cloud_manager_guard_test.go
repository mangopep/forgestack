package playground

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// This file contains permanent regression tests for the P0 finding from
// the second independent audit: Manager.Cleanup (and therefore the plain
// `playground cleanup` CLI command, which calls it directly -- see
// cmd/playground/main.go) could persist StatusVerifiedClean after local
// cleanup succeeded even while docker-resources.json or
// cloud-resources.json still tracked a resource that call had no client
// to check, because the invariant "verified_clean only once every
// configured domain is verified" was previously enforced only inside
// Controller.cleanupOne, not at the Manager/library boundary every
// public entry point actually goes through.
//
// The fix is guardVerifiedCleanStatus (lifecycle.go), the single choke
// point both Manager.Cleanup and Controller.retryLocalCleanup funnel
// through (both call the shared, lower-case `cleanup`, which is the only
// caller of finalizeCleanup in this package -- see lifecycle.go/
// controller.go). It downgrades a requested StatusVerifiedClean to the
// already-retryable StatusCleanupPending, and returns a descriptive
// error, whenever dir still has a tracked Docker or cloud resource this
// particular call cannot verify -- including when the tracking file
// itself could not be read at all (fail closed, never fail open).

func dockerFileRef(id string) TrackedDockerResource {
	return TrackedDockerResource{Kind: DockerContainer, Ref: "guard-" + id}
}

// TestManagerCleanupDirect_TrackedDockerOnly_FailsClosed covers
// requirement 1: a direct Manager.Cleanup call (no DockerClient
// available to it at all) must refuse to persist verified_clean while a
// tracked Docker resource remains, and must leave the tracked-resource
// record untouched as evidence.
func TestManagerCleanupDirect_TrackedDockerOnly_FailsClosed(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	res := dockerFileRef(env.ID)
	if err := m.TrackDockerResource(root, env.ID, "alice", res); err != nil {
		t.Fatal(err)
	}

	result, err := m.Cleanup(root, env.ID, "alice")
	if err == nil {
		t.Fatal("expected direct Manager.Cleanup to refuse verified_clean while a tracked docker resource remains unverified")
	}
	if result.Environment.Status != StatusCleanupPending {
		t.Fatalf("expected cleanup_pending (recoverable), got %q", result.Environment.Status)
	}

	dir := filepath.Join(root, env.ID)
	if _, err := os.Lstat(filepath.Join(dir, "resources")); !os.IsNotExist(err) {
		t.Fatalf("expected local resources to still be removed despite the guard, Lstat err=%v", err)
	}
	tracked, err := loadTrackedDockerResources(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracked) != 1 || tracked[0] != res {
		t.Fatalf("expected the tracked docker resource evidence to survive untouched, got %+v", tracked)
	}
}

// TestManagerCleanupDirect_TrackedCloudOnly_FailsClosed covers
// requirement 2. See also TestTrackCloudResourceSurvivesLocalCleanup
// (cloud_tracked_test.go) for the same defect caught from a different
// angle.
func TestManagerCleanupDirect_TrackedCloudOnly_FailsClosed(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-guard-cloud-only")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}

	result, err := m.Cleanup(root, env.ID, "alice")
	if err == nil {
		t.Fatal("expected direct Manager.Cleanup to refuse verified_clean while a tracked cloud resource remains unverified")
	}
	if result.Environment.Status != StatusCleanupPending {
		t.Fatalf("expected cleanup_pending (recoverable), got %q", result.Environment.Status)
	}

	dir := filepath.Join(root, env.ID)
	tracked, err := loadTrackedCloudResources(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracked) != 1 || tracked[0] != ref {
		t.Fatalf("expected the tracked cloud resource evidence to survive untouched, got %+v", tracked)
	}
}

// TestManagerCleanupDirect_TrackedDockerAndCloud_FailsClosed covers
// requirement 3: both domains tracked, neither client available.
func TestManagerCleanupDirect_TrackedDockerAndCloud_FailsClosed(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	dockerRes := dockerFileRef(env.ID)
	if err := m.TrackDockerResource(root, env.ID, "alice", dockerRes); err != nil {
		t.Fatal(err)
	}
	cloudRef := testRef("forgestack-guard-combined")
	if err := m.TrackCloudResource(root, env.ID, "alice", cloudRef); err != nil {
		t.Fatal(err)
	}

	result, err := m.Cleanup(root, env.ID, "alice")
	if err == nil {
		t.Fatal("expected direct Manager.Cleanup to refuse verified_clean while both tracked resources remain unverified")
	}
	if result.Environment.Status != StatusCleanupPending {
		t.Fatalf("expected cleanup_pending (recoverable), got %q", result.Environment.Status)
	}

	dir := filepath.Join(root, env.ID)
	dockerTracked, err := loadTrackedDockerResources(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(dockerTracked) != 1 || dockerTracked[0] != dockerRes {
		t.Fatalf("expected the tracked docker resource evidence to survive, got %+v", dockerTracked)
	}
	cloudTracked, err := loadTrackedCloudResources(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cloudTracked) != 1 || cloudTracked[0] != cloudRef {
		t.Fatalf("expected the tracked cloud resource evidence to survive, got %+v", cloudTracked)
	}
}

// TestManagerCleanupDirect_LocalOnly_StillSucceeds covers requirement 5:
// the overwhelmingly common case -- no tracked Docker/cloud resource at
// all -- must be completely unaffected by the guard.
func TestManagerCleanupDirect_LocalOnly_StillSucceeds(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := m.Cleanup(root, env.ID, "alice")
	if err != nil {
		t.Fatalf("expected local-only cleanup to succeed exactly as before, got err=%v", err)
	}
	if result.Environment.Status != StatusVerifiedClean {
		t.Fatalf("expected verified_clean, got %q", result.Environment.Status)
	}
	if result.Cleanup != "VERIFIED CLEAN" {
		t.Fatalf("expected the unchanged local-only success message, got %q", result.Cleanup)
	}
}

// TestManagerCleanupDirect_CorruptTrackingFiles_FailClosed covers
// requirement 6: a malformed or partially-written docker-resources.json
// or cloud-resources.json must never be treated as "no resources
// tracked" -- an unreadable tracking file is exactly the situation the
// guard exists to never paper over.
func TestManagerCleanupDirect_CorruptTrackingFiles_FailClosed(t *testing.T) {
	cases := []struct {
		name string
		path func(dir string) string
		data []byte
	}{
		{"malformed_docker_json", trackedResourcesPath, []byte("{not valid json")},
		{"truncated_docker_json", trackedResourcesPath, []byte(`[{"kind":"container","ref":`)},
		{"malformed_cloud_json", cloudResourcesPath, []byte("{not valid json")},
		{"truncated_cloud_json", cloudResourcesPath, []byte(`[{"kind":"s3_bucket","account":`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			m := NewManager()
			env, err := m.Create(root, "alice", "playground", time.Minute, nil)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(root, env.ID)
			// Simulate a crash mid-write or hand-corruption: write
			// directly, bypassing TrackDockerResource/TrackCloudResource
			// (which would reject this on write), exactly as a real
			// partial write or disk corruption would leave the file.
			if err := os.WriteFile(tc.path(dir), tc.data, 0600); err != nil {
				t.Fatal(err)
			}

			result, err := m.Cleanup(root, env.ID, "alice")
			if err == nil {
				t.Fatal("expected an unreadable tracking file to fail closed, not to be treated as no resources tracked")
			}
			if result.Environment.Status != StatusCleanupPending {
				t.Fatalf("expected cleanup_pending (recoverable) on an unreadable tracking file, got %q", result.Environment.Status)
			}
			// The corrupt file itself must be left exactly as it was --
			// the guard only reads, it never attempts to repair or
			// discard evidence a human may need to diagnose.
			data, err := os.ReadFile(tc.path(dir))
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != string(tc.data) {
				t.Fatalf("expected the corrupt tracking file to be left untouched, got %q", data)
			}
		})
	}
}

// TestManagerCleanupDirect_RestartRecoveryAfterRejectedCleanup covers
// requirement 7: after a direct Manager.Cleanup call is rejected by the
// guard and leaves the environment cleanup_pending, a properly
// configured Controller (fresh process, no shared in-memory state) must
// still be able to pick it up and drive it to a real verified_clean.
func TestManagerCleanupDirect_RestartRecoveryAfterRejectedCleanup(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	res := dockerFileRef(env.ID)
	if err := m.TrackDockerResource(root, env.ID, "alice", res); err != nil {
		t.Fatal(err)
	}

	// The rejected direct call, exactly as the CLI's `cleanup` command
	// would perform it.
	if _, err := m.Cleanup(root, env.ID, "alice"); err == nil {
		t.Fatal("expected the direct cleanup to be rejected by the guard")
	}
	envs, err := ListEnvironments(root)
	if err != nil {
		t.Fatal(err)
	}
	if envs[0].Status != StatusCleanupPending {
		t.Fatalf("expected cleanup_pending after the rejected direct call, got %q", envs[0].Status)
	}

	// "Restart": a brand-new Controller/Manager with no shared memory,
	// now correctly configured with the Docker client that was missing
	// before, matching the resource that was tracked.
	fake := NewFakeDockerClient()
	fake.Seed(res.Kind, res.Ref, map[string]string{DockerLabelEnvironmentID: env.ID})
	fresh := Controller{
		Manager:            NewManager(),
		Now:                func() time.Time { return time.Now().UTC() },
		Sleep:              noSleep,
		MaxCleanupAttempts: 1,
		Docker:             fake,
	}
	results, err := fresh.CleanupDue(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Environment.Status != StatusVerifiedClean {
		t.Fatalf("expected the restarted, properly configured Controller to resolve the pending docker resource, got %+v", results)
	}
	if fake.Has(res.Ref) {
		t.Fatal("expected the docker resource to have actually been removed")
	}
}

// TestManagerAndControllerCleanup_ConcurrentWithTrackedResource covers
// requirement 8: a direct (guarded, client-less, bound to reject)
// Manager.Cleanup call racing a properly configured Controller.CleanupDue
// for the exact same environment must never corrupt state, never lose
// the eventual real cleanup, and never let the rejected call's own
// write regress a concurrently-achieved verified_clean back to
// cleanup_pending. The per-environment flock (withEnvironmentLock)
// already serializes both call shapes' entire read-decide-write cycle,
// so this proves that guarantee still holds with the new guard added to
// the critical section rather than racing past it.
func TestManagerAndControllerCleanup_ConcurrentWithTrackedResource(t *testing.T) {
	for iter := 0; iter < 20; iter++ {
		root := t.TempDir()
		m := NewManager()
		env, err := m.Create(root, "alice", "playground", time.Minute, nil)
		if err != nil {
			t.Fatal(err)
		}
		res := dockerFileRef(env.ID)
		if err := m.TrackDockerResource(root, env.ID, "alice", res); err != nil {
			t.Fatal(err)
		}
		fake := NewFakeDockerClient()
		fake.Seed(res.Kind, res.Ref, map[string]string{DockerLabelEnvironmentID: env.ID})

		controller := Controller{
			Manager:            m,
			Now:                func() time.Time { return time.Now().UTC() },
			Sleep:              noSleep,
			MaxCleanupAttempts: 1,
			Docker:             fake,
		}

		done := make(chan struct{}, 2)
		go func() {
			defer func() { done <- struct{}{} }()
			_, _ = m.Cleanup(root, env.ID, "alice") // guarded direct call -- expected to be rejected or to observe already-clean state
		}()
		go func() {
			defer func() { done <- struct{}{} }()
			_, _ = controller.CleanupDue(context.Background(), root)
		}()
		<-done
		<-done

		// A second, final controller pass drains any cleanup_pending
		// left by whichever call ran first (the direct call may have
		// run before the environment's status was even cleanup_pending
		// yet, since CleanupDue itself transitions it).
		_, _ = controller.CleanupDue(context.Background(), root)

		envs, err := ListEnvironments(root)
		if err != nil {
			t.Fatal(err)
		}
		status := envs[0].Status
		resourceGone := !fake.Has(res.Ref)

		if status == StatusVerifiedClean && !resourceGone {
			t.Fatalf("iter %d: CRITICAL -- verified_clean persisted while the tracked docker resource is still present", iter)
		}
		if status != StatusVerifiedClean && resourceGone {
			t.Fatalf("iter %d: resource is actually gone but status %q never converged to verified_clean", iter, status)
		}
		// The tracked-resource record itself must still parse as valid
		// JSON throughout -- concurrent access must never corrupt it.
		if _, err := loadTrackedDockerResources(filepath.Join(root, env.ID)); err != nil {
			t.Fatalf("iter %d: tracked docker resource file corrupted by concurrent access: %v", iter, err)
		}
	}
}

// TestManagerCleanupDirect_NeverPersistsFalseVerifiedClean covers
// requirement 9 as a table-driven stress check across every tracked-
// resource shape this guard must catch: docker-only, cloud-only,
// combined, and (as a control) genuinely none.
func TestManagerCleanupDirect_NeverPersistsFalseVerifiedClean(t *testing.T) {
	cases := []struct {
		name        string
		trackDocker bool
		trackCloud  bool
		expectBlock bool
	}{
		{"docker_only", true, false, true},
		{"cloud_only", false, true, true},
		{"both", true, true, true},
		{"neither", false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			m := NewManager()
			env, err := m.Create(root, "alice", "playground", time.Minute, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.trackDocker {
				if err := m.TrackDockerResource(root, env.ID, "alice", dockerFileRef(env.ID)); err != nil {
					t.Fatal(err)
				}
			}
			if tc.trackCloud {
				if err := m.TrackCloudResource(root, env.ID, "alice", testRef("forgestack-"+tc.name)); err != nil {
					t.Fatal(err)
				}
			}

			result, err := m.Cleanup(root, env.ID, "alice")
			blocked := err != nil
			if blocked != tc.expectBlock {
				t.Fatalf("expected blocked=%v, got blocked=%v (err=%v)", tc.expectBlock, blocked, err)
			}
			if tc.expectBlock && result.Environment.Status == StatusVerifiedClean {
				t.Fatalf("CRITICAL: verified_clean persisted despite a tracked, unverified resource (%s)", tc.name)
			}
			if !tc.expectBlock && result.Environment.Status != StatusVerifiedClean {
				t.Fatalf("expected the no-tracked-resource control case to still reach verified_clean, got %q", result.Environment.Status)
			}
		})
	}
}
