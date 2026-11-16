package costguard

import (
	"testing"
	"time"
)

func mustLoadFixture(t *testing.T, name string) Estimate {
	t.Helper()
	est, err := LoadFixtureEstimate(readTestdata(t, name))
	if err != nil {
		t.Fatalf("LoadFixtureEstimate(%s): %v", name, err)
	}
	return est
}

func defaultBudget() Budget {
	return Budget{
		MaxHourlyUSD:   1.0,
		MaxMonthlyUSD:  10.0,
		MaxLifetime:    24 * time.Hour,
		MaxEstimateAge: 24 * time.Hour,
	}
}

// TestMockEstimateNeverApprovesEvenWellWithinBudget is the single most
// important test in this package: a MOCK estimate that is cheap, complete,
// fresh, and has no flagged resources at all must still never be approved.
// Source, not the numbers, gates approval.
func TestMockEstimateNeverApprovesEvenWellWithinBudget(t *testing.T) {
	est := mustLoadFixture(t, "estimate-cheap.json")
	d := Evaluate(est, defaultBudget(), nil, time.Hour, time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC))
	if d.Approved {
		t.Fatal("a MOCK estimate must never approve a deployment, regardless of how far under budget it is")
	}
	if d.Source != SourceMock {
		t.Errorf("expected Decision.Source to reflect the estimate's actual source, got %s", d.Source)
	}
	found := false
	for _, r := range d.Reasons {
		if r != "" {
			found = true
		}
	}
	if !found {
		t.Error("expected a reason explaining the rejection")
	}
}

func TestUnavailableEstimateNeverApproves(t *testing.T) {
	est := Estimate{Source: SourceUnavailable, GeneratedAt: time.Now()}
	d := Evaluate(est, defaultBudget(), nil, time.Hour, time.Now())
	if d.Approved {
		t.Fatal("an UNAVAILABLE estimate must never approve a deployment")
	}
}

// TestSyntheticLiveEstimateCanApprove proves Evaluate's branch logic itself
// works when every condition is met -- it does NOT exercise any real
// pricing lookup. The Estimate here is hand-constructed in this test with
// Source forced to Live; no adapter, fixture, or network call produced it.
// See docs/architecture/COST-GUARD.md: no code path outside this one test
// line ever constructs a SourceLive value.
func TestSyntheticLiveEstimateCanApprove(t *testing.T) {
	hourly, monthly := 0.01, 5.0
	est := Estimate{
		Source:       SourceLive, // constructed directly in-test; not from any real lookup
		GeneratedAt:  time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC),
		TotalHourly:  &hourly,
		TotalMonthly: &monthly,
		Resources: []ResourceCost{
			{Address: "aws_s3_bucket.lab[0]", ResourceType: "aws_s3_bucket", HourlyCost: &hourly, MonthlyCost: &monthly},
		},
	}
	now := time.Date(2026, 11, 17, 1, 0, 0, 0, time.UTC)
	d := Evaluate(est, defaultBudget(), nil, time.Hour, now)
	if !d.Approved {
		t.Fatalf("expected approval for a fresh, in-budget, complete LIVE estimate, got reasons: %v", d.Reasons)
	}
}

func TestEvaluateRejectsOverHourlyBudget(t *testing.T) {
	hourly := 5.0
	est := Estimate{Source: SourceLive, GeneratedAt: time.Now(), TotalHourly: &hourly}
	budget := defaultBudget()
	d := Evaluate(est, budget, nil, time.Hour, time.Now())
	if d.Approved {
		t.Fatal("expected rejection when hourly cost exceeds budget")
	}
}

func TestEvaluateRejectsOverMonthlyBudget(t *testing.T) {
	est := mustLoadFixture(t, "estimate-over-budget.json")
	est.Source = SourceLive // isolate the budget check from the source check
	d := Evaluate(est, defaultBudget(), nil, time.Hour, time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC))
	if d.Approved {
		t.Fatal("expected rejection when monthly cost exceeds budget")
	}
}

