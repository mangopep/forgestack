package playground

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func cloudResourcesPath(dir string) string { return filepath.Join(dir, "cloud-resources.json") }

// TrackCloudResource records that environment id, owned by owner, also
// owns a cloud resource -- validated with the same ownership check
// Cleanup/TrackDockerResource already use, so a caller cannot attach a
// tracked cloud resource to an environment it doesn't own, and rejecting
// any resource kind this package does not yet support removing, or any
// ref with a blank Account/Region/Ref, before it is ever persisted (see
// ValidateCloudResourceRef).
//
// Persisted as cloud-resources.json, a sibling of environment.json inside
// the environment's own directory -- never inside the disposable
// resources/ subdirectory Cleanup removes -- so this record survives
// exactly as long as the environment's own ownership record does. See
// docs/architecture/CLOUD-CLEANUP-S3.md's "metadata durability"
// limitation for what this does and does not guarantee: it protects this
// record from the playground's own Cleanup, not from the environment
// root directory itself being deleted out-of-band.
//
// The whole read-modify-write runs under withEnvironmentLock, for the
// same lost-update reason TrackDockerResource does: without it, two
// concurrent calls tracking two different resources for the same
// environment could each load the same starting list and one would
// silently discard the other's entry on write.
func (m Manager) TrackCloudResource(root, id, owner string, res CloudResourceRef) error {
	if !safeID.MatchString(id) {
		return errors.New("invalid environment ID")
	}
	if err := ValidateCloudResourceRef(res); err != nil {
		return err
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
		existing, err := loadTrackedCloudResources(dir)
		if err != nil {
			return err
		}
		for _, r := range existing {
			if r == res {
				return nil
			}
		}
		existing = append(existing, res)
		return writeTrackedCloudResources(dir, existing)
	})
}

func loadTrackedCloudResources(dir string) ([]CloudResourceRef, error) {
	data, err := os.ReadFile(cloudResourcesPath(dir)) //nolint:gosec // G304: dir is built from a regex-validated environment ID under an operator-supplied root, not arbitrary input
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read tracked cloud resources: %w", err)
	}
	var resources []CloudResourceRef
	if err := json.Unmarshal(data, &resources); err != nil {
		return nil, fmt.Errorf("parse tracked cloud resources: %w", err)
	}
	return resources, nil
}

func writeTrackedCloudResources(dir string, resources []CloudResourceRef) error {
	data, err := json.MarshalIndent(resources, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(cloudResourcesPath(dir), append(data, '\n'))
}
