package docgen

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// WorkflowSummary is a minimal, deterministic summary of one GitHub Actions
// workflow file. It is extracted with line-oriented regexps rather than a
// full YAML parser to keep this tool dependency-free (standard library
// only); it reads top-level `name:` and the trigger keys nested under `on:`.
type WorkflowSummary struct {
	File     string
	Name     string
	Triggers []string
}

var (
	nameRe    = regexp.MustCompile(`^name:\s*(.+?)\s*$`)
	onBlockRe = regexp.MustCompile(`^on:\s*$`)
	onInline  = regexp.MustCompile(`^on:\s*\[(.+)\]\s*$`)
	triggerRe = regexp.MustCompile(`^\s{2}([a-zA-Z_]+):`)
)

// SummarizeWorkflows reads every *.yml/*.yaml file directly in dir and
// returns a deterministic summary of each, sorted by filename.
func SummarizeWorkflows(dir string) ([]WorkflowSummary, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".yml") || strings.HasSuffix(e.Name(), ".yaml") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	var out []WorkflowSummary
	for _, name := range files {
		// name came from os.ReadDir(dir) just above, not from untrusted
		// input, so this is a closed set of real filenames, not a
		// traversal vector.
		//nolint:gosec // G304
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		out = append(out, summarizeOne(name, string(content)))
	}
	return out, nil
}

func summarizeOne(filename, content string) WorkflowSummary {
	s := WorkflowSummary{File: filename}
	lines := strings.Split(content, "\n")
	inOnBlock := false
	for _, line := range lines {
		if m := nameRe.FindStringSubmatch(line); m != nil && s.Name == "" {
			s.Name = strings.Trim(m[1], `"'`)
			continue
		}
		if m := onInline.FindStringSubmatch(line); m != nil {
			for _, t := range strings.Split(m[1], ",") {
				s.Triggers = append(s.Triggers, strings.TrimSpace(t))
			}
			continue
		}
		if onBlockRe.MatchString(line) {
			inOnBlock = true
			continue
		}
		if inOnBlock {
			if m := triggerRe.FindStringSubmatch(line); m != nil {
				s.Triggers = append(s.Triggers, m[1])
				continue
			}
			if strings.HasPrefix(line, "  ") || strings.TrimSpace(line) == "" {
				continue
			}
			inOnBlock = false
		}
	}
	return s
}

// RenderCIMarkdown renders the workflow inventory as Markdown.
func RenderCIMarkdown(summaries []WorkflowSummary) string {
	var b strings.Builder
	b.WriteString("<!-- GENERATED FILE. Do not edit by hand. -->\n")
	b.WriteString("<!-- Regenerate with: go run ./cmd/docgen generate ci -->\n\n")
	b.WriteString("# CI/CD Workflow Inventory (generated)\n\n")
	b.WriteString("Extracted directly from `.github/workflows/*.yml` by line-oriented parsing of `name:` and the trigger keys under `on:` — not a full YAML parse, but sufficient for an inventory. If a workflow's triggers aren't listed correctly here, check the raw file; this is a summary, not a substitute for reading it.\n\n")
	b.WriteString("| Workflow file | Name | Triggers |\n")
	b.WriteString("|---|---|---|\n")
	for _, s := range summaries {
		triggers := strings.Join(s.Triggers, ", ")
		if triggers == "" {
			triggers = "(none detected)"
		}
		name := s.Name
		if name == "" {
			name = "(unnamed)"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", s.File, name, triggers)
	}
	return b.String()
}
