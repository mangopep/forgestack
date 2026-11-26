package playground

import (
	"os/exec"
	"strings"
	"testing"
)

// TestNoAWSCredentialOrConfigPackageIsEverCompiledIn is a durable,
// build-graph-level proof -- not just a code-review observation -- that
// nothing in this package (or anything it imports) can resolve AWS
// credentials or build an aws.Config, which is the only way real network
// access to AWS could ever happen. "github.com/aws/aws-sdk-go-v2/config"
// and ".../credentials" are the two packages that do that; this package
// depends on "github.com/aws/aws-sdk-go-v2/service/s3" only for its
// request/response type definitions (cloud_s3_api.go), never for
// constructing a client. If either of those two packages ever appears
// in this package's build dependency graph again, this test fails the
// build before a single AWS API call could ever be made from it.
func TestNoAWSCredentialOrConfigPackageIsEverCompiledIn(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput() //nolint:gosec // G204: fixed argv, no external input
	if err != nil {
		t.Fatalf("go list -deps .: %v\n%s", err, out)
	}
	deps := string(out)
	for _, forbidden := range []string{
		"github.com/aws/aws-sdk-go-v2/config",
		"github.com/aws/aws-sdk-go-v2/credentials",
	} {
		if strings.Contains(deps, forbidden) {
			t.Fatalf("THE STRICT INVARIANT THIS PACKAGE DEPENDS ON IS BROKEN: %q is now in this package's build dependency graph -- "+
				"this means something here could now resolve real AWS credentials. Find and remove that import.", forbidden)
		}
	}
}
