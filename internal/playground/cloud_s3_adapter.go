package playground

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithy "github.com/aws/smithy-go"
)

// maxDeleteObjectsBatch is S3's own hard limit on how many (Key,
// VersionId) pairs a single DeleteObjects call may carry. Not a tuning
// knob -- a real API constraint this adapter must respect when emptying
// a bucket with more than 1000 versions/delete markers.
const maxDeleteObjectsBatch = 1000

// S3Adapter is the production CloudClient implementation for S3
// buckets, backed by S3API (cloud_s3_api.go). It makes real AWS calls
// only when API is a real *s3.Client that OTHER code constructed and
// authenticated; in every test in this package, API is an in-memory
// fake and zero network traffic occurs.
//
// Account and Region together fix this adapter's own scope. Region is
// never chosen by this package -- it is whatever real endpoint/region
// the caller's *s3.Client was configured for; this field exists so this
// adapter can label every CloudResourceRef it produces and reject
// (checkScope) a ref from a different scope before ever calling the
// API with it. Account is passed as ExpectedBucketOwner on every single
// S3 call this adapter makes that supports it (all seven do): an
// independent, server-side check -- S3 itself refuses the request with
// AccessDenied if the bucket's real owning account does not match --
// entirely apart from this package's own tag-based ownership check.
//
// Account is still operator-asserted, never independently confirmed by
// any call this adapter makes: confirming it from AWS itself would need
// an STS GetCallerIdentity call and the sts service module, which this
// package deliberately does not add (see the task that authorized this
// adapter: "do not add SDK config or credentials modules unless
// technically necessary" -- STS is not necessary for S3 cleanup, only
// for independently confirming an operator-supplied string). See
// docs/architecture/CLOUD-CLEANUP-S3.md for the full writeup of this
// limitation and why it is safe: ExpectedBucketOwner still means a
// wrong Account value can only ever cause an API call to be correctly
// REFUSED by AWS (fewer successful removals, never more), it can never
// grant unintended access, because every call is authenticated by the
// credentials baked into API, never by the Account string.
type S3Adapter struct {
	API     S3API
	Account string
	Region  string
}

// NewS3Adapter returns an S3Adapter wrapping api. It constructs nothing
// live itself and resolves no credential: api is entirely caller-
// supplied, and this function never calls config.LoadDefaultConfig,
// never reads ~/.aws, never reads AWS_* environment variables, and never
// contacts the EC2/ECS instance metadata service. A caller wiring in a
// real adapter builds a real, authenticated *s3.Client themselves,
// outside this package, and passes it as api; every test in this
// package passes a fake (cloud_s3_fake_test.go) instead. See
// docs/architecture/CLOUD-CLEANUP-S3.md's NOT EXECUTED live validation
// plan for what constructing and exercising a real client would
// require, and why this increment deliberately stops short of it.
func NewS3Adapter(api S3API, account, region string) *S3Adapter {
	return &S3Adapter{API: api, Account: account, Region: region}
}

// Compile-time proof S3Adapter actually implements CloudClient (cloud.go).
var _ CloudClient = (*S3Adapter)(nil)

var errS3ScopeMismatch = errors.New("cloud resource ref's account/region does not match this S3Adapter's own configured scope")

// checkScope rejects a ref this adapter's own account+region
// configuration cannot legitimately act on -- before any API call is
// ever made with it. The CloudClient interface already documents that
// one client is scoped to exactly one account+region; this is that
// contract enforced in code for the one implementation in this package
// that could otherwise be handed a cross-scope ref by a caller bug.
func (a *S3Adapter) checkScope(ref CloudResourceRef) error {
	if ref.Account != a.Account || ref.Region != a.Region {
		return fmt.Errorf("%w (ref account=%q region=%q, adapter account=%q region=%q)",
			errS3ScopeMismatch, ref.Account, ref.Region, a.Account, a.Region)
	}
	return nil
}

func strPtr(s string) *string { return &s }

func strVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// s3ErrorCode returns err's AWS error code (e.g. "NoSuchBucket",
// "SlowDown", "NoSuchTagSet") if err -- however deeply wrapped -- is an
// AWS API error, and "" otherwise.
func s3ErrorCode(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}
	return ""
}

// classifyS3Error maps a raw S3API error into this package's own
// CloudClient sentinel vocabulary (cloud.go), so every caller elsewhere
// in this package -- CloudReconcile, RemoveTrackedCloudResources,
// Controller.attemptCloudCleanup -- already knows how to react, with no
// S3-specific knowledge of its own. Deliberately fails closed: any error
// code this function does not specifically recognize is mapped to
// ErrCloudUnavailable (never treated as "confirmed absent" and never
// silently swallowed), because treating an unrecognized AWS failure mode
// as success is exactly the kind of mistake this package's core
// invariant exists to rule out.
func classifyS3Error(op string, err error) error {
	if err == nil {
		return nil
	}
	switch s3ErrorCode(err) {
	case "SlowDown", "ThrottlingException", "RequestLimitExceeded", "TooManyRequestsException", "Throttling":
		return fmt.Errorf("s3 %s throttled: %w: %w", op, ErrCloudThrottled, err)
	case "ExpiredToken", "ExpiredTokenException", "RequestExpired", "InvalidClientTokenId",
		"UnrecognizedClientException", "AuthFailure", "InvalidAccessKeyId", "SignatureDoesNotMatch":
		return fmt.Errorf("s3 %s credentials rejected: %w: %w", op, ErrCloudCredentialExpired, err)
	case "AccessDenied", "Forbidden", "AllAccessDisabled":
		// Could be a real permission problem, could be ExpectedBucketOwner
		// correctly refusing a scope mismatch -- either way, this
		// adapter cannot distinguish "not ours" from "temporarily
		// denied" from here, so it reports unavailable (never "gone",
		// never "clean") rather than guessing.
		return fmt.Errorf("s3 %s access denied: %w: %w", op, ErrCloudUnavailable, err)
	case "BucketNotEmpty":
		return ErrCloudBucketNotEmpty
	default:
		return fmt.Errorf("s3 %s: %w: %w", op, ErrCloudUnavailable, err)
	}
}

// getBucketTags returns bucket's current tags as a map, with
// "NoSuchTagSet" (a bucket that simply has no tags -- not a hard error)
// and "NoSuchBucket" (gone already) collapsed into an empty, nil-error
// map, exactly matching FakeCloudClient.VerifyOwnership's existing "not
// found is never an error" contract (cloud_fake.go).
func (a *S3Adapter) getBucketTags(ctx context.Context, bucket string) (map[string]string, error) {
	resp, err := a.API.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{
		Bucket:              strPtr(bucket),
		ExpectedBucketOwner: strPtr(a.Account),
	})
	if err != nil {
		switch s3ErrorCode(err) {
		case "NoSuchTagSet", "NoSuchBucket":
			return map[string]string{}, nil
		}
		return nil, classifyS3Error("GetBucketTagging", err)
	}
	tags := make(map[string]string, len(resp.TagSet))
	for _, t := range resp.TagSet {
		tags[strVal(t.Key)] = strVal(t.Value)
	}
	return tags, nil
}

