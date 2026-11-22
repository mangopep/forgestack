package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ttlGracePeriod is slept after every `create --ttl 1s` below, before
// `cleanup-due` runs, so ExpireDue actually finds the environment due --
// exactly the same real-world gap between an environment's TTL and the
// next scheduled cleanup-due/worker pass, just compressed to under two
// seconds for a fast, deterministic test.
const ttlGracePeriod = 1100 * time.Millisecond

// These tests exercise the real compiled playground binary end to end,
// exactly like TestCLICleanupBypassIsBlockedWhenDockerResourceIsTracked
// in main_test.go: never calling internal/playground's Go API directly.
// Every scenario here is entirely local and offline -- no AWS account,
// no AWS credentials, no network access of any kind. That is the whole
// point of --cloud-mode=sim: SeedSimulatedS3Fixtures
// (internal/playground/cloud_provider.go) seeds an in-memory
// FakeCloudClient from what was tracked via `track-cloud`, nothing more.

const (
	testAccount = "123456789012"
	testRegion  = "us-east-1"
)

func TestCLICloudProvider_NoCloudOptionsNeverInitializesCloudComponent(t *testing.T) {
	bin := buildPlaygroundBinary(t)
	root := t.TempDir()

	out := runPlayground(t, bin, "create", "--root", root, "--owner", "alice", "--mode", "playground", "--ttl", "1s")
	created := mustParseID(t, out)
	time.Sleep(ttlGracePeriod)

	// An untracked, ordinary environment with no --cloud-provider at
	// all must behave exactly as if this feature did not exist: cleans
	// up and exits 0, with no mention of any cloud provider anywhere in
	// its output.
	cmd := exec.Command(bin, "cleanup-due", "--root", root) //nolint:gosec // G204: bin is a binary this test built into its own t.TempDir(); root is this test's own t.TempDir()
	combined, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected cleanup-due with no --cloud-provider to succeed, got err=%v output=%s", err, combined)
	}
	if strings.Contains(string(combined), "cloud provider") {
		t.Fatalf("expected no cloud-provider log line when --cloud-provider is omitted, got %s", combined)
	}

	data, err := os.ReadFile(filepath.Join(root, created.ID, "environment.json")) //nolint:gosec // G304: this test's own t.TempDir() and its own earlier `create` output
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	if env.Status != "verified_clean" {
		t.Fatalf("expected verified_clean for an ordinary environment with no cloud options, got %q", env.Status)
	}
}

func TestCLICloudProvider_NormalOperationCleansUpAnOwnedBucket(t *testing.T) {
	bin := buildPlaygroundBinary(t)
	root := t.TempDir()

	out := runPlayground(t, bin, "create", "--root", root, "--owner", "alice", "--mode", "playground", "--ttl", "1s")
	created := mustParseID(t, out)
	time.Sleep(ttlGracePeriod)

	runPlayground(t, bin, "track-cloud",
		"--root", root, "--owner", "alice", "--id", created.ID,
		"--cloud-provider", "s3", "--cloud-account", testAccount, "--cloud-region", testRegion,
		"--ref", "alice-normal-bucket")

	combined := mustRunPlaygroundCombined(t, bin, "cleanup-due",
		"--root", root, "--cloud-provider", "s3", "--cloud-account", testAccount, "--cloud-region", testRegion)
	if !strings.Contains(combined, "SIMULATED") {
		t.Fatalf("expected the cloud-provider log line to say SIMULATED, got %s", combined)
	}

	status := environmentStatus(t, root, created.ID)
	if status != "verified_clean" {
		t.Fatalf("expected verified_clean for a correctly tagged, in-scope bucket, got %q", status)
	}
}

