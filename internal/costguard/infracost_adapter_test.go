package costguard

import (
	"context"
	"testing"
)

// TestLiveAdapterRefusesADirectoryRegardlessOfInfracostInstallation is a
// regression test for a confirmed issue found in adversarial review: the
// directory guard must reject a directory path before ever reaching
// exec.LookPath, so this test's outcome does not depend on whether the
// infracost binary happens to be installed on the machine running it (it
// is on this development machine; it should not be on a bare CI runner,
// and the guard must still be exercised either way).
func TestLiveAdapterRefusesADirectoryRegardlessOfInfracostInstallation(t *testing.T) {
	t.Setenv(InfracostAPIKeyEnvVar, "placeholder-not-a-real-key")
	dir := t.TempDir()
	est, err := LoadLiveEstimateFromInfracost(context.Background(), dir)
	if err == nil {
		t.Fatal("expected an error when planJSONPath is a directory")
	}
	if est.Source != SourceUnavailable {
		t.Errorf("expected SourceUnavailable for a rejected directory path, got %s", est.Source)
	}
}

func TestLiveAdapterFailsClosedOnAMissingPath(t *testing.T) {
	t.Setenv(InfracostAPIKeyEnvVar, "placeholder-not-a-real-key")
	est, err := LoadLiveEstimateFromInfracost(context.Background(), "/nonexistent/path/does-not-exist.json")
	if err == nil {
		t.Fatal("expected an error for a plan path that doesn't exist")
	}
	if est.Source != SourceUnavailable {
		t.Errorf("expected SourceUnavailable, got %s", est.Source)
	}
}

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
