package playground

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Permanent regression tests for the P0 fail-open bug in CloudReconcile:
// ListByTag-only discovery could not distinguish a tracked resource
// that was genuinely deleted from one that merely lost (or had changed)
// its ownership tags, so the latter was silently classified Missing and
// could let an environment reach StatusVerifiedClean while the real
// resource was still fully present. The fix adds CloudClient.Exists, an
// exact-identity (tag-blind) check CloudReconcile now runs for every
// tracked resource tag-based discovery does not surface -- see
// cloud_reconcile.go's CloudReconciliation.OwnershipChanged/Unverifiable
// doc comments for the full design.

// fullTags is the standard three-tag set TrackCloudResource/VerifyOwnership
// expect; drifting one of the three (or all three) is this file's own
// way of simulating tag loss/change.
func fullTags(id, owner string) map[string]string {
	return cloudTags(id, owner)
}

// TestCloudReconcile_AllOwnershipTagsRemoved_NeverReportsMissing is the
// core repro for the original bug: a tracked resource whose tags are
// ALL stripped vanishes from ListByTag entirely, yet it must still be
// classified OwnershipChanged (still exists), never Missing, and must
// never make CloudReconcile report Clean().
func TestCloudReconcile_AllOwnershipTagsRemoved_NeverReportsMissing(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-all-tags-removed")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, fullTags(env.ID, "alice"), 0, 0, 0)
	fake.DriftTag(ref, CloudTagEnvironmentID, "")
	fake.DriftTag(ref, CloudTagOwner, "")
	fake.DriftTag(ref, CloudTagManagedBy, "")

	rec, err := CloudReconcile(context.Background(), fake, root, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Missing) != 0 {
		t.Fatalf("CRITICAL: expected zero Missing (the bucket still exists), got %+v", rec.Missing)
	}
	if len(rec.OwnershipChanged) != 1 || rec.OwnershipChanged[0].Ref != ref.Ref {
		t.Fatalf("expected the detagged bucket to be reported OwnershipChanged, got %+v", rec.OwnershipChanged)
	}
	if rec.Clean() {
		t.Fatal("CRITICAL: Clean() must never be true while the real resource still exists")
	}
	if !fake.HasRef(ref) {
		t.Fatal("test setup error: the bucket should still exist")
	}
}

// TestCloudReconcile_EnvironmentTagChanged_StillCaughtAsOwnershipChanged
// isolates changing ONLY the environment-id tag (the one ListByTag
// filters on) -- the exact mechanism of the original bug.
func TestCloudReconcile_EnvironmentTagChanged_StillCaughtAsOwnershipChanged(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-env-tag-changed")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, fullTags(env.ID, "alice"), 0, 0, 0)
	fake.DriftTag(ref, CloudTagEnvironmentID, "env-some-other-id")

	rec, err := CloudReconcile(context.Background(), fake, root, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Missing) != 0 {
		t.Fatalf("expected zero Missing, got %+v", rec.Missing)
	}
	if len(rec.OwnershipChanged) != 1 {
		t.Fatalf("expected OwnershipChanged to catch the re-tagged bucket, got %+v", rec)
	}
	if rec.Clean() {
		t.Fatal("CRITICAL: Clean() must be false when the environment-id tag changed but the bucket still exists")
	}
}