// ListByTag implements CloudClient.ListByTag for S3 buckets: enumerate
// every bucket in this adapter's own Region (filtered server-side via
// ListBucketsInput.BucketRegion, then re-checked client-side -- fail
// closed, never widen scope on an ambiguous response), then read each
// one's tags directly (never cached) and keep only those whose tagKey
// matches tagValue exactly.
func (a *S3Adapter) ListByTag(ctx context.Context, kind CloudResourceKind, tagKey, tagValue string) ([]CloudResourceRef, error) {
	if err := ValidateCloudResourceKind(kind); err != nil {
		return nil, err
	}
	var out []CloudResourceRef
	var token *string
	for {
		resp, err := a.API.ListBuckets(ctx, &s3.ListBucketsInput{
			BucketRegion:      strPtr(a.Region),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, classifyS3Error("ListBuckets", err)
		}
		for _, b := range resp.Buckets {
			name := strVal(b.Name)
			if name == "" {
				continue
			}
			// Defense in depth: re-check the region locally even though
			// BucketRegion was already requested server-side above. A
			// bucket whose BucketRegion this response leaves blank is
			// skipped, never included -- this adapter's scope only ever
			// narrows, never widens, on an ambiguous answer.
			if strVal(b.BucketRegion) != a.Region {
				continue
			}
			tags, err := a.getBucketTags(ctx, name)
			if err != nil {
				return nil, err
			}
			if v, ok := tags[tagKey]; ok && v == tagValue {
				out = append(out, CloudResourceRef{Kind: CloudS3Bucket, Account: a.Account, Region: a.Region, Ref: name})
			}
		}
		if strVal(resp.ContinuationToken) == "" {
			break
		}
		token = resp.ContinuationToken
	}
	return out, nil
}

// VerifyOwnership implements CloudClient.VerifyOwnership for S3 buckets:
// a direct, uncached GetBucketTagging call, comparing every key in
// requiredTags against what the bucket's tags actually are right now --
// a missing tag (including the bucket having no tags at all, or being
// gone already) counts as a mismatch, never as "don't care" or an error.
func (a *S3Adapter) VerifyOwnership(ctx context.Context, ref CloudResourceRef, requiredTags map[string]string) (bool, error) {
	if err := ValidateCloudResourceKind(ref.Kind); err != nil {
		return false, err
	}
	if err := a.checkScope(ref); err != nil {
		return false, err
	}
	tags, err := a.getBucketTags(ctx, ref.Ref)
	if err != nil {
		return false, err
	}
	for k, want := range requiredTags {
		if tags[k] != want {
			return false, nil
		}
	}
	return true, nil
}

// emptyBucket removes every object version and delete marker, and
// aborts every in-progress multipart upload, this adapter's own
// enumeration finds for bucket -- never anything else, never a
// caller-selected key. Returns the count of items it found (for the
// caller to decide whether a subsequent DeleteBucket should even be
// attempted) and the first error encountered, if any.
func (a *S3Adapter) emptyBucket(ctx context.Context, bucket string) (removed int, err error) {
	n, err := a.deleteAllObjectVersions(ctx, bucket)
	removed += n
	if err != nil {
		return removed, err
	}
	n, err = a.abortAllMultipartUploads(ctx, bucket)
	removed += n
	if err != nil {
		return removed, err
	}
	return removed, nil
}

func (a *S3Adapter) deleteAllObjectVersions(ctx context.Context, bucket string) (int, error) {
	var keyMarker, versionIDMarker *string
	total := 0
	for {
		resp, err := a.API.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
			Bucket:              strPtr(bucket),
			ExpectedBucketOwner: strPtr(a.Account),
			KeyMarker:           keyMarker,
			VersionIdMarker:     versionIDMarker,
		})
		if err != nil {
			if s3ErrorCode(err) == "NoSuchBucket" {
				return total, nil // already gone -- nothing to empty
			}
			return total, classifyS3Error("ListObjectVersions", err)
		}

		var ids []types.ObjectIdentifier
		for _, v := range resp.Versions {
			ids = append(ids, types.ObjectIdentifier{Key: v.Key, VersionId: v.VersionId})
		}
		for _, d := range resp.DeleteMarkers {
			ids = append(ids, types.ObjectIdentifier{Key: d.Key, VersionId: d.VersionId})
		}
		n, err := a.batchDelete(ctx, bucket, ids)
		total += n
		if err != nil {
			return total, err
		}

		if resp.IsTruncated == nil || !*resp.IsTruncated {
			break
		}
		keyMarker = resp.NextKeyMarker
		versionIDMarker = resp.NextVersionIdMarker
	}
	return total, nil
}

