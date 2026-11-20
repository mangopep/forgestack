package playground

import (
	"context"
	"testing"
	"time"
)

func testRef(id string) CloudResourceRef {
	return CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: id}
}

func TestCloudReconcileDetectsOrphanedAndMissingResources(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Tracked but will not exist in the cloud -> Missing.
	gone := testRef("forgestack-gone")
	if err := m.TrackCloudResource(root, env.ID, "alice", gone); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	// Present under this exact environment's tag but never tracked -> Orphaned.
	untracked := testRef("forgestack-untracked")
	fake.SeedBucket(untracked, map[string]string{CloudTagEnvironmentID: env.ID, CloudTagOwner: "alice", CloudTagManagedBy: CloudManagedByValue}, 0, 0, 0)
	// A completely different environment's resource must never show up
	// in this environment's reconciliation.
	other := testRef("forgestack-other-env")
	fake.SeedBucket(other, map[string]string{CloudTagEnvironmentID: "env-0000000000000000", CloudTagOwner: "bob", CloudTagManagedBy: CloudManagedByValue}, 0, 0, 0)

	rec, err := CloudReconcile(context.Background(), fake, root, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.CloudAvailable {
		t.Fatal("expected CloudAvailable to be true for a working fake")
	}
	if len(rec.Missing) != 1 || rec.Missing[0].Ref != "forgestack-gone" {
		t.Errorf("expected 'forgestack-gone' to be reported missing, got %+v", rec.Missing)
	}
	if len(rec.Orphaned) != 1 || rec.Orphaned[0].Ref != "forgestack-untracked" {
		t.Errorf("expected 'forgestack-untracked' to be reported orphaned, got %+v", rec.Orphaned)
	}
	for _, p := range rec.Present {
		if p.Ref == "forgestack-other-env" {
			t.Error("a different environment's resource must never appear in this environment's Present list")
		}
	}
}

func TestCloudReconcileReportsUnavailableRatherThanAssumingClean(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"throttled", ErrCloudThrottled},
		{"credential expired", ErrCloudCredentialExpired},
		{"generic unavailable", ErrCloudUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			m := NewManager()
			env, err := m.Create(root, "alice", "playground", time.Minute, nil)
			if err != nil {
				t.Fatal(err)
			}
			fake := NewFakeCloudClient()
			fake.ListErr = tc.err
			rec, err := CloudReconcile(context.Background(), fake, root, env.ID)
			if err != nil {
				t.Fatal(err)
			}
			if rec.CloudAvailable {
				t.Fatal("expected CloudAvailable to be false when the client reports unavailable")
			}
			if rec.Clean() {
				t.Fatal("an unavailable cloud check must never report Clean() == true -- absence of evidence is not evidence of absence")
			}
		})
	}
}