// TestCloudReconcile_OwnerOrManagedByTagChanged_StillBlockedByTagScopedPresent
// proves the owner-tag-only and managed-by-tag-only drift cases are
// both caught through the EXISTING mechanism (the bucket still matches
// the environment-id tag, so it remains in Present, which already
// makes Clean() false) -- this is not the new Exists-based path, but
// the task requires covering both explicitly as named cases, and
// removal is additionally refused by VerifyOwnership (see
// TestRemoveTrackedCloudResourcesRefusesWhenOwnershipDriftsBeforeRemoval /
// TestRemoveTrackedCloudResourcesRefusesWhenManagedByTagMissing).
func TestCloudReconcile_OwnerOrManagedByTagChanged_StillBlockedByTagScopedPresent(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
	}{
		{"owner_tag_changed", CloudTagOwner},
		{"managed_by_tag_changed", CloudTagManagedBy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			m := NewManager()
			env, err := m.Create(root, "alice", "playground", time.Minute, nil)
			if err != nil {
				t.Fatal(err)
			}
			ref := testRef("forgestack-" + tc.name)
			if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
				t.Fatal(err)
			}
			fake := NewFakeCloudClient()
			fake.SeedBucket(ref, fullTags(env.ID, "alice"), 0, 0, 0)
			fake.DriftTag(ref, tc.key, "someone-else")

			rec, err := CloudReconcile(context.Background(), fake, root, env.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(rec.Present) != 1 {
				t.Fatalf("expected the bucket to remain in Present (environment-id tag unchanged), got %+v", rec.Present)
			}
			if rec.Clean() {
				t.Fatalf("CRITICAL: Clean() must be false when %s no longer matches", tc.key)
			}
		})
	}
}

// TestCloudReconcile_ExactResourcePresentButAbsentFromListByTag is the
// most direct statement of the fix: ListByTag itself returns nothing at
// all (simulating a listing/query that simply cannot see this resource
// by tag, for any reason), yet Exists confirms it is still there.
func TestCloudReconcile_ExactResourcePresentButAbsentFromListByTag(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-invisible-to-list")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, fullTags(env.ID, "alice"), 0, 0, 0)
	fake.DriftTag(ref, CloudTagEnvironmentID, "") // drops it out of ListByTag's own filter

	rec, err := CloudReconcile(context.Background(), fake, root, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range rec.Present {
		if p.Ref == ref.Ref {
			t.Fatal("test setup error: expected ListByTag to NOT surface this resource")
		}
	}
	if len(rec.OwnershipChanged) != 1 {
		t.Fatalf("expected Exists to independently catch it as OwnershipChanged, got %+v", rec)
	}
}

// TestCloudReconcile_ExactResourceGenuinelyDeleted_IsMissing proves the
// fix does not overcorrect: a tracked resource that is truly gone must
// still be classified Missing (and Clean() must still be reachable),
// never dragged into OwnershipChanged or Unverifiable.
func TestCloudReconcile_ExactResourceGenuinelyDeleted_IsMissing(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-genuinely-gone")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient() // never seeded -- truly never existed/already removed

	rec, err := CloudReconcile(context.Background(), fake, root, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Missing) != 1 || rec.Missing[0].Ref != ref.Ref {
		t.Fatalf("expected the genuinely absent resource to be Missing, got %+v", rec)
	}
	if len(rec.OwnershipChanged) != 0 || len(rec.Unverifiable) != 0 {
		t.Fatalf("expected no false positives, got %+v", rec)
	}
	if !rec.Clean() {
		t.Fatalf("expected Clean() == true once the only tracked resource is genuinely gone, got %+v", rec)
	}
}

// TestCloudReconcile_ExistsAccessDenied_IsUnverifiableNeverMissing proves
// an AccessDenied response on the exact-identity check fails closed --
// it must never be read as "confirmed absent."
func TestCloudReconcile_ExistsAccessDenied_IsUnverifiableNeverMissing(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-exists-access-denied")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, fullTags(env.ID, "alice"), 0, 0, 0)
	fake.DriftTag(ref, CloudTagEnvironmentID, "") // force the Exists path
	fake.ExistsErr = ErrCloudUnavailable

	rec, err := CloudReconcile(context.Background(), fake, root, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Missing) != 0 {
		t.Fatalf("CRITICAL: AccessDenied/unavailable on Exists must never be read as Missing, got %+v", rec.Missing)
	}
	if len(rec.Unverifiable) != 1 {
		t.Fatalf("expected the resource to be Unverifiable, got %+v", rec)
	}
	if rec.Clean() {
		t.Fatal("CRITICAL: Clean() must be false when a tracked resource's existence could not be verified")
	}
}

// TestCloudReconcile_ExistsThrottledOrCredentialExpired_IsUnverifiable
// covers the other two ErrCloud* unavailability sentinels on the Exists
// path specifically (ErrCloudUnavailable is covered above).
func TestCloudReconcile_ExistsThrottledOrCredentialExpired_IsUnverifiable(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"throttled", ErrCloudThrottled},
		{"credential_expired", ErrCloudCredentialExpired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			m := NewManager()
			env, err := m.Create(root, "alice", "playground", time.Minute, nil)
			if err != nil {
				t.Fatal(err)
			}
			ref := testRef("forgestack-exists-" + tc.name)
			if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
				t.Fatal(err)
			}
			fake := NewFakeCloudClient()
			fake.SeedBucket(ref, fullTags(env.ID, "alice"), 0, 0, 0)
			fake.DriftTag(ref, CloudTagEnvironmentID, "")
			fake.ExistsErr = tc.err

			rec, err := CloudReconcile(context.Background(), fake, root, env.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(rec.Unverifiable) != 1 || len(rec.Missing) != 0 {
				t.Fatalf("expected Unverifiable, never Missing, got %+v", rec)
			}
			if rec.Clean() {
				t.Fatal("CRITICAL: Clean() must be false while Exists cannot be verified")
			}
		})
	}
}

