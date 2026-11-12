// Integration tests for the branchpolicy binary itself, not just the
// internal/branchpolicy library: these prove that a blocked decision
// actually produces a non-zero process exit code (what GitHub Actions
// treats as a failing, merge-blocking check), and that missing required
// input fails closed rather than silently exiting 0. Testing the library's
// Decision.Allowed value alone would not catch a mistake in main()'s
// exit-code wiring (e.g. forgetting the os.Exit(1) call).
package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
)

// goBuildArgs isolates the one variable argument (the output path) passed
// to `go build`, so the gosec G204 "subprocess launched with variable"
// finding has a named, reviewable reason instead of being suppressed: bin
// is always a path this test created under t.TempDir(), never external
// input.
func goBuildArgs(bin string) []string {
	return []string{"build", "-o", bin, "."}
}

// buildBranchPolicy compiles the command once per test run into a temp
// binary, so every subtest execs the real built artifact instead of
// `go run`-ing the source repeatedly.
func buildBranchPolicy(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "branchpolicy")
	build := exec.Command("go", goBuildArgs(bin)...) //nolint:gosec // bin is a fixed t.TempDir() path, not external input
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/branchpolicy failed: %v\n%s", err, out)
	}
	return bin
}

func TestMainExitCodes(t *testing.T) {
	bin := buildBranchPolicy(t)

	cases := []struct {
		name         string
		env          []string
		wantExitCode int
	}{
		{
			name:         "allowed flow exits 0",
			env:          []string{"HEAD_REF=staging", "BASE_REF=main", "ACTOR="},
			wantExitCode: 0,
		},
		{
			name:         "blocked flow exits 1, not 0 — a real CI-failing exit code",
			env:          []string{"HEAD_REF=feature/x", "BASE_REF=main", "ACTOR="},
			wantExitCode: 1,
		},
		{
			name:         "develop skipping straight to main exits 1",
			env:          []string{"HEAD_REF=develop", "BASE_REF=main", "ACTOR="},
			wantExitCode: 1,
		},
		{
			name:         "missing HEAD_REF/BASE_REF fails closed with exit 2, never 0",
			env:          []string{},
			wantExitCode: 2,
		},
		{
			name:         "spoofed dependabot branch without the real actor exits 1, not exempted",
			env:          []string{"HEAD_REF=dependabot/evil", "BASE_REF=main", "ACTOR=someone-else"},
			wantExitCode: 1,
		},
		{
			name:         "genuine dependabot actor on a dependabot/* branch exits 0",
			env:          []string{"HEAD_REF=dependabot/gomod/foo", "BASE_REF=main", "ACTOR=dependabot[bot]"},
			wantExitCode: 0,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command(bin)
			cmd.Env = c.env
			err := cmd.Run()
			exitCode := 0
			if err != nil {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) {
					t.Fatalf("unexpected error running binary: %v", err)
				}
				exitCode = exitErr.ExitCode()
			}
			if exitCode != c.wantExitCode {
				t.Errorf("exit code = %d, want %d", exitCode, c.wantExitCode)
			}
		})
	}
}
