// Command secreport assembles ForgeStack's F4 security report from real,
// already-saved scanner evidence files (govulncheck, pip-audit, Trivy,
// gitleaks, npm audit). It makes no network calls and runs no scanners
// itself — it is the aggregation/reporting step, run after the scanners
// that produced the evidence it reads. See docs/security/ for the
// generated report's consumers and docs/adr/0005-f4-security-supply-chain.md
// for why this exists as a small, focused tool rather than a dashboard.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/mangopep/forgestack/internal/secreport"
)

func main() {
	var (
		repository        = flag.String("repository", "mangopep/forgestack", "repository name recorded in the report")
		commit            = flag.String("commit", "", "git commit SHA this evidence was gathered against")
		branch            = flag.String("branch", "", "branch name this evidence was gathered against")
		product           = flag.String("product", "forgestack", "product name this evidence is linked to (Audit & Trust linkage field)")
		environment       = flag.String("environment", "ci", "environment this evidence was gathered against (e.g. ci, local, staging)")
		owner             = flag.String("owner", "mangopep", "owner accountable for this evidence/report")
		govulncheckPath   = flag.String("govulncheck", "", "path to govulncheck -json output")
		pipAuditPath      = flag.String("pip-audit", "", "path to pip-audit --format=json output")
		trivyGoImage      = flag.String("trivy-go-image", "", "path to trivy image --format json output for the Go image")
		trivyPyImage      = flag.String("trivy-python-image", "", "path to trivy image --format json output for the Python image")
		trivyGoDockerfile = flag.String("trivy-go-dockerfile", "", "path to trivy config --format json output for the Go Dockerfile")
		trivyPyDockerfile = flag.String("trivy-python-dockerfile", "", "path to trivy config --format json output for the Python Dockerfile")
		gitleaksPath      = flag.String("gitleaks", "", "path to gitleaks --report-format=json output")
		npmAuditPath      = flag.String("npm-audit", "", "path to npm audit --json output")
		npmBaselinePath   = flag.String("npm-audit-baseline", "", "path to the npm audit baseline file")
		jsonOut           = flag.String("json-out", "secreport.json", "path to write the JSON report")
		mdOut             = flag.String("md-out", "secreport.md", "path to write the Markdown report")
		vexOut            = flag.String("vex-out", "vex.json", "path to write the OpenVEX-shaped assessment document")
	)
	flag.Parse()

	var tools []secreport.ToolResult
	var findings []secreport.Finding
	var exceptions []secreport.Exception
	var failures []secreport.ScanFailure

	addResult := func(tool secreport.ToolResult, f []secreport.Finding, err error, toolName string) {
		tools = append(tools, tool)
		findings = append(findings, f...)
		if err != nil {
			failures = append(failures, secreport.ScanFailure{Tool: toolName, Error: err.Error()})
		}
	}

	r, f, err := secreport.ParseGovulncheck(*govulncheckPath)
	addResult(r, f, err, "govulncheck")

	r, f, err = secreport.ParsePipAudit(*pipAuditPath)
	addResult(r, f, err, "pip-audit")

	r, f, err = secreport.ParseTrivy("trivy-go-image", "go container image", *trivyGoImage)
	addResult(r, f, err, "trivy-go-image")

	r, f, err = secreport.ParseTrivy("trivy-python-image", "python container image", *trivyPyImage)
	addResult(r, f, err, "trivy-python-image")

	r, f, err = secreport.ParseTrivy("trivy-go-dockerfile", "go Dockerfile", *trivyGoDockerfile)
	addResult(r, f, err, "trivy-go-dockerfile")

	r, f, err = secreport.ParseTrivy("trivy-python-dockerfile", "python Dockerfile", *trivyPyDockerfile)
	addResult(r, f, err, "trivy-python-dockerfile")

	r, f, err = secreport.ParseGitleaks(*gitleaksPath)
	addResult(r, f, err, "gitleaks")

	npmResult, npmFindings, npmExceptions, err := secreport.ParseNpmAudit(*npmAuditPath, *npmBaselinePath)
	tools = append(tools, npmResult)
	findings = append(findings, npmFindings...)
	exceptions = append(exceptions, npmExceptions...)
	if err != nil {
		failures = append(failures, secreport.ScanFailure{Tool: "npm-audit", Error: err.Error()})
	}

	// #40 used to be tracked here directly (a hardcoded secreport.Exception
	// describing Trivy's ignore-unfixed suppression on apps/python-api's
	// base image) rather than parsed from a file, since it was a manual,
	// time-bounded exception, not a live scanner output. Removed
	// 2026-11-14: the underlying finding was actually fixed (base image
	// moved to python:3.12-alpine, 0 HIGH/CRITICAL remaining), and
	// ignore-unfixed was removed from CI entirely -- there is no longer
	// any suppression mechanism to describe here. See
	// docs/security/findings-register.json's #40 entry for the fix.

	report := secreport.Build(secreport.ReportInput{
		Repository:   *repository,
		Commit:       *commit,
		Branch:       *branch,
		Product:      *product,
		Environment:  *environment,
		Owner:        *owner,
		Tools:        tools,
		Findings:     findings,
		Exceptions:   exceptions,
		ScanFailures: failures,
	})

	if err := report.WriteJSON(*jsonOut); err != nil {
		fmt.Fprintln(os.Stderr, "write JSON report:", err)
		os.Exit(1)
	}
	if err := report.WriteMarkdown(*mdOut); err != nil {
		fmt.Fprintln(os.Stderr, "write Markdown report:", err)
		os.Exit(1)
	}

	vex := report.BuildVEX("mangopep")
	if problems := secreport.ValidateVEX(vex); len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "VEX validation:", p)
		}
		fmt.Fprintln(os.Stderr, "VEX validation found unsupported or expired claims (see above) — written anyway for review, not silently dropped")
	}
	if err := vex.WriteJSON(*vexOut); err != nil {
		fmt.Fprintln(os.Stderr, "write VEX document:", err)
		os.Exit(1)
	}

	fmt.Printf("Report written: %s, %s, %s\nOverall status: %s\n", *jsonOut, *mdOut, *vexOut, report.OverallStatus)
}
