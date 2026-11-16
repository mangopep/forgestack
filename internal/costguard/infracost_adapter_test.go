package costguard

import (
	"context"
	"testing"
)

// TestLiveAdapterIsUnavailableWithoutAnAPIKey is the one test that touches
// infracost_adapter.go, and it deliberately proves the opt-in, inert
// behavior: with no INFRACOST_API_KEY set, this must return UNAVAILABLE
// without ever invoking the infracost binary or making a network call.
// This is the only Infracost-adapter behavior this repository's test suite
// or CI ever exercises -- there is no test anywhere that sets a real (or
// even a placeholder) API key and expects a live result.
func TestLiveAdapterIsUnavailableWithoutAnAPIKey(t *testing.T) {
	t.Setenv(InfracostAPIKeyEnvVar, "")
	est, err := LoadLiveEstimateFromInfracost(context.Background(), "unused-path.json")
	if err != nil {
		t.Fatalf("expected no error (fail closed, not fail loud) when the API key is unset, got: %v", err)
	}
	if est.Source != SourceUnavailable {
		t.Fatalf("expected SourceUnavailable with no API key set, got %s", est.Source)
	}
}
