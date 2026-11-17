package playground

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

// TestDockerClientRealSmokeTest is the one test in this package that talks
// to a real Docker daemon, and it's skipped outright if one isn't
// reachable -- every other test in this package uses FakeDockerClient
// (see docker_fake.go), per "use deterministic test doubles" for anything
// that doesn't specifically need the real thing. This test uses only
// `docker network create/rm` -- no image pull, no container run -- to stay
// fast and avoid any privileged operation. It creates two networks with
// two different label values specifically to prove the real client's
// label filter is as strict as the fake's: removing one by its exact
// label must never touch the other.
func TestDockerClientRealSmokeTest(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not on PATH; skipping real-Docker smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := NewDockerClient()

	// A real "is the daemon actually reachable" probe, not just "is the
	// binary installed" -- ListByLabel returning ErrDockerUnavailable
	// means the daemon isn't running, which this test must skip on rather
	// than fail on (a developer without Docker Desktop running shouldn't
	// see a red test for an environment limitation, not a code bug).
	if _, err := client.ListByLabel(ctx, DockerNetwork, DockerLabelEnvironmentID, "env-0000000000000000"); errors.Is(err, ErrDockerUnavailable) {
		t.Skip("docker daemon not reachable; skipping real-Docker smoke test")
	}

	suffix := time.Now().UTC().Format("20060102150405.000000")
	ownName := "forgestack-test-own-" + suffix
	otherName := "forgestack-test-other-" + suffix
	ownID := "env-smoketestown0"
	otherID := "env-smoketestoth1"

	mustCreateNetwork(t, ownName, ownID)
	defer removeNetworkIfPresent(ownName)
	mustCreateNetwork(t, otherName, otherID)
	defer removeNetworkIfPresent(otherName)

	refs, err := client.ListByLabel(ctx, DockerNetwork, DockerLabelEnvironmentID, ownID)
	if err != nil {
		t.Fatalf("ListByLabel: %v", err)
	}
	if len(refs) != 1 || refs[0] != ownName {
		t.Fatalf("expected to find exactly %q, got %v", ownName, refs)
	}

	if err := client.Remove(ctx, DockerNetwork, ownName); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	afterOwn, err := client.ListByLabel(ctx, DockerNetwork, DockerLabelEnvironmentID, ownID)
	if err != nil {
		t.Fatalf("ListByLabel after remove: %v", err)
	}
	if len(afterOwn) != 0 {
		t.Errorf("expected the removed network to be gone, still found: %v", afterOwn)
	}

	afterOther, err := client.ListByLabel(ctx, DockerNetwork, DockerLabelEnvironmentID, otherID)
	if err != nil {
		t.Fatalf("ListByLabel for the other network: %v", err)
	}
	if len(afterOther) != 1 || afterOther[0] != otherName {
		t.Fatalf("the other environment's network must survive untouched, got %v", afterOther)
	}
}

func mustCreateNetwork(t *testing.T, name, envID string) {
	t.Helper()
	cmd := exec.Command("docker", "network", "create", //nolint:gosec // G204: name/envID are built from a fixed timestamp format and a literal constant in this test, not external input
		"--label", DockerLabelEnvironmentID+"="+envID,
		"--label", DockerLabelOwner+"=forgestack-test",
		name)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("docker network create %s: %v: %s", name, err, out)
	}
}

func removeNetworkIfPresent(name string) {
	_ = exec.Command("docker", "network", "rm", name).Run() //nolint:gosec,errcheck // best-effort test cleanup; name is this test's own fixed-format identifier
}
