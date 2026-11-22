package playground

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func cloudTags(id, owner string) map[string]string {
	return map[string]string{CloudTagEnvironmentID: id, CloudTagOwner: owner, CloudTagManagedBy: CloudManagedByValue}
}

func TestCleanupDueSucceedsWhenCloudResourcesAreCleanlyRemoved(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-clean")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, cloudTags(env.ID, "alice"), 0, 0, 0)

	c := Controller{
		Manager:            m,
		Now:                func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:              noSleep,
		MaxCleanupAttempts: 1,
		RetryBackoff:       0,
		Cloud:              fake,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	results, err := c.CleanupDue(context.Background(), root)
	if err != nil {
		t.Fatalf("expected a clean cloud removal to succeed, got %v", err)
	}
	if len(results) != 1 || results[0].Environment.Status != StatusVerifiedClean {
		t.Fatalf("expected verified_clean, got %+v", results)
	}
	if fake.Has("forgestack-clean") {
		t.Error("the bucket should have been removed")
	}
}

// TestCleanupDueDowngradesWhenCloudResourcesCannotBeVerifiedAbsent is the
// cloud equivalent of the existing Docker downgrade test.
func TestCleanupDueDowngradesWhenCloudResourcesCannotBeVerifiedAbsent(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-stuck")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, cloudTags(env.ID, "alice"), 0, 0, 0)
	fake.RemoveErr = errors.New("simulated cloud removal failure")

	c := Controller{
		Manager:            m,
		Now:                func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:              noSleep,
		MaxCleanupAttempts: 1,
		RetryBackoff:       0,
		Cloud:              fake,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	results, err := c.CleanupDue(context.Background(), root)
	if err == nil {
		t.Fatal("expected an error when a tracked cloud resource cannot be removed/verified absent")
	}
	if len(results) != 1 || results[0].Environment.Status != StatusCleanupUnverified {
		t.Fatalf("expected the local-clean-but-cloud-dirty environment to be downgraded to CLEANUP_UNVERIFIED, got %+v", results)
	}
	if !fake.Has("forgestack-stuck") {
		t.Error("the simulated-failed-removal bucket should still be present in the fake")
	}
}

// TestCleanupDueReportsBothDockerAndCloudFailuresTogether proves the two
// additive domains are independent: both are attempted even though
// Docker fails, and the final result names both.
func TestCleanupDueReportsBothDockerAndCloudFailuresTogether(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.TrackDockerResource(root, env.ID, "alice", TrackedDockerResource{Kind: DockerContainer, Ref: "abc123"}); err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-both")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}

	dockerFake := NewFakeDockerClient()
	dockerFake.Seed(DockerContainer, "abc123", map[string]string{DockerLabelEnvironmentID: env.ID})
	dockerFake.RemoveErr = errors.New("simulated docker removal failure")

	cloudFake := NewFakeCloudClient()
	cloudFake.SeedBucket(ref, cloudTags(env.ID, "alice"), 0, 0, 0)
	cloudFake.RemoveErr = errors.New("simulated cloud removal failure")

	c := Controller{
		Manager:            m,
		Now:                func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:              noSleep,
		MaxCleanupAttempts: 1,
		RetryBackoff:       0,
		Docker:             dockerFake,
		Cloud:              cloudFake,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	results, err := c.CleanupDue(context.Background(), root)
	if err == nil {
		t.Fatal("expected an error when both docker and cloud resources fail to verify absent")
	}
	if len(results) != 1 || results[0].Environment.Status != StatusCleanupUnverified {
		t.Fatalf("expected downgrade to CLEANUP_UNVERIFIED, got %+v", results)
	}
	if !dockerFake.Has("abc123") {
		t.Error("docker's resource should still be present -- docker failure must still be attempted and reported even though cloud also fails")
	}
	if !cloudFake.Has("forgestack-both") {
		t.Error("cloud's resource should still be present -- cloud failure must still be attempted and reported even though docker also fails")
	}
	msg := results[0].Cleanup
	if !strings.Contains(msg, "docker") || !strings.Contains(msg, "cloud") {
		t.Errorf("expected the cleanup message to name both failed domains, got %q", msg)
	}
}

// TestCleanupDueRecoversFromTransientCloudFailureOnASubsequentPass proves
// a throttled Remove call is retried by the *next* overall CleanupDue
// pass (the same cadence Docker failures already rely on), not by an
// internal per-call retry loop -- matching this package's existing
// "a transient failure surfaces as cleanup_unverified for one pass, and
// the next pass tries again" behavior.
func TestCleanupDueRecoversFromTransientCloudFailureOnASubsequentPass(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-transient")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, cloudTags(env.ID, "alice"), 0, 0, 0)
	fake.RemoveFailuresRemaining = 1 // fails once, then succeeds

	c := Controller{
		Manager:            m,
		Now:                func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:              noSleep,
		MaxCleanupAttempts: 1,
		RetryBackoff:       0,
		Cloud:              fake,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}

	firstPass, err := c.CleanupDue(context.Background(), root)
	if err == nil {
		t.Fatal("expected the first pass to fail on the injected transient throttle")
	}
	if len(firstPass) != 1 || firstPass[0].Environment.Status != StatusCleanupUnverified {
		t.Fatalf("expected the first pass to downgrade to CLEANUP_UNVERIFIED, got %+v", firstPass)
	}

	secondPass, err := c.CleanupDue(context.Background(), root)
	if err != nil {
		t.Fatalf("expected the second pass to succeed once the transient failure clears, got %v", err)
	}
	if len(secondPass) != 1 || secondPass[0].Environment.Status != StatusVerifiedClean {
		t.Fatalf("expected the second pass to reach verified_clean, got %+v", secondPass)
	}
	if fake.Has("forgestack-transient") {
		t.Error("the bucket should have been removed on the second pass")
	}
}

// TestCleanupDueNeverAttemptsCloudForProductEnvironment proves the
// existing product-environment protection (automatic cleanup is
// forbidden for persistent product environments, enforced in
// loadOwnedEnvironment) still applies once a Cloud client is configured
// -- a product environment with a tracked cloud resource must never be
// touched by CleanupDue at all.
func TestCleanupDueNeverAttemptsCloudForProductEnvironment(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	if _, err := m.Create(root, "alice", "product", 0, nil); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()

	c := Controller{
		Manager:            m,
		Now:                func() time.Time { return time.Now().Add(100 * 365 * 24 * time.Hour) },
		Sleep:              noSleep,
		MaxCleanupAttempts: 1,
		RetryBackoff:       0,
		Cloud:              fake,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	results, err := c.CleanupDue(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected a product environment to never be touched by CleanupDue, got %+v", results)
	}
	if fake.CallCount("Remove") != 0 || fake.CallCount("VerifyOwnership") != 0 {
		t.Errorf("expected zero cloud calls for a product environment, got calls: %v", fake.Calls())
	}
}
