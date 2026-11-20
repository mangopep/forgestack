package playground

import (
	"context"
	"fmt"
	"path/filepath"
)

// CloudReconciliation is the result of independently comparing what this
// package's own records say an environment owns in the cloud against
// what the cloud provider itself currently reports for that environment's
// exact tag value. Mirrors Reconciliation (reconcile.go) deliberately --
// same Clean() semantics, same "unavailable is never clean" rule.
type CloudReconciliation struct {
	CloudAvailable bool               `json:"cloud_available"`
	Tracked        []CloudResourceRef `json:"tracked"`
	// Present is every resource the client reports under this
	// environment's tag, regardless of whether it was tracked.
	Present []CloudResourceRef `json:"present"`
	// Orphaned is present in the cloud, under this environment's own
	// tag, but never recorded as tracked.
	Orphaned []CloudResourceRef `json:"orphaned,omitempty"`
	// Missing is tracked but no longer present -- removed by something
	// other than this package, or never actually created.
	Missing []CloudResourceRef `json:"missing,omitempty"`
}

// Clean reports whether cloud reconciliation found nothing live at all.
// Reconciliation that could not run at all (CloudAvailable == false) is
// never "clean": the absence of evidence is not evidence of absence.
func (r CloudReconciliation) Clean() bool {
	return r.CloudAvailable && len(r.Present) == 0
}

// CloudReconcile independently compares id's tracked cloud resources
// against what client reports present for CloudTagEnvironmentID == id.
// Unavailability (throttling, credential failure, network error) is
// reported as CloudAvailable: false, never as an empty-looking clean
// result -- a caller must be able to tell "nothing found" apart from
// "couldn't check."
func CloudReconcile(ctx context.Context, client CloudClient, root, id string) (CloudReconciliation, error) {
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return CloudReconciliation{}, err
	}
	dir := filepath.Join(rootPath, id)
	tracked, err := loadTrackedCloudResources(dir)
	if err != nil {
		return CloudReconciliation{}, err
	}

	rec := CloudReconciliation{Tracked: tracked}
	present, listErr := client.ListByTag(ctx, CloudS3Bucket, CloudTagEnvironmentID, id)
	if listErr != nil {
		if isCloudUnavailable(listErr) {
			rec.CloudAvailable = false
			return rec, nil
		}
		return CloudReconciliation{}, fmt.Errorf("list cloud resources: %w", listErr)
	}
	rec.Present = present
	rec.CloudAvailable = true

	trackedSet := map[CloudResourceRef]bool{}
	for _, t := range tracked {
		trackedSet[t] = true
	}
	presentSet := map[CloudResourceRef]bool{}
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

// RemoveTrackedCloudResources removes every resource client reports
// present under id's exact tag (CloudTagEnvironmentID == id) -- never
// anything matched any other way -- and returns a fresh
// CloudReconciliation confirming what, if anything, remains afterward.
//
// Before removing each resource, it independently re-verifies ownership
// (CloudTagEnvironmentID, CloudTagOwner, and CloudTagManagedBy must all
// still match exactly) via VerifyOwnership -- a direct read, never the
// ListByTag result above -- closing the window between "listed as
// present" and "about to delete" in which a resource's tags could have
// changed. A resource that fails this re-check, or whose VerifyOwnership
// call itself errors, is never removed and is reported back as still
// Present (and therefore not Clean) in the final reconciliation, not
// silently skipped.
func RemoveTrackedCloudResources(ctx context.Context, client CloudClient, root, id, owner string) (CloudReconciliation, error) {
	before, err := CloudReconcile(ctx, client, root, id)
	if err != nil {
		return CloudReconciliation{}, err
	}
	if !before.CloudAvailable {
		return before, nil
	}
	requiredTags := map[string]string{
		CloudTagEnvironmentID: id,
		CloudTagOwner:         owner,
		CloudTagManagedBy:     CloudManagedByValue,
	}
	var removeErr error
	for _, res := range before.Present {
		owned, verifyErr := client.VerifyOwnership(ctx, res, requiredTags)
		switch {
		case verifyErr != nil:
			if removeErr == nil {
				removeErr = fmt.Errorf("verify ownership of %s: %w", res.Ref, verifyErr)
			}
			continue
		case !owned:
			// Tags changed since listing, or this was never really
			// ours -- never removed on the strength of a stale list
			// alone.
			if removeErr == nil {
				removeErr = fmt.Errorf("%s no longer verifiably owned by this environment immediately before removal", res.Ref)
			}
			continue
		}
		if err := client.Remove(ctx, res); err != nil && removeErr == nil {
			removeErr = fmt.Errorf("remove %s %s: %w", res.Kind, res.Ref, err)
		}
	}
	after, reconcileErr := CloudReconcile(ctx, client, root, id)
	if reconcileErr != nil {
		return CloudReconciliation{}, reconcileErr
	}
	if removeErr != nil {
		return after, removeErr
	}
	return after, nil
}
