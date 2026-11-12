// Command branchpolicy enforces the feature/* -> develop -> staging -> main
// branch flow on pull requests. It reads the head/base branch names from
// GitHub Actions' standard pull_request event environment variables and
// exits non-zero (failing the CI job) if the PR's source-to-target
// combination is not permitted.
package main

import (
	"fmt"
	"os"

	"github.com/mangopep/forgestack/internal/branchpolicy"
)

func main() {
	head := firstNonEmpty(os.Getenv("HEAD_REF"), os.Getenv("GITHUB_HEAD_REF"))
	base := firstNonEmpty(os.Getenv("BASE_REF"), os.Getenv("GITHUB_BASE_REF"))
	if head == "" || base == "" {
		fmt.Fprintln(os.Stderr, "branchpolicy: HEAD_REF/BASE_REF (or GITHUB_HEAD_REF/GITHUB_BASE_REF) must both be set")
		os.Exit(2)
	}

	decision := branchpolicy.Evaluate(head, base)
	if decision.Allowed {
		fmt.Printf("branchpolicy: ALLOWED — %q -> %q: %s\n", head, base, decision.Reason)
		return
	}

	fmt.Fprintf(os.Stderr, "branchpolicy: BLOCKED — %q -> %q is not a permitted branch flow.\n%s\n\n"+
		"Required flow: feature/* -> develop -> staging -> main, with a documented\n"+
		"hotfix/* exception for urgent production fixes targeting main directly.\n"+
		"Retarget this PR's base branch, or open it from the correct source branch.\n",
		head, base, decision.Reason)
	os.Exit(1)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
