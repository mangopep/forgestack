package secreport

import "testing"

func TestComputeOverallStatusPassWhenClean(t *testing.T) {
	tools := []ToolResult{{Name: "a", Status: StatusRan}}
	got := computeOverallStatus(tools, nil, nil)
	if got != "PASS" {
		t.Errorf("got %q, want PASS", got)
	}
}

func TestComputeOverallStatusNeverPassesWithMissingEvidence(t *testing.T) {
	// This is the one rule F4 names explicitly: never report PASS when a
	// required scan did not run.
	tools := []ToolResult{{Name: "a", Status: StatusMissing}}
	got := computeOverallStatus(tools, nil, nil)
	if got == "PASS" {
		t.Error("must never report PASS when a tool's evidence is missing")
	}
}

func TestComputeOverallStatusFailsOnUnsuppressedCritical(t *testing.T) {
	tools := []ToolResult{{Name: "a", Status: StatusRan}}
	findings := []Finding{{Severity: SeverityCritical, SuppressedUnfixed: false}}
	got := computeOverallStatus(tools, findings, nil)
	if got == "PASS" {
		t.Error("must not report PASS with an unsuppressed CRITICAL finding present")
	}
}

func TestComputeOverallStatusPassesWithOnlySuppressedCritical(t *testing.T) {
	tools := []ToolResult{{Name: "a", Status: StatusRan}}
	findings := []Finding{{Severity: SeverityCritical, SuppressedUnfixed: true}}
	got := computeOverallStatus(tools, findings, nil)
	if got != "PASS" {
		t.Errorf("got %q, want PASS (a tracked, suppressed-unfixed finding must not block)", got)
	}
}

func TestComputeOverallStatusFlagsNotVerifiedWhenOtherwiseClean(t *testing.T) {
	tools := []ToolResult{{Name: "signing", Status: StatusNotVerified}}
	got := computeOverallStatus(tools, nil, nil)
	if got == "PASS" {
		t.Error("a not-verified tool must be visible in the status, not silently dropped")
	}
}

func TestComputeOverallStatusNeverPassesWithExpiredException(t *testing.T) {
	// The Audit & Trust extension's own named state: a stale exception is
	// not valid cover, same principle as missing evidence.
	tools := []ToolResult{{Name: "a", Status: StatusRan}}
	exceptions := []Exception{{ID: "#1", ReviewBy: "2000-01-01"}}
	got := computeOverallStatus(tools, nil, exceptions)
	if got == "PASS" {
		t.Error("must never report PASS when an exception's review date has passed")
	}
}

func TestComputeOverallStatusPassesWithUnexpiredException(t *testing.T) {
	tools := []ToolResult{{Name: "a", Status: StatusRan}}
	exceptions := []Exception{{ID: "#1", ReviewBy: "2099-01-01"}}
	got := computeOverallStatus(tools, nil, exceptions)
	if got != "PASS" {
		t.Errorf("got %q, want PASS (a non-expired exception must not block)", got)
	}
}

func buildInput(tools []ToolResult, findings []Finding, exceptions []Exception, failures []ScanFailure) ReportInput {
	return ReportInput{
		Repository:   "repo",
		Commit:       "sha",
		Branch:       "branch",
		Product:      "forgestack",
		Environment:  "ci",
		Owner:        "mangopep",
		Tools:        tools,
		Findings:     findings,
		Exceptions:   exceptions,
		ScanFailures: failures,
	}
}

func TestBuildSortsFindingsBySeverity(t *testing.T) {
	findings := []Finding{
		{ID: "low", Severity: SeverityLow},
		{ID: "critical", Severity: SeverityCritical},
		{ID: "high", Severity: SeverityHigh},
	}
	report := Build(buildInput(nil, findings, nil, nil))
	if report.Findings[0].ID != "critical" {
		t.Errorf("expected critical finding first, got %+v", report.Findings)
	}
}

func TestBuildCountsFindingsPerTool(t *testing.T) {
	tools := []ToolResult{{Name: "toolA"}, {Name: "toolB"}}
	findings := []Finding{{Tool: "toolA"}, {Tool: "toolA"}, {Tool: "toolB"}}
	report := Build(buildInput(tools, findings, nil, nil))
	counts := map[string]int{}
	for _, t := range report.Tools {
		counts[t.Name] = t.FindingCount
	}
	if counts["toolA"] != 2 || counts["toolB"] != 1 {
		t.Errorf("counts = %+v, want toolA=2 toolB=1", counts)
	}
}

func TestBuildCarriesAuditTrustLinkageFields(t *testing.T) {
	report := Build(buildInput(nil, nil, nil, nil))
	if report.Product == "" || report.Environment == "" || report.Owner == "" {
		t.Errorf("expected product/environment/owner all populated, got %+v", report)
	}
}

func TestWriteJSONAndMarkdownRoundTrip(t *testing.T) {
	dir := t.TempDir()
	report := Build(buildInput(
		[]ToolResult{{Name: "a", Status: StatusRan}},
		[]Finding{{Tool: "a", ID: "X", Severity: SeverityHigh, Component: "c", ControlType: ControlAutomated}},
		[]Exception{{ID: "#1", Component: "c", Owner: "me", ReviewBy: "2099-01-01", ControlType: ControlManual}},
		nil,
	))
	jsonPath := dir + "/report.json"
	mdPath := dir + "/report.md"
	if err := report.WriteJSON(jsonPath); err != nil {
		t.Fatal(err)
	}
	if err := report.WriteMarkdown(mdPath); err != nil {
		t.Fatal(err)
	}
}
