package playground

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNewCloudClient_UnknownProviderIsRejected(t *testing.T) {
	if _, _, err := NewCloudClient("ec2", CloudModeSimulated, "123456789012", "us-east-1", t.TempDir()); err == nil {
		t.Fatal("expected an error for an unregistered provider")
	}
}

func TestNewCloudClient_LiveModeFailsClosedBeforeAnythingElse(t *testing.T) {
	// Deliberately passes blank account/region/root too -- if live mode
	// were ever accidentally reordered to check those first, this test
	// would catch it reporting the wrong error.
	_, _, err := NewCloudClient("s3", CloudModeLive, "", "", "")
	if err == nil {
		t.Fatal("expected live mode to be rejected")
	}
	if !errors.Is(err, errLiveCloudModeNotAvailable) {
		t.Fatalf("expected errLiveCloudModeNotAvailable, got %v", err)
	}
}

func TestNewCloudClient_UnknownModeIsRejected(t *testing.T) {
	if _, _, err := NewCloudClient("s3", "turbo", "123456789012", "us-east-1", t.TempDir()); err == nil {
		t.Fatal("expected an error for an unrecognized mode")
	}
}

func TestNewCloudClient_SimulatedRequiresAccountAndRegion(t *testing.T) {
	if _, _, err := NewCloudClient("s3", CloudModeSimulated, "", "us-east-1", t.TempDir()); err == nil {
		t.Fatal("expected an error for a blank account")
	}
	if _, _, err := NewCloudClient("s3", CloudModeSimulated, "123456789012", "", t.TempDir()); err == nil {
		t.Fatal("expected an error for a blank region")
	}
}

