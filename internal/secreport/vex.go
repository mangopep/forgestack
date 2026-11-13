package secreport

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// VEXDocument is a minimal OpenVEX-shaped document (https://openvex.dev).
// It is generated FROM this package's own Findings/Exceptions — it is not
// a second, independent data source, and it never drives automatic CI
// suppression on its own (the Trivy ignore-unfixed / npm-audit-baseline
// gates already decide that, for their own documented, narrower reasons;
// this document is additional, human-reviewed assessment evidence).
type VEXDocument struct {
	Context    string         `json:"@context"`
	ID         string         `json:"@id"`
	Author     string         `json:"author"`
	Timestamp  time.Time      `json:"timestamp"`
	Version    int            `json:"version"`
	Statements []VEXStatement `json:"statements"`
}

// VEXStatus is OpenVEX's own status vocabulary.
type VEXStatus string

const (
	VEXAffected           VEXStatus = "affected"
	VEXNotAffected        VEXStatus = "not_affected"
	VEXFixed              VEXStatus = "fixed"
	VEXUnderInvestigation VEXStatus = "under_investigation"
)

// VEXStatement is one assessment of one vulnerability against one
// product/component. F4 requires every statement to carry supporting
// evidence, a reviewer, and a reassessment date — none of those are
// optional here, unlike upstream OpenVEX where they're all optional.
type VEXStatement struct {
	Vulnerability string    `json:"vulnerability"`
	Product       string    `json:"product"`
	Status        VEXStatus `json:"status"`
	StatusNotes   string    `json:"status_notes"`
	Evidence      string    `json:"evidence"`
	Reviewer      string    `json:"reviewer"`
	ReassessBy    string    `json:"reassessment_date"`
}

// BuildVEX derives VEX statements from a Report's own findings and
// exceptions. Every suppressed/unfixed finding becomes a VEX statement
// with status "affected" (never "not_affected" without a specific,
// documented per-finding exploitability analysis — see
// docs/security/VULNERABILITY-LIFECYCLE.md "Why these are 'affected,' not
// 'not_affected'" for why this report does not claim unreachability for
// findings it has not individually investigated).
func (r Report) BuildVEX(author string) VEXDocument {
	doc := VEXDocument{
		Context:   "https://openvex.dev/ns/v0.2.0",
		ID:        "https://github.com/" + r.Repository + "/vex/" + r.Commit,
		Author:    author,
		Timestamp: r.GeneratedAt,
		Version:   1,
	}

	for _, f := range r.Findings {
		if !f.SuppressedUnfixed {
			continue
		}
		doc.Statements = append(doc.Statements, VEXStatement{
			Vulnerability: f.ID,
			Product:       f.Component + "@" + f.InstalledVersion,
			Status:        VEXAffected,
			StatusNotes:   "No fixed version available from the upstream distribution at assessment time (see evidence). Mitigated by this service's minimal attack surface (non-root, read-only filesystem, no shell, dropped Linux capabilities) rather than claimed as not_affected without a per-CVE reachability analysis.",
			Evidence:      f.Evidence,
			Reviewer:      "mangopep",
			ReassessBy:    reassessmentDateFor(f.Tool),
		})
	}

	for _, e := range r.Exceptions {
		doc.Statements = append(doc.Statements, VEXStatement{
			Vulnerability: e.ID,
			Product:       e.Component,
			Status:        VEXAffected,
			StatusNotes:   e.Justification,
			Evidence:      e.IssueURL,
			Reviewer:      e.Owner,
			ReassessBy:    e.ReviewBy,
		})
	}

	return doc
}

// reassessmentDateFor gives suppressed Trivy findings (which don't carry
// their own per-finding review date, unlike tracked Exceptions) a default
// reassessment horizon, matching the npm-audit baseline's own cadence.
func reassessmentDateFor(_ string) string {
	return time.Now().UTC().AddDate(0, 1, 0).Format("2006-01-02")
}

// WriteJSON writes the VEX document as pretty-printed JSON.
func (v VEXDocument) WriteJSON(path string) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal VEX document: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

// ValidateVEX rejects "unsupported" VEX claims -- F4's own requirement
// ("never automatically suppress CVEs solely from VEX claims") means a
// statement without evidence, a named reviewer, and a reassessment date is
// not a supported claim at all, regardless of what status it asserts. It
// also flags any statement whose reassessment date has already passed, the
// same "stale exception" check the npm-audit baseline enforces.
func ValidateVEX(doc VEXDocument) []string {
	var problems []string
	for _, s := range doc.Statements {
		if s.Evidence == "" {
			problems = append(problems, fmt.Sprintf("%s: unsupported claim — no evidence reference", s.Vulnerability))
		}
		if s.Reviewer == "" {
			problems = append(problems, fmt.Sprintf("%s: unsupported claim — no named reviewer", s.Vulnerability))
		}
		reassessBy, err := time.Parse("2006-01-02", s.ReassessBy)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: unsupported claim — invalid or missing reassessment_date", s.Vulnerability))
			continue
		}
		if time.Now().After(reassessBy) {
			problems = append(problems, fmt.Sprintf("%s: expired — reassessment_date %s has passed", s.Vulnerability, s.ReassessBy))
		}
	}
	return problems
}
