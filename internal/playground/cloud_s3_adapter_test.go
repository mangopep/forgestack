package playground

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

// All tests in this file exercise S3Adapter (cloud_s3_adapter.go) purely
// against FakeS3API (cloud_s3_fake_test.go) -- entirely in-memory, no
// real *s3.Client, no AWS credentials, no network call, no AWS endpoint.
// See docs/architecture/CLOUD-CLEANUP-S3.md's "SIMULATED vs AWS-LIVE"
// section: every PASS in this file proves S3Adapter's own orchestration
// logic (pagination, batching, error mapping, scope checks) against a
// hand-written model of S3's documented behavior -- it does NOT prove a
// real AWS account behaves identically. That is the NOT EXECUTED live
// validation plan's job.

func adapterFor(api *FakeS3API) *S3Adapter {
	return NewS3Adapter(api, "111111111111", "ap-south-1")
}

func TestNewS3AdapterTouchesNothingAtConstruction(t *testing.T) {
	api := NewFakeS3API()
	_ = adapterFor(api)
	if got := api.CallCount(""); got != 0 {
		t.Fatalf("expected zero API calls merely from constructing an S3Adapter, got %d", got)
	}
}

func TestS3AdapterListByTag_MatchesOnlyTaggedBucketsInOwnRegion(t *testing.T) {
	api := NewFakeS3API()
	api.SeedBucket("match-me", "111111111111", "ap-south-1", cloudTags("env-aaaa", "alice"))
	api.SeedBucket("different-tag-value", "111111111111", "ap-south-1", cloudTags("env-bbbb", "alice"))
	api.SeedBucket("no-tags-at-all", "111111111111", "ap-south-1", nil)
	api.SeedBucket("right-tag-wrong-region", "111111111111", "us-east-1", cloudTags("env-aaaa", "alice"))
	a := adapterFor(api)

	refs, err := a.ListByTag(context.Background(), CloudS3Bucket, CloudTagEnvironmentID, "env-aaaa")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Ref != "match-me" {
		t.Fatalf("expected exactly one match (match-me), got %+v", refs)
	}
	if refs[0].Account != a.Account || refs[0].Region != a.Region {
		t.Fatalf("expected the returned ref to carry this adapter's own account/region, got %+v", refs[0])
	}
}

func TestS3AdapterListByTag_PaginatesAcrossMultipleListBucketsCalls(t *testing.T) {
	api := NewFakeS3API()
	api.PageSize = 1 // force one bucket per page
	for i := 0; i < 5; i++ {
		name := "bucket-" + string(rune('a'+i))
		api.SeedBucket(name, "111111111111", "ap-south-1", cloudTags("env-page", "alice"))
	}
	a := adapterFor(api)

	refs, err := a.ListByTag(context.Background(), CloudS3Bucket, CloudTagEnvironmentID, "env-page")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 5 {
		t.Fatalf("expected all 5 paginated buckets to be found, got %d: %+v", len(refs), refs)
	}
	if calls := api.CallCount("ListBuckets"); calls < 5 {
		t.Fatalf("expected ListBuckets to have been paginated across multiple calls, got only %d calls", calls)
	}
}

func TestS3AdapterListByTag_UntaggedBucketIsSkippedNotError(t *testing.T) {
	api := NewFakeS3API()
	api.SeedBucket("no-tags", "111111111111", "ap-south-1", nil) // triggers NoSuchTagSet from the fake
	a := adapterFor(api)

	refs, err := a.ListByTag(context.Background(), CloudS3Bucket, CloudTagEnvironmentID, "anything")
	if err != nil {
		t.Fatalf("expected NoSuchTagSet to be treated as 'no match', not an error, got %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("expected no matches, got %+v", refs)
	}
}

func TestS3AdapterListByTag_RejectsUnsupportedKind(t *testing.T) {
	api := NewFakeS3API()
	a := adapterFor(api)
	if _, err := a.ListByTag(context.Background(), CloudResourceKind("ec2_instance"), "k", "v"); err == nil {
		t.Fatal("expected an unsupported resource kind to be rejected before any API call")
	}
	if got := api.CallCount(""); got != 0 {
		t.Fatalf("expected zero API calls for a rejected kind, got %d", got)
	}
}

func TestS3AdapterListByTag_ThrottlingMapsToErrCloudThrottled(t *testing.T) {
	api := NewFakeS3API()
	api.ListBucketsErr = apiErr("SlowDown", "please slow down")
	a := adapterFor(api)

	_, err := a.ListByTag(context.Background(), CloudS3Bucket, "k", "v")
	if !errors.Is(err, ErrCloudThrottled) {
		t.Fatalf("expected ErrCloudThrottled, got %v", err)
	}
}

func TestS3AdapterListByTag_CredentialExpiryMapsToErrCloudCredentialExpired(t *testing.T) {
	api := NewFakeS3API()
	api.ListBucketsErr = apiErr("ExpiredToken", "the security token has expired")
	a := adapterFor(api)

	_, err := a.ListByTag(context.Background(), CloudS3Bucket, "k", "v")
	if !errors.Is(err, ErrCloudCredentialExpired) {
		t.Fatalf("expected ErrCloudCredentialExpired, got %v", err)
	}
}

func TestS3AdapterListByTag_UnrecognizedErrorFailsClosedAsUnavailable(t *testing.T) {
	api := NewFakeS3API()
	api.ListBucketsErr = apiErr("InternalError", "we have no idea what happened")
	a := adapterFor(api)

	_, err := a.ListByTag(context.Background(), CloudS3Bucket, "k", "v")
	if !errors.Is(err, ErrCloudUnavailable) {
		t.Fatalf("expected an unrecognized AWS error to fail closed as ErrCloudUnavailable, got %v", err)
	}
}

func TestS3AdapterVerifyOwnership_MatchesExactly(t *testing.T) {
	api := NewFakeS3API()
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "owned-bucket"}
	api.SeedBucket(ref.Ref, ref.Account, ref.Region, cloudTags("env-owned", "alice"))
	a := adapterFor(api)

	owned, err := a.VerifyOwnership(context.Background(), ref, cloudTags("env-owned", "alice"))
	if err != nil {
		t.Fatal(err)
	}
	if !owned {
		t.Fatal("expected ownership to verify")
	}
}