func TestNewCloudClient_SimulatedReturnsScopedClientLabeledSimulated(t *testing.T) {
	client, label, err := NewCloudClient("s3", CloudModeSimulated, "123456789012", "us-east-1", t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := client.(*scopedSimulatedCloudClient); !ok {
		t.Fatalf("expected a *scopedSimulatedCloudClient, got %T", client)
	}
	if !strings.Contains(label, "SIMULATED") {
		t.Fatalf("expected the label to say SIMULATED, got %q", label)
	}
	// The architecture's own verification-mode derivation (never a
	// caller-set flag) must independently agree this is SIMULATED.
	if mode := cloudVerificationMode(client); mode != VerificationSimulated {
		t.Fatalf("expected cloudVerificationMode to report %q, got %q", VerificationSimulated, mode)
	}
}

func TestNewCloudClient_CrossScopeRefIsRejectedByReturnedClient(t *testing.T) {
	root := t.TempDir()
	env, err := NewManager().Create(root, "alice", "playground", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	outOfScope := CloudResourceRef{Kind: CloudS3Bucket, Account: "999999999999", Region: "eu-west-1", Ref: "alice-bucket"}
	if err := NewManager().TrackCloudResource(root, env.ID, "alice", outOfScope); err != nil {
		t.Fatal(err)
	}

	client, _, err := NewCloudClient("s3", CloudModeSimulated, "123456789012", "us-east-1", root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Exists(context.Background(), outOfScope); !errors.Is(err, errSimulatedScopeMismatch) {
		t.Fatalf("expected Exists to refuse a cross-scope ref with errSimulatedScopeMismatch, got %v", err)
	}
	if _, err := client.VerifyOwnership(context.Background(), outOfScope, nil); !errors.Is(err, errSimulatedScopeMismatch) {
		t.Fatalf("expected VerifyOwnership to refuse a cross-scope ref with errSimulatedScopeMismatch, got %v", err)
	}
	if err := client.Remove(context.Background(), outOfScope); !errors.Is(err, errSimulatedScopeMismatch) {
		t.Fatalf("expected Remove to refuse a cross-scope ref with errSimulatedScopeMismatch, got %v", err)
	}
}

func TestKnownCloudProviders_ListsS3(t *testing.T) {
	providers := KnownCloudProviders()
	found := false
	for _, p := range providers {
		if p == "s3" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected %q in %v", "s3", providers)
	}
}

func TestSeedSimulatedS3Fixtures_DefaultBucketIsCleanAndOwned(t *testing.T) {
	root := t.TempDir()
	env, err := NewManager().Create(root, "alice", "playground", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "123456789012", Region: "us-east-1", Ref: "alice-bucket"}
	if err := NewManager().TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}

	fc := NewFakeCloudClient()
	if err := seedSimulatedS3Fixtures(fc, root, "123456789012", "us-east-1"); err != nil {
		t.Fatal(err)
	}
	ok, err := fc.VerifyOwnership(context.Background(), ref, map[string]string{
		CloudTagEnvironmentID: env.ID,
		CloudTagOwner:         "alice",
		CloudTagManagedBy:     CloudManagedByValue,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected the default fixture to be correctly tagged/owned")
	}
}

func TestSeedSimulatedS3Fixtures_MissingTagSuffixIsNeverOwned(t *testing.T) {
	root := t.TempDir()
	env, err := NewManager().Create(root, "alice", "playground", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "123456789012", Region: "us-east-1", Ref: "alice-bucket-missing-tag"}
	if err := NewManager().TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}

	fc := NewFakeCloudClient()
	if err := seedSimulatedS3Fixtures(fc, root, "123456789012", "us-east-1"); err != nil {
		t.Fatal(err)
	}
	ok, err := fc.VerifyOwnership(context.Background(), ref, map[string]string{
		CloudTagEnvironmentID: env.ID,
		CloudTagOwner:         "alice",
		CloudTagManagedBy:     CloudManagedByValue,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected the -missing-tag fixture to never verify ownership")
	}
	if !fc.HasRef(ref) {
		t.Fatal("expected the -missing-tag bucket to still exist (drifted tags, not deleted)")
	}
}

func TestSeedSimulatedS3Fixtures_WrongAccountRegionIsNeverSeeded(t *testing.T) {
	root := t.TempDir()
	env, err := NewManager().Create(root, "alice", "playground", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "999999999999", Region: "eu-west-1", Ref: "alice-bucket"}
	if err := NewManager().TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}

	fc := NewFakeCloudClient()
	// Simulated client scoped to a DIFFERENT account/region than the
	// tracked ref.
	if err := seedSimulatedS3Fixtures(fc, root, "123456789012", "us-east-1"); err != nil {
		t.Fatal(err)
	}
	if fc.HasRef(ref) {
		t.Fatal("expected a tracked resource in a different account/region to never be seeded into this client's scope")
	}
}

func TestSeedSimulatedS3Fixtures_FlakySuffixConvergesAcrossPersistedAttempts(t *testing.T) {
	root := t.TempDir()
	env, err := NewManager().Create(root, "alice", "playground", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "123456789012", Region: "us-east-1", Ref: "alice-bucket-flaky-2"}
	if err := NewManager().TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	dir, err := environmentDir(root, env.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Attempt 0 (no prior attempts persisted yet): 2 failures still owed.
	fc := NewFakeCloudClient()
	if err := seedSimulatedS3Fixtures(fc, root, "123456789012", "us-east-1"); err != nil {
		t.Fatal(err)
	}
	if err := fc.Remove(context.Background(), ref); err == nil {
		t.Fatal("expected the first simulated attempt to fail")
	}
	// Simulate the controller persisting one failed attempt, the way
	// attemptCloudCleanup (controller.go) really does.
	if err := saveCloudCleanupAttemptState(dir, cloudCleanupAttemptState{Attempts: 1}); err != nil {
		t.Fatal(err)
	}

	// Attempt 1 (1 prior attempt persisted, simulating a process
	// restart with a fresh FakeCloudClient): 1 failure still owed.
	fc = NewFakeCloudClient()
	if err := seedSimulatedS3Fixtures(fc, root, "123456789012", "us-east-1"); err != nil {
		t.Fatal(err)
	}
	if err := fc.Remove(context.Background(), ref); err == nil {
		t.Fatal("expected the second simulated attempt to still fail")
	}
	if err := saveCloudCleanupAttemptState(dir, cloudCleanupAttemptState{Attempts: 2}); err != nil {
		t.Fatal(err)
	}

	// Attempt 2 (2 prior attempts persisted): 0 failures owed, succeeds.
	fc = NewFakeCloudClient()
	if err := seedSimulatedS3Fixtures(fc, root, "123456789012", "us-east-1"); err != nil {
		t.Fatal(err)
	}
	if err := fc.Remove(context.Background(), ref); err != nil {
		t.Fatalf("expected the third simulated attempt to succeed, got %v", err)
	}
}
