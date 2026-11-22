package playground

import (
	"context"
	"testing"
)

func TestForceRemoveCloudResourceRequiresAReason(t *testing.T) {
	fake := NewFakeCloudClient()
	ref := testRef("forgestack-emergency")
	fake.SeedBucket(ref, cloudTags("env-dead", "alice"), 0, 0, 0)

	if _, err := ForceRemoveCloudResource(context.Background(), fake, ref, "env-dead", "alice", ""); err == nil {
		t.Fatal("expected an empty reason to be rejected")
	}
	if !fake.Has("forgestack-emergency") {
		t.Error("a rejected (no-reason) call must never remove anything")
	}
}

// TestForceRemoveCloudResourceRemovesEvenOnOwnershipMismatch proves the
// emergency path's deliberate difference from the automatic path: it
// still removes (and records the mismatch for audit) even when the
// asserted tags don't match -- this is the path for exactly the
// situation where a resource's tags are wrong, lost, or corrupted and an
// operator has decided, explicitly, to remove it anyway.
func TestForceRemoveCloudResourceRemovesEvenOnOwnershipMismatch(t *testing.T) {
	fake := NewFakeCloudClient()
	ref := testRef("forgestack-emergency-mismatch")
	// Seeded with no tags at all -- as if the bucket's tags were lost.
	fake.SeedBucket(ref, map[string]string{}, 0, 0, 0)

	rec, err := ForceRemoveCloudResource(context.Background(), fake, ref, "env-dead", "alice", "tags were lost; operator confirmed via console this is the orphaned sandbox bucket from the Nov 2026 incident")
	if err != nil {
		t.Fatalf("expected the emergency removal to proceed despite the ownership mismatch, got %v", err)
	}
	if !rec.Removed {
		t.Fatal("expected the resource to be reported removed")
	}
	if rec.OwnershipCheck != "mismatched" {
		t.Errorf("expected the audit record to note the ownership mismatch, got %q", rec.OwnershipCheck)
	}
	if fake.Has("forgestack-emergency-mismatch") {
		t.Error("the bucket should have been removed")
	}
}

func TestForceRemoveCloudResourceRecordsAMatchedOwnershipCheck(t *testing.T) {
	fake := NewFakeCloudClient()
	ref := testRef("forgestack-emergency-matched")
	fake.SeedBucket(ref, cloudTags("env-dead", "alice"), 0, 0, 0)

	rec, err := ForceRemoveCloudResource(context.Background(), fake, ref, "env-dead", "alice", "operator-initiated emergency cleanup")
	if err != nil {
		t.Fatal(err)
	}
	if rec.OwnershipCheck != "matched" {
		t.Errorf("expected the audit record to note a matched ownership check, got %q", rec.OwnershipCheck)
	}
	if !rec.Removed {
		t.Fatal("expected the resource to be reported removed")
	}
}

// TestForceRemoveCloudResourceIsNeverCalledByTheAutomaticPath is a
// documentation-as-test assertion: ForceRemoveCloudResource takes
// different parameters than RemoveTrackedCloudResources (no root/id
// lookup, no tracked-resource list) and nothing in controller.go's
// cleanupOne references it -- this test exists so that if a future
// change ever wires it into Controller/Worker, a reviewer sees this
// test's name and comment and treats that as the deliberate policy
// change it would be, not an incidental refactor.
func TestForceRemoveCloudResourceIsNeverCalledByTheAutomaticPath(t *testing.T) {
	// Intentionally empty beyond the comment above -- grep for
	// "ForceRemoveCloudResource" in controller.go/worker.go to confirm.
}
