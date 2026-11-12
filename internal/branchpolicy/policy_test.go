package branchpolicy

import "testing"

func TestEvaluate(t *testing.T) {
	cases := []struct {
		name        string
		head, base  string
		actor       string
		wantAllowed bool
	}{
		// --- permitted scenarios: the required flow ---
		{"feature into develop", "feature/add-login", "develop", "", true},
		{"fix into develop", "fix/null-pointer", "develop", "", true},
		{"develop into staging", "develop", "staging", "", true},
		{"release branch into staging", "release/1.2.0", "staging", "", true},
		{"staging into main", "staging", "main", "", true},

		// --- forbidden scenarios: skipping required stages ---
		{"feature directly into staging", "feature/add-login", "staging", "", false},
		{"feature directly into main", "feature/add-login", "main", "", false},
		{"fix directly into main", "fix/null-pointer", "main", "", false},
		{"develop directly into main (skips staging)", "develop", "main", "", false},
		{"random branch into staging", "spike/experiment", "staging", "", false},
		{"random branch into main", "spike/experiment", "main", "", false},

		// --- hotfix simulation: the documented emergency exception ---
		{"hotfix into main is the documented exception", "hotfix/payment-outage", "main", "", true},
		{"hotfix backport into develop is also permitted", "hotfix/payment-outage", "develop", "", true},
		{"hotfix cannot skip straight into staging", "hotfix/payment-outage", "staging", "", false},

		// --- automation exemption, genuine ---
		{"dependabot PR into main is exempt when actor matches", "dependabot/github_actions/foo", "main", "dependabot[bot]", true},
		{"dependabot PR into develop is exempt when actor matches", "dependabot/gomod/bar", "develop", "dependabot[bot]", true},
		{"dependabot actor match is case-insensitive", "dependabot/gomod/bar", "main", "Dependabot[Bot]", true},

		// --- branches with no defined policy ---
		{"arbitrary base branch is allowed by default", "feature/x", "someones-scratch-branch", "", true},

		// --- invalid/malicious branch names must not bypass the policy ---
		{"spoofed dependabot branch name, wrong actor, is NOT exempt into main", "dependabot/evil", "main", "random-fork-contributor", false},
		{"spoofed dependabot branch name, empty actor, is NOT exempt into main", "dependabot/evil", "main", "", false},
		{"spoofed dependabot branch name, wrong actor, is NOT exempt into staging", "dependabot/evil", "staging", "random-fork-contributor", false},
		{"release without trailing content does not match release/* prefix", "release", "staging", "", false},
		{"hotfix without trailing content does not match hotfix/* prefix", "hotfix", "main", "", false},
		{"wrong case 'Staging' head is not the real staging branch", "Staging", "main", "", false},
		{"wrong case 'Hotfix/x' head does not match hotfix/* prefix (case-sensitive)", "Hotfix/x", "main", "", false},
		{"trailing-slash-only hotfix branch name into main is still an exception by prefix rule", "hotfix/", "main", "", true},
		{"empty head branch into main is rejected, not default-allowed", "", "main", "", false},
		{"empty head branch into staging is rejected, not default-allowed", "", "staging", "", false},
		{"path-traversal-style head branch into develop is still just develop's normal entry point", "feature/../../etc", "develop", "", true},
		{"path-traversal-style head branch into main is still rejected", "feature/../../etc", "main", "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Evaluate(c.head, c.base, c.actor)
			if got.Allowed != c.wantAllowed {
				t.Errorf("Evaluate(%q, %q, %q) = allowed=%v, want allowed=%v (reason: %s)",
					c.head, c.base, c.actor, got.Allowed, c.wantAllowed, got.Reason)
			}
			if got.Reason == "" {
				t.Error("Decision.Reason must never be empty — it's shown directly to the PR author")
			}
		})
	}
}