func TestCLICloudProvider_MissingTagNeverVerifiesClean(t *testing.T) {
	bin := buildPlaygroundBinary(t)
	root := t.TempDir()

	out := runPlayground(t, bin, "create", "--root", root, "--owner", "alice", "--mode", "playground", "--ttl", "1s")
	created := mustParseID(t, out)
	time.Sleep(ttlGracePeriod)

	runPlayground(t, bin, "track-cloud",
		"--root", root, "--owner", "alice", "--id", created.ID,
		"--cloud-provider", "s3", "--cloud-account", testAccount, "--cloud-region", testRegion,
		"--ref", "alice-bucket-missing-tag")

	// cleanup-due is expected to report the environment as remaining,
	// never verified -- exit code is not asserted non-zero here because
	// CleanupDue returns the per-environment status either way; the
	// durable state on disk is the source of truth (see
	// TestCLICleanupStillSucceedsForOrdinaryLocalOnlyEnvironment for the
	// equivalent Docker-domain pattern).
	cmd := exec.Command(bin, "cleanup-due", //nolint:gosec // G204: bin is a binary this test built into its own t.TempDir(); all args are this test's own fixed/generated flags
		"--root", root, "--cloud-provider", "s3", "--cloud-account", testAccount, "--cloud-region", testRegion)
	_, _ = cmd.CombinedOutput()

	status := environmentStatus(t, root, created.ID)
	if status == "verified_clean" {
		t.Fatalf("expected a tag-drifted bucket to never be reported verified_clean, got %q", status)
	}
}

func TestCLICloudProvider_WrongAccountRegionNeverVerifiesClean(t *testing.T) {
	bin := buildPlaygroundBinary(t)
	root := t.TempDir()

	out := runPlayground(t, bin, "create", "--root", root, "--owner", "alice", "--mode", "playground", "--ttl", "1s")
	created := mustParseID(t, out)
	time.Sleep(ttlGracePeriod)

	// Tracked under a DIFFERENT account than the cleanup worker below is
	// scoped to.
	runPlayground(t, bin, "track-cloud",
		"--root", root, "--owner", "alice", "--id", created.ID,
		"--cloud-provider", "s3", "--cloud-account", "999999999999", "--cloud-region", "eu-west-1",
		"--ref", "alice-bucket")

	cmd := exec.Command(bin, "cleanup-due", //nolint:gosec // G204: bin is a binary this test built into its own t.TempDir(); all args are this test's own fixed/generated flags
		"--root", root, "--cloud-provider", "s3", "--cloud-account", testAccount, "--cloud-region", testRegion)
	_, _ = cmd.CombinedOutput()

	status := environmentStatus(t, root, created.ID)
	if status == "verified_clean" {
		t.Fatalf("expected a resource tracked under a different account/region to never be falsely reported clean, got %q", status)
	}
	// The tracked record itself must survive -- never discarded just
	// because this run's client couldn't see it.
	if _, err := os.Stat(filepath.Join(root, created.ID, "cloud-resources.json")); err != nil {
		t.Fatalf("expected cloud-resources.json to survive: %v", err)
	}
}

