package branchpolicy

import "testing"

func TestEvaluate(t *testing.T) {
	cases := []struct {
		name        string
		head, base  string
		wantAllowed bool
	}{
		// --- permitted scenarios: the required flow ---
		{"feature into develop", "feature/add-login", "develop", true},
		{"fix into develop", "fix/null-pointer", "develop", true},
		{"develop into staging", "develop", "staging", true},
		{"release branch into staging", "release/1.2.0", "staging", true},
		{"staging into main", "staging", "main", true},

		// --- forbidden scenarios: skipping required stages ---
		{"feature directly into staging", "feature/add-login", "staging", false},
		{"feature directly into main", "feature/add-login", "main", false},
		{"fix directly into main", "fix/null-pointer", "main", false},
		{"develop directly into main (skips staging)", "develop", "main", false},
		{"random branch into staging", "spike/experiment", "staging", false},
		{"random branch into main", "spike/experiment", "main", false},

		// --- hotfix simulation: the documented emergency exception ---
		{"hotfix into main is the documented exception", "hotfix/payment-outage", "main", true},
		{"hotfix backport into develop is also permitted", "hotfix/payment-outage", "develop", true},
		{"hotfix cannot skip straight into staging", "hotfix/payment-outage", "staging", false},

		// --- automation exemption ---
		{"dependabot PR into main is exempt", "dependabot/github_actions/foo", "main", true},
		{"dependabot PR into develop is exempt", "dependabot/gomod/bar", "develop", true},

		// --- branches with no defined policy ---
		{"arbitrary base branch is allowed by default", "feature/x", "someones-scratch-branch", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Evaluate(c.head, c.base)
			if got.Allowed != c.wantAllowed {
				t.Errorf("Evaluate(%q, %q) = allowed=%v, want allowed=%v (reason: %s)",
					c.head, c.base, got.Allowed, c.wantAllowed, got.Reason)
			}
			if got.Reason == "" {
				t.Error("Decision.Reason must never be empty — it's shown directly to the PR author")
			}
		})
	}
}
