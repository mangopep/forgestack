package playground

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithy "github.com/aws/smithy-go"
)

// fakeS3Object is one version or delete marker in a fakeS3Bucket.
// Modeling both in the same slice, distinguished by deleteMarker,
// mirrors how a real ListObjectVersions response interleaves them.
type fakeS3Object struct {
	key          string
	versionID    string
	isLatest     bool
	deleteMarker bool
}

type fakeS3Upload struct {
	key      string
	uploadID string
}

type fakeS3Bucket struct {
	account string
	region  string
	tags    map[string]string
	objects []fakeS3Object
	uploads []fakeS3Upload
}

// FakeS3API is a deterministic, entirely in-memory S3API (cloud_s3_api.go)
// test double. Every test in this package that exercises S3Adapter uses
// this -- never a real *s3.Client, never AWS credentials, never a
// network call, never an AWS endpoint of any kind. It models exactly the
// S3 behaviors S3Adapter must handle explicitly: cross-call pagination
// (KeyMarker/VersionIdMarker/UploadIdMarker/ContinuationToken), object
// versions, delete markers, in-progress multipart uploads,
// BucketNotEmpty, per-item DeleteObjects failures, and injectable
// AWS-shaped errors (smithy.GenericAPIError with a specific ErrorCode)
// for throttling/credential-expiry/access-denied/not-found.
type FakeS3API struct {
	mu      sync.Mutex
	buckets map[string]*fakeS3Bucket

	// PageSize, if > 0, caps how many items ListBuckets/
	// ListObjectVersions/ListMultipartUploads return per call --
	// forcing deterministic pagination without needing thousands of
	// seeded items to reach a real page boundary.
	PageSize int

	// Injected errors/behaviors, consulted on every matching call until
	// cleared, exactly like FakeCloudClient's injectable fields
	// (cloud_fake.go).
	ListBucketsErr          error
	GetBucketTaggingErr     map[string]error // keyed by bucket name
	ListObjectVersionsErr   error
	DeleteObjectsErr        error
	ListMultipartUploadsErr error
	AbortMultipartUploadErr error
	DeleteBucketErr         error
	HeadBucketErr           map[string]error // keyed by bucket name, for S3Adapter.Exists tests

	// DeleteObjectsPartialFailures makes specific "key/versionID" pairs
	// come back as a per-item types.Error inside an otherwise-successful
	// DeleteObjects response -- simulating AWS reporting a partial
	// failure (e.g. an object lock) rather than a transport-level error.
	DeleteObjectsPartialFailures map[string]bool

	// RemoveFailuresRemaining, if > 0, makes that many DeleteBucket
	// calls fail with a throttling error before a later call succeeds.
	DeleteBucketFailuresRemaining int

	calls []string
}

// NewFakeS3API returns an empty FakeS3API.
func NewFakeS3API() *FakeS3API {
	return &FakeS3API{buckets: map[string]*fakeS3Bucket{}}
}

// SeedBucket adds bucket (account, region, tags) with no objects/uploads yet.
func (f *FakeS3API) SeedBucket(name, account, region string, tags map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	copied := make(map[string]string, len(tags))
	for k, v := range tags {
		copied[k] = v
	}
	f.buckets[name] = &fakeS3Bucket{account: account, region: region, tags: copied}
}

// AddObjectVersion seeds one live object version.
func (f *FakeS3API) AddObjectVersion(bucket, key, versionID string, isLatest bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.buckets[bucket]
	if b == nil {
		return
	}
	b.objects = append(b.objects, fakeS3Object{key: key, versionID: versionID, isLatest: isLatest})
}

// AddDeleteMarker seeds one delete marker.
func (f *FakeS3API) AddDeleteMarker(bucket, key, versionID string, isLatest bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.buckets[bucket]
	if b == nil {
		return
	}
	b.objects = append(b.objects, fakeS3Object{key: key, versionID: versionID, isLatest: isLatest, deleteMarker: true})
}