// TestCLICloudProvider_RetriesEscalationAndRestartRecovery proves the
// bounded-retry/backoff/escalation machinery in
// internal/playground/controller.go and cloud_retry.go -- entirely
// pre-existing, unmodified by this CLI integration -- still functions
// correctly when driven through the real CLI across separate process
// restarts, using a "-flaky-2" fixture (fails for 2 attempts, succeeds
// on the 3rd; see SeedSimulatedS3Fixtures's doc comment).
func TestCLICloudProvider_RetriesEscalationAndRestartRecovery(t *testing.T) {
	bin := buildPlaygroundBinary(t)
	root := t.TempDir()

	out := runPlayground(t, bin, "create", "--root", root, "--owner", "alice", "--mode", "playground", "--ttl", "1s")
	created := mustParseID(t, out)
	time.Sleep(ttlGracePeriod)

	runPlayground(t, bin, "track-cloud",
		"--root", root, "--owner", "alice", "--id", created.ID,
		"--cloud-provider", "s3", "--cloud-account", testAccount, "--cloud-region", testRegion,
		"--ref", "alice-bucket-flaky-2")

	runOnce := func() {
		cmd := exec.Command(bin, "cleanup-due", //nolint:gosec // G204: bin is a binary this test built into its own t.TempDir(); all args are this test's own fixed/generated flags
			"--root", root, "--cloud-provider", "s3", "--cloud-account", testAccount, "--cloud-region", testRegion,
			// Disables the safety backoff between real attempts (1 minute
			// by default) so this test can drive several restarts in
			// well under a second; a real deployment would keep the
			// default.
			"--cloud-retry-backoff", "0s")
		_, _ = cmd.CombinedOutput()
	}

	// Attempt 1 (process restart 1): fails, not yet escalated.
	runOnce()
	status := environmentStatus(t, root, created.ID)
	if status != "cleanup_unverified" {
		t.Fatalf("after attempt 1, expected cleanup_unverified, got %q", status)
	}
	if _, err := os.Stat(filepath.Join(root, created.ID, "cloud-cleanup-state.json")); err != nil {
		t.Fatalf("expected the attempt-state sidecar to persist across this restart: %v", err)
	}

	// Attempt 2 (process restart 2, fresh binary invocation and fresh
	// in-memory FakeCloudClient -- the persisted attempt-state sidecar
	// is the only thing carrying the retry count across this restart):
	// still fails, still not yet escalated.
	runOnce()
	status = environmentStatus(t, root, created.ID)
	if status != "cleanup_unverified" {
		t.Fatalf("after attempt 2, expected still cleanup_unverified, got %q", status)
	}

	// Attempt 3 (process restart 3): the fixture now owes 0 failures,
	// succeeds, and the restart-safe attempt state must be cleared.
	runOnce()
	status = environmentStatus(t, root, created.ID)
	if status != "verified_clean" {
		t.Fatalf("after attempt 3, expected verified_clean, got %q", status)
	}
	if data, err := os.ReadFile(filepath.Join(root, created.ID, "cloud-cleanup-state.json")); err == nil { //nolint:gosec // G304: this test's own t.TempDir()
		if !strings.Contains(string(data), `"attempts": 0`) && strings.TrimSpace(string(data)) != "{}" {
			t.Fatalf("expected the attempt-state sidecar to be cleared after success, got %s", data)
		}
	}
}

func TestCLICloudProvider_LiveModeFailsClosedWithNoAWSEnvironment(t *testing.T) {
	bin := buildPlaygroundBinary(t)
	root := t.TempDir()

	cmd := exec.Command(bin, "cleanup-due", //nolint:gosec // G204: bin is a binary this test built into its own t.TempDir(); all args are this test's own fixed flags
		"--root", root, "--cloud-provider", "s3", "--cloud-mode", "live", "--cloud-account", testAccount, "--cloud-region", testRegion)
	// Deliberately scrub every AWS_*/credential-shaped environment
	// variable (and HOME, which is where ~/.aws would be found) before
	// running: if live mode ever attempted real credential resolution,
	// this would surface as a different, AWS-SDK-shaped error instead
	// of the static fail-closed message asserted below.
	cmd.Env = filterEnv(os.Environ(), "AWS_", "HOME")
	combined, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected --cloud-mode=live to fail closed, got exit 0: %s", combined)
	}
	if !strings.Contains(string(combined), "live AWS cloud mode is not available") {
		t.Fatalf("expected the static fail-closed message, got %s", combined)
	}
}

func filterEnv(env []string, dropPrefixes ...string) []string {
	var out []string
	for _, kv := range env {
		drop := false
		for _, prefix := range dropPrefixes {
			if strings.HasPrefix(kv, prefix) {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}

func mustParseID(t *testing.T, out []byte) struct{ ID string } {
	t.Helper()
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &created); err != nil {
		t.Fatalf("could not parse `create` output %q: %v", out, err)
	}
	if created.ID == "" {
		t.Fatalf("expected `create` to report a new environment ID, got %q", out)
	}
	return struct{ ID string }{created.ID}
}

func mustRunPlaygroundCombined(t *testing.T, bin string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...) //nolint:gosec // G204: bin is a binary this test built into its own t.TempDir(), and args are this test's own fixed/generated flags, not external input
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("playground %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

func environmentStatus(t *testing.T, root, id string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, id, "environment.json")) //nolint:gosec // G304: this test's own t.TempDir() and its own earlier `create` output
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	return env.Status
}