// TestS3AdapterExists_WrongAccountOrRegionRejectedBeforeAnyAPICall
// proves S3Adapter.Exists enforces the same scope check every other
// S3Adapter method does, never making an API call for a ref outside
// its own configured account/region.
func TestS3AdapterExists_WrongAccountOrRegionRejectedBeforeAnyAPICall(t *testing.T) {
	api := NewFakeS3API()
	api.SeedBucket("scoped-bucket", "111111111111", "ap-south-1", nil)
	adapter := NewS3Adapter(api, "111111111111", "ap-south-1")

	for _, tc := range []struct {
		name string
		ref  CloudResourceRef
	}{
		{"wrong_account", CloudResourceRef{Kind: CloudS3Bucket, Account: "999999999999", Region: "ap-south-1", Ref: "scoped-bucket"}},
		{"wrong_region", CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "us-east-1", Ref: "scoped-bucket"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := api.CallCount("HeadBucket")
			_, err := adapter.Exists(context.Background(), tc.ref)
			if err == nil {
				t.Fatal("expected a scope-mismatch error")
			}
			if after := api.CallCount("HeadBucket"); after != before {
				t.Errorf("expected zero HeadBucket calls for an out-of-scope ref, before=%d after=%d", before, after)
			}
		})
	}
}

// TestS3AdapterExists_AmbiguousErrorsNeverInterpretedAsAbsence covers
// the S3-specific exact-identity failure modes the task names:
// AccessDenied, throttling, and credential expiry on HeadBucket itself
// must never be read as "bucket does not exist."
func TestS3AdapterExists_AmbiguousErrorsNeverInterpretedAsAbsence(t *testing.T) {
	for _, tc := range []struct {
		name string
		code string
	}{
		{"access_denied", "AccessDenied"},
		{"throttled", "SlowDown"},
		{"credential_expired", "ExpiredToken"},
		{"unrecognized", "SomeFutureAWSErrorCodeThisAdapterHasNeverSeen"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := NewFakeS3API()
			api.SeedBucket("ambiguous-bucket", "111111111111", "ap-south-1", nil)
			api.HeadBucketErr = map[string]error{"ambiguous-bucket": apiErr(tc.code, "simulated "+tc.name)}
			adapter := NewS3Adapter(api, "111111111111", "ap-south-1")

			exists, err := adapter.Exists(context.Background(), CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "ambiguous-bucket"})
			if err == nil {
				t.Fatalf("expected a non-nil error for an ambiguous %s response, got exists=%v", tc.code, exists)
			}
			if exists {
				t.Fatal("CRITICAL: an ambiguous error must never report exists=true alongside an error being ignored")
			}
		})
	}
}

