package docgen

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// BrokenLink describes one Markdown link whose target could not be resolved
// to a real file on disk.
type BrokenLink struct {
	File   string
	Line   int
	Target string
}

var mdLinkRe = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)

// CheckLinks walks every .md file under root and verifies that relative
// (non-http, non-mailto, non-anchor-only) links resolve to a real file.
// It deliberately does not check http(s) links — that would require network
// access this tool should not depend on — and does not check content
// correctness, only that the referenced file exists.
func CheckLinks(root string) ([]BrokenLink, error) {
	var broken []BrokenLink
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		// p came from filepath.Walk over root (a trusted local repo path
		// this CLI was pointed at), not from untrusted input.
		//nolint:gosec // G304
		content, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		dir := filepath.Dir(p)
		for lineNo, line := range strings.Split(string(content), "\n") {
			for _, m := range mdLinkRe.FindAllStringSubmatch(line, -1) {
				target := m[1]
				if isSkippableLink(target) {
					continue
				}
				target = strings.SplitN(target, "#", 2)[0]
				if target == "" {
					continue // pure in-page anchor
				}
				resolved := filepath.Join(dir, target)
				// resolved is only ever stat'd (existence check), never
				// opened/read/executed, so a crafted relative link can at
				// most learn whether a path exists — no content is ever
				// disclosed through this check.
				//nolint:gosec // G703
				if _, statErr := os.Stat(resolved); statErr != nil {
					broken = append(broken, BrokenLink{File: p, Line: lineNo + 1, Target: m[1]})
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(broken, func(i, j int) bool {
		if broken[i].File != broken[j].File {
			return broken[i].File < broken[j].File
		}
		return broken[i].Line < broken[j].Line
	})
	return broken, nil
}

func isSkippableLink(target string) bool {
	return strings.HasPrefix(target, "http://") ||
		strings.HasPrefix(target, "https://") ||
		strings.HasPrefix(target, "mailto:") ||
		strings.HasPrefix(target, "#")
}