// AddMultipartUpload seeds one in-progress multipart upload.
func (f *FakeS3API) AddMultipartUpload(bucket, key, uploadID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.buckets[bucket]
	if b == nil {
		return
	}
	b.uploads = append(b.uploads, fakeS3Upload{key: key, uploadID: uploadID})
}

// HasBucket reports whether bucket is still present.
func (f *FakeS3API) HasBucket(bucket string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.buckets[bucket]
	return ok
}

// ObjectCount returns how many versions+delete markers bucket still has.
func (f *FakeS3API) ObjectCount(bucket string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.buckets[bucket]
	if b == nil {
		return 0
	}
	return len(b.objects)
}

// UploadCount returns how many in-progress multipart uploads bucket still has.
func (f *FakeS3API) UploadCount(bucket string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.buckets[bucket]
	if b == nil {
		return 0
	}
	return len(b.uploads)
}

// CallCount returns how many recorded calls start with prefix.
func (f *FakeS3API) CallCount(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func apiErr(code, message string) error {
	return &smithy.GenericAPIError{Code: code, Message: message}
}

func (f *FakeS3API) pageSize() int {
	if f.PageSize <= 0 {
		return 1 << 30
	}
	return f.PageSize
}

func (f *FakeS3API) ListBuckets(_ context.Context, params *s3.ListBucketsInput, _ ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "ListBuckets")
	if f.ListBucketsErr != nil {
		return nil, f.ListBucketsErr
	}
	var names []string
	for name, b := range f.buckets {
		if params.BucketRegion != nil && *params.BucketRegion != "" && b.region != *params.BucketRegion {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	start := 0
	if params.ContinuationToken != nil {
		for i, n := range names {
			if n == *params.ContinuationToken {
				start = i
				break
			}
		}
	}
	end := start + f.pageSize()
	if end > len(names) {
		end = len(names)
	}
	page := names[start:end]

	out := &s3.ListBucketsOutput{}
	for _, name := range page {
		b := f.buckets[name]
		out.Buckets = append(out.Buckets, types.Bucket{Name: strPtr(name), BucketRegion: strPtr(b.region)})
	}
	if end < len(names) {
		out.ContinuationToken = strPtr(names[end])
	}
	return out, nil
}

func (f *FakeS3API) GetBucketTagging(_ context.Context, params *s3.GetBucketTaggingInput, _ ...func(*s3.Options)) (*s3.GetBucketTaggingOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := strVal(params.Bucket)
	f.calls = append(f.calls, "GetBucketTagging:"+name)
	if err := f.GetBucketTaggingErr[name]; err != nil {
		return nil, err
	}
	b, ok := f.buckets[name]
	if !ok {
		return nil, apiErr("NoSuchBucket", "The specified bucket does not exist")
	}
	if err := f.checkExpectedOwner(params.ExpectedBucketOwner, b); err != nil {
		return nil, err
	}
	if len(b.tags) == 0 {
		return nil, apiErr("NoSuchTagSet", "The TagSet does not exist")
	}
	out := &s3.GetBucketTaggingOutput{}
	var keys []string
	for k := range b.tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out.TagSet = append(out.TagSet, types.Tag{Key: strPtr(k), Value: strPtr(b.tags[k])})
	}
	return out, nil
}

func (f *FakeS3API) checkExpectedOwner(expected *string, b *fakeS3Bucket) error {
	if expected != nil && *expected != "" && *expected != b.account {
		return apiErr("AccessDenied", "Access Denied: ExpectedBucketOwner mismatch")
	}
	return nil
}

func (f *FakeS3API) ListObjectVersions(_ context.Context, params *s3.ListObjectVersionsInput, _ ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := strVal(params.Bucket)
	f.calls = append(f.calls, "ListObjectVersions:"+name)
	if f.ListObjectVersionsErr != nil {
		return nil, f.ListObjectVersionsErr
	}
	b, ok := f.buckets[name]
	if !ok {
		return nil, apiErr("NoSuchBucket", "The specified bucket does not exist")
	}
	if err := f.checkExpectedOwner(params.ExpectedBucketOwner, b); err != nil {
		return nil, err
	}

	objs := append([]fakeS3Object(nil), b.objects...)
	sort.Slice(objs, func(i, j int) bool {
		if objs[i].key != objs[j].key {
			return objs[i].key < objs[j].key
		}
		return objs[i].versionID < objs[j].versionID
	})

	start := 0
	if params.KeyMarker != nil {
		for i, o := range objs {
			if o.key == strVal(params.KeyMarker) && o.versionID == strVal(params.VersionIdMarker) {
				start = i + 1
				break
			}
		}
	}
	end := start + f.pageSize()
	if end > len(objs) {
		end = len(objs)
	}
	page := objs[start:end]

	out := &s3.ListObjectVersionsOutput{Name: strPtr(name)}
	for _, o := range page {
		if o.deleteMarker {
			out.DeleteMarkers = append(out.DeleteMarkers, types.DeleteMarkerEntry{Key: strPtr(o.key), VersionId: strPtr(o.versionID), IsLatest: boolPtr(o.isLatest)})
		} else {
			out.Versions = append(out.Versions, types.ObjectVersion{Key: strPtr(o.key), VersionId: strPtr(o.versionID), IsLatest: boolPtr(o.isLatest)})
		}
	}
	truncated := end < len(objs)
	out.IsTruncated = boolPtr(truncated)
	if truncated {
		out.NextKeyMarker = strPtr(objs[end-1].key)
		out.NextVersionIdMarker = strPtr(objs[end-1].versionID)
	}
	return out, nil
}

func (f *FakeS3API) DeleteObjects(_ context.Context, params *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := strVal(params.Bucket)
	f.calls = append(f.calls, "DeleteObjects:"+name)
	if f.DeleteObjectsErr != nil {
		return nil, f.DeleteObjectsErr
	}
	b, ok := f.buckets[name]
	if !ok {
		return nil, apiErr("NoSuchBucket", "The specified bucket does not exist")
	}
	if err := f.checkExpectedOwner(params.ExpectedBucketOwner, b); err != nil {
		return nil, err
	}
	if params.Delete == nil || len(params.Delete.Objects) > maxDeleteObjectsBatch {
		return nil, apiErr("MalformedXML", "too many objects in one DeleteObjects call")
	}

	out := &s3.DeleteObjectsOutput{}
	for _, want := range params.Delete.Objects {
		wantKey, wantVer := strVal(want.Key), strVal(want.VersionId)
		if f.DeleteObjectsPartialFailures[wantKey+"/"+wantVer] {
			out.Errors = append(out.Errors, types.Error{Key: want.Key, VersionId: want.VersionId, Code: strPtr("AccessDenied"), Message: strPtr("simulated per-item delete failure")})
			continue
		}
		idx := -1
		for i, o := range b.objects {
			if o.key == wantKey && o.versionID == wantVer {
				idx = i
				break
			}
		}
		if idx >= 0 {
			b.objects = append(b.objects[:idx], b.objects[idx+1:]...)
		}
		out.Deleted = append(out.Deleted, types.DeletedObject{Key: want.Key, VersionId: want.VersionId})
	}
	return out, nil
}

func (f *FakeS3API) ListMultipartUploads(_ context.Context, params *s3.ListMultipartUploadsInput, _ ...func(*s3.Options)) (*s3.ListMultipartUploadsOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := strVal(params.Bucket)
	f.calls = append(f.calls, "ListMultipartUploads:"+name)
	if f.ListMultipartUploadsErr != nil {
		return nil, f.ListMultipartUploadsErr
	}
	b, ok := f.buckets[name]
	if !ok {
		return nil, apiErr("NoSuchBucket", "The specified bucket does not exist")
	}
	if err := f.checkExpectedOwner(params.ExpectedBucketOwner, b); err != nil {
		return nil, err
	}

	ups := append([]fakeS3Upload(nil), b.uploads...)
	sort.Slice(ups, func(i, j int) bool {
		if ups[i].key != ups[j].key {
			return ups[i].key < ups[j].key
		}
		return ups[i].uploadID < ups[j].uploadID
	})

	start := 0
	if params.KeyMarker != nil {
		for i, u := range ups {
			if u.key == strVal(params.KeyMarker) && u.uploadID == strVal(params.UploadIdMarker) {
				start = i + 1
				break
			}
		}
	}
	end := start + f.pageSize()
	if end > len(ups) {
		end = len(ups)
	}
	page := ups[start:end]

	out := &s3.ListMultipartUploadsOutput{Bucket: strPtr(name)}
	for _, u := range page {
		out.Uploads = append(out.Uploads, types.MultipartUpload{Key: strPtr(u.key), UploadId: strPtr(u.uploadID)})
	}
	truncated := end < len(ups)
	out.IsTruncated = boolPtr(truncated)
	if truncated {
		out.NextKeyMarker = strPtr(ups[end-1].key)
		out.NextUploadIdMarker = strPtr(ups[end-1].uploadID)
	}
	return out, nil
}

func (f *FakeS3API) AbortMultipartUpload(_ context.Context, params *s3.AbortMultipartUploadInput, _ ...func(*s3.Options)) (*s3.AbortMultipartUploadOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := strVal(params.Bucket)
	f.calls = append(f.calls, fmt.Sprintf("AbortMultipartUpload:%s:%s", name, strVal(params.UploadId)))
	if f.AbortMultipartUploadErr != nil {
		return nil, f.AbortMultipartUploadErr
	}
	b, ok := f.buckets[name]
	if !ok {
		return nil, apiErr("NoSuchBucket", "The specified bucket does not exist")
	}
	if err := f.checkExpectedOwner(params.ExpectedBucketOwner, b); err != nil {
		return nil, err
	}
	idx := -1
	for i, u := range b.uploads {
		if u.key == strVal(params.Key) && u.uploadID == strVal(params.UploadId) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, apiErr("NoSuchUpload", "The specified multipart upload does not exist")
	}
	b.uploads = append(b.uploads[:idx], b.uploads[idx+1:]...)
	return &s3.AbortMultipartUploadOutput{}, nil
}

func (f *FakeS3API) DeleteBucket(_ context.Context, params *s3.DeleteBucketInput, _ ...func(*s3.Options)) (*s3.DeleteBucketOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := strVal(params.Bucket)
	f.calls = append(f.calls, "DeleteBucket:"+name)
	if f.DeleteBucketErr != nil {
		return nil, f.DeleteBucketErr
	}
	if f.DeleteBucketFailuresRemaining > 0 {
		f.DeleteBucketFailuresRemaining--
		return nil, apiErr("SlowDown", "simulated transient throttling")
	}
	b, ok := f.buckets[name]
	if !ok {
		return nil, apiErr("NoSuchBucket", "The specified bucket does not exist")
	}
	if err := f.checkExpectedOwner(params.ExpectedBucketOwner, b); err != nil {
		return nil, err
	}
	if len(b.objects) > 0 || len(b.uploads) > 0 {
		return nil, apiErr("BucketNotEmpty", "The bucket you tried to delete is not empty")
	}
	delete(f.buckets, name)
	return &s3.DeleteBucketOutput{}, nil
}

// HeadBucket checks existence by EXACT bucket name alone, deliberately
// never consulting tags -- exactly matching the real HeadBucket API's
// own semantics and giving S3Adapter.Exists something tag-blind to
// call.
func (f *FakeS3API) HeadBucket(_ context.Context, params *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := strVal(params.Bucket)
	f.calls = append(f.calls, "HeadBucket:"+name)
	if err := f.HeadBucketErr[name]; err != nil {
		return nil, err
	}
	b, ok := f.buckets[name]
	if !ok {
		return nil, apiErr("NoSuchBucket", "The specified bucket does not exist")
	}
	if err := f.checkExpectedOwner(params.ExpectedBucketOwner, b); err != nil {
		return nil, err
	}
	return &s3.HeadBucketOutput{}, nil
}

var _ S3API = (*FakeS3API)(nil)
