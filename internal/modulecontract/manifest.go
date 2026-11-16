// Package modulecontract validates and resolves ForgeStack module manifests.
package modulecontract

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var moduleName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
var version = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// Manifest is the deliberately small, language-neutral module contract.
type Manifest struct {
	Name            string            `json:"name"`
	Version         string            `json:"version"`
	ContractVersion string            `json:"contract_version"`
	Category        string            `json:"category"`
	Provides        []string          `json:"provides"`
	Requires        []Dependency      `json:"requires,omitempty"`
	Optional        []Dependency      `json:"optional,omitempty"`
	Languages       []string          `json:"languages,omitempty"`
	Runtimes        []string          `json:"runtimes,omitempty"`
	Configuration   map[string]string `json:"configuration,omitempty"`
	Secrets         []string          `json:"secrets,omitempty"`
	Deployment      []string          `json:"deployment_targets,omitempty"`
	HealthChecks    []string          `json:"health_checks,omitempty"`
	Ownership       Ownership         `json:"ownership"`
	Cleanup         string            `json:"cleanup_behavior"`
}

// Dependency may constrain the version of the module providing a capability.
type Dependency struct {
	Capability string `json:"capability"`
	Version    string `json:"version,omitempty"` // exact MAJOR.MINOR.PATCH or >=MAJOR.MINOR.PATCH
}

// Ownership documents who owns resources created by the module and required tags.
type Ownership struct {
	Scope string   `json:"scope"`
	Tags  []string `json:"required_tags,omitempty"`
}

// Composition is a deterministic, reviewable resolution result.
type Composition struct {
	Modules     []string            `json:"modules"`
	Connections map[string][]string `json:"connections"`
	Unconnected []string            `json:"unconnected_optional_capabilities,omitempty"`
}

func Read(path string) (Manifest, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path is this CLI's own operator-supplied argument, not untrusted input
	if err != nil {
		return Manifest{}, err
	}
	defer func() { _ = f.Close() }()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Manifest{}, fmt.Errorf("%s: expected one JSON value", path)
	}
	if err := Validate(m); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

func Validate(m Manifest) error {
	if err := validateIdentity(m); err != nil {
		return err
	}
	if err := validateMetadataValues(m); err != nil {
		return err
	}
	if m.Ownership.Scope != "environment" && m.Ownership.Scope != "module" {
		return errors.New("ownership.scope must be environment or module")
	}
	if m.Cleanup != "environment-owned-only" && m.Cleanup != "explicit-only" {
		return errors.New("cleanup_behavior must be environment-owned-only or explicit-only")
	}
	if err := validateDependencies(m); err != nil {
		return err
	}
	if err := validateUniqueProvides(m.Provides); err != nil {
		return err
	}
	for key := range m.Configuration {
		if strings.TrimSpace(key) == "" {
			return errors.New("configuration keys cannot be empty")
		}
	}
	return nil
}

func validateIdentity(m Manifest) error {
	if !moduleName.MatchString(m.Name) {
		return fmt.Errorf("name %q must be lowercase kebab-case", m.Name)
	}
	if !version.MatchString(m.Version) {
		return fmt.Errorf("version %q must be MAJOR.MINOR.PATCH", m.Version)
	}
	if !version.MatchString(m.ContractVersion) {
		return fmt.Errorf("contract_version %q must be MAJOR.MINOR.PATCH", m.ContractVersion)
	}
	if strings.TrimSpace(m.Category) == "" {
		return errors.New("category is required")
	}
	if len(m.Provides) == 0 {
		return errors.New("at least one provided capability is required")
	}
	return nil
}

func validateMetadataValues(m Manifest) error {
	for _, group := range [][]string{m.Provides, m.Languages, m.Runtimes, m.Secrets, m.Deployment, m.HealthChecks, m.Ownership.Tags} {
		for _, value := range group {
			if strings.TrimSpace(value) == "" {
				return errors.New("capability and metadata values cannot be empty")
			}
		}
	}
	return nil
}

func validateDependencies(m Manifest) error {
	for _, d := range append(append([]Dependency{}, m.Requires...), m.Optional...) {
		if strings.TrimSpace(d.Capability) == "" {
			return errors.New("dependency capability is required")
		}
		if d.Version != "" && !validVersionConstraint(d.Version) {
			return fmt.Errorf("invalid dependency version constraint %q", d.Version)
		}
	}
	return nil
}