func TestEvaluateRejectsMissingPricing(t *testing.T) {
	est := mustLoadFixture(t, "estimate-missing-pricing.json")
	est.Source = SourceLive
	d := Evaluate(est, defaultBudget(), nil, time.Hour, time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC))
	if d.Approved {
		t.Fatal("expected rejection when a resource has no pricing data at all")
	}
}

func TestEvaluateRejectsPartialEstimate(t *testing.T) {
	est := mustLoadFixture(t, "estimate-partial.json")
	est.Source = SourceLive
	d := Evaluate(est, defaultBudget(), nil, time.Hour, time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC))
	if d.Approved {
		t.Fatal("expected rejection for a partial estimate (one priced resource, one unpriced) even though totals look cheap")
	}
}

func TestEvaluateRejectsStaleEstimate(t *testing.T) {
	est := mustLoadFixture(t, "estimate-stale.json")
	est.Source = SourceLive
	d := Evaluate(est, defaultBudget(), nil, time.Hour, time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC))
	if d.Approved {
		t.Fatal("expected rejection for an estimate generated in 2020")
	}
}

func TestEvaluateRejectsUnapprovedCostlyResource(t *testing.T) {
	resources, err := ScanPlan(readTestdata(t, "plan-nat-gateway.json"))
	if err != nil {
		t.Fatal(err)
	}
	budget := defaultBudget()
	flags := FlagCostlyResources(resources, budget)
	hourly := 0.045
	est := Estimate{Source: SourceLive, GeneratedAt: time.Now(), TotalHourly: &hourly}
	d := Evaluate(est, budget, flags, time.Hour, time.Now())
	if d.Approved {
		t.Fatal("expected rejection for an unapproved NAT gateway even with a cheap total estimate")
	}
}

func TestEvaluateApprovesExplicitlyApprovedCostlyResource(t *testing.T) {
	resources, err := ScanPlan(readTestdata(t, "plan-nat-gateway.json"))
	if err != nil {
		t.Fatal(err)
	}
	budget := defaultBudget()
	budget.ApprovedCostlyResourceTypes = []string{"aws_nat_gateway"}
	flags := FlagCostlyResources(resources, budget)
	hourly, monthly := 0.045, 5.0
	est := Estimate{
		Source: SourceLive, GeneratedAt: time.Now(),
		TotalHourly: &hourly, TotalMonthly: &monthly,
		Resources: []ResourceCost{{Address: "aws_nat_gateway.example[0]", ResourceType: "aws_nat_gateway", HourlyCost: &hourly, MonthlyCost: &monthly}},
	}
	d := Evaluate(est, budget, flags, time.Hour, time.Now())
	if !d.Approved {
		t.Fatalf("expected approval once the NAT gateway type is explicitly approved, got reasons: %v", d.Reasons)
	}
}

func TestEvaluateRejectsTTLExceedingMaxLifetime(t *testing.T) {
	hourly, monthly := 0.0, 0.02
	est := Estimate{Source: SourceLive, GeneratedAt: time.Now(), TotalHourly: &hourly, TotalMonthly: &monthly}
	d := Evaluate(est, defaultBudget(), nil, 48*time.Hour, time.Now())
	if d.Approved {
		t.Fatal("expected rejection when the requested TTL exceeds the budget's max lifetime")
	}
}

func TestEvaluateReportsEverySimultaneousReason(t *testing.T) {
	// A MOCK, stale, over-budget estimate should report all three reasons at
	// once, not stop at the first -- an operator fixing one issue should
	// immediately see the next one, not re-run to discover it.
	hourly := 99.0
	est := Estimate{Source: SourceMock, GeneratedAt: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), TotalHourly: &hourly}
	d := Evaluate(est, defaultBudget(), nil, time.Hour, time.Now())
	if d.Approved {
		t.Fatal("expected rejection")
	}
	if len(d.Reasons) < 3 {
		t.Errorf("expected at least 3 independent reasons (mock source, stale, over hourly budget), got %d: %v", len(d.Reasons), d.Reasons)
	}
}