// TestS3AdapterExists_ConfirmedAbsentAndConfirmedPresent are the two
// unambiguous baseline cases, proving Exists is not simply
// fail-closed-always -- it correctly reports both outcomes when the
// backend is unambiguous.
func TestS3AdapterExists_ConfirmedAbsentAndConfirmedPresent(t *testing.T) {
	api := NewFakeS3API()
	api.SeedBucket("still-there", "111111111111", "ap-south-1", nil)
	adapter := NewS3Adapter(api, "111111111111", "ap-south-1")

	exists, err := adapter.Exists(context.Background(), CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "still-there"})
	if err != nil || !exists {
		t.Fatalf("expected exists=true err=nil for a seeded bucket, got exists=%v err=%v", exists, err)
	}

	exists, err = adapter.Exists(context.Background(), CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "never-existed"})
	if err != nil || exists {
		t.Fatalf("expected exists=false err=nil for a bucket that was never seeded, got exists=%v err=%v", exists, err)
	}
}

// TestCloudReconcile_UnrelatedResourceNeverAffected proves this fix adds
// no new cross-environment leakage: a completely different environment's
// resource (own tags, own tracking record) must never appear in this
// environment's OwnershipChanged/Unverifiable/Missing, regardless of
// what happens to IT.
func TestCloudReconcile_UnrelatedResourceNeverAffected(t *testing.T) {
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
	refA := testRef("forgestack-unrelated-a")
	refB := testRef("forgestack-unrelated-b")
	if err := m.TrackCloudResource(root, envA.ID, "alice", refA); err != nil {
		t.Fatal(err)
	}
	if err := m.TrackCloudResource(root, envB.ID, "bob", refB); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(refA, fullTags(envA.ID, "alice"), 0, 0, 0)
	fake.SeedBucket(refB, fullTags(envB.ID, "bob"), 0, 0, 0)
	// Drift B's tags entirely -- only B's own reconciliation should ever
	// see this.
	fake.DriftTag(refB, CloudTagEnvironmentID, "")
	fake.DriftTag(refB, CloudTagOwner, "")
	fake.DriftTag(refB, CloudTagManagedBy, "")

	recA, err := CloudReconcile(context.Background(), fake, root, envA.ID)
	if err != nil {
		t.Fatal(err)
	}
	// env A's OWN bucket is untouched and still tracked+present -- that
	// alone correctly makes Clean() false (nothing has removed it); the
	// point of this test is that env B's drifted resource must never
	// leak into env A's classification lists.
	if len(recA.Present) != 1 || recA.Present[0].Ref != refA.Ref {
		t.Fatalf("expected env A's own bucket to still be reported present, got %+v", recA.Present)
	}
	for _, list := range [][]CloudResourceRef{recA.OwnershipChanged, recA.Unverifiable, recA.Missing} {
		for _, r := range list {
			if r.Ref == refB.Ref {
				t.Fatal("CRITICAL: env B's drifted resource leaked into env A's reconciliation")
			}
		}
	}

	recB, err := CloudReconcile(context.Background(), fake, root, envB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recB.OwnershipChanged) != 1 || recB.OwnershipChanged[0].Ref != refB.Ref {
		t.Fatalf("expected env B to independently catch its own drifted resource, got %+v", recB)
	}
}

// TestCloudReconcile_RestartRecoversOwnershipChangedFromPersistedState
// proves the fix needs no in-memory handoff: a fresh Controller/Manager,
// sharing no memory with the one that tracked the resource, still
// independently classifies a tag-drifted resource as OwnershipChanged
// (never falsely Missing) purely from persisted tracking state.
func TestCloudReconcile_RestartRecoversOwnershipChangedFromPersistedState(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-restart-ownership-changed")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, fullTags(env.ID, "alice"), 0, 0, 0)
	fake.DriftTag(ref, CloudTagEnvironmentID, "")

	// "Restart": a fresh Manager, no shared memory -- only persisted
	// cloud-resources.json state.
	rec, err := CloudReconcile(context.Background(), fake, root, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.OwnershipChanged) != 1 {
		t.Fatalf("expected the fresh reconcile to independently recover OwnershipChanged from persisted state, got %+v", rec)
	}
}

