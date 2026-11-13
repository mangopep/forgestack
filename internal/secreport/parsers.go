package secreport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// readTool opens path and reports whether that's even possible — every
// parser below starts by calling this so "the file doesn't exist" and "the
// file is garbage" are both reported as the tool's ToolResult status,
// never silently treated as zero findings.
func readTool(name, path string) ([]byte, ToolResult, bool) {
	if path == "" {
		return nil, ToolResult{Name: name, Status: StatusMissing, Note: "no evidence path provided"}, false
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is this CLI's own operator-supplied flag, not untrusted input
	if err != nil {
		return nil, ToolResult{Name: name, EvidencePath: path, Status: StatusMissing, Note: err.Error()}, false
	}
	return data, ToolResult{Name: name, EvidencePath: path}, true
}

// --- govulncheck ---

type govulncheckOSV struct {
	ID       string `json:"id"`
	Summary  string `json:"summary"`
	Modified string `json:"modified"`
}

type govulncheckEntry struct {
	OSV     *govulncheckOSV `json:"osv,omitempty"`
	Finding *struct {
		OSV string `json:"osv"`
	} `json:"finding,omitempty"`
}

// ParseGovulncheck reads `govulncheck -json ./...` output. govulncheck
// streams multiple top-level JSON values (not one array, not NDJSON
// line-delimited either) — json.Decoder.Decode in a loop is the correct
// way to read it, not a single json.Unmarshal call.
func ParseGovulncheck(path string) (ToolResult, []Finding, error) {
	data, result, ok := readTool("govulncheck", path)
	if !ok {
		return result, nil, nil
	}
	result.Status = StatusRan

	osvByID := map[string]govulncheckOSV{}
	findingOSVIDs := map[string]bool{}

	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		var entry govulncheckEntry
		if err := dec.Decode(&entry); err != nil {
			if err == io.EOF {
				break
			}
			result.Status = StatusParseFailed
			result.Note = err.Error()
			return result, nil, fmt.Errorf("parse govulncheck output: %w", err)
		}
		if entry.OSV != nil {
			osvByID[entry.OSV.ID] = *entry.OSV
		}
		if entry.Finding != nil {
			findingOSVIDs[entry.Finding.OSV] = true
		}
	}

	var findings []Finding
	for id := range findingOSVIDs {
		osv := osvByID[id]
		findings = append(findings, Finding{
			ControlType: ControlAutomated,
			Tool:        "govulncheck",
			ID:          id,
			Severity:    SeverityUnknown, // govulncheck's OSV records don't carry a normalized severity field
			Component:   "go module (reachable call path)",
			Description: osv.Summary,
			Environment: "go",
			Evidence:    path,
			Remediation: "upgrade the affected module to a version past " + osv.Modified,
		})
	}
	return result, findings, nil
}

// --- pip-audit ---

type pipAuditVuln struct {
	ID          string   `json:"id"`
	FixVersions []string `json:"fix_versions"`
	Description string   `json:"description"`
}

type pipAuditDependency struct {
	Name    string         `json:"name"`
	Version string         `json:"version"`
	Vulns   []pipAuditVuln `json:"vulns"`
}

type pipAuditReport struct {
	Dependencies []pipAuditDependency `json:"dependencies"`
}

// ParsePipAudit reads `pip-audit --format=json` output.
func ParsePipAudit(path string) (ToolResult, []Finding, error) {
	data, result, ok := readTool("pip-audit", path)
	if !ok {
		return result, nil, nil
	}
	result.Status = StatusRan

	var report pipAuditReport
	if err := json.Unmarshal(data, &report); err != nil {
		result.Status = StatusParseFailed
		result.Note = err.Error()
		return result, nil, fmt.Errorf("parse pip-audit output: %w", err)
	}

	var findings []Finding
	for _, dep := range report.Dependencies {
		for _, v := range dep.Vulns {
			fixed := ""
			if len(v.FixVersions) > 0 {
				fixed = v.FixVersions[0]
			}
			findings = append(findings, Finding{
				ControlType:      ControlAutomated,
				Tool:             "pip-audit",
				ID:               v.ID,
				Severity:         SeverityUnknown, // pip-audit's own JSON doesn't carry a normalized severity
				Component:        dep.Name,
				InstalledVersion: dep.Version,
				FixedVersion:     fixed,
				Description:      v.Description,
				Environment:      "python (apps/python-api)",
				Evidence:         path,
			})
		}
	}
	return result, findings, nil
}

