package playground

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Docker label keys this package uses to scope every Docker resource it
// will ever list or remove to exactly one owning environment. A real
// DockerClient filters strictly by these labels -- it has no code path
// that lists or removes anything by name, pattern, or "all resources of
// kind X," which is what makes cross-environment deletion structurally
// impossible rather than merely avoided by convention.
const (
	DockerLabelEnvironmentID = "com.forgestack.playground-id"
	DockerLabelOwner         = "com.forgestack.playground-owner"
)

// DockerResourceKind is one of the three Docker object kinds this package
// ever lists or removes. Never "image" -- images are shared across
// environments by design and this package has no business removing one.
type DockerResourceKind string

const (
	DockerContainer DockerResourceKind = "container"
	DockerVolume    DockerResourceKind = "volume"
	DockerNetwork   DockerResourceKind = "network"
)

// ErrDockerUnavailable is returned by a DockerClient method when the
// docker CLI isn't on PATH or the daemon isn't reachable. Callers must
// treat this as "reconciliation could not run," never as "nothing found" --
// the two are not the same, and conflating them would let real orphaned
// resources go undetected simply because Docker happened to be down.
var ErrDockerUnavailable = errors.New("docker is not available")

// DockerClient is the minimal Docker surface this package needs: list a
// resource kind by exact label value, and remove one by its exact
// reference (container ID, volume name, or network name). A real
// implementation should never support listing or removing by anything
// else -- no name pattern, no "all of kind X," no prune.
type DockerClient interface {
	ListByLabel(ctx context.Context, kind DockerResourceKind, labelKey, labelValue string) ([]string, error)
	Remove(ctx context.Context, kind DockerResourceKind, ref string) error
}

// execDockerClient shells out to the real docker CLI. It never passes
// --force/-f, never runs a prune command, and every list/remove call is
// scoped to one exact label value supplied by the caller -- see
// DockerLabelEnvironmentID's doc comment.
type execDockerClient struct{}

// NewDockerClient returns a DockerClient backed by the real docker CLI on
// this machine. Every operation it performs is scoped to an exact label
// value; see the package-level doc comment on DockerLabelEnvironmentID.
func NewDockerClient() DockerClient { return execDockerClient{} }

func (execDockerClient) ListByLabel(ctx context.Context, kind DockerResourceKind, labelKey, labelValue string) ([]string, error) {
	args, err := listArgs(kind, labelKey, labelValue)
	if err != nil {
		return nil, err
	}
	out, err := runDocker(ctx, args...)
	if err != nil {
		return nil, err
	}
	var refs []string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			refs = append(refs, line)
		}
	}
	return refs, nil
}

func listArgs(kind DockerResourceKind, labelKey, labelValue string) ([]string, error) {
	filter := fmt.Sprintf("label=%s=%s", labelKey, labelValue)
	switch kind {
	case DockerContainer:
		// -a: a stopped-but-not-yet-removed container must still be found
		// and cleaned up, not silently skipped because it isn't running.
		return []string{"ps", "-a", "--filter", filter, "--format", "{{.ID}}"}, nil
	case DockerVolume:
		return []string{"volume", "ls", "--filter", filter, "--format", "{{.Name}}"}, nil
	case DockerNetwork:
		return []string{"network", "ls", "--filter", filter, "--format", "{{.Name}}"}, nil
	default:
		return nil, fmt.Errorf("unknown docker resource kind %q", kind)
	}
}

func (execDockerClient) Remove(ctx context.Context, kind DockerResourceKind, ref string) error {
	if strings.TrimSpace(ref) == "" {
		return errors.New("refusing to remove an empty docker resource reference")
	}
	var args []string
	switch kind {
	case DockerContainer:
		// Deliberately no -f: a still-running container must fail removal
		// (reported up as a real error, never silently skipped or force-
		// killed) rather than this package force-stopping someone's
		// running workload.
		args = []string{"rm", ref}
	case DockerVolume:
		args = []string{"volume", "rm", ref}
	case DockerNetwork:
		args = []string{"network", "rm", ref}
	default:
		return fmt.Errorf("unknown docker resource kind %q", kind)
	}
	_, err := runDocker(ctx, args...)
	return err
}

func runDocker(ctx context.Context, args ...string) (string, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return "", ErrDockerUnavailable
	}
	//nolint:gosec // G204: args are a fixed, package-constructed argv (see listArgs/Remove above), never a caller-supplied shell string
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if isDockerUnavailableError(stderr.String()) {
			return "", ErrDockerUnavailable
		}
		return "", fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, truncateDockerOutput(stderr.String()))
	}
	return stdout.String(), nil
}

// isDockerUnavailableError reports whether docker CLI stderr indicates the
// daemon isn't reachable, rather than a real command failure. Older Docker
// CLIs report this as "Cannot connect to the Docker daemon"; newer ones
// (Docker Desktop's docker.sock proxy) instead say "failed to connect to
// the docker API ... dial unix ...: connect: no such file or directory" --
// both must be recognized, or a developer without Docker Desktop running
// sees a red test/command for an environment limitation, not a code bug.
func isDockerUnavailableError(stderr string) bool {
	return strings.Contains(stderr, "Cannot connect to the Docker daemon") ||
		strings.Contains(stderr, "failed to connect to the docker API") ||
		strings.Contains(stderr, "connect: no such file or directory") ||
		strings.Contains(stderr, "connect: connection refused")
}

func truncateDockerOutput(s string) string {
	const max = 2000
	if len(s) <= max {
		return s
	}
	return s[:max] + "... (truncated)"
}
