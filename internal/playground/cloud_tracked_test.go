package playground

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTrackCloudResourceRejectsOwnershipMismatch(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "forgestack-env-abc"}
	if err := m.TrackCloudResource(root, env.ID, "bob", ref); err == nil {
		t.Fatal("expected tracking a resource under the wrong owner to be rejected")
	}
	tracked, err := loadTrackedCloudResources(filepath.Join(root, env.ID))
	if err != nil {
		t.Fatal(err)
	}
	if len(tracked) != 0 {
		t.Errorf("expected no resource to be tracked after a rejected ownership mismatch, got %+v", tracked)
	}
}

func TestTrackCloudResourceRejectsInvalidID(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	ref := CloudResourceRef{Kind: CloudS3Bucket, Ref: "forgestack-env-abc"}
	if err := m.TrackCloudResource(root, "../escape", "alice", ref); err == nil {
		t.Fatal("expected an invalid environment ID to be rejected")
	}
}

// TestTrackCloudResourceRejectsUnsupportedKind enforces the v1 scope
// boundary (decision: S3 bucket cleanup only, no EC2/EKS/RDS/NAT Gateway)
// as an actual code-level guard, not just a comment -- tracking any other
// kind must fail before anything is ever persisted.
func TestTrackCloudResourceRejectsUnsupportedKind(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	unsupported := CloudResourceRef{Kind: CloudResourceKind("ec2_instance"), Ref: "i-0123456789"}
	if err := m.TrackCloudResource(root, env.ID, "alice", unsupported); err == nil {
		t.Fatal("expected tracking an unsupported cloud resource kind to be rejected")
	}
	tracked, err := loadTrackedCloudResources(filepath.Join(root, env.ID))
	if err != nil {
		t.Fatal(err)
	}
	if len(tracked) != 0 {
		t.Errorf("expected no resource to be tracked after an unsupported-kind rejection, got %+v", tracked)
	}
}

// TestTrackCloudResourceSurvivesLocalCleanup proves cloud-resources.json
// is persisted as a sibling of environment.json (never inside the
// disposable resources/ subdirectory Cleanup removes) -- required so
// that an environment's cloud-ownership record is never lost merely
// because its local resources were cleaned up first.
//
// It also proves the P0 fix from the independent audit: Manager.Cleanup
// (called directly here, exactly as the `playground cleanup` CLI command
// does, with no CloudClient at all) must NOT persist verified_clean
// while a tracked cloud resource remains unverified -- see
// guardVerifiedCleanStatus in lifecycle.go. Before the fix, this call
// returned a nil error and silently wrote verified_clean despite never
// checking the bucket; see TestManagerCleanupDirect_* in
// cloud_manager_guard_test.go for the dedicated regression coverage of
// that defect.
func TestTrackCloudResourceSurvivesLocalCleanup(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "forgestack-env-abc"}
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	result, err := m.Cleanup(root, env.ID, "alice")
	if err == nil {
		t.Fatal("expected direct Manager.Cleanup to refuse verified_clean while a tracked cloud resource remains unverified")
	}
	if result.Environment.Status != StatusCleanupPending {
		t.Fatalf("expected the environment to be left cleanup_pending (retryable), got %q", result.Environment.Status)
	}
	tracked, err := loadTrackedCloudResources(filepath.Join(root, env.ID))
	if err != nil {
		t.Fatal(err)
	}
	if len(tracked) != 1 || tracked[0] != ref {
		t.Fatalf("expected the tracked cloud resource record to survive local cleanup, got %+v", tracked)
	}
}