func TestS3AdapterVerifyOwnership_MissingTagIsMismatchNotError(t *testing.T) {
	api := NewFakeS3API()
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "partial-tags"}
	api.SeedBucket(ref.Ref, ref.Account, ref.Region, map[string]string{CloudTagEnvironmentID: "env-x"}) // missing Owner/ManagedBy
	a := adapterFor(api)

	owned, err := a.VerifyOwnership(context.Background(), ref, cloudTags("env-x", "alice"))
	if err != nil {
		t.Fatal(err)
	}
	if owned {
		t.Fatal("expected a missing required tag to count as a mismatch")
	}
}

func TestS3AdapterVerifyOwnership_GoneBucketIsMismatchNotError(t *testing.T) {
	api := NewFakeS3API()
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "never-existed"}
	a := adapterFor(api)

	owned, err := a.VerifyOwnership(context.Background(), ref, cloudTags("env-x", "alice"))
	if err != nil {
		t.Fatalf("expected a gone/never-existed bucket to be 'not owned', not an error, got %v", err)
	}
	if owned {
		t.Fatal("expected not owned")
	}
}

func TestS3AdapterVerifyOwnership_RejectsCrossScopeRef(t *testing.T) {
	api := NewFakeS3API()
	a := adapterFor(api)
	wrongScope := CloudResourceRef{Kind: CloudS3Bucket, Account: "999999999999", Region: "us-east-1", Ref: "whatever"}

	_, err := a.VerifyOwnership(context.Background(), wrongScope, nil)
	if !errors.Is(err, errS3ScopeMismatch) {
		t.Fatalf("expected a cross-account/region ref to be rejected before any API call, got %v", err)
	}
	if got := api.CallCount(""); got != 0 {
		t.Fatalf("expected zero API calls for a rejected cross-scope ref, got %d", got)
	}
}

func TestS3AdapterRemove_EmptiesVersionsDeleteMarkersAndUploadsThenDeletesBucket(t *testing.T) {
	api := NewFakeS3API()
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "full-bucket"}
	api.SeedBucket(ref.Ref, ref.Account, ref.Region, cloudTags("env-full", "alice"))
	api.AddObjectVersion(ref.Ref, "a.txt", "v1", true)
	api.AddObjectVersion(ref.Ref, "a.txt", "v0", false)
	api.AddDeleteMarker(ref.Ref, "b.txt", "dm1", true)
	api.AddMultipartUpload(ref.Ref, "c.txt", "upload-1")
	a := adapterFor(api)

	if err := a.Remove(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if api.HasBucket(ref.Ref) {
		t.Fatal("expected the bucket to have been deleted")
	}
}

func TestS3AdapterRemove_PaginatesVersionsAndMultipartUploads(t *testing.T) {
	api := NewFakeS3API()
	api.PageSize = 1
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "paginated-bucket"}
	api.SeedBucket(ref.Ref, ref.Account, ref.Region, cloudTags("env-p", "alice"))
	for i := 0; i < 4; i++ {
		api.AddObjectVersion(ref.Ref, "key"+string(rune('a'+i)), "v1", true)
	}
	for i := 0; i < 3; i++ {
		api.AddMultipartUpload(ref.Ref, "up"+string(rune('a'+i)), "upload-"+string(rune('a'+i)))
	}
	a := adapterFor(api)

	if err := a.Remove(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if api.HasBucket(ref.Ref) {
		t.Fatal("expected the bucket to have been fully emptied and deleted despite pagination")
	}
	if calls := api.CallCount("ListObjectVersions"); calls < 4 {
		t.Fatalf("expected ListObjectVersions to paginate across multiple calls, got %d", calls)
	}
	if calls := api.CallCount("ListMultipartUploads"); calls < 3 {
		t.Fatalf("expected ListMultipartUploads to paginate across multiple calls, got %d", calls)
	}
}

