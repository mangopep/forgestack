// Package secreport builds ForgeStack's F4 security report: a stable,
// versioned JSON document (plus a Markdown rendering) assembled from real
// saved scanner outputs — govulncheck, pip-audit, Trivy, gitleaks, npm
// audit — never from assumptions about what a scan "should" find.
//
// The one rule this package exists to enforce: a tool that didn't run, or
// whose evidence file is missing, is recorded as NotRun/Missing, never
// silently treated as "no findings." See Report.OverallStatus.
package secreport

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// SchemaVersion is this report format's own version, independent of
// ForgeStack's own version — a consumer parses against this field, not
// against assumptions about which fields exist.
const SchemaVersion = "1.0.0"

// ToolStatus is whether a scanner's evidence was actually available and
// usable when the report was built.
type ToolStatus string

const (
	StatusRan         ToolStatus = "ran"
	StatusMissing     ToolStatus = "missing_evidence" // no path given, or file absent
	StatusParseFailed ToolStatus = "parse_failed"     // evidence present but unreadable
	StatusNotVerified ToolStatus = "not_verified"     // known not run in this environment (e.g. needs live CI/OIDC)
)

// ToolResult records one scanner's participation in this report.
type ToolResult struct {
	Name         string     `json:"name"`
	Version      string     `json:"version,omitempty"`
	EvidencePath string     `json:"evidence_path,omitempty"`
	Status       ToolStatus `json:"status"`
	Note         string     `json:"note,omitempty"`
	FindingCount int        `json:"finding_count"`
}

// Severity is normalized across tools (Trivy/govulncheck/pip-audit/npm all
// spell these differently in their own JSON; this is the report's own
// vocabulary, not any one tool's).
type Severity string

const (
	SeverityCritical Severity = "CRITICAL"
	SeverityHigh     Severity = "HIGH"
	SeverityModerate Severity = "MODERATE"
	SeverityLow      Severity = "LOW"
	SeverityInfo     Severity = "INFO"
	SeverityUnknown  Severity = "UNKNOWN"
)

// Finding is one vulnerability/misconfiguration, normalized to the fields
// F4 requires tracked per finding: severity, component, fixed version,
// exploitability context, environment, owner, evidence, remediation,
// review date. Fields the source tool doesn't provide are left empty, not
// guessed.
type Finding struct {
	Tool              string      `json:"tool"`
	ID                string      `json:"id"`
	Severity          Severity    `json:"severity"`
	Component         string      `json:"component"`
	InstalledVersion  string      `json:"installed_version,omitempty"`
	FixedVersion      string      `json:"fixed_version,omitempty"`
	Description       string      `json:"description,omitempty"`
	Exploitability    string      `json:"exploitability_context,omitempty"`
	Environment       string      `json:"environment"`
	Owner             string      `json:"owner,omitempty"`
	Evidence          string      `json:"evidence"`
	Remediation       string      `json:"remediation,omitempty"`
	ReviewBy          string      `json:"review_by,omitempty"`
	SuppressedUnfixed bool        `json:"suppressed_unfixed"`
	ControlType       ControlType `json:"control_type"`
}

// ControlType distinguishes a control an automated scanner actually
// verified from one that depends on a human organizational process. F4's
// Audit & Trust requirement is explicit: never let a manual claim borrow
// the credibility of a real automated check by conflating the two.
type ControlType string

const (
	ControlAutomated ControlType = "automated"
	ControlManual    ControlType = "manual"
)

// Exception is a narrowly-scoped, reviewable, time-bounded suppression —
// F4 requires every exception to be justified and have an owner and a
// review deadline, never a silent blanket ignore. ControlType defaults to
// ControlManual: an exception exists because a human reviewed and accepted
// a tradeoff a scanner alone cannot approve.
type Exception struct {
	ID            string      `json:"id"` // e.g. "#35", "#40"
	Component     string      `json:"component"`
	Justification string      `json:"justification"`
	Owner         string      `json:"owner"`
	ReviewBy      string      `json:"review_by"`
	IssueURL      string      `json:"issue_url,omitempty"`
	ControlType   ControlType `json:"control_type"`
}

