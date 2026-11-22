package playground

import (
	"fmt"
	"net/url"
)

// consoleURLForCloudResource builds the AWS Console URL a human would
// use to independently eyeball ref's current state. It never makes a
// network call -- this is pure string construction from ref's own
// fields, offline, exactly like the rest of this package's URL-free
// design.
//
// When present is true, it links to the resource's own page (where it
// still exists to be looked at). When present is false -- the resource
// was independently verified absent -- its individual page no longer
// exists in the AWS Console, so this links to the service's own
// inventory/list page for that region instead, which always exists and
// lets a human confirm the resource's continued absence themselves
// (e.g. by searching for it and finding nothing).
//
// Returns "" for any CloudResourceKind this function does not have a
// console mapping for yet (there is only one today: CloudS3Bucket) --
// never a guessed or malformed URL.
//
// THIS LINK IS MANUAL-NAVIGATION CONVENIENCE ONLY, NEVER PROOF OF
// CLEANUP. It is attached to a ResourceReport strictly AFTER that
// resource's Present/Verified fields have already been independently
// decided by a fresh CloudReconcile read (see addCloudResourceReports,
// cleanup_report.go) -- generating or including this URL never itself
// verifies, confirms, or implies anything about the resource's actual
// state. A human clicking it to look is a reasonable extra check; a
// system treating the link's mere existence (or a human's claim to
// have clicked it) as equivalent to this package's own independent
// verification would be a mistake this package's own code never makes
// and this comment explicitly warns against.
//
// KNOWN LIMITATION -- account selection: this URL encodes only the
// bucket name and Region, never ref.Account. This is harmless for S3
// specifically because bucket names are globally unique across every
// AWS account in a partition -- there is no real-world ambiguity this
// omission could cause for the one CloudResourceKind this function
// supports today. It would NOT necessarily be safe to omit Account for
// a future resource kind whose identifier is only unique per-account
// (many AWS resource types are). Any future addition to this function's
// switch statement for a new CloudResourceKind must explicitly
// reconsider whether Account needs to be encoded (e.g. via the
// `account` query parameter the AWS Console's cross-account switcher
// uses) or otherwise disambiguated -- do not copy this function's
// S3-specific omission by default.
func consoleURLForCloudResource(ref CloudResourceRef, present bool) string {
	switch ref.Kind {
	case CloudS3Bucket:
		return s3ConsoleURL(ref.Ref, ref.Region, present)
	default:
		return ""
	}
}

// s3ConsoleURL returns the bucket's own object-listing page when
// present, or the regional S3 bucket-list ("S3 home") page when the
// bucket has been independently verified absent and so has no page of
// its own left to link to.
func s3ConsoleURL(bucket, region string, present bool) string {
	region = url.QueryEscape(region)
	if present {
		return fmt.Sprintf("https://s3.console.aws.amazon.com/s3/buckets/%s?region=%s&tab=objects", url.PathEscape(bucket), region)
	}
	return fmt.Sprintf("https://s3.console.aws.amazon.com/s3/home?region=%s", region)
}
