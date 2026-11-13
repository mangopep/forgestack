// Package sbomcheck validates a CycloneDX SBOM against the NTIA/CISA
// minimum-elements baseline and augments it with the producer/provenance
// fields syft doesn't fill in on its own (metadata.authors,
// commit/repository properties).
//
// Validate never reports an element "present" by assuming it — every
// element is counted explicitly, and "not every component has a license"
// is reported as a real coverage gap, not silently passed. See F4's own
// instruction: never claim a capability (here, SBOM completeness) that
// the evidence doesn't support.
package sbomcheck

import (
	"encoding/json"
	"fmt"
	"time"
)

// CycloneDX is the subset of the CycloneDX 1.5/1.6 schema this package
// reads and writes. Unknown fields round-trip via json.RawMessage where
// practical, but this package only validates/augments the fields listed
// here — it is not a general-purpose CycloneDX library.
type CycloneDX struct {
	BOMFormat    string       `json:"bomFormat"`
	SpecVersion  string       `json:"specVersion"`
	SerialNumber string       `json:"serialNumber,omitempty"`
	Version      int          `json:"version,omitempty"`
	Metadata     Metadata     `json:"metadata"`
	Components   []Component  `json:"components"`
	Dependencies []Dependency `json:"dependencies,omitempty"`
}

type Metadata struct {
	Timestamp  string          `json:"timestamp"`
	Authors    []Contact       `json:"authors,omitempty"`
	Supplier   *Contact        `json:"supplier,omitempty"`
	Component  *Component      `json:"component,omitempty"`
	Tools      json.RawMessage `json:"tools,omitempty"`
	Properties []Property      `json:"properties,omitempty"`
}

type Contact struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

type Component struct {
	BOMRef     string            `json:"bom-ref,omitempty"`
	Type       string            `json:"type,omitempty"`
	Name       string            `json:"name"`
	Version    string            `json:"version,omitempty"`
	PURL       string            `json:"purl,omitempty"`
	CPE        string            `json:"cpe,omitempty"`
	Hashes     []json.RawMessage `json:"hashes,omitempty"`
	Licenses   []json.RawMessage `json:"licenses,omitempty"`
	Properties []Property        `json:"properties,omitempty"`
}

type Property struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Dependency struct {
	Ref       string   `json:"ref"`
	DependsOn []string `json:"dependsOn,omitempty"`
}

// Element is one CISA/NTIA minimum element this package checks for.
type Element string

const (
	ElementBOMFormat     Element = "bom_format_and_spec_version"
	ElementTimestamp     Element = "timestamp"
	ElementProducer      Element = "producer_author_or_supplier"
	ElementToolMetadata  Element = "tool_metadata"
	ElementComponentInfo Element = "component_name_and_version"
	ElementIdentifiers   Element = "unique_identifiers_purl_or_cpe"
	ElementRelationships Element = "dependency_relationships"
	ElementHashes        Element = "hashes_where_available"
	ElementLicenses      Element = "licenses_where_available"
)

// ValidationReport is the result of checking one SBOM. It never collapses
// to a single bool — "pass" requires every required element present AND
// every coverage gap explicitly recorded, not silently ignored.
type ValidationReport struct {
	Path            string               `json:"path"`
	ComponentCount  int                  `json:"component_count"`
	MissingRequired []Element            `json:"missing_required"`
	CoverageGaps    map[Element]Coverage `json:"coverage_gaps"`
	Pass            bool                 `json:"pass"`
}

// Coverage records "N of M components have this element" for elements
// that are legitimately not present on every component (hashes, licenses)
// — these are reported, never required to be 100%, but never hidden either.
type Coverage struct {
	Present int `json:"present"`
	Total   int `json:"total"`
}

// Parse reads raw CycloneDX JSON.
func Parse(data []byte) (CycloneDX, error) {
	var doc CycloneDX
	if err := json.Unmarshal(data, &doc); err != nil {
		return CycloneDX{}, fmt.Errorf("parse CycloneDX document: %w", err)
	}
	return doc, nil
}

