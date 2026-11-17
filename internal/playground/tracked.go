package playground

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// TrackedDockerResource is one Docker object this package's own records
// say a specific environment owns -- recorded by the caller (e.g. right
// after `docker run --label com.forgestack.playground-id=<id> ...`), not
// discovered. Reconciliation (reconcile.go) compares this list against
// what Docker itself reports for the same label to detect drift in either
// direction.
type TrackedDockerResource struct {
	Kind DockerResourceKind `json:"kind"`
	Ref  string             `json:"ref"`
}

func trackedResourcesPath(dir string) string { return filepath.Join(dir, "docker-resources.json") }

// TrackDockerResource records that environment id, owned by owner, also
// owns a Docker resource -- validated with the same ownership check
// Cleanup itself uses, so a caller cannot attach a tracked resource to an
// environment it doesn't own. Appends; does not deduplicate beyond exact
// (kind, ref) match, since re-tracking the same resource twice is harmless
// and simpler than requiring every caller to check first.
//
// The whole read-modify-write runs under withEnvironmentLock (the same
// per-environment lock Cleanup uses): without it, two concurrent calls
// tracking two different resources for the same environment could both
// load the same "existing" list, each append their one resource, and
// whichever write happens last would silently discard the other's --
// a lost update, not merely a races-but-converges case like Cleanup's.
func (m Manager) TrackDockerResource(root, id, owner string, res TrackedDockerResource) error {
	if !safeID.MatchString(id) {
		return errors.New("invalid environment ID")
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	dir := filepath.Join(rootPath, id)
	metadataPath := filepath.Join(dir, "environment.json")
	return withEnvironmentLock(dir, func() error {
		if _, err := loadOwnedEnvironment(metadataPath, id, owner); err != nil {
			return err
		}
		existing, err := loadTrackedDockerResources(dir)
		if err != nil {
			return err
		}
		for _, r := range existing {
			if r.Kind == res.Kind && r.Ref == res.Ref {
				return nil
			}
		}
		existing = append(existing, res)
		return writeTrackedDockerResources(dir, existing)
	})
}

func loadTrackedDockerResources(dir string) ([]TrackedDockerResource, error) {
	data, err := os.ReadFile(trackedResourcesPath(dir)) //nolint:gosec // G304: dir is built from a regex-validated environment ID under an operator-supplied root, not arbitrary input
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read tracked docker resources: %w", err)
	}
	var resources []TrackedDockerResource
	if err := json.Unmarshal(data, &resources); err != nil {
		return nil, fmt.Errorf("parse tracked docker resources: %w", err)
	}
	return resources, nil
}

func writeTrackedDockerResources(dir string, resources []TrackedDockerResource) error {
	data, err := json.MarshalIndent(resources, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(trackedResourcesPath(dir), append(data, '\n'))
}