func validateUniqueProvides(capabilities []string) error {
	seen := map[string]bool{}
	for _, capability := range capabilities {
		if seen[capability] {
			return fmt.Errorf("duplicate provided capability %q", capability)
		}
		seen[capability] = true
	}
	return nil
}

// Resolve checks required capabilities and links optional capabilities when present.
func Resolve(manifests []Manifest) (Composition, error) {
	if len(manifests) == 0 {
		return Composition{}, errors.New("select at least one module")
	}
	byName := map[string]Manifest{}
	providers := map[string][]string{}
	for _, m := range manifests {
		if err := Validate(m); err != nil {
			return Composition{}, fmt.Errorf("module %q: %w", m.Name, err)
		}
		if _, exists := byName[m.Name]; exists {
			return Composition{}, fmt.Errorf("duplicate module %q", m.Name)
		}
		byName[m.Name] = m
		for _, c := range m.Provides {
			providers[c] = append(providers[c], m.Name)
		}
	}
	result := Composition{Connections: map[string][]string{}}
	for name, m := range byName {
		result.Modules = append(result.Modules, name)
		links, err := resolveRequired(name, m.Requires, providers, byName)
		if err != nil {
			return Composition{}, err
		}
		result.Connections[name] = append(result.Connections[name], links...)
		optLinks, unconnected := resolveOptional(name, m.Optional, providers, byName)
		result.Connections[name] = append(result.Connections[name], optLinks...)
		result.Unconnected = append(result.Unconnected, unconnected...)
	}
	sort.Strings(result.Modules)
	for name := range result.Connections {
		sort.Strings(result.Connections[name])
	}
	for name, links := range result.Connections {
		if len(links) == 0 {
			delete(result.Connections, name)
		}
	}
	sort.Strings(result.Unconnected)
	return result, nil
}

func resolveRequired(name string, deps []Dependency, providers map[string][]string, byName map[string]Manifest) ([]string, error) {
	var links []string
	for _, d := range deps {
		available := compatibleProviders(d, externalProviders(providers[d.Capability], name), byName)
		if len(available) == 0 {
			return nil, fmt.Errorf("module %q requires capability %q (version %q), but no compatible selected module provides it", name, d.Capability, d.Version)
		}
		for _, provider := range available {
			links = append(links, provider+":"+d.Capability)
		}
	}
	return links, nil
}

func resolveOptional(name string, deps []Dependency, providers map[string][]string, byName map[string]Manifest) (links []string, unconnected []string) {
	for _, d := range deps {
		available := compatibleProviders(d, externalProviders(providers[d.Capability], name), byName)
		for _, provider := range available {
			links = append(links, provider+":"+d.Capability)
		}
		if len(available) == 0 {
			unconnected = append(unconnected, name+":"+d.Capability)
		}
	}
	return links, unconnected
}

func externalProviders(providers []string, consumer string) []string {
	var result []string
	for _, provider := range providers {
		if provider != consumer {
			result = append(result, provider)
		}
	}
	return result
}

func validVersionConstraint(value string) bool {
	return version.MatchString(strings.TrimPrefix(value, ">="))
}

func compatibleProviders(dep Dependency, providers []string, manifests map[string]Manifest) []string {
	var result []string
	for _, name := range providers {
		if dep.Version == "" || versionMatches(manifests[name].Version, dep.Version) {
			result = append(result, name)
		}
	}
	return result
}

func versionMatches(actual, constraint string) bool {
	if !strings.HasPrefix(constraint, ">=") {
		return actual == constraint
	}
	actualParts := strings.Split(actual, ".")
	minimumParts := strings.Split(strings.TrimPrefix(constraint, ">="), ".")
	for i := range actualParts {
		a, _ := strconv.Atoi(actualParts[i])
		b, _ := strconv.Atoi(minimumParts[i])
		if a != b {
			return a > b
		}
	}
	return true
}

// Encode writes stable JSON with a trailing newline.
func (c Composition) Encode(w io.Writer) error {
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	return e.Encode(c)
}