// batchDelete calls DeleteObjects in chunks of at most
// maxDeleteObjectsBatch, and treats ANY per-item failure DeleteObjects
// reports back (types.Error entries in the response, not just a
// transport-level error) as a reason to stop and report
// ErrCloudBucketNotEmpty -- never silently continue past a partial
// failure and never report success while even one version/delete marker
// this adapter itself just enumerated is left behind.
func (a *S3Adapter) batchDelete(ctx context.Context, bucket string, ids []types.ObjectIdentifier) (int, error) {
	deleted := 0
	for len(ids) > 0 {
		batch := ids
		if len(batch) > maxDeleteObjectsBatch {
			batch = ids[:maxDeleteObjectsBatch]
		}
		ids = ids[len(batch):]

		resp, err := a.API.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket:              strPtr(bucket),
			ExpectedBucketOwner: strPtr(a.Account),
			Delete:              &types.Delete{Objects: batch, Quiet: boolPtr(true)},
		})
		if err != nil {
			return deleted, classifyS3Error("DeleteObjects", err)
		}
		deleted += len(resp.Deleted)
		if len(resp.Errors) > 0 {
			return deleted, fmt.Errorf("%w: DeleteObjects reported %d item(s) it could not remove (first: %s %s: %s)",
				ErrCloudBucketNotEmpty, len(resp.Errors), strVal(resp.Errors[0].Key), strVal(resp.Errors[0].VersionId), strVal(resp.Errors[0].Message))
		}
	}
	return deleted, nil
}

func (a *S3Adapter) abortAllMultipartUploads(ctx context.Context, bucket string) (int, error) {
	var keyMarker, uploadIDMarker *string
	total := 0
	for {
		resp, err := a.API.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{
			Bucket:              strPtr(bucket),
			ExpectedBucketOwner: strPtr(a.Account),
			KeyMarker:           keyMarker,
			UploadIdMarker:      uploadIDMarker,
		})
		if err != nil {
			if s3ErrorCode(err) == "NoSuchBucket" {
				return total, nil
			}
			return total, classifyS3Error("ListMultipartUploads", err)
		}
		for _, u := range resp.Uploads {
			_, err := a.API.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
				Bucket:              strPtr(bucket),
				Key:                 u.Key,
				UploadId:            u.UploadId,
				ExpectedBucketOwner: strPtr(a.Account),
			})
			if err != nil && s3ErrorCode(err) != "NoSuchUpload" {
				return total, classifyS3Error("AbortMultipartUpload", err)
			}
			total++
		}
		if resp.IsTruncated == nil || !*resp.IsTruncated {
			break
		}
		keyMarker = resp.NextKeyMarker
		uploadIDMarker = resp.NextUploadIdMarker
	}
	return total, nil
}

func boolPtr(b bool) *bool { return &b }

// Remove implements CloudClient.Remove for S3 buckets: empties the
// bucket (every object version, every delete marker, every in-progress
// multipart upload this adapter's own enumeration finds -- never
// anything a caller selects, never anything in another bucket), then
// deletes the now-empty bucket. Never re-verifies ownership itself --
// the CloudClient interface's documented contract is that a caller
// (RemoveTrackedCloudResources, in this package) calls VerifyOwnership
// immediately before Remove; Remove trusts that already happened, same
// as FakeCloudClient.Remove.
func (a *S3Adapter) Remove(ctx context.Context, ref CloudResourceRef) error {
	if err := ValidateCloudResourceKind(ref.Kind); err != nil {
		return err
	}
	if err := a.checkScope(ref); err != nil {
		return err
	}
	if _, err := a.emptyBucket(ctx, ref.Ref); err != nil {
		return err
	}
	_, err := a.API.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket:              strPtr(ref.Ref),
		ExpectedBucketOwner: strPtr(a.Account),
	})
	if err != nil {
		if s3ErrorCode(err) == "NoSuchBucket" {
			return nil // already gone -- matches Docker/FakeCloudClient's "already absent is not an error"
		}
		return classifyS3Error("DeleteBucket", err)
	}
	return nil
}
