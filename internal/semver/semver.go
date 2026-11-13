// Package semver validates version strings against the SemVer 2.0.0 grammar
// and classifies the relationship between two versions (breaking, minor,
// patch, or equal) per docs/VERSIONING.md's policy -- a MAJOR bump is the
// only one that policy requires a written breaking-change note for.
package semver

import (
	"fmt"
	"regexp"
	"strconv"
)

// semverPattern is the official SemVer 2.0.0 regular expression (see
// semver.org), with an optional leading "v" -- Git tag convention.
var semverPattern = regexp.MustCompile(
	`^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)` +
		`(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?` +
		`(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`,
)

// Version is a parsed SemVer version.
type Version struct {
	Major, Minor, Patch int
	Prerelease          string
	BuildMetadata       string
}

// Parse validates s against the SemVer grammar and returns its parsed form.
func Parse(s string) (Version, error) {
	m := semverPattern.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("%q is not a valid SemVer version (expected MAJOR.MINOR.PATCH, optionally v-prefixed)", s)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	return Version{Major: major, Minor: minor, Patch: patch, Prerelease: m[4], BuildMetadata: m[5]}, nil
}

// Relationship is how two versions compare, per docs/VERSIONING.md.
type Relationship string

const (
	RelationshipEqual      Relationship = "equal"
	RelationshipPatch      Relationship = "patch"      // patch changed only
	RelationshipCompatible Relationship = "compatible" // minor changed, major same
	RelationshipBreaking   Relationship = "breaking"   // major changed
	RelationshipDowngrade  Relationship = "downgrade"  // newer is actually older
)

// Compare classifies the step from oldVersion to newVersion.
func Compare(oldVersion, newVersion Version) Relationship {
	switch {
	case newVersion.Major != oldVersion.Major:
		if newVersion.Major < oldVersion.Major {
			return RelationshipDowngrade
		}
		return RelationshipBreaking
	case newVersion.Minor != oldVersion.Minor:
		if newVersion.Minor < oldVersion.Minor {
			return RelationshipDowngrade
		}
		return RelationshipCompatible
	case newVersion.Patch != oldVersion.Patch:
		if newVersion.Patch < oldVersion.Patch {
			return RelationshipDowngrade
		}
		return RelationshipPatch
	default:
		return RelationshipEqual
	}
}
