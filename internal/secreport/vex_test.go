package secreport

import "testing"

func TestBuildVEXCreatesStatementForSuppressedFinding(t *testing.T) {
	report := Build(buildInput(
		nil,
		[]Finding{{Tool: "trivy", ID: "CVE-1", Component: "libfoo", InstalledVersion: "1.0", SuppressedUnfixed: true, Evidence: "trivy.json"}},
		nil, nil,
	))
	vex := report.BuildVEX("test-author")
	if len(vex.Statements) != 1 {
		t.Fatalf("expected 1 VEX statement, got %d", len(vex.Statements))
	}
	s := vex.Statements[0]
	if s.Status != VEXAffected {
		t.Errorf("status = %v, want %v (a suppressed-but-unfixed finding must not be claimed not_affected without per-CVE analysis)", s.Status, VEXAffected)
	}
	if s.Evidence == "" || s.Reviewer == "" || s.ReassessBy == "" {
		t.Errorf("every VEX statement must carry evidence, reviewer, and a reassessment date, got %+v", s)
	}
}

func TestBuildVEXSkipsNonSuppressedFindings(t *testing.T) {
	report := Build(buildInput(
		nil,
		[]Finding{{Tool: "trivy", ID: "CVE-2", SuppressedUnfixed: false}},
		nil, nil,
	))
	vex := report.BuildVEX("test-author")
	if len(vex.Statements) != 0 {
		t.Errorf("expected 0 VEX statements for an unresolved, non-suppressed finding, got %d", len(vex.Statements))
	}
}

func TestBuildVEXIncludesTrackedExceptions(t *testing.T) {
	report := Build(buildInput(nil, nil,
		[]Exception{{ID: "#35", Component: "website", Owner: "mangopep", ReviewBy: "2099-01-01", Justification: "tracked"}},
		nil,
	))
	vex := report.BuildVEX("test-author")
	if len(vex.Statements) != 1 || vex.Statements[0].Vulnerability != "#35" {
		t.Fatalf("expected 1 statement for #35, got %+v", vex.Statements)
	}
}

func TestValidateVEXRejectsStatementWithNoEvidence(t *testing.T) {
	doc := VEXDocument{Statements: []VEXStatement{
		{Vulnerability: "CVE-X", Reviewer: "me", ReassessBy: "2099-01-01", Evidence: ""},
	}}
	problems := ValidateVEX(doc)
	if len(problems) == 0 {
		t.Error("expected a validation problem for a statement with no evidence")
	}
}

func TestValidateVEXRejectsStatementWithNoReviewer(t *testing.T) {
	doc := VEXDocument{Statements: []VEXStatement{
		{Vulnerability: "CVE-X", Reviewer: "", ReassessBy: "2099-01-01", Evidence: "some-scan.json"},
	}}
	problems := ValidateVEX(doc)
	if len(problems) == 0 {
		t.Error("expected a validation problem for a statement with no reviewer")
	}
}

func TestValidateVEXFlagsExpiredReassessment(t *testing.T) {
	doc := VEXDocument{Statements: []VEXStatement{
		{Vulnerability: "CVE-X", Reviewer: "me", ReassessBy: "2000-01-01", Evidence: "some-scan.json"},
	}}
	problems := ValidateVEX(doc)
	if len(problems) == 0 {
		t.Error("expected a validation problem for a statement whose reassessment date has passed")
	}
}

func TestValidateVEXAcceptsCompleteStatement(t *testing.T) {
	doc := VEXDocument{Statements: []VEXStatement{
		{Vulnerability: "CVE-X", Reviewer: "me", ReassessBy: "2099-01-01", Evidence: "some-scan.json"},
	}}
	problems := ValidateVEX(doc)
	if len(problems) != 0 {
		t.Errorf("expected no problems for a complete, non-expired statement, got %v", problems)
	}
}
