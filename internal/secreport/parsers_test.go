package secreport

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseGovulncheckMissingFileReportsMissing(t *testing.T) {
	result, findings, err := ParseGovulncheck("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusMissing {
		t.Errorf("status = %v, want %v", result.Status, StatusMissing)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for a missing file, got %d", len(findings))
	}
}

func TestParseGovulncheckExtractsFindingLinkedToOSV(t *testing.T) {
	// A minimal but faithful shape of govulncheck -json's multi-document
	// stream: an osv record, then a finding that references its ID.
	path := writeTemp(t, "govulncheck.json", `
{"osv":{"id":"GO-2024-0001","summary":"example vuln","modified":"v1.2.3"}}
{"finding":{"osv":"GO-2024-0001"}}
`)
	result, findings, err := ParseGovulncheck(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusRan {
		t.Errorf("status = %v, want %v", result.Status, StatusRan)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].ID != "GO-2024-0001" {
		t.Errorf("finding ID = %q, want GO-2024-0001", findings[0].ID)
	}
}

func TestParseGovulncheckNoFindingMeansZeroResults(t *testing.T) {
	// An OSV record with no matching "finding" entry means govulncheck
	// determined the vulnerable code isn't actually reachable — this must
	// NOT be reported as a finding (that's the whole point of govulncheck
	// over plain version-matching).
	path := writeTemp(t, "govulncheck.json", `
{"osv":{"id":"GO-2024-9999","summary":"unreachable","modified":"v1.0.0"}}
`)
	_, findings, err := ParseGovulncheck(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected 0 findings for an OSV with no finding entry, got %d", len(findings))
	}
}

func TestParsePipAuditExtractsVulnWithFixVersion(t *testing.T) {
	path := writeTemp(t, "pip-audit.json", `{
		"dependencies": [
			{"name": "clean-pkg", "version": "1.0.0", "vulns": []},
			{"name": "vuln-pkg", "version": "2.0.0", "vulns": [
				{"id": "PYSEC-2024-1", "fix_versions": ["2.0.1"], "description": "a vuln"}
			]}
		]
	}`)
	result, findings, err := ParsePipAudit(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusRan {
		t.Errorf("status = %v, want %v", result.Status, StatusRan)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].Component != "vuln-pkg" || findings[0].FixedVersion != "2.0.1" {
		t.Errorf("finding = %+v, want component vuln-pkg fixed 2.0.1", findings[0])
	}
}

func TestParseTrivyMarksUnfixedAsSuppressed(t *testing.T) {
	path := writeTemp(t, "trivy.json", `{
		"ArtifactName": "example:latest",
		"Results": [
			{"Target": "example (debian 12)", "Vulnerabilities": [
				{"VulnerabilityID": "CVE-FIXED", "PkgName": "a", "Severity": "HIGH", "FixedVersion": "1.2.3", "Status": "fixed"},
				{"VulnerabilityID": "CVE-UNFIXED", "PkgName": "b", "Severity": "CRITICAL", "FixedVersion": "", "Status": "affected"}
			]}
		]
	}`)
	_, findings, err := ParseTrivy("trivy-test", "test image", path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}
	byID := map[string]Finding{}
	for _, f := range findings {
		byID[f.ID] = f
	}
	if byID["CVE-FIXED"].SuppressedUnfixed {
		t.Error("a finding with a FixedVersion must not be marked suppressed/unfixed")
	}
	if !byID["CVE-UNFIXED"].SuppressedUnfixed {
		t.Error("a finding with no FixedVersion must be marked suppressed/unfixed")
	}
}

func TestParseGitleaksEmptyArrayMeansNoLeaks(t *testing.T) {
	path := writeTemp(t, "gitleaks.json", `[]`)
	result, findings, err := ParseGitleaks(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusRan {
		t.Errorf("status = %v, want %v (an empty array is a real, clean result, not missing evidence)", result.Status, StatusRan)
	}
	if len(findings) != 0 {
		t.Errorf("expected 0 findings, got %d", len(findings))
	}
}

func TestParseGitleaksFindingIsAlwaysCritical(t *testing.T) {
	path := writeTemp(t, "gitleaks.json", `[{"RuleID": "aws-access-key", "File": "leaked.env", "Commit": "abc123"}]`)
	_, findings, err := ParseGitleaks(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 || findings[0].Severity != SeverityCritical {
		t.Errorf("expected exactly 1 CRITICAL finding, got %+v", findings)
	}
}

func TestParseNpmAuditWithinBaselineProducesExceptionNotFinding(t *testing.T) {
	auditPath := writeTemp(t, "npm-audit.json", `{"metadata": {"vulnerabilities": {"critical": 16, "high": 1, "moderate": 6, "low": 3, "info": 0, "total": 26}}}`)
	baselinePath := writeTemp(t, "baseline.json", `{
		"issue": "#35",
		"review_by": "2099-01-01",
		"baseline_counts": {"critical": 16, "high": 1, "moderate": 6, "low": 3, "info": 0, "total": 26}
	}`)
	result, findings, exceptions, err := ParseNpmAudit(auditPath, baselinePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusRan {
		t.Errorf("status = %v, want %v", result.Status, StatusRan)
	}
	if len(findings) != 0 {
		t.Errorf("expected 0 findings when within baseline, got %d: %+v", len(findings), findings)
	}
	if len(exceptions) != 1 || exceptions[0].ID != "#35" {
		t.Errorf("expected exactly 1 exception for #35, got %+v", exceptions)
	}
}

func TestParseNpmAuditBeyondBaselineProducesFinding(t *testing.T) {
	auditPath := writeTemp(t, "npm-audit.json", `{"metadata": {"vulnerabilities": {"critical": 20, "high": 1, "moderate": 6, "low": 3, "info": 0, "total": 30}}}`)
	baselinePath := writeTemp(t, "baseline.json", `{
		"issue": "#35",
		"review_by": "2099-01-01",
		"baseline_counts": {"critical": 16, "high": 1, "moderate": 6, "low": 3, "info": 0, "total": 26}
	}`)
	_, findings, exceptions, err := ParseNpmAudit(auditPath, baselinePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) == 0 {
		t.Error("expected a finding when actual critical count (20) exceeds baseline (16)")
	}
	if len(exceptions) != 0 {
		t.Errorf("expected no exception granted when findings exceed baseline, got %+v", exceptions)
	}
}

func TestParseNpmAuditExpiredBaselineProducesFinding(t *testing.T) {
	auditPath := writeTemp(t, "npm-audit.json", `{"metadata": {"vulnerabilities": {"critical": 16, "high": 1, "moderate": 6, "low": 3, "info": 0, "total": 26}}}`)
	baselinePath := writeTemp(t, "baseline.json", `{
		"issue": "#35",
		"review_by": "2000-01-01",
		"baseline_counts": {"critical": 16, "high": 1, "moderate": 6, "low": 3, "info": 0, "total": 26}
	}`)
	_, findings, _, err := ParseNpmAudit(auditPath, baselinePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, f := range findings {
		if f.ID == "baseline-expired" {
			found = true
		}
	}
	if !found {
		t.Error("expected a 'baseline-expired' finding when review_by has passed")
	}
}
