// Package costguard is a vendor-independent cost-policy engine for Terraform
// changes. It has no dependency on any pricing vendor: the core types and
// Evaluate here work from a plain Estimate value that could come from a
// hand-written fixture (MOCK, used everywhere in this package's own tests
// and in CI), a future live pricing adapter (LIVE, opt-in, never required),
// or an explicit absence of pricing data (UNAVAILABLE). A MOCK or
// UNAVAILABLE estimate can never approve a real deployment -- that rule is
// enforced in Evaluate itself, not left to a caller to remember.
package costguard

import (
	"encoding/json"
	"fmt"
	"time"
)

// Source identifies where an Estimate's numbers actually came from. This is
// the one piece of data this whole package treats as load-bearing: Evaluate
// refuses to approve anything unless Source is Live.
type Source string

const (
	// SourceLive means a real pricing lookup produced these numbers. Nothing
	// in this repository's tests or CI ever constructs a SourceLive value --
	// doing so requires an opt-in live adapter and the caller's own pricing
	// API credentials, neither of which this package invokes on its own.
	SourceLive Source = "LIVE"
	// SourceMock means these numbers came from a deterministic local
	// fixture, for testing the policy logic itself. A MOCK estimate can
	// never cause Evaluate to approve a deployment, regardless of how far
	// under budget it claims to be.
	SourceMock Source = "MOCK"
	// SourceUnavailable means no pricing data could be obtained at all
	// (adapter not configured, lookup failed, etc.). Treated the same as
	// MOCK for approval purposes: never approved.
	SourceUnavailable Source = "UNAVAILABLE"
)

// ResourceCost is one resource's cost line, with pricing explicitly optional
// -- a nil HourlyCost/MonthlyCost means "no price available for this
// resource type," which Evaluate treats as an incomplete estimate, not a
// zero cost.
type ResourceCost struct {
	Address      string   `json:"address"`
	ResourceType string   `json:"resource_type"`
	HourlyCost   *float64 `json:"hourly_cost,omitempty"`
	MonthlyCost  *float64 `json:"monthly_cost,omitempty"`
	// UsageBased flags a resource whose real bill depends on usage this
	// estimate cannot capture (e.g. S3 storage/requests, NAT gateway data
	// processing) -- the cost numbers above are a floor, not a ceiling.
	UsageBased bool `json:"usage_based,omitempty"`
	// Assumptions records what the estimate assumed to produce a number
	// (region, instance size defaults, usage volume, ...).
	Assumptions []string `json:"assumptions,omitempty"`
	// UnsupportedNote explains why HourlyCost/MonthlyCost are nil, when they
	// are. Required (by convention, not type system) whenever both are nil.
	UnsupportedNote string `json:"unsupported_note,omitempty"`
}

// Incomplete reports whether this resource has no usable pricing at all.
func (r ResourceCost) Incomplete() bool {
	return r.HourlyCost == nil && r.MonthlyCost == nil
}

// Estimate is a cost estimate for a whole Terraform plan, explicitly labeled
// with where it came from and when it was produced.
type Estimate struct {
	Source      Source    `json:"source"`
	GeneratedAt time.Time `json:"generated_at"`
	// ToolVersion is informational: the pricing tool's version for LIVE, or
	// a fixture identifier for MOCK (e.g. "fixture:estimate-cheap.json").
	ToolVersion  string         `json:"tool_version,omitempty"`
	Resources    []ResourceCost `json:"resources"`
	TotalHourly  *float64       `json:"total_hourly,omitempty"`
	TotalMonthly *float64       `json:"total_monthly,omitempty"`
	Currency     string         `json:"currency,omitempty"`
}

// Budget is a playground's configured spending and lifetime limits. Every
// field is required for Evaluate to reason about it; a zero value is a real
// zero limit, not "unset." In Go code, MaxLifetime/MaxEstimateAge are
// ordinary time.Duration values; in a budget JSON file, they are strings
// like "24h" (see MarshalJSON/UnmarshalJSON below) -- time.Duration's own
// JSON encoding is nanoseconds as a bare number, which no one should have
// to write by hand in a budget file.
type Budget struct {
	MaxHourlyUSD   float64
	MaxMonthlyUSD  float64
	MaxLifetime    time.Duration
	MaxEstimateAge time.Duration
	// ApprovedCostlyResourceTypes is an explicit per-budget opt-in list of
	// Terraform resource types that CostlyResourceTypes flags by default.
	// Listing a type here is an explicit, reviewable decision to allow it --
	// not a default.
	ApprovedCostlyResourceTypes []string
}

// budgetJSON mirrors Budget with duration fields as strings, for a budget
// file a human writes by hand (e.g. "24h", "30m").
type budgetJSON struct {
	MaxHourlyUSD                float64  `json:"max_hourly_usd"`
	MaxMonthlyUSD               float64  `json:"max_monthly_usd"`
	MaxLifetime                 string   `json:"max_lifetime"`
	MaxEstimateAge              string   `json:"max_estimate_age"`
	ApprovedCostlyResourceTypes []string `json:"approved_costly_resource_types,omitempty"`
}

func (b Budget) MarshalJSON() ([]byte, error) {
	return json.Marshal(budgetJSON{
		MaxHourlyUSD:                b.MaxHourlyUSD,
		MaxMonthlyUSD:               b.MaxMonthlyUSD,
		MaxLifetime:                 b.MaxLifetime.String(),
		MaxEstimateAge:              b.MaxEstimateAge.String(),
		ApprovedCostlyResourceTypes: b.ApprovedCostlyResourceTypes,
	})
}

func (b *Budget) UnmarshalJSON(data []byte) error {
	var raw budgetJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	lifetime, err := parseOptionalDuration(raw.MaxLifetime)
	if err != nil {
		return fmt.Errorf("max_lifetime: %w", err)
	}
	age, err := parseOptionalDuration(raw.MaxEstimateAge)
	if err != nil {
		return fmt.Errorf("max_estimate_age: %w", err)
	}
	b.MaxHourlyUSD = raw.MaxHourlyUSD
	b.MaxMonthlyUSD = raw.MaxMonthlyUSD
	b.MaxLifetime = lifetime
	b.MaxEstimateAge = age
	b.ApprovedCostlyResourceTypes = raw.ApprovedCostlyResourceTypes
	return nil
}

func parseOptionalDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}

// CostlyResourceFlag records one resource that matched a known-costly
// pattern, and whether the budget explicitly approved that resource type.
type CostlyResourceFlag struct {
	Address            string `json:"address"`
	ResourceType       string `json:"resource_type"`
	Reason             string `json:"reason"`
	ExplicitlyApproved bool   `json:"explicitly_approved"`
}

// Decision is Evaluate's result: whether to approve, and every reason it
// didn't if it's a rejection. Approved is only ever true when Source is
// Live and every other check passed -- see Evaluate's doc comment.
type Decision struct {
	Approved         bool                 `json:"approved"`
	Source           Source               `json:"source"`
	Reasons          []string             `json:"reasons,omitempty"`
	FlaggedResources []CostlyResourceFlag `json:"flagged_resources,omitempty"`
	TotalHourly      *float64             `json:"total_hourly,omitempty"`
	TotalMonthly     *float64             `json:"total_monthly,omitempty"`
}
