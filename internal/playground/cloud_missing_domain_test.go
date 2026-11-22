package playground

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// This file contains permanent regression tests for the third
// independent audit's finding: Controller.verifyAdditionalDomains could
// persist StatusVerifiedClean directly (via its own c.persist call,
// never routed through Manager.cleanup/guardVerifiedCleanStatus) while
// a tracked resource in a domain THIS Controller simply has no client
// for was silently never checked at all. Example: Controller.Cloud
// configured, Controller.Docker nil, environment tracks both a Docker
// and a cloud resource -- cloud verification succeeds, Docker is
// skipped entirely, and the old code persisted verified_clean anyway.

func seedTrackedEnv(t *testing.T, root string, trackDocker, trackCloud bool) (Environment, *FakeDockerClient, *FakeCloudClient) {
	t.Helper()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	var dockerFake *FakeDockerClient
	if trackDocker {
		if err := m.TrackDockerResource(root, env.ID, "alice", TrackedDockerResource{Kind: DockerContainer, Ref: "stray-container"}); err != nil {
			t.Fatal(err)
		}
		dockerFake = NewFakeDockerClient()
		dockerFake.Seed(DockerContainer, "stray-container", map[string]string{DockerLabelEnvironmentID: env.ID})
	}
	var cloudFake *FakeCloudClient
	if trackCloud {
		ref := testRef("forgestack-missing-domain-" + env.ID)
		if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
			t.Fatal(err)
		}
		cloudFake = NewFakeCloudClient()
		cloudFake.SeedBucket(ref, cloudTags(env.ID, "alice"), 0, 0, 0)
	}
	return env, dockerFake, cloudFake
}

