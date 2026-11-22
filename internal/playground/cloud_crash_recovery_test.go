package playground

import (
	"context"
	"testing"
	"time"
)

// This file contains permanent regression tests for the CRITICAL finding
// from the independent S3 cleanup audit: StatusVerifiedClean could be
// persisted to disk before every configured additive domain (Docker,
// Cloud) had actually been verified clean, because Manager.Cleanup's own
// internal write of the terminal status happened before the controller
// ever reached the Docker/Cloud phases. A crash in that window left
// environment.json permanently claiming verified_clean -- a status
// excluded from needsCleanup's retry set -- while real resources
// (locally this package only models Docker/S3, but the risk generalizes
// to any future resource kind) remained untouched forever, with no
// automatic recovery.
//
// The fix (lifecycle.go's parameterized successStatus, controller.go's
// localSuccessStatus/verifyAdditionalDomains) means local cleanup never
// writes the terminal StatusVerifiedClean when any additive domain is
// configured -- it writes StatusCleanupPending instead, which IS in
// needsCleanup's retry set. Only verifyAdditionalDomains's own single
// locked write, once every configured domain is independently confirmed
// clean, can ever persist StatusVerifiedClean.
//
// Each test below simulates the exact crash point the audit found: call
// retryLocalCleanup (which, before the fix, already reached and
// persisted verified_clean at this point) and stop there -- as if the
// process died right after local cleanup succeeded, before any
// additional-domain phase ran. Then start a BRAND NEW Controller/Manager
// (no shared in-memory state at all) against the same root, exactly as a
// restarted process or a second worker instance would, and confirm the
// resource is found and actually removed.

func crashSimController(m Manager, now time.Time, docker DockerClient, cloud CloudClient) Controller {
	return Controller{
		Manager:            m,
		Now:                func() time.Time { return now },
		Sleep:              noSleep,
		MaxCleanupAttempts: 1,
		RetryBackoff:       0,
		Docker:             docker,
		Cloud:              cloud,
	}
}

