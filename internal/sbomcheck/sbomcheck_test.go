package sbomcheck

import (
	"encoding/json"
	"testing"
)

const minimalValidSBOM = `{
	"bomFormat": "CycloneDX",
	"specVersion": "1.5",
	"metadata": {
		"timestamp": "2026-01-01T00:00:00Z",
		"authors": [{"name": "Test"}],
		"tools": {"components": [{"name": "syft"}]}
	},
	"components": [
		{"type": "library", "name": "example", "version": "1.0.0", "purl": "pkg:generic/example@1.0.0", "hashes": [{"alg":"SHA-256","content":"abc"}], "licenses": [{"license":{"id":"MIT"}}]}
	],
	"dependencies": [{"ref": "example"}]
}`

func TestValidatePassesOnCompleteSBOM(t *testing.T) {
	doc, err := Parse([]byte(minimalValidSBOM))
	if err != nil {
		t.Fatal(err)
	}
	report := Validate("test.json", doc)
	if !report.Pass {
		t.Errorf("expected PASS, got missing: %v", report.MissingRequired)
	}
}

func TestValidateFailsWithoutProducer(t *testing.T) {
	var root map[string]any
	if err := json.Unmarshal([]byte(minimalValidSBOM), &root); err != nil {
		t.Fatal(err)
	}
	metadata := root["metadata"].(map[string]any)
	delete(metadata, "authors")
	data, _ := json.Marshal(root)

	doc, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	report := Validate("test.json", doc)
	if report.Pass {
		t.Error("expected FAIL with no producer/author recorded")
	}
	found := false
	for _, e := range report.MissingRequired {
		if e == ElementProducer {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ElementProducer in missing list, got %v", report.MissingRequired)
	}
}

func TestValidateFailsWithoutDependencyRelationships(t *testing.T) {
	var root map[string]any
	if err := json.Unmarshal([]byte(minimalValidSBOM), &root); err != nil {
		t.Fatal(err)
	}
	delete(root, "dependencies")
	data, _ := json.Marshal(root)

	doc, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	report := Validate("test.json", doc)
	if report.Pass {
		t.Error("expected FAIL with no dependency relationships recorded")
	}
}

func TestValidateIgnoresFileTypeComponentsForVersionLicenseChecks(t *testing.T) {
	// A file-type entry (syft's per-file provenance tracking) legitimately
	// has no version or license — it must not fail the SBOM just for that.
	sbom := `{
		"bomFormat": "CycloneDX",
		"specVersion": "1.5",
		"metadata": {
			"timestamp": "2026-01-01T00:00:00Z",
			"authors": [{"name": "Test"}],
			"tools": {"components": [{"name": "syft"}]}
		},
		"components": [
			{"type": "library", "name": "example", "version": "1.0.0", "purl": "pkg:generic/example@1.0.0", "licenses": [{"license":{"id":"MIT"}}]},
			{"type": "file", "name": "/etc/passwd", "hashes": [{"alg":"SHA-256","content":"abc"}]}
		],
		"dependencies": [{"ref": "example"}]
	}`
	doc, err := Parse([]byte(sbom))
	if err != nil {
		t.Fatal(err)
	}
	report := Validate("test.json", doc)
	if !report.Pass {
		t.Errorf("expected PASS; a file-type component with no version must not fail the check, got missing: %v", report.MissingRequired)
	}
	if cov := report.CoverageGaps[ElementLicenses]; cov.Total != 1 {
		t.Errorf("license coverage total = %d, want 1 (the file entry must be excluded from the denominator)", cov.Total)
	}
}

func TestValidateFailsOnMissingComponentVersion(t *testing.T) {
	sbom := `{
		"bomFormat": "CycloneDX",
		"specVersion": "1.5",
		"metadata": {
			"timestamp": "2026-01-01T00:00:00Z",
			"authors": [{"name": "Test"}],
			"tools": {"components": [{"name": "syft"}]}
		},
		"components": [
			{"type": "library", "name": "example", "purl": "pkg:generic/example"}
		],
		"dependencies": [{"ref": "example"}]
	}`
	doc, err := Parse([]byte(sbom))
	if err != nil {
		t.Fatal(err)
	}
	report := Validate("test.json", doc)
	if report.Pass {
		t.Error("expected FAIL: a real package component with no version must fail the check")
	}
}

func TestAugmentAddsProducerWithoutDroppingUnmodeledFields(t *testing.T) {
	sbom := `{
		"bomFormat": "CycloneDX",
		"specVersion": "1.5",
		"metadata": {"timestamp": "2026-01-01T00:00:00Z"},
		"components": [{"type": "library", "name": "example", "version": "1.0.0", "description": "an example component with a field this package does not model"}]
	}`
	augmented, err := Augment([]byte(sbom), "ForgeStack CI", "mangopep/forgestack", "deadbeef")
	if err != nil {
		t.Fatal(err)
	}

	var root map[string]any
	if err := json.Unmarshal(augmented, &root); err != nil {
		t.Fatal(err)
	}
	metadata := root["metadata"].(map[string]any)
	authors, ok := metadata["authors"].([]any)
	if !ok || len(authors) != 1 {
		t.Fatalf("expected 1 author injected, got %v", metadata["authors"])
	}

	components := root["components"].([]any)
	comp := components[0].(map[string]any)
	if comp["description"] != "an example component with a field this package does not model" {
		t.Error("Augment must not drop fields it doesn't model (e.g. description)")
	}
}

func TestAugmentDoesNotOverwriteExistingAuthors(t *testing.T) {
	sbom := `{
		"bomFormat": "CycloneDX",
		"specVersion": "1.5",
		"metadata": {"timestamp": "2026-01-01T00:00:00Z", "authors": [{"name": "Original Author"}]},
		"components": []
	}`
	augmented, err := Augment([]byte(sbom), "ForgeStack CI", "", "")
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(augmented, &root); err != nil {
		t.Fatal(err)
	}
	metadata := root["metadata"].(map[string]any)
	authors := metadata["authors"].([]any)
	first := authors[0].(map[string]any)
	if first["name"] != "Original Author" {
		t.Errorf("expected original author preserved, got %v", first["name"])
	}
}
