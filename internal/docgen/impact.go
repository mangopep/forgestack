// Package docgen implements deterministic documentation-impact detection and
// documentation generation from source artifacts (Go handlers, workflow files).
// It intentionally does not call any AI provider: everything here is reproducible
// from the repository's own files, so results are identical for every run.
package docgen

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
)

// ImpactRule maps a glob-style file pattern to a documentation area.
// Pattern supports "*" (any characters within a path segment) and "**"
// (any characters across segments), matching the common convention used
// by .gitignore-style tools.
type ImpactRule struct {
	Pattern string `json:"pattern"`
	Area    string `json:"area"`
	DocPath string `json:"doc_path"`
	Kind    string `json:"kind"` // "generated" (deterministically regenerated) or "manual" (needs human review)
}

// ImpactMap is the machine-readable configuration loaded from
// docs/doc-impact-map.json.
type ImpactMap struct {
	Rules []ImpactRule `json:"rules"`
}

// LoadImpactMap reads and parses the impact map configuration file.
func LoadImpactMap(path string) (ImpactMap, error) {
	//nolint:gosec // G304: path is this CLI's own repo-relative config path
	// argument (default docs/doc-impact-map.json), supplied by a trusted
	// local developer or CI step, not untrusted network input.
	data, err := os.ReadFile(path)
	if err != nil {
		return ImpactMap{}, fmt.Errorf("read impact map: %w", err)
	}
	var m ImpactMap
	if err := json.Unmarshal(data, &m); err != nil {
		return ImpactMap{}, fmt.Errorf("parse impact map: %w", err)
	}
	for i, r := range m.Rules {
		if r.Pattern == "" || r.Area == "" || r.DocPath == "" || r.Kind == "" {
			return ImpactMap{}, fmt.Errorf("impact map rule %d is missing a required field", i)
		}
		if r.Kind != "generated" && r.Kind != "manual" {
			return ImpactMap{}, fmt.Errorf("impact map rule %d has unknown kind %q (want generated or manual)", i, r.Kind)
		}
	}
	return m, nil
}

// globToRegexp converts the limited glob syntax above into an anchored regexp.
// "**/" matches zero or more whole path segments (including zero, so
// "a/**/b" matches "a/b" as well as "a/x/b"); a trailing "**" matches
// anything including slashes; a lone "*" matches within one path segment.
func globToRegexp(pattern string) (*regexp.Regexp, error) {
	var out []byte
	out = append(out, '^')
	i := 0
	for i < len(pattern) {
		switch {
		case strings.HasPrefix(pattern[i:], "**/"):
			out = append(out, "(?:.*/)?"...)
			i += 3
		case pattern[i:] == "**":
			out = append(out, ".*"...)
			i += 2
		case pattern[i] == '*':
			out = append(out, "[^/]*"...)
			i++
		default:
			c := pattern[i]
			if isRegexpMeta(c) {
				out = append(out, '\\')
			}
			out = append(out, c)
			i++
		}
	}
	out = append(out, '$')
	return regexp.Compile(string(out))
}

func isRegexpMeta(c byte) bool {
	switch c {
	case '.', '+', '(', ')', '|', '^', '$', '[', ']', '{', '}', '\\':
		return true
	}
	return false
}

// MatchFile reports whether changedFile matches the rule's pattern.
func (r ImpactRule) MatchFile(changedFile string) (bool, error) {
	// A bare filename with no "/" or glob chars matches that file anywhere
	// a path.Base equality would, as well as an exact path match.
	if r.Pattern == path.Base(r.Pattern) && !hasGlobChars(r.Pattern) {
		return changedFile == r.Pattern || path.Base(changedFile) == r.Pattern, nil
	}
	re, err := globToRegexp(r.Pattern)
	if err != nil {
		return false, err
	}
	return re.MatchString(changedFile), nil
}

func hasGlobChars(s string) bool {
	for _, c := range s {
		if c == '*' || c == '?' {
			return true
		}
	}
	return false
}

// ImpactedArea is one documentation area affected by a set of changed files.
type ImpactedArea struct {
	Area         string   `json:"area"`
	DocPath      string   `json:"doc_path"`
	Kind         string   `json:"kind"`
	MatchedFiles []string `json:"matched_files"`
}

// ImpactReport is the full, machine-readable result of running the impact
// map against a list of changed files.
type ImpactReport struct {
	ChangedFiles      []string       `json:"changed_files"`
	Impacted          []ImpactedArea `json:"impacted_areas"`
	RequiresDocUpdate bool           `json:"requires_doc_update"`
}

// Match runs every rule in m against changedFiles and returns the impact
// report. Changes that match no rule produce no impacted area and do not
// set RequiresDocUpdate — this is intentional: unrelated code changes must
// not trigger a documentation requirement.
func Match(m ImpactMap, changedFiles []string) (ImpactReport, error) {
	report := ImpactReport{ChangedFiles: append([]string{}, changedFiles...)}
	byArea := map[string]*ImpactedArea{}
	var order []string
	for _, rule := range m.Rules {
		for _, f := range changedFiles {
			ok, err := rule.MatchFile(f)
			if err != nil {
				return ImpactReport{}, err
			}
			if !ok {
				continue
			}
			key := rule.Area + "|" + rule.DocPath
			a, exists := byArea[key]
			if !exists {
				a = &ImpactedArea{Area: rule.Area, DocPath: rule.DocPath, Kind: rule.Kind}
				byArea[key] = a
				order = append(order, key)
			}
			a.MatchedFiles = append(a.MatchedFiles, f)
		}
	}
	sort.Strings(order)
	for _, key := range order {
		a := byArea[key]
		sort.Strings(a.MatchedFiles)
		report.Impacted = append(report.Impacted, *a)
		report.RequiresDocUpdate = true
	}
	return report, nil
}