func TestCrashRecovery_DockerOnly_VerifiedCleanNeverPersistsBeforeDockerPhaseRuns(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.TrackDockerResource(root, env.ID, "alice", TrackedDockerResource{Kind: DockerContainer, Ref: "crash-container"}); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeDockerClient()
	fake.Seed(DockerContainer, "crash-container", map[string]string{DockerLabelEnvironmentID: env.ID})

	now := env.ExpiresAt.Add(time.Millisecond)
	c := crashSimController(m, now, fake, nil)

	// Simulate the crash point: local cleanup alone, nothing further.
	hasAdditionalDomains := c.Docker != nil || c.Cloud != nil
	localSuccessStatus := StatusVerifiedClean
	if hasAdditionalDomains {
		localSuccessStatus = StatusCleanupPending
	}
	result, err := c.retryLocalCleanup(root, env.ID, env.Owner, localSuccessStatus)
	if err != nil {
		t.Fatal(err)
	}
	if result.Environment.Status != StatusCleanupPending {
		t.Fatalf("THE AUDITED DEFECT: local-only cleanup must not yet report a terminal status while a Docker phase is still pending; got %q", result.Environment.Status)
	}
	envs, err := ListEnvironments(root)
	if err != nil {
		t.Fatal(err)
	}
	if envs[0].Status != StatusCleanupPending {
		t.Fatalf("THE AUDITED DEFECT: on-disk status must still be cleanup_pending (retryable) immediately after the simulated crash point, got %q", envs[0].Status)
	}

	// "Crash." Brand-new process: fresh Manager, fresh Controller, no
	// shared memory with anything above.
	fresh := crashSimController(NewManager(), now, fake, nil)
	results, err := fresh.CleanupDue(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Environment.Status != StatusVerifiedClean {
		t.Fatalf("expected the restarted process to find and resolve the pending Docker phase, got %+v", results)
	}
	if fake.Has("crash-container") {
		t.Fatal("the docker resource should have been removed by the restarted process")
	}
}

func TestCrashRecovery_CloudOnly_VerifiedCleanNeverPersistsBeforeCloudPhaseRuns(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-crash-cloud")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, cloudTags(env.ID, "alice"), 0, 0, 0)

	now := env.ExpiresAt.Add(time.Millisecond)
	c := crashSimController(m, now, nil, fake)

	result, err := c.retryLocalCleanup(root, env.ID, env.Owner, StatusCleanupPending)
	if err != nil {
		t.Fatal(err)
	}
	if result.Environment.Status != StatusCleanupPending {
		t.Fatalf("THE AUDITED DEFECT: local-only cleanup must not yet report a terminal status while a Cloud phase is still pending; got %q", result.Environment.Status)
	}
	envs, err := ListEnvironments(root)
	if err != nil {
		t.Fatal(err)
	}
	if envs[0].Status != StatusCleanupPending {
		t.Fatalf("THE AUDITED DEFECT: on-disk status must still be cleanup_pending immediately after the simulated crash point, got %q", envs[0].Status)
	}

	fresh := crashSimController(NewManager(), now, nil, fake)
	results, err := fresh.CleanupDue(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Environment.Status != StatusVerifiedClean {
		t.Fatalf("expected the restarted process to find and resolve the pending Cloud phase, got %+v", results)
	}
	if fake.Has("forgestack-crash-cloud") {
		t.Fatal("the cloud bucket should have been removed by the restarted process")
	}
}

func setupCombinedCrashEnv(t *testing.T) (root string, env Environment, dockerFake *FakeDockerClient, cloudFake *FakeCloudClient, ref CloudResourceRef) {
	t.Helper()
	root = t.TempDir()
	m := NewManager()
	var err error
	env, err = m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.TrackDockerResource(root, env.ID, "alice", TrackedDockerResource{Kind: DockerContainer, Ref: "crash-combined-container"}); err != nil {
		t.Fatal(err)
	}
	ref = testRef("forgestack-crash-combined")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	dockerFake = NewFakeDockerClient()
	dockerFake.Seed(DockerContainer, "crash-combined-container", map[string]string{DockerLabelEnvironmentID: env.ID})
	cloudFake = NewFakeCloudClient()
	cloudFake.SeedBucket(ref, cloudTags(env.ID, "alice"), 0, 0, 0)
	return root, env, dockerFake, cloudFake, ref
}

func assertCombinedCrashPointIsRecoverable(t *testing.T, root string, env Environment) {
	t.Helper()
	envs, err := ListEnvironments(root)
	if err != nil {
		t.Fatal(err)
	}
	if envs[0].Status != StatusCleanupPending {
		t.Fatalf("THE AUDITED DEFECT: on-disk status must still be cleanup_pending at this simulated crash point, got %q", envs[0].Status)
	}
}

func TestCrashRecovery_DockerAndCloudCombined_VerifiedCleanNeverPersistsEarly(t *testing.T) {
	root, env, dockerFake, cloudFake, ref := setupCombinedCrashEnv(t)

	now := env.ExpiresAt.Add(time.Millisecond)
	c := crashSimController(NewManager(), now, dockerFake, cloudFake)

	result, err := c.retryLocalCleanup(root, env.ID, env.Owner, StatusCleanupPending)
	if err != nil {
		t.Fatal(err)
	}
	if result.Environment.Status != StatusCleanupPending {
		t.Fatalf("THE AUDITED DEFECT: combined-domain local cleanup must not yet report a terminal status, got %q", result.Environment.Status)
	}

	// Simulate the crash happening even one phase further in: Docker
	// phase completes successfully, then the process dies before the
	// Cloud phase ever runs. Both resources must still be recoverable.
	rec, dockerErr := RemoveTrackedDockerResources(context.Background(), dockerFake, root, env.ID)
	if dockerErr != nil || !rec.Clean() {
		t.Fatalf("expected the docker phase itself to succeed in this simulation, got rec=%+v err=%v", rec, dockerErr)
	}
	assertCombinedCrashPointIsRecoverable(t, root, env)
	if !cloudFake.HasRef(ref) {
		t.Fatal("test setup error: the cloud bucket should still be present at this simulated crash point")
	}

	fresh := crashSimController(NewManager(), now, dockerFake, cloudFake)
	results, err := fresh.CleanupDue(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Environment.Status != StatusVerifiedClean {
		t.Fatalf("expected the restarted process to find and resolve the remaining cloud phase, got %+v", results)
	}
	if cloudFake.HasRef(ref) {
		t.Fatal("the cloud bucket should have been removed by the restarted process")
	}
	if dockerFake.Has("crash-combined-container") {
		t.Fatal("the docker container (already removed before the simulated crash) must remain removed")
	}
}

// TestCrashRecovery_LocalOnlyControllerStillWritesVerifiedCleanImmediately
// proves the fix did not regress the common case: a Controller with no
// additive domains configured at all must still reach verified_clean in
// a single local cleanup call, exactly as before this change.
func TestCrashRecovery_LocalOnlyControllerStillWritesVerifiedCleanImmediately(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := crashSimController(m, env.ExpiresAt.Add(time.Millisecond), nil, nil)
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	results, err := c.CleanupDue(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Environment.Status != StatusVerifiedClean {
		t.Fatalf("expected a local-only controller to still reach verified_clean directly, got %+v", results)
	}
}
