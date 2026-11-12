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
