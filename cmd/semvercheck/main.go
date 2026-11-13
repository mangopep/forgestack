// Command semvercheck validates version strings and classifies the
// relationship between two of them (breaking/compatible/patch/equal/
// downgrade), per docs/VERSIONING.md. It makes no judgment about whether
// a breaking change is acceptable -- that's a human, written-note
// decision (see that doc's breaking-change policy) -- it only tells you
// which class of change a version bump represents.
package main

import (
	"fmt"
	"os"

	"github.com/mangopep/forgestack/internal/semver"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "validate":
		runValidate(os.Args[2:])
	case "compat":
		runCompat(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  semvercheck validate <version>")
	fmt.Fprintln(os.Stderr, "  semvercheck compat <old-version> <new-version>")
}

func runValidate(args []string) {
	if len(args) != 1 {
		usage()
		os.Exit(2)
	}
	v, err := semver.Parse(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
	fmt.Printf("OK: %s is valid SemVer (major=%d minor=%d patch=%d)\n", args[0], v.Major, v.Minor, v.Patch)
}

func runCompat(args []string) {
	if len(args) != 2 {
		usage()
		os.Exit(2)
	}
	oldVersion, err := semver.Parse(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "FAIL: old version:", err)
		os.Exit(1)
	}
	newVersion, err := semver.Parse(args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "FAIL: new version:", err)
		os.Exit(1)
	}
	rel := semver.Compare(oldVersion, newVersion)
	fmt.Println(rel)
	if rel == semver.RelationshipDowngrade {
		os.Exit(1)
	}
}
