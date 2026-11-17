package playground

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

// Reconciliation is the result of independently comparing what this
// package's own records say an environment owns in Docker against what
// Docker itself currently reports for that environment's exact label
// value. It is read-only by construction -- Reconcile never removes
// anything; RemoveTrackedDockerResources (below) does that, separately,
// so a caller can inspect drift before deciding to act on it.
type Reconciliation struct {
	DockerAvailable bool                    `json:"docker_available"`
	Tracked         []TrackedDockerResource `json:"tracked"`
	// Present is every resource Docker reports under this environment's
	// label, regardless of whether it was tracked.
	Present []TrackedDockerResource `json:"present"`
	// Orphaned is present in Docker, under this environment's own label,
	// but never recorded as tracked -- drift worth surfacing, not
	// necessarily a bug (a resource could have been labeled and created
	// outside TrackDockerResource), but never silently assumed clean.
	Orphaned []TrackedDockerResource `json:"orphaned,omitempty"`
	// Missing is tracked but no longer present in Docker -- removed by
	// something other than this package, or never actually created.
	Missing []TrackedDockerResource `json:"missing,omitempty"`
}

// Clean reports whether Docker reconciliation found nothing live at all
// (no tracked resources remain present, and -- since Reconcile populates
// Orphaned from exactly what's Present -- no untracked resources under
// this label either). Reconciliation that could not run at all
// (DockerAvailable == false) is never "clean": the absence of evidence is
// not evidence of absence.
func (r Reconciliation) Clean() bool {
	return r.DockerAvailable && len(r.Present) == 0
}

// Reconcile independently compares id's tracked Docker resources against
// what Docker itself reports for DockerLabelEnvironmentID == id. If
// Docker is unavailable, it returns DockerAvailable: false rather than an
// error or an empty-looking result -- a caller must be able to tell
// "nothing found" apart from "couldn't check."
func Reconcile(ctx context.Context, docker DockerClient, root, id string) (Reconciliation, error) {
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return Reconciliation{}, err
	}
	dir := filepath.Join(rootPath, id)
	tracked, err := loadTrackedDockerResources(dir)
	if err != nil {
		return Reconciliation{}, err
	}

	rec := Reconciliation{Tracked: tracked}
	for _, kind := range []DockerResourceKind{DockerContainer, DockerVolume, DockerNetwork} {
		refs, listErr := docker.ListByLabel(ctx, kind, DockerLabelEnvironmentID, id)
		if listErr != nil {
			if isDockerUnavailable(listErr) {
				rec.DockerAvailable = false
				return rec, nil
			}
			return Reconciliation{}, fmt.Errorf("list %s resources: %w", kind, listErr)
		}
		for _, ref := range refs {
			rec.Present = append(rec.Present, TrackedDockerResource{Kind: kind, Ref: ref})
		}
	}
	rec.DockerAvailable = true

	trackedSet := map[TrackedDockerResource]bool{}
	for _, t := range tracked {
		trackedSet[t] = true
	}
	presentSet := map[TrackedDockerResource]bool{}
	for _, p := range rec.Present {
		presentSet[p] = true
		if !trackedSet[p] {
			rec.Orphaned = append(rec.Orphaned, p)
		}
	}
	for _, t := range tracked {
		if !presentSet[t] {
			rec.Missing = append(rec.Missing, t)
		}
	}
	return rec, nil
}

// RemoveTrackedDockerResources removes every resource Docker reports
// present under id's exact label (DockerLabelEnvironmentID == id) --
// never anything matched any other way -- and returns a fresh
// Reconciliation confirming what, if anything, remains afterward. This is
// the Docker-resource equivalent of removeOwnedResources/finalizeCleanup
// for local files: remove the owned set, then independently re-verify
// rather than inferring success from the removal calls not erroring.
func RemoveTrackedDockerResources(ctx context.Context, docker DockerClient, root, id string) (Reconciliation, error) {
	before, err := Reconcile(ctx, docker, root, id)
	if err != nil {
		return Reconciliation{}, err
	}
	if !before.DockerAvailable {
		return before, nil
	}
	var removeErr error
	for _, res := range before.Present {
		if err := docker.Remove(ctx, res.Kind, res.Ref); err != nil && removeErr == nil {
			removeErr = fmt.Errorf("remove %s %s: %w", res.Kind, res.Ref, err)
		}
	}
	after, reconcileErr := Reconcile(ctx, docker, root, id)
	if reconcileErr != nil {
		return Reconciliation{}, reconcileErr
	}
	if removeErr != nil {
		return after, removeErr
	}
	return after, nil
}

func isDockerUnavailable(err error) bool {
	return errors.Is(err, ErrDockerUnavailable)
}
