package playground

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3API is the minimal subset of *s3.Client's own method set that
// S3Adapter (cloud_s3_adapter.go) needs to implement CloudClient for S3
// buckets. Every method signature here -- including the ctx,
// *s3.XxxInput, optFns ...func(*s3.Options) shape -- is copied verbatim
// from the real github.com/aws/aws-sdk-go-v2/service/s3 Client, not
// reinvented, so that a real *s3.Client satisfies this interface with
// zero adapter/wrapper code once some OTHER code constructs one with
// real, resolved AWS credentials (see the compile-time assertion
// below).
//
// This package deliberately does not import
// github.com/aws/aws-sdk-go-v2/config or
// github.com/aws/aws-sdk-go-v2/credentials, and calls neither: resolving
// real AWS credentials and constructing a real, authenticated *s3.Client
// is entirely the caller's responsibility, kept out of this package on
// purpose (see docs/architecture/CLOUD-CLEANUP-S3.md's "who constructs
// the real client" section). Every test in this package supplies a
// hand-written, fully in-memory fake implementing this interface instead
// (see cloud_s3_fake_test.go) -- never a real *s3.Client, never real AWS
// credentials, never a network call, never an AWS endpoint.
type S3API interface {
	// ListBuckets enumerates buckets, filterable by BucketRegion
	// server-side -- see S3Adapter.ListByTag.
	ListBuckets(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error)
	// GetBucketTagging reads one bucket's current tags directly --
	// never cached -- used by both ListByTag (to filter) and
	// VerifyOwnership (to re-check immediately before any removal).
	GetBucketTagging(ctx context.Context, params *s3.GetBucketTaggingInput, optFns ...func(*s3.Options)) (*s3.GetBucketTaggingOutput, error)
	// ListObjectVersions enumerates every live object version AND every
	// delete marker in one bucket -- both are required to actually
	// empty a versioned bucket; a plain object listing alone is not
	// enough and would leave DeleteBucket failing with BucketNotEmpty.
	ListObjectVersions(ctx context.Context, params *s3.ListObjectVersionsInput, optFns ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error)
	// ListMultipartUploads enumerates in-progress multipart uploads,
	// which also block DeleteBucket until aborted.
	ListMultipartUploads(ctx context.Context, params *s3.ListMultipartUploadsInput, optFns ...func(*s3.Options)) (*s3.ListMultipartUploadsOutput, error)
	// DeleteObjects batch-deletes up to 1000 (Key, VersionId) pairs per
	// call -- used to remove the object versions and delete markers
	// ListObjectVersions found, batched by S3Adapter.Remove.
	DeleteObjects(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
	// AbortMultipartUpload cancels one in-progress multipart upload.
	AbortMultipartUpload(ctx context.Context, params *s3.AbortMultipartUploadInput, optFns ...func(*s3.Options)) (*s3.AbortMultipartUploadOutput, error)
	// DeleteBucket is only ever called by S3Adapter.Remove after every
	// object version, delete marker, and multipart upload this
	// adapter's own enumeration found has already been removed/aborted.
	DeleteBucket(ctx context.Context, params *s3.DeleteBucketInput, optFns ...func(*s3.Options)) (*s3.DeleteBucketOutput, error)
	// HeadBucket checks whether a bucket exists by exact name alone --
	// never by tag -- used by S3Adapter.Exists. Deliberately a
	// different call than GetBucketTagging: HeadBucket's whole purpose
	// is an identity check, so S3Adapter.Exists never depends on tag-
	// read permissions or a bucket's tag state to answer "is it there."
	HeadBucket(ctx context.Context, params *s3.HeadBucketInput, optFns ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
}

// This is a compile-time-only type assertion: it proves *s3.Client
// satisfies S3API exactly as written above, without ever constructing
// one (nil pointer, never dereferenced, never dialed) and without this
// package importing aws-sdk-go-v2/config or resolving any credential.
// If a future SDK release changes any of these eight methods' shape,
// this line -- not a runtime failure somewhere else -- is what breaks
// the build.
var _ S3API = (*s3.Client)(nil)