// --- Trivy (image and config scans share one result shape) ---

type trivyVulnerability struct {
	VulnerabilityID  string `json:"VulnerabilityID"`
	PkgName          string `json:"PkgName"`
	InstalledVersion string `json:"InstalledVersion"`
	FixedVersion     string `json:"FixedVersion"`
	Severity         string `json:"Severity"`
	Title            string `json:"Title"`
	Status           string `json:"Status"` // "affected", "fixed", "will_not_fix", "fix_deferred", "under_investigation"
}

type trivyMisconfiguration struct {
	ID       string `json:"ID"`
	Title    string `json:"Title"`
	Severity string `json:"Severity"`
	Message  string `json:"Message"`
}

type trivyResult struct {
	Target            string                  `json:"Target"`
	Vulnerabilities   []trivyVulnerability    `json:"Vulnerabilities"`
	Misconfigurations []trivyMisconfiguration `json:"Misconfigurations"`
}

type trivyReport struct {
	ArtifactName string        `json:"ArtifactName"`
	Results      []trivyResult `json:"Results"`
}

// ParseTrivy reads a `trivy image`/`trivy config --format json` report.
// environment labels which scanned artifact this is (e.g. "go-image",
// "python-image", "go-dockerfile") for the report's Environment field.
func ParseTrivy(toolName, environment, path string) (ToolResult, []Finding, error) {
	data, result, ok := readTool(toolName, path)
	if !ok {
		return result, nil, nil
	}
	result.Status = StatusRan

	var report trivyReport
	if err := json.Unmarshal(data, &report); err != nil {
		result.Status = StatusParseFailed
		result.Note = err.Error()
		return result, nil, fmt.Errorf("parse trivy output: %w", err)
	}

	var findings []Finding
	for _, r := range report.Results {
		for _, v := range r.Vulnerabilities {
			unfixed := v.Status != "fixed" && v.FixedVersion == ""
			findings = append(findings, Finding{
				ControlType:       ControlAutomated,
				Tool:              toolName,
				ID:                v.VulnerabilityID,
				Severity:          Severity(v.Severity),
				Component:         v.PkgName,
				InstalledVersion:  v.InstalledVersion,
				FixedVersion:      v.FixedVersion,
				Description:       v.Title,
				Environment:       environment,
				Evidence:          path,
				SuppressedUnfixed: unfixed,
			})
		}
		for _, m := range r.Misconfigurations {
			findings = append(findings, Finding{
				ControlType: ControlAutomated,
				Tool:        toolName,
				ID:          m.ID,
				Severity:    Severity(m.Severity),
				Component:   r.Target,
				Description: m.Title + ": " + m.Message,
				Environment: environment,
				Evidence:    path,
			})
		}
	}
	return result, findings, nil
}

// --- gitleaks ---

type gitleaksFinding struct {
	RuleID string `json:"RuleID"`
	File   string `json:"File"`
	Commit string `json:"Commit"`
}

// ParseGitleaks reads a `gitleaks detect --report-format=json` report (a
// JSON array, empty array `[]` when no secrets are found — NOT absent).
func ParseGitleaks(path string) (ToolResult, []Finding, error) {
	data, result, ok := readTool("gitleaks", path)
	if !ok {
		return result, nil, nil
	}
	result.Status = StatusRan

	var leaks []gitleaksFinding
	if err := json.Unmarshal(data, &leaks); err != nil {
		result.Status = StatusParseFailed
		result.Note = err.Error()
		return result, nil, fmt.Errorf("parse gitleaks output: %w", err)
	}

	var findings []Finding
	for _, l := range leaks {
		findings = append(findings, Finding{
			ControlType: ControlAutomated,
			Tool:        "gitleaks",
			ID:          l.RuleID,
			Severity:    SeverityCritical, // a committed secret is always treated as critical regardless of rule
			Component:   l.File,
			Description: "potential committed secret at commit " + l.Commit,
			Environment: "git history",
			Evidence:    path,
			Remediation: "rotate the exposed credential immediately; purging it from history alone is not sufficient",
		})
	}
	return result, findings, nil
}