// TestRemoveTrackedCloudResourcesNeverTouchesAnotherEnvironment is the
// strict-ownership test for cloud specifically: two environments each
// have a tracked, present bucket; removing one environment's resources
// must leave the other's completely untouched, verified both by the
// fake's internal state and by independently reconciling the untouched
// one.
func TestRemoveTrackedCloudResourcesNeverTouchesAnotherEnvironment(t *testing.T) {
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
	refA := testRef("forgestack-a-bucket")
	refB := testRef("forgestack-b-bucket")
	if err := m.TrackCloudResource(root, envA.ID, "alice", refA); err != nil {
		t.Fatal(err)
	}
	if err := m.TrackCloudResource(root, envB.ID, "bob", refB); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(refA, map[string]string{CloudTagEnvironmentID: envA.ID, CloudTagOwner: "alice", CloudTagManagedBy: CloudManagedByValue}, 0, 0, 0)
	fake.SeedBucket(refB, map[string]string{CloudTagEnvironmentID: envB.ID, CloudTagOwner: "bob", CloudTagManagedBy: CloudManagedByValue}, 0, 0, 0)

	after, err := RemoveTrackedCloudResources(context.Background(), fake, root, envA.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !after.Clean() {
		t.Fatalf("expected env A to be clean after removal, got %+v", after)
	}
	if fake.Has("forgestack-a-bucket") {
		t.Error("env A's bucket should have been removed")
	}
	if !fake.Has("forgestack-b-bucket") {
		t.Fatal("env B's bucket must survive env A's cleanup untouched")
	}

	recB, err := CloudReconcile(context.Background(), fake, root, envB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recB.Present) != 1 || recB.Present[0].Ref != "forgestack-b-bucket" {
		t.Errorf("expected env B to still show its bucket present, got %+v", recB)
	}
}

// TestRemoveTrackedCloudResourcesHandlesNonEmptyBucketThenSucceeds proves
// the ORCHESTRATION layer (RemoveTrackedCloudResources) correctly treats
// a Remove call that succeeds for a seeded non-empty bucket as success --
// nothing more. It does NOT exercise any real per-object/version/
// delete-marker/multipart-upload removal logic, because no such logic
// exists anywhere in this codebase yet: FakeCloudClient.Remove always
// succeeds for a non-empty bucket unless it's named in UnemptiableRefs
// (see TestRemoveTrackedCloudResourcesReportsUnemptiableBucketAsFailure
// for that failure path). Real S3 emptying semantics (versions, delete
// markers, in-progress multipart uploads) are explicitly AWS_NOT_VERIFIED
// -- see docs/architecture/CLOUD-CLEANUP-S3.md -- until a real
// AWS-SDK-backed CloudClient exists and is tested against them.
func TestRemoveTrackedCloudResourcesHandlesNonEmptyBucketThenSucceeds(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-nonempty")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, map[string]string{CloudTagEnvironmentID: env.ID, CloudTagOwner: "alice", CloudTagManagedBy: CloudManagedByValue}, 3, 2, 1)

	after, err := RemoveTrackedCloudResources(context.Background(), fake, root, env.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !after.Clean() {
		t.Fatalf("expected a non-empty-but-emptiable bucket to be fully removed, got %+v", after)
	}
}

// TestRemoveTrackedCloudResourcesReportsUnemptiableBucketAsFailure proves
// the inverse: a bucket this package cannot safely empty (e.g. an object
// lock/legal hold in the real world) must never be reported as removed.
func TestRemoveTrackedCloudResourcesReportsUnemptiableBucketAsFailure(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-locked")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, map[string]string{CloudTagEnvironmentID: env.ID, CloudTagOwner: "alice", CloudTagManagedBy: CloudManagedByValue}, 1, 0, 0)
	fake.UnemptiableRefs = map[string]bool{"forgestack-locked": true}

	after, err := RemoveTrackedCloudResources(context.Background(), fake, root, env.ID, "alice")
	if err == nil {
		t.Fatal("expected an error when a bucket cannot be safely emptied")
	}
	if after.Clean() {
		t.Fatalf("expected the unemptiable bucket to still be reported present, got %+v", after)
	}
	if !fake.Has("forgestack-locked") {
		t.Error("the unemptiable bucket must still exist -- it must never be reported removed")
	}
}

// TestRemoveTrackedCloudResourcesRefusesWhenOwnershipDriftsBeforeRemoval
// is the requirement-7 test: ownership must be independently re-verified
// via a direct read immediately before deletion, not inferred from an
// earlier ListByTag result. Here the bucket's owner tag changes after
// being listed as present but before VerifyOwnership is called for it --
// simulating exactly the time-of-check-to-time-of-use window
// VerifyOwnership exists to close.
func TestRemoveTrackedCloudResourcesRefusesWhenOwnershipDriftsBeforeRemoval(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-drifted")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, map[string]string{CloudTagEnvironmentID: env.ID, CloudTagOwner: "alice", CloudTagManagedBy: CloudManagedByValue}, 0, 0, 0)
	// Tags drift between the listing CloudReconcile does internally and
	// the VerifyOwnership check RemoveTrackedCloudResources performs
	// immediately before removal.
	fake.DriftTag(ref, CloudTagOwner, "someone-else")

	after, err := RemoveTrackedCloudResources(context.Background(), fake, root, env.ID, "alice")
	if err == nil {
		t.Fatal("expected an error when ownership no longer verifies immediately before removal")
	}
	if after.Clean() {
		t.Fatalf("expected the drifted-ownership bucket to still be reported present, got %+v", after)
	}
	if !fake.Has("forgestack-drifted") {
		t.Error("a bucket whose ownership tags no longer match must never be removed")
	}
}

// TestRemoveTrackedCloudResourcesRefusesWhenManagedByTagMissing proves a
// bucket that happens to carry a matching environment-id tag (coincidence
// or operator error) but was never tagged forgestack.managed-by is never
// treated as this package's to remove.
func TestRemoveTrackedCloudResourcesRefusesWhenManagedByTagMissing(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-untagged-managedby")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	// No CloudTagManagedBy tag at all.
	fake.SeedBucket(ref, map[string]string{CloudTagEnvironmentID: env.ID, CloudTagOwner: "alice"}, 0, 0, 0)

	after, err := RemoveTrackedCloudResources(context.Background(), fake, root, env.ID, "alice")
	if err == nil {
		t.Fatal("expected an error when the managed-by tag is missing")
	}
	if after.Clean() {
		t.Fatalf("expected the bucket to still be reported present, got %+v", after)
	}
	if !fake.Has("forgestack-untagged-managedby") {
		t.Error("a bucket missing the managed-by tag must never be removed")
	}
}

// TestRemoveTrackedCloudResourcesVerifiesOwnershipBeforeEveryRemove
// proves VerifyOwnership is actually called, once per resource,
// immediately before the matching Remove call -- not skipped, not
// batched, not called only once for multiple resources.
func TestRemoveTrackedCloudResourcesVerifiesOwnershipBeforeEveryRemove(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	refA := testRef("forgestack-verify-a")
	refB := testRef("forgestack-verify-b")
	if err := m.TrackCloudResource(root, env.ID, "alice", refA); err != nil {
		t.Fatal(err)
	}
	if err := m.TrackCloudResource(root, env.ID, "alice", refB); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	tags := map[string]string{CloudTagEnvironmentID: env.ID, CloudTagOwner: "alice", CloudTagManagedBy: CloudManagedByValue}
	fake.SeedBucket(refA, tags, 0, 0, 0)
	fake.SeedBucket(refB, tags, 0, 0, 0)

	if _, err := RemoveTrackedCloudResources(context.Background(), fake, root, env.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	if got := fake.CallCount("VerifyOwnership"); got != 2 {
		t.Errorf("expected exactly 2 VerifyOwnership calls (one per resource), got %d: %v", got, fake.Calls())
	}
	if got := fake.CallCount("Remove"); got != 2 {
		t.Errorf("expected exactly 2 Remove calls, got %d: %v", got, fake.Calls())
	}
}
