package playground

import (
	"context"
	"errors"
	"strings"
)

// ForceRemovalRecord is the audit evidence produced by
// ForceRemoveCloudResource: what was asserted, what the independent tag
// check actually found, why, and the outcome. Callers should persist this
// (it is never persisted by this package itself) as the audit trail for
// an emergency removal.
type ForceRemovalRecord struct {
	Ref                   CloudResourceRef `json:"ref"`
	AssertedEnvironmentID string           `json:"asserted_environment_id"`
	AssertedOwner         string           `json:"asserted_owner"`
	Reason                string           `json:"reason"`
	// OwnershipCheck is "matched", "mismatched", or "could_not_verify" --
	// recorded for the audit trail, but deliberately does not gate
	// whether Remove is attempted. See ForceRemoveCloudResource's doc
	// comment for why.
	OwnershipCheck string `json:"ownership_check"`
	Removed        bool   `json:"removed"`
	Error          string `json:"error,omitempty"`
}

// ForceRemoveCloudResource is a deliberately separate, explicitly invoked
// emergency path for removing one specific cloud resource that the
// automatic path (RemoveTrackedCloudResources) will not touch -- for
// example, its owner tag was lost or corrupted, or an operator needs to
// remove it before its environment is even expired. It is never called
// by Controller, Worker, TrackCloudResource, or any other automatic
// cleanup path in this package -- the only caller is a human (or an
// operator script acting on a human's explicit instruction), with every
// field supplied explicitly, so there is nothing for an automatic pass to
// accidentally trigger.
//
// It still runs VerifyOwnership once and records the outcome in the
// returned ForceRemovalRecord, as audit evidence -- but does not refuse
// to remove on a mismatch the way the automatic path does: an operator
// invoking this function is very often doing so precisely because the
// resource's tags are wrong, missing, or otherwise unverifiable, and
// requiring a clean tag match would make the emergency path unable to
// handle the situations it exists for. The caller (a human reading
// reason and the returned record) is responsible for confirming this is
// genuinely the resource they mean to remove.
//
// A non-empty reason is mandatory -- this function refuses to run
// without one -- so every emergency removal this package performs has a
// recorded justification, never a bare "someone called Remove."
func ForceRemoveCloudResource(ctx context.Context, client CloudClient, ref CloudResourceRef, assertedEnvironmentID, assertedOwner, reason string) (ForceRemovalRecord, error) {
	if strings.TrimSpace(reason) == "" {
		return ForceRemovalRecord{}, errors.New("a non-empty reason is required for an emergency cloud resource removal")
	}
	if strings.TrimSpace(ref.Ref) == "" {
		return ForceRemovalRecord{}, errors.New("a non-empty resource Ref is required -- there is nothing to remove")
	}
	rec := ForceRemovalRecord{
		Ref:                   ref,
		AssertedEnvironmentID: assertedEnvironmentID,
		AssertedOwner:         assertedOwner,
		Reason:                reason,
	}
	owned, verifyErr := client.VerifyOwnership(ctx, ref, map[string]string{
		CloudTagEnvironmentID: assertedEnvironmentID,
		CloudTagOwner:         assertedOwner,
		CloudTagManagedBy:     CloudManagedByValue,
	})
	switch {
	case verifyErr != nil:
		rec.OwnershipCheck = "could_not_verify"
	case owned:
		rec.OwnershipCheck = "matched"
	default:
		rec.OwnershipCheck = "mismatched"
	}

	if removeErr := client.Remove(ctx, ref); removeErr != nil {
		rec.Error = removeErr.Error()
		return rec, removeErr
	}
	rec.Removed = true
	return rec, nil
}