// ScanFailure records a tool that was expected to run but errored, as
// distinct from one that simply wasn't configured (see ToolStatus).
type ScanFailure struct {
	Tool  string `json:"tool"`
	Error string `json:"error"`
}

// Report is the full, versioned document. Product/Environment/Owner are
// the Audit & Trust linkage fields (every evidence record must tie back to
// which product, which environment, and who owns it — see
// docs/ROADMAP.md's "Audit & Trust" module and
// docs/architecture/SUPPLY-CHAIN-SECURITY.md).
type Report struct {
	SchemaVersion string        `json:"schema_version"`
	GeneratedAt   time.Time     `json:"generated_at"`
	Repository    string        `json:"repository"`
	Commit        string        `json:"commit"`
	Branch        string        `json:"branch"`
	Product       string        `json:"product"`
	Environment   string        `json:"environment"`
	Owner         string        `json:"owner"`
	Tools         []ToolResult  `json:"tools"`
	Findings      []Finding     `json:"findings"`
	Exceptions    []Exception   `json:"exceptions"`
	ScanFailures  []ScanFailure `json:"scan_failures"`
	OverallStatus string        `json:"overall_status"`
}

// ReportInput is Build's input — a struct rather than a growing positional
// parameter list, specifically because Product/Environment/Owner were
// added after Repository/Commit/Branch/Tools/Findings/Exceptions/
// ScanFailures already existed; a struct lets new linkage fields join
// without reordering or breaking every existing call site's argument order.
type ReportInput struct {
	Repository   string
	Commit       string
	Branch       string
	Product      string
	Environment  string
	Owner        string
	Tools        []ToolResult
	Findings     []Finding
	Exceptions   []Exception
	ScanFailures []ScanFailure
}

// severityWeight orders severities from most to least severe, for stable
// Markdown rendering and for OverallStatus's blocking-severity check.
var severityWeight = map[Severity]int{
	SeverityCritical: 0,
	SeverityHigh:     1,
	SeverityModerate: 2,
	SeverityLow:      3,
	SeverityInfo:     4,
	SeverityUnknown:  5,
}

// computeOverallStatus never returns PASS if any required tool didn't run,
// and never returns PASS if an un-suppressed HIGH/CRITICAL finding exists,
// and never returns PASS if a tracked exception's review date has passed.
// This is the rule F4 calls out by name: "never report PASS when a
// required scan did not run" — extended here to cover EXPIRED exceptions
// the same way, since a stale exception is not valid cover either.
func computeOverallStatus(tools []ToolResult, findings []Finding, exceptions []Exception) string {
	var missing, notVerified []string
	for _, t := range tools {
		switch t.Status {
		case StatusMissing, StatusParseFailed:
			missing = append(missing, t.Name)
		case StatusNotVerified:
			notVerified = append(notVerified, t.Name)
		}
	}

	blocking := false
	for _, f := range findings {
		if f.SuppressedUnfixed {
			continue
		}
		if f.Severity == SeverityCritical || f.Severity == SeverityHigh {
			blocking = true
		}
	}

	var expired []string
	for _, e := range exceptions {
		if reviewBy, err := time.Parse("2006-01-02", e.ReviewBy); err == nil && time.Now().After(reviewBy) {
			expired = append(expired, e.ID)
		}
	}

	switch {
	case blocking:
		return "FAIL: unresolved HIGH/CRITICAL findings present"
	case len(expired) > 0:
		return fmt.Sprintf("EXPIRED: exception review date has passed for: %s", strings.Join(expired, ", "))
	case len(missing) > 0:
		return fmt.Sprintf("MISSING: evidence missing or unreadable for: %s", strings.Join(missing, ", "))
	case len(notVerified) > 0:
		return fmt.Sprintf("NOT_VERIFIED: all available evidence clean, but not independently verified: %s", strings.Join(notVerified, ", "))
	default:
		return "PASS"
	}
}

