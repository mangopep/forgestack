package playground

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func reportResource(report CleanupReport, domain string) (ResourceReport, bool) {
	for _, r := range report.Resources {
		if r.Domain == domain {
			return r, true
		}
	}
	return ResourceReport{}, false
}

func TestGenerateCleanupReport_LocalOnlyVerifiedClean(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Cleanup(root, env.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	envs, err := ListEnvironments(root)
	if err != nil {
		t.Fatal(err)
	}

	c := Controller{Manager: m, Now: func() time.Time { return time.Now().UTC() }}
	report, err := c.GenerateCleanupReport(context.Background(), root, envs[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Safety != SafetyVerifiedClean {
		t.Fatalf("expected VERIFIED CLEAN, got %q", report.Safety)
	}
	if report.Simulated {
		t.Fatal("expected a local-only report (no Docker/Cloud configured) to never be marked Simulated")
	}
	local, ok := reportResource(report, "local")
	if !ok || local.Present || !local.Verified {
		t.Fatalf("expected a verified-absent local resource entry, got %+v", local)
	}
	if report.EnvironmentID != env.ID || report.Owner != "alice" {
		t.Fatalf("expected environment ID/owner to be populated, got %+v", report)
	}
}

func TestGenerateCleanupReport_DockerResourcePresent_ActionRequiredNoConsoleLink(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.TrackDockerResource(root, env.ID, "alice", TrackedDockerResource{Kind: DockerContainer, Ref: "still-there"}); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeDockerClient()
	fake.Seed(DockerContainer, "still-there", map[string]string{DockerLabelEnvironmentID: env.ID})

	c := Controller{Manager: m, Now: func() time.Time { return time.Now().UTC() }, Docker: fake}
	report, err := c.GenerateCleanupReport(context.Background(), root, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Safety != SafetyActionRequired {
		t.Fatalf("expected ACTION REQUIRED while a tracked docker resource remains present, got %q", report.Safety)
	}
	if !report.Simulated {
		t.Fatal("expected Simulated=true: a FakeDockerClient with no explicit DockerVerificationMode must never claim live evidence")
	}
	docker, ok := reportResource(report, "docker")
	if !ok || !docker.Present || !docker.Verified {
		t.Fatalf("expected a verified-present docker resource entry, got %+v", docker)
	}
	if docker.ConsoleURL != "" {
		t.Fatalf("expected no AWS Console link for a Docker resource, got %q", docker.ConsoleURL)
	}
}

func TestGenerateCleanupReport_CloudResourcePresent_ConsoleLinkToBucketPage(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "still-live-bucket"}
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, cloudTags(env.ID, "alice"), 0, 0, 0)

	c := Controller{Manager: m, Now: func() time.Time { return time.Now().UTC() }, Cloud: fake}
	report, err := c.GenerateCleanupReport(context.Background(), root, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Safety != SafetyActionRequired {
		t.Fatalf("expected ACTION REQUIRED, got %q", report.Safety)
	}
	cloud, ok := reportResource(report, "cloud")
	if !ok || !cloud.Present {
		t.Fatalf("expected a present cloud resource entry, got %+v", cloud)
	}
	if !strings.Contains(cloud.ConsoleURL, "s3/buckets/still-live-bucket") {
		t.Fatalf("expected the console link to point at the bucket's own page, got %q", cloud.ConsoleURL)
	}
	if cloud.Account != ref.Account || cloud.Region != ref.Region {
		t.Fatalf("expected account/region to be populated, got %+v", cloud)
	}
}

func TestGenerateCleanupReport_CloudResourceRemoved_ConsoleLinkToInventoryPage(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "now-gone-bucket"}
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient() // bucket never seeded -- tracked but gone

	c := Controller{Manager: m, Now: func() time.Time { return time.Now().UTC() }, Cloud: fake}
	report, err := c.GenerateCleanupReport(context.Background(), root, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	cloud, ok := reportResource(report, "cloud")
	if !ok || cloud.Present {
		t.Fatalf("expected a verified-absent cloud resource entry, got %+v", cloud)
	}
	if strings.Contains(cloud.ConsoleURL, "/buckets/now-gone-bucket") {
		t.Fatalf("expected the deleted bucket's individual page link NOT to be used, got %q", cloud.ConsoleURL)
	}
	if !strings.Contains(cloud.ConsoleURL, "s3/home") {
		t.Fatalf("expected a verified-absent bucket to link to the regional S3 inventory page instead, got %q", cloud.ConsoleURL)
	}
}

func TestGenerateCleanupReport_UnverifiedWhenCloudUnavailable(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "cant-check"}
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	// Isolate the cloud-unavailable behavior: local resources must
	// already be gone, exactly as they would be by the time Controller
	// reaches the cloud-reconciliation phase of a real cleanupOne call.
	if err := os.RemoveAll(filepath.Join(root, env.ID, "resources")); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.ListErr = ErrCloudThrottled

	c := Controller{Manager: m, Now: func() time.Time { return time.Now().UTC() }, Cloud: fake}
	report, err := c.GenerateCleanupReport(context.Background(), root, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Safety != SafetyUnverified {
		t.Fatalf("expected UNVERIFIED (never ACTION REQUIRED, never VERIFIED CLEAN) when the cloud API could not be checked, got %q", report.Safety)
	}
}

func TestGenerateCleanupReport_NeverVerifiedCleanMerelyBecauseNoError(t *testing.T) {
	// Requirement 5: a report must never read VERIFIED CLEAN just
	// because a removal call didn't error -- only because an
	// independent re-check actually found nothing.
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "lying-about-removal"}
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	fake.SeedBucket(ref, cloudTags(env.ID, "alice"), 0, 0, 0)
	// Simulate "Remove() returned nil" but the resource is still there --
	// GenerateCleanupReport never trusts a removal call's own success,
	// only CloudReconcile's independent read, so it must still report
	// ACTION REQUIRED.
	c := Controller{Manager: m, Now: func() time.Time { return time.Now().UTC() }, Cloud: fake}
	report, err := c.GenerateCleanupReport(context.Background(), root, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Safety == SafetyVerifiedClean {
		t.Fatal("CRITICAL: reported VERIFIED CLEAN while an independent check still finds the resource present")
	}
}

func TestGenerateCleanupReport_MissingResourceMetadataFailsVisiblyNotSilently(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, env.ID)
	// Isolate the corrupt-metadata behavior: local resources must
	// already be gone, exactly as they would be by the time Controller
	// reaches the Docker-reconciliation phase of a real cleanupOne call.
	if err := os.RemoveAll(filepath.Join(dir, "resources")); err != nil {
		t.Fatal(err)
	}
	// Corrupt docker-resources.json directly -- simulating missing/
	// malformed resource metadata.
	if err := os.WriteFile(filepath.Join(dir, "docker-resources.json"), []byte("{not valid json"), 0600); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeDockerClient()

	c := Controller{Manager: m, Now: func() time.Time { return time.Now().UTC() }, Docker: fake}
	report, err := c.GenerateCleanupReport(context.Background(), root, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Safety != SafetyUnverified {
		t.Fatalf("expected UNVERIFIED for unreadable resource metadata, got %q", report.Safety)
	}
	if len(report.Errors) == 0 {
		t.Fatal("expected the corrupt-metadata failure to be visibly recorded in report.Errors, not silently dropped")
	}
}

func TestGenerateCleanupReport_CloudErrorIncludedVerbatim(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := Controller{Manager: m, Now: func() time.Time { return time.Now().UTC() }}
	cleanupErr := errCloudResourcesRemain
	report, err := c.GenerateCleanupReport(context.Background(), root, env, cleanupErr)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range report.Errors {
		if strings.Contains(e, cleanupErr.Error()) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the cleanup attempt's own error to be included verbatim, got %+v", report.Errors)
	}
}

// TestGenerateCleanupReport_NoCallerMechanismCanAssertLive is the
// permanent regression test for the second independent audit's finding
// that LIVE verification must require authenticated evidence from an
// approved real adapter, not a caller-set enum. Controller has no
// CloudVerificationMode/DockerVerificationMode field at all (removed --
// this test would fail to compile if it were ever added back with the
// old free-floating-assertion semantics). VerificationMode is derived
// solely from whether the actual client type implements
// LiveCloudClient/LiveDockerClient -- which nothing in this repository
// does.
func TestGenerateCleanupReport_NoCallerMechanismCanAssertLive(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	dockerFake := NewFakeDockerClient()
	c := Controller{Manager: m, Now: func() time.Time { return time.Now().UTC() }, Cloud: fake, Docker: dockerFake}
	report, err := c.GenerateCleanupReport(context.Background(), root, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.CloudVerification != VerificationSimulated {
		t.Fatalf("expected VerificationSimulated (no caller-set mechanism exists), got %q", report.CloudVerification)
	}
	if report.DockerVerification != VerificationSimulated {
		t.Fatalf("expected VerificationSimulated (no caller-set mechanism exists), got %q", report.DockerVerification)
	}
	if !report.Simulated {
		t.Fatal("expected Simulated=true: nothing in this repository can produce live-verified evidence")
	}
	if _, ok := any(fake).(LiveCloudClient); ok {
		t.Fatal("FakeCloudClient must never implement LiveCloudClient")
	}
	if _, ok := any(dockerFake).(LiveDockerClient); ok {
		t.Fatal("FakeDockerClient must never implement LiveDockerClient")
	}
	s3Adapter := NewS3Adapter(NewFakeS3API(), "111111111111", "ap-south-1")
	if _, ok := any(s3Adapter).(LiveCloudClient); ok {
		t.Fatal("S3Adapter must never implement LiveCloudClient -- no authenticated-evidence mechanism exists in this repository")
	}
}

// TestGenerateCleanupReport_ActiveEnvironmentNeverVerifiedClean is a
// permanent regression test for the second independent audit's
// CRITICAL finding: an environment that was never cleaned at all
// (still StatusActive) must never be reported VERIFIED CLEAN, no matter
// what a fresh resource check happens to find.
func TestGenerateCleanupReport_ActiveEnvironmentNeverVerifiedClean(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Remove local resources by hand (simulating a coincidentally-empty
	// resources dir) without ever running real cleanup -- env.Status on
	// disk is still "active".
	if err := os.RemoveAll(filepath.Join(root, env.ID, "resources")); err != nil {
		t.Fatal(err)
	}
	c := Controller{Manager: m, Now: func() time.Time { return time.Now().UTC() }}
	report, err := c.GenerateCleanupReport(context.Background(), root, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Outcome != StatusActive {
		t.Fatalf("test setup error: expected env.Status to still be active, got %q", report.Outcome)
	}
	if report.Safety == SafetyVerifiedClean {
		t.Fatal("AUDIT REGRESSION: an environment that was never cleaned (still active) must never be reported VERIFIED CLEAN")
	}
}

// TestGenerateCleanupReport_NilCloudClientWithTrackedResourceNeverVerifiedClean
// is the permanent regression test for the second independent audit's
// first CRITICAL repro: a tracked cloud resource must never be silently
// invisible merely because this report's Controller has no Cloud
// client configured.
func TestGenerateCleanupReport_NilCloudClientWithTrackedResourceNeverVerifiedClean(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := CloudResourceRef{Kind: CloudS3Bucket, Account: "111111111111", Region: "ap-south-1", Ref: "forgestack-untouched-by-this-report"}
	if err := m.TrackCloudResource(root, env.ID, "alice", ref); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, env.ID, "resources")); err != nil {
		t.Fatal(err)
	}
	c := Controller{Manager: m, Now: func() time.Time { return time.Now().UTC() }} // Cloud intentionally nil
	report, err := c.GenerateCleanupReport(context.Background(), root, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Safety == SafetyVerifiedClean {
		t.Fatal("AUDIT REGRESSION: reported VERIFIED CLEAN while a tracked cloud resource was never checked because Controller.Cloud was nil")
	}
	cloud, ok := reportResource(report, "cloud")
	if !ok || cloud.Verified || !cloud.Tracked || cloud.Error == "" {
		t.Fatalf("expected the tracked-but-unverifiable cloud resource to appear in the report, got %+v", cloud)
	}
}

// TestGenerateCleanupReport_NilDockerClientWithTrackedResourceNeverVerifiedClean
// is the Docker equivalent of the test above -- the second CRITICAL
// repro from the audit.
func TestGenerateCleanupReport_NilDockerClientWithTrackedResourceNeverVerifiedClean(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.TrackDockerResource(root, env.ID, "alice", TrackedDockerResource{Kind: DockerContainer, Ref: "ignored-container"}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, env.ID, "resources")); err != nil {
		t.Fatal(err)
	}
	c := Controller{Manager: m, Now: func() time.Time { return time.Now().UTC() }} // Docker intentionally nil
	report, err := c.GenerateCleanupReport(context.Background(), root, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Safety == SafetyVerifiedClean {
		t.Fatal("AUDIT REGRESSION: reported VERIFIED CLEAN while a tracked docker resource was never checked because Controller.Docker was nil")
	}
	docker, ok := reportResource(report, "docker")
	if !ok || docker.Verified || !docker.Tracked || docker.Error == "" {
		t.Fatalf("expected the tracked-but-unverifiable docker resource to appear in the report, got %+v", docker)
	}
}

// TestGenerateCleanupReport_CorruptTrackingFileWithNoClientFailsClosed
// proves the fail-closed behavior also holds when a client is nil AND
// the tracking file itself is corrupt (the combination of both
// original audit repro ingredients).
func TestGenerateCleanupReport_CorruptTrackingFileWithNoClientFailsClosed(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, env.ID)
	if err := os.RemoveAll(filepath.Join(dir, "resources")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cloud-resources.json"), []byte("{not valid json"), 0600); err != nil {
		t.Fatal(err)
	}
	c := Controller{Manager: m, Now: func() time.Time { return time.Now().UTC() }} // Cloud nil
	report, err := c.GenerateCleanupReport(context.Background(), root, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Safety != SafetyUnverified {
		t.Fatalf("expected UNVERIFIED for a corrupt tracking file with no client, got %q", report.Safety)
	}
	if len(report.Errors) == 0 {
		t.Fatal("expected the corrupt-metadata failure to be visibly recorded, not silently dropped")
	}
}

// TestGenerateCleanupReport_RequiresPersistedStatusEvenWhenFreshCheckIsClean
// proves the OTHER half of the fix: a fresh check alone is not enough
// either -- VERIFIED CLEAN requires env.Status to already BE
// verified_clean on disk. An environment stuck mid-flight
// (cleanup_pending) whose resources happen to already be gone must
// still report UNVERIFIED, not VERIFIED CLEAN, since the persisted
// decision itself has not concluded.
func TestGenerateCleanupReport_RequiresPersistedStatusEvenWhenFreshCheckIsClean(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, env.ID, "resources")); err != nil {
		t.Fatal(err)
	}
	env.Status = StatusCleanupPending
	c := Controller{Manager: m, Now: func() time.Time { return time.Now().UTC() }}
	report, err := c.GenerateCleanupReport(context.Background(), root, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Safety == SafetyVerifiedClean {
		t.Fatal("expected cleanup_pending (not yet concluded) to never report VERIFIED CLEAN even though the fresh check found nothing")
	}
}

func TestGenerateCleanupReport_DefaultsToSimulatedWhenUnset(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	fake := NewFakeCloudClient()
	c := Controller{Manager: m, Now: func() time.Time { return time.Now().UTC() }, Cloud: fake} // CloudVerificationMode left unset
	report, err := c.GenerateCleanupReport(context.Background(), root, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.CloudVerification != VerificationSimulated {
		t.Fatalf("expected the unset zero value to default to VerificationSimulated, got %q", report.CloudVerification)
	}
	if !report.Simulated {
		t.Fatal("expected Simulated=true by default -- never claim live AWS-verified evidence unless explicitly asserted")
	}
}

func TestPersistCleanupReport_RetainsFullHistoryAcrossMultipleReports(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	r1 := reportWithOutcome(env.ID, StatusCleanupIncomplete, SafetyActionRequired, time.Now().UTC())
	r2 := reportWithOutcome(env.ID, StatusVerifiedClean, SafetyVerifiedClean, time.Now().UTC().Add(time.Minute))

	if err := PersistCleanupReport(root, r1); err != nil {
		t.Fatal(err)
	}
	if err := PersistCleanupReport(root, r2); err != nil {
		t.Fatal(err)
	}
	reports, err := LoadCleanupReports(root, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 2 {
		t.Fatalf("expected both reports retained as failure/outcome history, got %d", len(reports))
	}
	if reports[0].Outcome != StatusCleanupIncomplete || reports[1].Outcome != StatusVerifiedClean {
		t.Fatalf("expected reports retained in order, got %+v", reports)
	}
}

func TestPersistCleanupReport_RejectsInvalidEnvironmentID(t *testing.T) {
	root := t.TempDir()
	report := reportWithOutcome("../escape", StatusVerifiedClean, SafetyVerifiedClean, time.Now().UTC())
	if err := PersistCleanupReport(root, report); err == nil {
		t.Fatal("expected an invalid environment ID to be rejected")
	}
}