// TestCloudReconcile_ConcurrentReconcileCallsAreConsistentAndNeverFalseClean
// runs many concurrent CloudReconcile calls against the same drifted
// resource and proves every single one agrees: never Clean(), never
// Missing, always OwnershipChanged.
func TestCloudReconcile_ConcurrentReconcileCallsAreConsistentAndNeverFalseClean(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-concurrent-ownership-changed")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, fullTags(env.ID, "alice"), 0, 0, 0)
	fake.DriftTag(ref, CloudTagEnvironmentID, "")

	var wg sync.WaitGroup
	results := make([]CloudReconciliation, 10)
	errs := make([]error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = CloudReconcile(context.Background(), fake, root, env.ID)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: unexpected error: %v", i, err)
		}
		if results[i].Clean() {
			t.Fatalf("goroutine %d: CRITICAL: Clean() == true under concurrent access while the resource still exists", i)
		}
		if len(results[i].Missing) != 0 || len(results[i].OwnershipChanged) != 1 {
			t.Fatalf("goroutine %d: expected consistent OwnershipChanged classification, got %+v", i, results[i])
		}
	}
}

// TestCloudCleanup_OwnershipChangedNeverProducesFalseVerifiedCleanReportOrNotification
// is the full integration test the task's last bullet names explicitly:
// the entire Controller.CleanupDue -> GenerateCleanupReport ->
// SendCleanupNotification pipeline, for an environment whose only
// tracked cloud resource has had its ownership tags stripped, must
// never produce StatusVerifiedClean, a SafetyVerifiedClean report, or a
// verified_clean notification.
func TestCloudCleanup_OwnershipChangedNeverProducesFalseVerifiedCleanReportOrNotification(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := testRef("forgestack-no-false-clean")
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, fullTags(env.ID, "alice"), 0, 0, 0)
	fake.DriftTag(ref, CloudTagEnvironmentID, "")
	fake.DriftTag(ref, CloudTagOwner, "")
	fake.DriftTag(ref, CloudTagManagedBy, "")

	c := Controller{
		Manager:                 m,
		Now:                     func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:                   noSleep,
		MaxCleanupAttempts:      1,
		Cloud:                   fake,
		MaxCloudCleanupAttempts: 3,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	results, cleanupErr := c.CleanupDue(context.Background(), root)
	if cleanupErr == nil {
		t.Fatal("CRITICAL: expected CleanupDue to report an error -- the resource is still present under drifted tags")
	}
	if len(results) != 1 || results[0].Environment.Status == StatusVerifiedClean {
		t.Fatalf("CRITICAL: expected anything other than verified_clean, got %+v", results)
	}

	report, err := c.GenerateCleanupReport(context.Background(), root, results[0].Environment, cleanupErr)
	if err != nil {
		t.Fatal(err)
	}
	if report.Safety == SafetyVerifiedClean {
		t.Fatalf("CRITICAL: report.Safety must never be SafetyVerifiedClean while the resource still exists, got %+v", report)
	}

	notifier := &FakeNotifier{}
	sent, err := SendCleanupNotification(context.Background(), root, report, notifier, c.Now(), nil)
	_ = err
	if sent {
		for _, n := range notifier.Received() {
			if n.Kind == NotificationVerifiedClean {
				t.Fatal("CRITICAL: a false verified_clean notification was delivered")
			}
		}
	}
	if !fake.HasRef(ref) {
		t.Fatal("test setup error: the bucket should still exist throughout")
	}
}
