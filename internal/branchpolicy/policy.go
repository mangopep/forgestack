// Package branchpolicy enforces ForgeStack's GitFlow-style branch promotion
// policy: feature/* -> develop -> staging -> main, with a documented
// hotfix/* exception for urgent production fixes. It is pure logic (no
// GitHub API calls, no HTTP) so it can be unit-tested with real permitted
// and forbidden scenarios, not just trusted from a workflow YAML file.
package branchpolicy

import "strings"

// Decision is the policy's verdict for one PR's head/base pair.
type Decision struct {
	Allowed bool
	Reason  string
}

// Evaluate decides whether a pull request from headRef into baseRef is
// permitted under the required flow. Branch names are compared exactly for
// develop/staging/main, and by prefix for feature/, hotfix/, release/, and
// dependabot/ branches.
func Evaluate(headRef, baseRef string) Decision {
	// Automated dependency-update PRs (Dependabot) are exempt: they are not
	// human feature work and, until develop becomes the default branch
	// (a separate, approval-gated migration step), they target main by
	// GitHub's own default-branch behavior. Revisit this exemption once
	// dependabot.yml's target-branch is updated as part of that migration.
	if strings.HasPrefix(headRef, "dependabot/") {
		return Decision{true, "automated dependency-update PRs are exempt from branch-flow policy"}
	}

	switch baseRef {
	case "develop":
		return Decision{true, "merging into develop is the normal entry point for feature/fix work (and for backporting a hotfix)"}

	case "staging":
		if headRef == "develop" || strings.HasPrefix(headRef, "release/") {
			return Decision{true, "develop or a release/* branch promoting into staging is the normal flow"}
		}
		return Decision{
			false,
			"only 'develop' or a 'release/*' branch may target 'staging' directly; " +
				"feature/fix branches must merge into develop first, then staging is promoted from develop",
		}

	case "main":
		if headRef == "staging" {
			return Decision{true, "staging promoting into main is the normal release flow"}
		}
		if strings.HasPrefix(headRef, "hotfix/") {
			return Decision{
				true,
				"documented exception: hotfix/* may target main directly for urgent production fixes. " +
					"This fix must also be backported into develop (and staging) in a follow-up PR — " +
					"it does not happen automatically.",
			}
		}
		return Decision{
			false,
			"only 'staging' (normal promotion) or a 'hotfix/*' branch (documented emergency exception) " +
				"may target 'main' directly",
		}

	default:
		// No policy is defined for arbitrary other base branches (e.g. a
		// maintainer's own scratch branch). Allow by default rather than
		// block work this policy was never meant to govern.
		return Decision{true, "no flow policy is defined for base '" + baseRef + "'; allowing by default"}
	}
}
