package costguard

import (
	"encoding/json"
	"fmt"
)

// LoadFixtureEstimate parses a hand-written, committed JSON fixture into an
// Estimate and stamps its Source as MOCK regardless of what the fixture
// file itself says -- a fixture is never allowed to claim to be LIVE, even
// by a typo. This is the only estimate source this package's own tests and
// any CI workflow ever use: no network call, no pricing vendor, no API key.
func LoadFixtureEstimate(data []byte) (Estimate, error) {
	var est Estimate
	if err := json.Unmarshal(data, &est); err != nil {
		return Estimate{}, fmt.Errorf("parse fixture estimate: %w", err)
	}
	est.Source = SourceMock
	if est.ToolVersion == "" {
		est.ToolVersion = "fixture"
	}
	return est, nil
}
