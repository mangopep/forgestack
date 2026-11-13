package secreport

import (
	"encoding/json"
	"time"
)

type npmAuditReport struct {
	Metadata struct {
		Vulnerabilities map[string]int `json:"vulnerabilities"`
	} `json:"metadata"`
}

type npmAuditBaseline struct {
	Issue          string         `json:"issue"`
	RecordedDate   string         `json:"recorded_date"`
	ReviewBy       string         `json:"review_by"`
	BaselineCounts map[string]int `json:"baseline_counts"`
}

// ParseNpmAudit reads `npm audit --json` plus the tracked baseline file
// (see website/.security/npm-audit-baseline.json and
// scripts/check-npm-audit-baseline.py — the same baseline the CI gate
// enforces). Every finding within the baseline is reported as a tracked
// Exception, never silently dropped; anything beyond the baseline is a
// real, un-suppressed Finding.
func ParseNpmAudit(auditPath, baselinePath string) (ToolResult, []Finding, []Exception, error) {
	auditData, result, ok := readTool("npm-audit", auditPath)
	result.Status = StatusRan
	if !ok {
		return result, nil, nil, nil
	}

	var audit npmAuditReport
	if err := json.Unmarshal(auditData, &audit); err != nil {
		result.Status = StatusParseFailed
		result.Note = err.Error()
		return result, nil, nil, err
	}

	baselineData, baselineResult, baselineOK := readTool("npm-audit-baseline", baselinePath)
	if !baselineOK {
		result.Note = "baseline file " + baselinePath + " " + baselineResult.Note
		unbaselinedResult, findings, exceptions := buildUnbaselinedNpmFindings(audit)
		return unbaselinedResult, findings, exceptions, nil
	}

	var baseline npmAuditBaseline
	if err := json.Unmarshal(baselineData, &baseline); err != nil {
		result.Status = StatusParseFailed
		result.Note = "baseline parse error: " + err.Error()
		return result, nil, nil, err
	}

	expired := false
	if reviewBy, err := time.Parse("2006-01-02", baseline.ReviewBy); err == nil {
		expired = time.Now().After(reviewBy)
	}

	var findings []Finding
	var exceptions []Exception
	totalWithinBaseline := true
	for severity, count := range audit.Metadata.Vulnerabilities {
		if severity == "total" {
			continue
		}
		baselineCount := baseline.BaselineCounts[severity]
		if count > baselineCount {
			totalWithinBaseline = false
			findings = append(findings, Finding{
				ControlType: ControlAutomated,
				Tool:        "npm-audit",
				ID:          "new-finding-" + severity,
				Severity:    normalizeNpmSeverity(severity),
				Component:   "website/ (npm dependencies)",
				Description: "new findings beyond the tracked baseline",
				Environment: "website build tooling",
				Evidence:    auditPath,
			})
		}
	}

	if totalWithinBaseline && !expired {
		exceptions = append(exceptions, Exception{
			ControlType:   ControlManual,
			ID:            baseline.Issue,
			Component:     "website/ (Docusaurus build/dev-tooling dependencies)",
			Justification: "npm audit fix requires --force; the only available fix is a Docusaurus downgrade, rejected as unsafe. Affected packages are build-time only, not shipped in website/build/.",
			Owner:         "mangopep",
			ReviewBy:      baseline.ReviewBy,
			IssueURL:      baseline.Issue,
		})
	}
	if expired {
		findings = append(findings, Finding{
			ControlType: ControlAutomated,
			Tool:        "npm-audit",
			ID:          "baseline-expired",
			Severity:    SeverityHigh,
			Component:   "website/.security/npm-audit-baseline.json",
			Description: "the npm audit exception's review_by date has passed and must be re-reviewed",
			Environment: "website build tooling",
			Evidence:    baselinePath,
		})
	}

	return result, findings, exceptions, nil
}

func buildUnbaselinedNpmFindings(audit npmAuditReport) (ToolResult, []Finding, []Exception) {
	var findings []Finding
	for severity, count := range audit.Metadata.Vulnerabilities {
		if severity == "total" || count == 0 {
			continue
		}
		findings = append(findings, Finding{
			ControlType: ControlAutomated,
			Tool:        "npm-audit",
			ID:          "unbaselined-" + severity,
			Severity:    normalizeNpmSeverity(severity),
			Component:   "website/ (npm dependencies)",
			Description: "no baseline file found to compare against",
			Environment: "website build tooling",
		})
	}
	return ToolResult{Name: "npm-audit", Status: StatusRan, Note: "no baseline on disk; all findings reported as unreviewed"}, findings, nil
}

func normalizeNpmSeverity(s string) Severity {
	switch s {
	case "critical":
		return SeverityCritical
	case "high":
		return SeverityHigh
	case "moderate":
		return SeverityModerate
	case "low":
		return SeverityLow
	case "info":
		return SeverityInfo
	default:
		return SeverityUnknown
	}
}
