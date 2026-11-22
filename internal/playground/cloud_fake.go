package playground

import (
	"context"
	"strings"
	"sync"
)

// fakeCloudBucket is one FakeCloudClient bucket's in-memory state --
// deliberately modeling every S3 edge case this package must handle
// explicitly: live object versions, delete markers (what S3 leaves behind
// when a versioned object is "deleted" without a version ID), and
// in-progress multipart uploads, any of which can make a real
// DeleteBucket fail with BucketNotEmpty even when a plain object listing
// looks empty.
type fakeCloudBucket struct {
	ref              CloudResourceRef
	tags             map[string]string
	objectVersions   int
	deleteMarkers    int
	multipartUploads int
}

// FakeCloudClient is a deterministic, in-memory CloudClient test double.
// Every test in this package that exercises cloud cleanup uses this --
// never a real AWS SDK client -- per this repository's explicit
// zero-real-AWS-calls boundary for this increment (see
// docs/architecture/CLOUD-CLEANUP-S3.md). Safe for concurrent use: tests
// exercising concurrent TrackCloudResource/cleanup calls share one
// FakeCloudClient across goroutines.
//
// Buckets are keyed by the full CloudResourceRef (Kind, Account, Region,
// Ref) -- not by Ref alone -- so two buckets that happen to share a name
// but belong to different accounts or regions are never confused with
// each other, exercising exactly the identity this package's own
// reconciliation logic is supposed to preserve (see
// TestCloudReconcileNeverConfusesSameNamedBucketsAcrossAccounts and
// TestCloudReconcileNeverConfusesSameNamedBucketsAcrossRegions).
type FakeCloudClient struct {
	mu      sync.Mutex
	buckets map[CloudResourceRef]*fakeCloudBucket

	// Injectable failures, consulted by the corresponding method on every
	// call until cleared -- tests use these to simulate exactly the
	// failure modes a real AWS SDK call can produce, deterministically
	// and without a network call or a clock.
	ListErr            error
	VerifyOwnershipErr error
	// ExistsErr, if set, is returned by every Exists call instead of
	// checking the fake's own state -- for simulating AccessDenied/
	// throttling/credential-expiry on the exact-identity existence
	// check itself, independent of ListByTag/VerifyOwnership.
	ExistsErr error
	// RemoveErr, if set, is returned by every Remove call instead of
	// actually removing anything.
	RemoveErr error
	// RemoveFailuresRemaining, if > 0, causes that many Remove calls to
	// fail with RemoveTransientErr (defaulting to ErrCloudThrottled)
	// before a later call succeeds -- for simulating a transient failure
	// a later cleanup pass is expected to recover from.
	RemoveFailuresRemaining int
	RemoveTransientErr      error
	// UnemptiableRefs names buckets (by Ref alone, matching any
	// account/region) whose Remove call always fails with
	// ErrCloudBucketNotEmpty, regardless of RemoveFailuresRemaining --
	// simulating a bucket this package cannot safely empty (e.g. a real
	// object lock/legal hold), as distinct from an ordinary non-empty
	// bucket (which this fake empties and removes successfully, to prove
	// normal non-empty-bucket handling works).
	UnemptiableRefs map[string]bool

	calls []string
}

// NewFakeCloudClient returns an empty FakeCloudClient.
func NewFakeCloudClient() *FakeCloudClient {
	return &FakeCloudClient{buckets: map[CloudResourceRef]*fakeCloudBucket{}}
}

// SeedBucket adds a bucket to the fake's state, as if it already existed
// under the given ref, carrying tags. objectVersions, deleteMarkers, and
// multipartUploads let a test construct the non-empty-bucket edge cases
// Remove must handle rather than choke on.
func (f *FakeCloudClient) SeedBucket(ref CloudResourceRef, tags map[string]string, objectVersions, deleteMarkers, multipartUploads int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	copied := make(map[string]string, len(tags))
	for k, v := range tags {
		copied[k] = v
	}
	f.buckets[ref] = &fakeCloudBucket{ref: ref, tags: copied, objectVersions: objectVersions, deleteMarkers: deleteMarkers, multipartUploads: multipartUploads}
}

