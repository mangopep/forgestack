package costguard

import (
	"fmt"
	"time"
)

// Evaluate decides whether a cost estimate, under a budget, may approve a
// real deployment. It fails closed by construction: Approved starts false
// and is only ever set true at the very end, and only when every check
// below passed. There is no code path that returns Approved=true for a
// non-Live Source -- that rule is not a late check that could be skipped,
// it is the first thing this function does.
//
//   - Source must be Live. A MOCK estimate (however far under budget) or an
//     UNAVAILABLE one never approves anything -- this is deliberate per
//     ForgeStack's cost-governance policy: fixtures and failed lookups must
//     never be mistaken for real pricing.
//   - The estimate must not be stale (older than budget.MaxEstimateAge).
//   - Every resource must have usable pricing; any resource with
//     Incomplete() pricing fails the whole estimate closed rather than
//     silently treating it as free.
//   - Every flagged costly resource must be explicitly approved in the
//     budget's ApprovedCostlyResourceTypes.
//   - Total hourly/monthly cost (when present) must not exceed the budget.
//   - The requested playground lifetime must not exceed budget.MaxLifetime.
func Evaluate(est Estimate, budget Budget, flags []CostlyResourceFlag, requestedTTL time.Duration, now time.Time) Decision {
	d := Decision{
		Source:           est.Source,
		FlaggedResources: flags,
		TotalHourly:      est.TotalHourly,
		TotalMonthly:     est.TotalMonthly,
	}

	d.Reasons = append(d.Reasons, sourceReasons(est)...)
	d.Reasons = append(d.Reasons, stalenessReasons(est, budget, now)...)
	d.Reasons = append(d.Reasons, pricingCompletenessReasons(est)...)
	d.Reasons = append(d.Reasons, unapprovedCostlyResourceReasons(flags)...)
	d.Reasons = append(d.Reasons, budgetReasons(est, budget)...)
	d.Reasons = append(d.Reasons, lifetimeReasons(budget, requestedTTL)...)

	d.Approved = est.Source == SourceLive && len(d.Reasons) == 0
	return d
}

func sourceReasons(est Estimate) []string {
	if est.Source == SourceLive {
		return nil
	}
	return []string{fmt.Sprintf(
		"estimate source is %s, not LIVE -- a mock or unavailable estimate can never approve a real deployment", est.Source)}
}

func stalenessReasons(est Estimate, budget Budget, now time.Time) []string {
	age := now.Sub(est.GeneratedAt)
	if budget.MaxEstimateAge <= 0 || age <= budget.MaxEstimateAge {
		return nil
	}
	return []string{fmt.Sprintf(
		"estimate is %s old, exceeds max estimate age %s -- pricing may be stale", age, budget.MaxEstimateAge)}
}

func pricingCompletenessReasons(est Estimate) []string {
	var reasons []string
	for _, r := range est.Resources {
		if !r.Incomplete() {
			continue
		}
		note := r.UnsupportedNote
		if note == "" {
			note = "no reason recorded"
		}
		reasons = append(reasons, fmt.Sprintf(
			"resource %s (%s) has no pricing data: %s", r.Address, r.ResourceType, note))
	}
	return reasons
}

func unapprovedCostlyResourceReasons(flags []CostlyResourceFlag) []string {
	var reasons []string
	for _, f := range flags {
		if f.ExplicitlyApproved {
			continue
		}
		reasons = append(reasons, fmt.Sprintf(
			"resource %s is a costly type (%s: %s) and is not in the budget's approved_costly_resource_types",
			f.Address, f.ResourceType, f.Reason))
	}
	return reasons
}

func budgetReasons(est Estimate, budget Budget) []string {
	var reasons []string
	if est.TotalHourly != nil && *est.TotalHourly > budget.MaxHourlyUSD {
		reasons = append(reasons, fmt.Sprintf(
			"estimated hourly cost $%.4f exceeds budget max_hourly_usd $%.4f", *est.TotalHourly, budget.MaxHourlyUSD))
	}
	if est.TotalMonthly != nil && *est.TotalMonthly > budget.MaxMonthlyUSD {
		reasons = append(reasons, fmt.Sprintf(
			"estimated monthly cost $%.2f exceeds budget max_monthly_usd $%.2f", *est.TotalMonthly, budget.MaxMonthlyUSD))
	}
	return reasons
}

func lifetimeReasons(budget Budget, requestedTTL time.Duration) []string {
	if budget.MaxLifetime <= 0 || requestedTTL <= budget.MaxLifetime {
		return nil
	}
	return []string{fmt.Sprintf(
		"requested lifetime %s exceeds budget max_lifetime %s", requestedTTL, budget.MaxLifetime)}
}
