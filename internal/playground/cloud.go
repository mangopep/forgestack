package playground

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// CloudResourceKind is one cloud resource type this package knows how to
// track and clean up. Only CloudS3Bucket exists today -- by explicit
// decision, v1 supports S3 buckets only: no EC2, EKS, RDS, or NAT Gateway
// cleanup. Breadth should follow, not precede, proof that the
// tag -> track -> reconcile -> verify -> remove -> re-verify pattern below
// is safe end to end on the lowest-blast-radius resource type this
// repository's own Terraform sandbox already creates
// (infrastructure/terraform/aws-s3-sandbox). See
// docs/architecture/CLOUD-CLEANUP-S3.md.
type CloudResourceKind string

const (
	CloudS3Bucket CloudResourceKind = "s3_bucket"
)

// ValidateCloudResourceKind enforces the v1 scope boundary in code, not
// just in a comment: any kind other than CloudS3Bucket is rejected before
// it ever reaches a CloudClient call. This is the single choke point that
// must change (deliberately, reviewed) before this package could ever
// track or remove a second resource kind.
func ValidateCloudResourceKind(kind CloudResourceKind) error {
	if kind != CloudS3Bucket {
		return fmt.Errorf("unsupported cloud resource kind %q: only %q is supported in this version", kind, CloudS3Bucket)
	}
	return nil
}

// ValidateCloudResourceRef rejects a CloudResourceRef with a blank Kind,
// Account, Region, or Ref before it is ever persisted. An incomplete ref
// is never a security hole on its own -- VerifyOwnership still gates
// every real removal on the live tag query, never on the tracked list's
// contents -- but a blank Account/Region/Ref silently breaks
// reconciliation's identity matching: a resource tracked with blank
// fields can never match what a real ListByTag call returns (which
// always has these fields populated), so it is reported Missing forever
// while the real resource it was meant to represent is reported Orphaned
// instead. Rejecting it at tracking time turns that into an immediate,
// actionable error instead of permanent, confusing drift noise.
func ValidateCloudResourceRef(ref CloudResourceRef) error {
	if err := ValidateCloudResourceKind(ref.Kind); err != nil {
		return err
	}
	if strings.TrimSpace(ref.Account) == "" {
		return errors.New("cloud resource Account is required")
	}
	if strings.TrimSpace(ref.Region) == "" {
		return errors.New("cloud resource Region is required")
	}
	if strings.TrimSpace(ref.Ref) == "" {
		return errors.New("cloud resource Ref is required")
	}
	return nil
}

// Tag keys and the one managed-by value this package's own cleanup ever
// writes or relies on. CloudTagManagedBy exists specifically so a resource
// this package did not create -- no forgestack.managed-by tag, even if it
// happens to carry a matching environment-id by coincidence or operator
// error -- is never treated as ours to remove; see VerifyOwnership on
// CloudClient and RemoveTrackedCloudResources.
const (
	CloudTagEnvironmentID = "forgestack.environment-id"
	CloudTagOwner         = "forgestack.owner"
	CloudTagManagedBy     = "forgestack.managed-by"
	CloudManagedByValue   = "forgestack-cleanup"
)

// Sentinel errors a CloudClient implementation returns so callers can
// distinguish "could not check" from "checked, and it's gone" -- the same
// distinction ErrDockerUnavailable exists for, extended to the specific
// AWS failure modes this package must handle explicitly rather than
// folding into one generic error:
//   - ErrCloudThrottled is retryable by the existing bounded-retry
//     controller / the next scheduled pass; it must never be treated as
//     "resource confirmed absent."
//   - ErrCloudCredentialExpired is not retryable by retrying the same
//     call -- a human or a credential refresh process must act -- but it
//     is still "could not check," never "checked, and it's gone."
//   - ErrCloudBucketNotEmpty means Remove could not safely finish
//     emptying the bucket (object versions, delete markers, or an
//     in-progress multipart upload it chose not to force through) and
//     must never be silently treated as a successful deletion.
var (
	ErrCloudUnavailable       = errors.New("cloud provider API is not reachable")
	ErrCloudThrottled         = errors.New("cloud provider API request was throttled")
	ErrCloudCredentialExpired = errors.New("cloud provider credentials expired or were rejected")
	ErrCloudBucketNotEmpty    = errors.New("bucket still contains objects, versions, or delete markers that could not be safely removed")
)

// CloudResourceRef identifies one cloud resource precisely enough that
// "the same resource" always means the same account, region, kind, and
// name -- never inferred from a bare name that could collide across
// accounts or regions. This is both the unit RemoveTrackedCloudResources
// targets and the unit persisted to disk by TrackCloudResource.
type CloudResourceRef struct {
	Kind    CloudResourceKind `json:"kind"`
	Account string            `json:"account"`
	Region  string            `json:"region"`
	// Ref is the resource's own name/identifier within Account+Region --
	// for CloudS3Bucket, the bucket name.
	Ref string `json:"ref"`
}

// CloudClient is the minimal cloud-provider surface this package needs,
// mirroring DockerClient's shape deliberately: list a resource kind by
// exact tag value, and remove one by its exact reference. No
// AWS-SDK-backed implementation of this interface exists in this
// repository yet -- building and exercising one against a real AWS
// account is explicitly deferred to a separately approved increment (see
// docs/architecture/CLOUD-CLEANUP-S3.md's live validation plan). Every
// test in this package uses FakeCloudClient (cloud_fake.go) instead.
type CloudClient interface {
	// ListByTag returns every resource of kind, in this client's own
	// account+region, that currently carries tagKey=tagValue. The client
	// itself is scoped to exactly one account+region; it never spans
	// multiple, so Account/Region on the returned refs always match the
	// client's own configuration, never a caller-supplied value.
	ListByTag(ctx context.Context, kind CloudResourceKind, tagKey, tagValue string) ([]CloudResourceRef, error)
	// VerifyOwnership re-reads ref's current tags directly -- never from
	// a cached ListByTag result -- and reports whether every key in
	// requiredTags still matches exactly (a missing tag counts as a
	// mismatch, never as "don't care"). Callers must call this
	// immediately before Remove, never substitute an earlier ListByTag
	// result for it: tags can change in the window between listing and
	// removal, and this closes that window with one more independent
	// read right before anything is deleted.
	VerifyOwnership(ctx context.Context, ref CloudResourceRef, requiredTags map[string]string) (bool, error)
	// Remove empties (if needed: object versions, delete markers, and
	// in-progress multipart uploads for an S3 bucket) and deletes ref.
	// Must return ErrCloudBucketNotEmpty rather than a generic error if
	// it cannot safely finish emptying the bucket, and must never report
	// success while any object, version, delete marker, or multipart
	// upload remains.
	Remove(ctx context.Context, ref CloudResourceRef) error
}

func isCloudUnavailable(err error) bool {
	return errors.Is(err, ErrCloudUnavailable) || errors.Is(err, ErrCloudThrottled) || errors.Is(err, ErrCloudCredentialExpired)
}