// Build assembles a Report from already-parsed tool results and findings.
// It does not itself decide what counts as "ran" vs "missing" — callers
// (the per-tool parsers) report that, so Build can't accidentally paper
// over a parse failure.
func Build(in ReportInput) Report {
	sort.SliceStable(in.Findings, func(i, j int) bool {
		return severityWeight[in.Findings[i].Severity] < severityWeight[in.Findings[j].Severity]
	})
	for i := range in.Tools {
		in.Tools[i].FindingCount = countFindingsForTool(in.Findings, in.Tools[i].Name)
	}
	return Report{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Repository:    in.Repository,
		Commit:        in.Commit,
		Branch:        in.Branch,
		Product:       in.Product,
		Environment:   in.Environment,
		Owner:         in.Owner,
		Tools:         in.Tools,
		Findings:      in.Findings,
		Exceptions:    in.Exceptions,
		ScanFailures:  in.ScanFailures,
		OverallStatus: computeOverallStatus(in.Tools, in.Findings, in.Exceptions),
	}
}

func countFindingsForTool(findings []Finding, tool string) int {
	n := 0
	for _, f := range findings {
		if f.Tool == tool {
			n++
		}
	}
	return n
}

// WriteJSON writes the report as pretty-printed, stable-schema JSON.
func (r Report) WriteJSON(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

// WriteMarkdown writes a human-readable summary.
func (r Report) WriteMarkdown(path string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# ForgeStack security report\n\n")
	fmt.Fprintf(&b, "- Schema version: `%s`\n", r.SchemaVersion)
	fmt.Fprintf(&b, "- Generated: %s\n", r.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "- Repository: `%s`\n", r.Repository)
	fmt.Fprintf(&b, "- Commit: `%s`\n", r.Commit)
	fmt.Fprintf(&b, "- Branch: `%s`\n", r.Branch)
	fmt.Fprintf(&b, "- **Overall status: %s**\n\n", r.OverallStatus)

	b.WriteString("## Tool evidence\n\n")
	b.WriteString("| Tool | Status | Findings | Note |\n|---|---|---|---|\n")
	for _, t := range r.Tools {
		fmt.Fprintf(&b, "| %s | %s | %d | %s |\n", t.Name, t.Status, t.FindingCount, t.Note)
	}
	b.WriteString("\n")

	if len(r.ScanFailures) > 0 {
		b.WriteString("## Scan failures\n\n")
		for _, f := range r.ScanFailures {
			fmt.Fprintf(&b, "- **%s**: %s\n", f.Tool, f.Error)
		}
		b.WriteString("\n")
	}

	blocking, suppressed := splitFindings(r.Findings)

	b.WriteString("## Findings (unresolved, not suppressed)\n\n")
	if len(blocking) == 0 {
		b.WriteString("None.\n\n")
	} else {
		writeFindingsTable(&b, blocking)
	}

	b.WriteString("## Suppressed / unfixed findings (tracked, not hidden)\n\n")
	if len(suppressed) == 0 {
		b.WriteString("None.\n\n")
	} else {
		writeFindingsTable(&b, suppressed)
	}

	if len(r.Exceptions) > 0 {
		b.WriteString("## Exceptions\n\n")
		b.WriteString("| ID | Component | Owner | Review by | Justification |\n|---|---|---|---|---|\n")
		for _, e := range r.Exceptions {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", e.ID, e.Component, e.Owner, e.ReviewBy, e.Justification)
		}
		b.WriteString("\n")
	}

	return os.WriteFile(path, []byte(b.String()), 0o600)
}

func splitFindings(findings []Finding) (blocking, suppressed []Finding) {
	for _, f := range findings {
		if f.SuppressedUnfixed {
			suppressed = append(suppressed, f)
		} else {
			blocking = append(blocking, f)
		}
	}
	return blocking, suppressed
}

func writeFindingsTable(b *strings.Builder, findings []Finding) {
	b.WriteString("| Severity | Component | Fixed version | Tool | ID |\n|---|---|---|---|---|\n")
	for _, f := range findings {
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s |\n", f.Severity, f.Component, f.FixedVersion, f.Tool, f.ID)
	}
	b.WriteString("\n")
}
