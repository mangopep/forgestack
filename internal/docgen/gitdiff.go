package docgen

import (
	"fmt"
	"os/exec"
	"strings"
)

// ChangedFiles runs `git diff --name-only base...head` in repoDir and
// returns the changed file paths. It shells out to the real git binary
// rather than reimplementing diff logic, so results always match what git
// itself reports.
func ChangedFiles(repoDir, base, head string) ([]string, error) {
	//nolint:gosec // G204: base/head come from this CLI's own flags (a trusted
	// local developer or CI step invoking docgen, e.g. --base origin/main),
	// not from untrusted network input; exec.Command never invokes a shell,
	// so there is no shell-injection vector either way.
	cmd := exec.Command("git", "diff", "--name-only", base+"..."+head)
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --name-only %s...%s: %w", base, head, err)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}