// Validate checks doc against the minimum-elements baseline.
func Validate(path string, doc CycloneDX) ValidationReport {
	report := ValidationReport{
		Path:           path,
		ComponentCount: len(doc.Components),
		CoverageGaps:   map[Element]Coverage{},
	}

	requireNonEmpty := func(el Element, present bool) {
		if !present {
			report.MissingRequired = append(report.MissingRequired, el)
		}
	}

	requireNonEmpty(ElementBOMFormat, doc.BOMFormat == "CycloneDX" && doc.SpecVersion != "")
	requireNonEmpty(ElementTimestamp, doc.Metadata.Timestamp != "")
	requireNonEmpty(ElementProducer, len(doc.Metadata.Authors) > 0 || doc.Metadata.Supplier != nil)
	requireNonEmpty(ElementToolMetadata, len(doc.Metadata.Tools) > 0)
	requireNonEmpty(ElementRelationships, len(doc.Dependencies) > 0)

	// syft's file-level entries (type "file": every individual file in a
	// container image, tracked for hash/provenance purposes) are not
	// "components" in the CISA/NTIA sense -- a file has no version,
	// license, or package identifier by definition, and requiring one
	// would be meaningless, not rigorous. Name/version/identifier/license
	// coverage is checked against actual packages only (library,
	// application, operating-system, container, etc.); hash coverage is
	// still checked across everything, since every file this package
	// reports on genuinely can have a hash.
	var packages []Component
	for _, c := range doc.Components {
		if c.Type != "file" {
			packages = append(packages, c)
		}
	}

	componentInfoOK := len(packages) > 0
	identifiersPresent, licensesPresent := 0, 0
	for _, c := range packages {
		if c.Name == "" || c.Version == "" {
			componentInfoOK = false
		}
		if c.PURL != "" || c.CPE != "" {
			identifiersPresent++
		}
		if len(c.Licenses) > 0 {
			licensesPresent++
		}
	}
	requireNonEmpty(ElementComponentInfo, componentInfoOK)
	requireNonEmpty(ElementIdentifiers, identifiersPresent > 0)

	hashesPresent := 0
	for _, c := range doc.Components {
		if len(c.Hashes) > 0 {
			hashesPresent++
		}
	}

	report.CoverageGaps[ElementIdentifiers] = Coverage{Present: identifiersPresent, Total: len(packages)}
	report.CoverageGaps[ElementHashes] = Coverage{Present: hashesPresent, Total: len(doc.Components)}
	report.CoverageGaps[ElementLicenses] = Coverage{Present: licensesPresent, Total: len(packages)}

	report.Pass = len(report.MissingRequired) == 0
	return report
}

// Augment fills in the producer/provenance fields syft does not set on
// its own: metadata.authors, metadata.timestamp (if absent), and
// commit/repository properties. It operates on the raw JSON tree (not
// through the narrower CycloneDX struct above) specifically so every
// field CycloneDX allows but this package doesn't model — descriptions,
// external references, scopes, and more — round-trips untouched. Only
// metadata.authors/.timestamp/.properties are added, and only if not
// already present.
func Augment(data []byte, producer, repository, commit string) ([]byte, error) {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse CycloneDX document for augmentation: %w", err)
	}

	metadata, _ := root["metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
	}

	if authors, ok := metadata["authors"].([]any); !ok || len(authors) == 0 {
		metadata["authors"] = []any{map[string]any{"name": producer}}
	}
	if ts, ok := metadata["timestamp"].(string); !ok || ts == "" {
		metadata["timestamp"] = time.Now().UTC().Format(time.RFC3339)
	}

	properties, _ := metadata["properties"].([]any)
	hasProp := func(name string) bool {
		for _, p := range properties {
			if pm, ok := p.(map[string]any); ok && pm["name"] == name {
				return true
			}
		}
		return false
	}
	if repository != "" && !hasProp("forgestack:repository") {
		properties = append(properties, map[string]any{"name": "forgestack:repository", "value": repository})
	}
	if commit != "" && !hasProp("forgestack:commit") {
		properties = append(properties, map[string]any{"name": "forgestack:commit", "value": commit})
	}
	metadata["properties"] = properties
	root["metadata"] = metadata

	return json.MarshalIndent(root, "", "  ")
}