// DriftTag mutates a seeded bucket's tag after seeding (value == ""
// removes the tag entirely), simulating a tag changing -- or being lost
// -- between when this package last tracked/listed a resource and when
// it tries to act on it again. Looked up by the exact ref (Kind, Account,
// Region, Ref) that was seeded, never by Ref alone, so drifting one
// account's bucket can never accidentally affect a same-named bucket in
// another account/region.
func (f *FakeCloudClient) DriftTag(ref CloudResourceRef, key, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.buckets[ref]
	if !ok {
		return
	}
	if value == "" {
		delete(b.tags, key)
		return
	}
	b.tags[key] = value
}

// Has reports whether any bucket named ref (in any account/region) is
// still present, for test assertions that only care about the name. Use
// HasRef for an exact (account, region, kind, ref) check.
func (f *FakeCloudClient) Has(ref string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k := range f.buckets {
		if k.Ref == ref {
			return true
		}
	}
	return false
}

// HasRef reports whether the exact CloudResourceRef (including account
// and region) is still present.
func (f *FakeCloudClient) HasRef(ref CloudResourceRef) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.buckets[ref]
	return ok
}

// CallCount returns how many recorded calls start with prefix (e.g.
// "VerifyOwnership" or "Remove"), for tests asserting ordering/frequency
// -- e.g. that VerifyOwnership is always called before the matching
// Remove.
func (f *FakeCloudClient) CallCount(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// Calls returns a copy of every call recorded so far, in order, for tests
// that need to assert relative ordering (e.g. VerifyOwnership:x precedes
// Remove:x).
func (f *FakeCloudClient) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *FakeCloudClient) ListByTag(_ context.Context, kind CloudResourceKind, tagKey, tagValue string) ([]CloudResourceRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "ListByTag")
	if f.ListErr != nil {
		return nil, f.ListErr
	}
	if kind != CloudS3Bucket {
		return nil, nil
	}
	var out []CloudResourceRef
	for _, b := range f.buckets {
		if b.tags[tagKey] == tagValue {
			out = append(out, b.ref)
		}
	}
	return out, nil
}

func (f *FakeCloudClient) VerifyOwnership(_ context.Context, ref CloudResourceRef, requiredTags map[string]string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "VerifyOwnership:"+ref.Ref)
	if f.VerifyOwnershipErr != nil {
		return false, f.VerifyOwnershipErr
	}
	b, ok := f.buckets[ref]
	if !ok {
		// Gone already -- never "owned," never an error: a caller must
		// treat "not found" as nothing to remove, not a crash.
		return false, nil
	}
	for k, v := range requiredTags {
		if b.tags[k] != v {
			return false, nil
		}
	}
	return true, nil
}

// Exists checks ref by exact identity ONLY -- the map is keyed by the
// full CloudResourceRef, never by tags -- so a bucket whose tags have
// been changed or removed via DriftTag still reports Exists == true
// here, exactly like a real HeadBucket-style check would.
func (f *FakeCloudClient) Exists(_ context.Context, ref CloudResourceRef) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "Exists:"+ref.Ref)
	if f.ExistsErr != nil {
		return false, f.ExistsErr
	}
	_, ok := f.buckets[ref]
	return ok, nil
}

func (f *FakeCloudClient) Remove(_ context.Context, ref CloudResourceRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "Remove:"+ref.Ref)
	if f.RemoveErr != nil {
		return f.RemoveErr
	}
	if f.RemoveFailuresRemaining > 0 {
		f.RemoveFailuresRemaining--
		err := f.RemoveTransientErr
		if err == nil {
			err = ErrCloudThrottled
		}
		return err
	}
	if f.UnemptiableRefs != nil && f.UnemptiableRefs[ref.Ref] {
		return ErrCloudBucketNotEmpty
	}
	if _, ok := f.buckets[ref]; !ok {
		return nil // already absent is not an error, matching Docker's Remove
	}
	// Simulate successfully emptying the bucket (all versions, delete
	// markers, and multipart uploads) before removing it -- this proves
	// only that the orchestration in cloud_reconcile.go tolerates a
	// Remove call succeeding for a bucket that was non-empty; it does
	// NOT exercise any real per-object/version/delete-marker/multipart-
	// upload removal logic, because no such logic exists anywhere in
	// this codebase -- that belongs entirely to the not-yet-built real
	// AWS-SDK-backed adapter (see docs/architecture/CLOUD-CLEANUP-S3.md's
	// AWS_NOT_VERIFIED row for this exact item).
	delete(f.buckets, ref)
	return nil
}
