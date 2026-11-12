package docgen

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Command is one developer-facing entry point extracted directly from the
// Makefile: a target name plus the shell command(s) it runs.
type Command struct {
	Target string
	Run    []string
}

var (
	phonyRe = regexp.MustCompile(`^\.PHONY:\s*(.+)$`)
	// A target line is "name:" or "name: deps..." at column 0, never
	// indented (that would be a recipe line).
	targetRe = regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_-]*):`)
)

// ExtractMakefileCommands parses a Makefile for its .PHONY targets, in the
// order declared there, and the recipe lines under each. Only targets
// listed in .PHONY are included — these are the developer-facing entry
// points (`make test`, `make run`, ...), not internal file-based rules.
func ExtractMakefileCommands(path string) ([]Command, error) {
	content, err := os.ReadFile(path) //nolint:gosec // path is a fixed caller-supplied Makefile path, not user input
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	lines := strings.Split(string(content), "\n")

	var order []string
	seen := map[string]bool{}
	for _, line := range lines {
		m := phonyRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		for _, name := range strings.Fields(m[1]) {
			if !seen[name] {
				seen[name] = true
				order = append(order, name)
			}
		}
	}

	runs := map[string][]string{}
	current := ""
	for _, line := range lines {
		if m := targetRe.FindStringSubmatch(line); m != nil {
			current = m[1]
			continue
		}
		if current == "" {
			continue
		}
		if strings.HasPrefix(line, "\t") {
			recipe := strings.TrimPrefix(line, "\t")
			recipe = strings.TrimPrefix(recipe, "@") // @ silences echo; not part of the command itself
			if recipe != "" {
				runs[current] = append(runs[current], recipe)
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		current = "" // a non-tab, non-blank line ends this target's recipe block
	}

	out := make([]Command, 0, len(order))
	for _, name := range order {
		out = append(out, Command{Target: name, Run: runs[name]})
	}
	return out, nil
}

// RenderCommandsMarkdown renders the Makefile command reference as Markdown.
func RenderCommandsMarkdown(commands []Command, sourceRelPath string) string {
	var b strings.Builder
	b.WriteString("<!-- GENERATED FILE. Do not edit by hand. -->\n")
	b.WriteString("<!-- Regenerate with: go run ./cmd/docgen generate commands -->\n\n")
	b.WriteString("# Command Reference (generated)\n\n")
	fmt.Fprintf(&b, "Extracted directly from `%s`'s `.PHONY` target list, in declared order, with each target's recipe lines — not hand-written. If a command here doesn't match what actually runs, check the raw file.\n\n", sourceRelPath)
	for _, c := range commands {
		fmt.Fprintf(&b, "## `make %s`\n\n", c.Target)
		if len(c.Run) == 0 {
			b.WriteString("_(no recipe lines found)_\n\n")
			continue
		}
		b.WriteString("```bash\n")
		for _, r := range c.Run {
			b.WriteString(r)
			b.WriteString("\n")
		}
		b.WriteString("```\n\n")
	}
	return b.String()
}