// TestVerifyAdditionalDomains_CloudOnlyConfigured_DockerTracked_NeverVerifiedClean
// is the exact repro from the audit: Cloud configured, Docker nil,
// both resources tracked.
func TestVerifyAdditionalDomains_CloudOnlyConfigured_DockerTracked_NeverVerifiedClean(t *testing.T) {
	root := t.TempDir()
	env, dockerFake, cloudFake := seedTrackedEnv(t, root, true, true)
	// This Controller never gets a Docker client -- the repro.
	cloudFake.SeedBucket(testRef("unused"), nil, 0, 0, 0) // no-op, keep cloudFake referenced if unseeded above
	_ = dockerFake

	c := Controller{
		Manager:            NewManager(),
		Now:                func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:              noSleep,
		MaxCleanupAttempts: 1,
		Cloud:              cloudFake,
		// Docker intentionally nil.
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	results, err := c.CleanupDue(context.Background(), root)
	if err == nil {
		t.Fatal("expected cleanup to report an error: a tracked docker resource could not be verified")
	}
	if len(results) != 1 {
		t.Fatalf("expected exactly one result, got %+v", results)
	}
	if results[0].Environment.Status == StatusVerifiedClean {
		t.Fatalf("AUDIT REGRESSION: persisted verified_clean while a tracked docker resource was never checked (no Docker client configured), got %+v", results[0])
	}
	envs, err := ListEnvironments(root)
	if err != nil {
		t.Fatal(err)
	}
	if envs[0].Status == StatusVerifiedClean {
		t.Fatalf("AUDIT REGRESSION: on-disk status is verified_clean, got %q", envs[0].Status)
	}
	// Tracked-resource evidence must survive.
	tracked, err := loadTrackedDockerResources(filepath.Join(root, env.ID))
	if err != nil {
		t.Fatal(err)
	}
	if len(tracked) != 1 {
		t.Fatalf("expected tracked docker resource evidence to survive, got %+v", tracked)
	}
}

// TestVerifyAdditionalDomains_DockerOnlyConfigured_CloudTracked_NeverVerifiedClean
// is the mirror-image repro.
func TestVerifyAdditionalDomains_DockerOnlyConfigured_CloudTracked_NeverVerifiedClean(t *testing.T) {
	root := t.TempDir()
	env, dockerFake, cloudFake := seedTrackedEnv(t, root, true, true)
	_ = cloudFake

	c := Controller{
		Manager:            NewManager(),
		Now:                func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:              noSleep,
		MaxCleanupAttempts: 1,
		Docker:             dockerFake,
		// Cloud intentionally nil.
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	results, err := c.CleanupDue(context.Background(), root)
	if err == nil {
		t.Fatal("expected cleanup to report an error: a tracked cloud resource could not be verified")
	}
	if len(results) != 1 || results[0].Environment.Status == StatusVerifiedClean {
		t.Fatalf("AUDIT REGRESSION: persisted verified_clean while a tracked cloud resource was never checked (no Cloud client configured), got %+v", results)
	}
	tracked, err := loadTrackedCloudResources(filepath.Join(root, env.ID))
	if err != nil {
		t.Fatal(err)
	}
	if len(tracked) != 1 {
		t.Fatalf("expected tracked cloud resource evidence to survive, got %+v", tracked)
	}
}

// TestVerifyAdditionalDomains_BothConfigured_BothTracked_VerifiedCleanOnlyAfterBothAbsent
// proves the positive case still works correctly: once both domains
// are actually configured and both resources actually removed,
// verified_clean is reached.
func TestVerifyAdditionalDomains_BothConfigured_BothTracked_VerifiedCleanOnlyAfterBothAbsent(t *testing.T) {
	root := t.TempDir()
	env, dockerFake, cloudFake := seedTrackedEnv(t, root, true, true)

	c := Controller{
		Manager:                 NewManager(),
		Now:                     func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:                   noSleep,
		MaxCleanupAttempts:      1,
		Docker:                  dockerFake,
		Cloud:                   cloudFake,
		MaxCloudCleanupAttempts: 3,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	results, err := c.CleanupDue(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Environment.Status != StatusVerifiedClean {
		t.Fatalf("expected verified_clean once both configured domains are actually clean, got %+v", results)
	}
	if dockerFake.Has("stray-container") {
		t.Fatal("expected the docker resource to actually be removed")
	}
}

// TestVerifyAdditionalDomains_NeitherConfigured_ResourcesTracked_FailsClosed
// proves the "both nil" case (already covered by Manager.cleanup's own
// guard, exercised here through the full Controller path) still fails
// closed.
func TestVerifyAdditionalDomains_NeitherConfigured_ResourcesTracked_FailsClosed(t *testing.T) {
	root := t.TempDir()
	env, _, _ := seedTrackedEnv(t, root, true, true)

	c := Controller{
		Manager:            NewManager(),
		Now:                func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:              noSleep,
		MaxCleanupAttempts: 1,
		// Neither Docker nor Cloud configured.
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	results, err := c.CleanupDue(context.Background(), root)
	if err == nil {
		t.Fatal("expected an error: tracked resources exist with no client at all configured")
	}
	if len(results) != 1 || results[0].Environment.Status == StatusVerifiedClean {
		t.Fatalf("AUDIT REGRESSION: persisted verified_clean with no client configured at all, got %+v", results)
	}
}

// TestVerifyAdditionalDomains_CorruptTrackingMetadata_FailsClosed proves
// an unreadable tracking file for the unconfigured domain is treated
// exactly like "yes, something is tracked," never "nothing is tracked."
func TestVerifyAdditionalDomains_CorruptTrackingMetadata_FailsClosed(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, env.ID)
	if err := os.WriteFile(filepath.Join(dir, "docker-resources.json"), []byte("{not valid json"), 0600); err != nil {
		t.Fatal(err)
	}
	cloudFake := NewFakeCloudClient()

	c := Controller{
		Manager:            NewManager(),
		Now:                func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:              noSleep,
		MaxCleanupAttempts: 1,
		Cloud:              cloudFake, // Docker nil, and its tracking file is corrupt
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	results, err := c.CleanupDue(context.Background(), root)
	if err == nil {
		t.Fatal("expected an error: corrupt docker tracking metadata must fail closed")
	}
	if len(results) != 1 || results[0].Environment.Status == StatusVerifiedClean {
		t.Fatalf("AUDIT REGRESSION: persisted verified_clean despite unreadable docker tracking metadata, got %+v", results)
	}
	// Evidence (even corrupt) must survive untouched.
	data, err := os.ReadFile(filepath.Join(dir, "docker-resources.json")) //nolint:gosec // G304: dir is this test's own t.TempDir()-derived environment directory, not external input
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{not valid json" {
		t.Fatalf("expected the corrupt tracking file to be left untouched, got %q", data)
	}
}

// TestVerifyAdditionalDomains_RestartAfterMissingClientFailure_CleanupResumes
// proves requirement 7: once the missing client is supplied on a later
// pass (simulating an operator fixing the Controller's own
// configuration, or a process restart with the correct wiring),
// cleanup resumes and reaches verified_clean.
func TestVerifyAdditionalDomains_RestartAfterMissingClientFailure_CleanupResumes(t *testing.T) {
	root := t.TempDir()
	env, dockerFake, cloudFake := seedTrackedEnv(t, root, true, true)

	misconfigured := Controller{
		Manager:            NewManager(),
		Now:                func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:              noSleep,
		MaxCleanupAttempts: 1,
		Cloud:              cloudFake, // Docker missing
	}
	if _, err := misconfigured.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	if _, err := misconfigured.CleanupDue(context.Background(), root); err == nil {
		t.Fatal("expected the misconfigured pass to fail")
	}

	fixed := Controller{
		Manager:                 NewManager(),
		Now:                     func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:                   noSleep,
		MaxCleanupAttempts:      1,
		Docker:                  dockerFake,
		Cloud:                   cloudFake,
		MaxCloudCleanupAttempts: 3,
	}
	results, err := fixed.CleanupDue(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Environment.Status != StatusVerifiedClean {
		t.Fatalf("expected cleanup to resume and reach verified_clean once properly configured, got %+v", results)
	}
}

// TestVerifyAdditionalDomains_ConcurrentCleanupCannotBypassVerification
// stress-tests the fix under concurrency: many overlapping CleanupDue
// calls, half of them misconfigured (missing Docker), must never let
// the misconfigured caller's write regress/corrupt a properly-
// configured caller's eventual success, and must never produce a false
// verified_clean while the docker resource remains.
func TestVerifyAdditionalDomains_ConcurrentCleanupCannotBypassVerification(t *testing.T) {
	for iter := 0; iter < 20; iter++ {
		root := t.TempDir()
		env, dockerFake, cloudFake := seedTrackedEnv(t, root, true, true)

		misconfigured := Controller{
			Manager:            NewManager(),
			Now:                func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
			Sleep:              noSleep,
			MaxCleanupAttempts: 1,
			Cloud:              cloudFake,
		}
		fixed := Controller{
			Manager:                 NewManager(),
			Now:                     func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
			Sleep:                   noSleep,
			MaxCleanupAttempts:      1,
			Docker:                  dockerFake,
			Cloud:                   cloudFake,
			MaxCloudCleanupAttempts: 3,
		}
		if _, err := fixed.ExpireDue(root); err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		for g := 0; g < 4; g++ {
			wg.Add(1)
			c := misconfigured
			if g%2 == 0 {
				c = fixed
			}
			go func(c Controller) {
				defer wg.Done()
				_, _ = c.CleanupDue(context.Background(), root)
			}(c)
		}
		wg.Wait()
		// Drain with the properly configured Controller in case the
		// misconfigured one left it cleanup_unverified this round.
		_, _ = fixed.CleanupDue(context.Background(), root)

		envs, err := ListEnvironments(root)
		if err != nil {
			t.Fatal(err)
		}
		status := envs[0].Status
		dockerGone := !dockerFake.Has("stray-container")
		if status == StatusVerifiedClean && !dockerGone {
			t.Fatalf("iter %d: AUDIT REGRESSION: verified_clean persisted while the docker resource is still present", iter)
		}
	}
}

// TestManagerCleanup_PublicPathsShareTheSameInvariant proves both
// public cleanup paths this package exposes -- Controller.CleanupDue
// and the direct Manager.Cleanup call the CLI's `cleanup` command uses
// -- are guarded by the same underlying tracked-file check (now shared
// via trackedDockerResourcesUnverifiable/trackedCloudResourcesUnverifiable,
// lifecycle.go), not two independently-maintained decisions.
func TestManagerCleanup_PublicPathsShareTheSameInvariant(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.TrackDockerResource(root, env.ID, "alice", TrackedDockerResource{Kind: DockerContainer, Ref: "x"}); err != nil {
		t.Fatal(err)
	}

	// Path 1: direct Manager.Cleanup (what the CLI's `cleanup` command calls).
	_, err1 := m.Cleanup(root, env.ID, "alice")
	if err1 == nil {
		t.Fatal("expected Manager.Cleanup to refuse verified_clean")
	}

	// Path 2: Controller.CleanupDue with Cloud configured but Docker nil.
	cloudFake := NewFakeCloudClient()
	c := Controller{Manager: NewManager(), Now: func() time.Time { return time.Now().UTC() }, Sleep: noSleep, MaxCleanupAttempts: 1, Cloud: cloudFake}
	env2, err := ListEnvironments(root)
	if err != nil {
		t.Fatal(err)
	}
	results, err2 := c.cleanupOne(context.Background(), root, env2[0])
	if err2 == nil {
		t.Fatal("expected Controller.cleanupOne to refuse verified_clean too")
	}
	if results.Environment.Status == StatusVerifiedClean {
		t.Fatalf("AUDIT REGRESSION: Controller.cleanupOne persisted verified_clean, got %+v", results)
	}
}