func TestS3AdapterRemove_BatchesDeleteObjectsOverTheThousandItemLimit(t *testing.T) {
	api := NewFakeS3API()
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "huge-bucket"}
	api.SeedBucket(ref.Ref, ref.Account, ref.Region, cloudTags("env-huge", "alice"))
	const n = maxDeleteObjectsBatch + 7
	for i := 0; i < n; i++ {
		api.AddObjectVersion(ref.Ref, "key", "v"+strconv.Itoa(i), i == n-1)
	}
	a := adapterFor(api)

	if err := a.Remove(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if api.ObjectCount(ref.Ref) != 0 && api.HasBucket(ref.Ref) {
		t.Fatalf("expected every one of %d versions to be removed", n)
	}
	if calls := api.CallCount("DeleteObjects"); calls < 2 {
		t.Fatalf("expected DeleteObjects to be split into at least 2 batches for %d items (limit %d), got %d calls", n, maxDeleteObjectsBatch, calls)
	}
}

func TestS3AdapterRemove_PerItemDeleteFailureReportsBucketNotEmptyNeverDeletesBucket(t *testing.T) {
	api := NewFakeS3API()
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "stubborn-bucket"}
	api.SeedBucket(ref.Ref, ref.Account, ref.Region, cloudTags("env-stubborn", "alice"))
	api.AddObjectVersion(ref.Ref, "locked.txt", "v1", true)
	api.DeleteObjectsPartialFailures = map[string]bool{"locked.txt/v1": true}
	a := adapterFor(api)

	err := a.Remove(context.Background(), ref)
	if !errors.Is(err, ErrCloudBucketNotEmpty) {
		t.Fatalf("expected ErrCloudBucketNotEmpty for a per-item delete failure, got %v", err)
	}
	if !api.HasBucket(ref.Ref) {
		t.Fatal("expected the bucket to NEVER be deleted while a per-item delete failure was reported")
	}
	if calls := api.CallCount("DeleteBucket"); calls != 0 {
		t.Fatalf("expected DeleteBucket to never even be attempted after a per-item delete failure, got %d calls", calls)
	}
}

func TestS3AdapterRemove_DeleteBucketBucketNotEmptyIsNeverSwallowed(t *testing.T) {
	// Simulates AWS's own eventual-consistency surprise: this adapter's
	// own enumeration believed the bucket was empty (nothing seeded),
	// but DeleteBucket itself -- the authoritative, final check -- still
	// reports BucketNotEmpty. This must never be treated as success.
	api := NewFakeS3API()
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "eventually-consistent-bucket"}
	api.SeedBucket(ref.Ref, ref.Account, ref.Region, cloudTags("env-ec", "alice"))
	api.DeleteBucketErr = apiErr("BucketNotEmpty", "The bucket you tried to delete is not empty")
	a := adapterFor(api)

	err := a.Remove(context.Background(), ref)
	if !errors.Is(err, ErrCloudBucketNotEmpty) {
		t.Fatalf("expected ErrCloudBucketNotEmpty, got %v", err)
	}
	if !api.HasBucket(ref.Ref) {
		t.Fatal("expected the bucket to still be present (DeleteBucketErr forced a failure)")
	}
}

func TestS3AdapterRemove_TransientThrottlingOnDeleteBucketIsRetryableNotSuccess(t *testing.T) {
	api := NewFakeS3API()
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "throttled-delete-bucket"}
	api.SeedBucket(ref.Ref, ref.Account, ref.Region, cloudTags("env-t", "alice"))
	api.DeleteBucketFailuresRemaining = 1
	a := adapterFor(api)

	err := a.Remove(context.Background(), ref)
	if !errors.Is(err, ErrCloudThrottled) {
		t.Fatalf("expected ErrCloudThrottled on the first (forced-failing) attempt, got %v", err)
	}
	if !api.HasBucket(ref.Ref) {
		t.Fatal("expected the bucket to still be present after the throttled attempt")
	}

	// A second attempt (simulating the next CleanupDue/worker pass)
	// succeeds now that the injected failure is exhausted.
	if err := a.Remove(context.Background(), ref); err != nil {
		t.Fatalf("expected the second attempt to succeed, got %v", err)
	}
	if api.HasBucket(ref.Ref) {
		t.Fatal("expected the bucket to be gone after the successful retry")
	}
}

