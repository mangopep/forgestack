package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLICleanupBypassIsBlockedWhenDockerResourceIsTracked is the
// permanent regression test for requirement 4 of the second independent
// audit: reproduce the actual `playground cleanup` CLI bypass end to
// end, through the real compiled binary (not just the Manager API it
// calls), and confirm it is now blocked.
//
// Before the fix, `playground cleanup` (cmd/playground/main.go's
// cleanup() calling playground.NewManager().Cleanup directly, with no
// Docker/Cloud client reachable from the CLI at all) would exit 0 and
// print "VERIFIED CLEAN" for an environment that still had a tracked
// Docker resource -- because Manager.Cleanup never consulted
// docker-resources.json before persisting verified_clean. That status is
// excluded from needsCleanup's retry set, so the tracked resource would
// never be revisited by any later `cleanup-due`/`worker` pass. After the
// fix, the same CLI invocation must exit non-zero, must not print
// "VERIFIED CLEAN", and the persisted environment.json must be left
// cleanup_pending (retryable) rather than verified_clean.
//
// This is entirely local and offline: track-docker only writes a JSON
// sidecar file (no Docker daemon call), and cleanup's guard path takes
// the same local-file-only route -- no network, no AWS, no real Docker
// required.
func TestCLICleanupBypassIsBlockedWhenDockerResourceIsTracked(t *testing.T) {
	bin := buildPlaygroundBinary(t)
	root := t.TempDir()

	out := runPlayground(t, bin, "create", "--root", root, "--owner", "alice", "--mode", "playground", "--ttl", "1h")
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &created); err != nil {
		t.Fatalf("could not parse `create` output %q: %v", out, err)
	}
	if created.ID == "" {
		t.Fatalf("expected `create` to report a new environment ID, got %q", out)
	}

	runPlayground(t, bin, "track-docker",
		"--root", root, "--owner", "alice", "--id", created.ID,
		"--kind", "container", "--ref", "bypass-repro-container")

	cmd := exec.Command(bin, "cleanup", "--root", root, "--owner", "alice", "--id", created.ID) //nolint:gosec // G204: bin is a binary this test just built into its own t.TempDir(), and created.ID is this test's own earlier `create` JSON output, not external input
	combined, runErr := cmd.CombinedOutput()
	if runErr == nil {
		t.Fatalf("THE AUDITED BYPASS: expected `playground cleanup` to exit non-zero while a tracked docker resource remains unverified; output: %s", combined)
	}
	if strings.Contains(string(combined), "VERIFIED CLEAN") {
		t.Fatalf("THE AUDITED BYPASS: `playground cleanup` must never report VERIFIED CLEAN while a tracked docker resource remains unverified; output: %s", combined)
	}

	data, err := os.ReadFile(filepath.Join(root, created.ID, "environment.json")) //nolint:gosec // G304: root is this test's own t.TempDir() and created.ID is this test's own earlier `create` JSON output, not external input
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	if env.Status != "cleanup_pending" {
		t.Fatalf("expected the persisted status to be cleanup_pending (recoverable), got %q", env.Status)
	}

	// The tracked-resource evidence itself must still be there for a
	// later, properly configured `cleanup-due --docker`/`worker --docker`
	// pass to act on.
	if _, err := os.Stat(filepath.Join(root, created.ID, "docker-resources.json")); err != nil {
		t.Fatalf("expected docker-resources.json to survive the rejected cleanup: %v", err)
	}
}

// TestCLICleanupStillSucceedsForOrdinaryLocalOnlyEnvironment proves the
// fix did not regress the overwhelmingly common case the CLI's `cleanup`
// command exists for: a playground with no tracked Docker/cloud
// resource at all must still clean up and exit 0, exactly as before.
func TestCLICleanupStillSucceedsForOrdinaryLocalOnlyEnvironment(t *testing.T) {
	bin := buildPlaygroundBinary(t)
	root := t.TempDir()

	out := runPlayground(t, bin, "create", "--root", root, "--owner", "alice", "--mode", "playground", "--ttl", "1h")
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &created); err != nil {
		t.Fatalf("could not parse `create` output %q: %v", out, err)
	}

	cmd := exec.Command(bin, "cleanup", "--root", root, "--owner", "alice", "--id", created.ID) //nolint:gosec // G204: bin is a binary this test just built into its own t.TempDir(), and created.ID is this test's own earlier `create` JSON output, not external input
	combined, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("expected an ordinary local-only cleanup to still succeed, got err=%v output=%s", runErr, combined)
	}
	if !strings.Contains(string(combined), "VERIFIED CLEAN") {
		t.Fatalf("expected VERIFIED CLEAN in output, got %s", combined)
	}
}

func buildPlaygroundBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "playground")
	build := exec.Command("go", "build", "-o", bin, ".") //nolint:gosec // G204: fixed "go build" argv with a path this test built into its own t.TempDir(), not external input
	build.Dir = mustWd(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("failed to build playground binary: %v\n%s", err, out)
	}
	return bin
}

func runPlayground(t *testing.T, bin string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(bin, args...) //nolint:gosec // G204: bin is a binary this test built into its own t.TempDir(), and args are this test's own fixed/generated flags, not external input
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("playground %v failed: %v\n%s", args, err, out)
	}
	return out
}

func mustWd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}
