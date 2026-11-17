package costguard

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBudgetJSONRoundTripsHumanReadableDurations(t *testing.T) {
	const budgetJSONText = `{
		"max_hourly_usd": 0.5,
		"max_monthly_usd": 20,
		"max_lifetime": "24h",
		"max_estimate_age": "30m",
		"approved_costly_resource_types": ["aws_nat_gateway"]
	}`
	var b Budget
	if err := json.Unmarshal([]byte(budgetJSONText), &b); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if b.MaxLifetime != 24*time.Hour {
		t.Errorf("MaxLifetime = %v, want 24h", b.MaxLifetime)
	}
	if b.MaxEstimateAge != 30*time.Minute {
		t.Errorf("MaxEstimateAge = %v, want 30m", b.MaxEstimateAge)
	}
	if b.MaxHourlyUSD != 0.5 || b.MaxMonthlyUSD != 20 {
		t.Errorf("unexpected budget amounts: %+v", b)
	}

	out, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var roundTripped Budget
	if err := json.Unmarshal(out, &roundTripped); err != nil {
		t.Fatalf("Unmarshal round-tripped JSON: %v", err)
	}
	if roundTripped.MaxLifetime != b.MaxLifetime || roundTripped.MaxEstimateAge != b.MaxEstimateAge ||
		roundTripped.MaxHourlyUSD != b.MaxHourlyUSD || roundTripped.MaxMonthlyUSD != b.MaxMonthlyUSD {
		t.Errorf("round trip mismatch: got %+v, want %+v", roundTripped, b)
	}
}

func TestBudgetJSONRejectsMalformedDuration(t *testing.T) {
	var b Budget
	err := json.Unmarshal([]byte(`{"max_lifetime": "not-a-duration"}`), &b)
	if err == nil {
		t.Fatal("expected an error for an unparsable max_lifetime")
	}
}