func TestS3AdapterRemove_AlreadyGoneBucketIsSuccessNotError(t *testing.T) {
	api := NewFakeS3API()
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "never-existed"}
	a := adapterFor(api)

	if err := a.Remove(context.Background(), ref); err != nil {
		t.Fatalf("expected removing an already-gone bucket to succeed (idempotent), got %v", err)
	}
}

func TestS3AdapterRemove_RejectsCrossScopeRefBeforeAnyAPICall(t *testing.T) {
	api := NewFakeS3API()
	a := adapterFor(api)
	wrongScope := CloudResourceRef{Kind: CloudS3Bucket, Account: "999999999999", Region: "us-east-1", Ref: "whatever"}

	if err := a.Remove(context.Background(), wrongScope); !errors.Is(err, errS3ScopeMismatch) {
		t.Fatalf("expected a cross-scope ref to be rejected, got %v", err)
	}
	if got := api.CallCount(""); got != 0 {
		t.Fatalf("expected zero API calls for a rejected cross-scope ref, got %d", got)
	}
}

func TestS3AdapterRemove_NeverTouchesAnUnrelatedBucket(t *testing.T) {
	api := NewFakeS3API()
	target := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "target-bucket"}
	other := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "unrelated-bucket"}
	api.SeedBucket(target.Ref, target.Account, target.Region, cloudTags("env-target", "alice"))
	api.SeedBucket(other.Ref, other.Account, other.Region, cloudTags("env-other", "bob"))
	api.AddObjectVersion(other.Ref, "important.txt", "v1", true)
	a := adapterFor(api)

	if err := a.Remove(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if api.HasBucket(target.Ref) {
		t.Fatal("expected the target bucket to be removed")
	}
	if !api.HasBucket(other.Ref) || api.ObjectCount(other.Ref) != 1 {
		t.Fatal("SECURITY REGRESSION: an unrelated bucket (or its objects) was touched")
	}
}

// TestS3AdapterIntegratesWithExistingCloudCleanupOrchestration proves
// S3Adapter is a drop-in CloudClient: wired into a real Controller via
// WithCloud, going through RemoveTrackedCloudResources/CloudReconcile
// exactly like FakeCloudClient does in every other test in this package
// -- preserving the existing verified_clean invariant, retry and
// escalation behavior unchanged, with no S3-specific code anywhere in
// controller.go/cloud_reconcile.go.
func TestS3AdapterIntegratesWithExistingCloudCleanupOrchestration(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "integration-bucket"}
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	api := NewFakeS3API()
	api.SeedBucket(ref.Ref, ref.Account, ref.Region, cloudTags(env.ID, "alice"))
	api.AddObjectVersion(ref.Ref, "leftover.txt", "v1", true)
	adapter := NewS3Adapter(api, ref.Account, ref.Region)

	c := Controller{
		Manager:                 m,
		Now:                     func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:                   noSleep,
		MaxCleanupAttempts:      1,
		Cloud:                   adapter,
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
		t.Fatalf("expected verified_clean through the real S3Adapter, got %+v", results)
	}
	if api.HasBucket(ref.Ref) {
		t.Fatal("expected the S3 bucket to actually be gone")
	}
}

// TestS3AdapterIntegration_ThrottlingEscalatesExactlyLikeFakeCloudClient
// proves the Controller's bounded-retry/escalation behavior (cloud_retry.go)
// is completely unaware of which CloudClient implementation is behind
// it: S3Adapter reporting ErrCloudThrottled drives the exact same
// escalation path FakeCloudClient already does in
// cloud_escalation_test.go.
func TestS3AdapterIntegration_ThrottlingEscalatesExactlyLikeFakeCloudClient(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "always-throttled"}
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	api := NewFakeS3API()
	api.SeedBucket(ref.Ref, ref.Account, ref.Region, cloudTags(env.ID, "alice"))
	api.DeleteBucketFailuresRemaining = 1 << 30 // never clears
	adapter := NewS3Adapter(api, ref.Account, ref.Region)

	c := Controller{
		Manager:                 m,
		Now:                     func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:                   noSleep,
		MaxCleanupAttempts:      1,
		Cloud:                   adapter,
		MaxCloudCleanupAttempts: 2,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CleanupDue(context.Background(), root); err == nil {
		t.Fatal("expected the first throttled pass to fail")
	}
	results, err := c.CleanupDue(context.Background(), root)
	if err == nil {
		t.Fatal("expected the second (exhausting) pass to escalate with a non-nil error")
	}
	if len(results) != 1 || results[0].Environment.Status != StatusCleanupAttentionRequired {
		t.Fatalf("expected escalation to cleanup_attention_required, got %+v", results)
	}
	if !api.HasBucket(ref.Ref) {
		t.Fatal("expected the bucket to remain present and tracked, never discarded")
	}
}
