package costguard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// InfracostAPIKeyEnvVar is the standard environment variable Infracost's
// own CLI reads for its pricing API key. This package never reads it except
// inside LoadLiveEstimateFromInfracost below, and only when that function
// is explicitly called -- nothing in this repository's tests, CI, or any
// other package calls it. costguard works completely without Infracost
// ever being installed; this file exists only so a user who wants real
// pricing has one, clearly-isolated place to opt into it.
const InfracostAPIKeyEnvVar = "INFRACOST_API_KEY" //nolint:gosec // G101: this is the env var NAME, not a credential value

// LoadLiveEstimateFromInfracost is the one place in this package that would
// shell out to an external pricing vendor, and it is opt-in only:
//
//   - It is never called by this package's own code, by cmd/costguard's
//     default behavior, by any test in this repository, or by any CI
//     workflow. The only way it runs is a caller explicitly invoking it --
//     e.g. `costguard estimate --live`, a flag that does nothing unless
//     typed on the command line.
//   - It requires the caller's own INFRACOST_API_KEY already set in the
//     environment. If it's unset, this returns an UNAVAILABLE estimate
//     immediately -- it does not invoke the infracost binary at all, so
//     there is no accidental network call or credential prompt just from
//     this function existing.
//   - It operates on a plan JSON file the caller already produced by
//     running `terraform plan -out=plan.bin && terraform show -json
//     plan.bin > plan.json` themselves. This package never runs
//     `terraform plan` -- doing so against a real provider block risks
//     exactly the credential-chain resolution and possible API calls this
//     project's cost-governance work must never trigger on its own (this
//     was verified directly: `infracost breakdown --path <dir>` against
//     this repo's AWS-provider sandbox module hung attempting exactly that,
//     and was killed rather than allowed to continue -- see
//     docs/architecture/COST-GUARD.md). Pointing infracost at an
//     already-generated plan *file* (not a directory with a live provider
//     block) is the safe, documented Infracost usage pattern that avoids
//     this entirely.
//
// Not exercised anywhere in this codebase: no API key has ever been
// created or used to call this function in this repository's history.
func LoadLiveEstimateFromInfracost(ctx context.Context, planJSONPath string) (Estimate, error) {
	if os.Getenv(InfracostAPIKeyEnvVar) == "" {
		return Estimate{
			Source:      SourceUnavailable,
			GeneratedAt: time.Now().UTC(),
			ToolVersion: "infracost (not invoked: " + InfracostAPIKeyEnvVar + " is unset)",
		}, nil
	}
	// Refuse a directory outright, rather than trusting the caller to only
	// ever pass a file: infracost's default behavior for a directory is to
	// run terraform init/plan itself, which is exactly the AWS-credential-
	// resolution hazard this adapter exists to avoid (see the doc comment
	// above and docs/architecture/COST-GUARD.md -- this was found by
	// actually hitting it). This check turns a possible silent hang/AWS
	// call into an immediate, clear rejection. Deliberately checked before
	// exec.LookPath below: validating our own input shouldn't depend on
	// whether the infracost binary happens to be installed on this
	// machine, and a test exercising this guard must behave identically
	// whether or not it is.
	info, statErr := os.Stat(planJSONPath)
	if statErr != nil {
		return Estimate{Source: SourceUnavailable, GeneratedAt: time.Now().UTC()},
			fmt.Errorf("stat plan JSON path %q: %w", planJSONPath, statErr)
	}
	if info.IsDir() {
		return Estimate{Source: SourceUnavailable, GeneratedAt: time.Now().UTC()},
			fmt.Errorf("refusing to run infracost against a directory (%s): infracost would run terraform init/plan itself against it, risking real provider credential resolution -- pass a pre-generated plan JSON file instead (terraform show -json <planfile> > plan.json)", planJSONPath)
	}

	if _, err := exec.LookPath("infracost"); err != nil {
		return Estimate{
			Source:      SourceUnavailable,
			GeneratedAt: time.Now().UTC(),
			ToolVersion: "infracost (not invoked: binary not found on PATH)",
		}, nil
	}

	//nolint:gosec // G204: args are passed directly to exec, never through a shell, so there is no injection vector; planJSONPath is an operator-supplied CLI argument, not external input
	cmd := exec.CommandContext(ctx, "infracost", "breakdown", "--path", planJSONPath, "--format", "json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Estimate{
			Source:      SourceUnavailable,
			GeneratedAt: time.Now().UTC(),
			ToolVersion: "infracost (invocation failed)",
		}, fmt.Errorf("infracost breakdown failed: %w: %s", err, truncate(stderr.String(), 2000))
	}

	est, err := parseInfracostJSON(stdout.Bytes())
	if err != nil {
		return Estimate{Source: SourceUnavailable, GeneratedAt: time.Now().UTC()}, err
	}
	est.Source = SourceLive
	est.GeneratedAt = time.Now().UTC()
	return est, nil
}

// infracostBreakdown is the minimal subset of Infracost's own `--format
// json` output this adapter reads, adapted into this package's
// vendor-independent Estimate shape.
type infracostBreakdown struct {
	Projects []struct {
		Breakdown struct {
			Resources []struct {
				Name         string  `json:"name"`
				ResourceType string  `json:"resourceType"`
				HourlyCost   *string `json:"hourlyCost"`
				MonthlyCost  *string `json:"monthlyCost"`
			} `json:"resources"`
			TotalHourlyCost  *string `json:"totalHourlyCost"`
			TotalMonthlyCost *string `json:"totalMonthlyCost"`
		} `json:"breakdown"`
	} `json:"projects"`
}

func parseInfracostJSON(data []byte) (Estimate, error) {
	var b infracostBreakdown
	if err := json.Unmarshal(data, &b); err != nil {
		return Estimate{}, fmt.Errorf("parse infracost output: %w", err)
	}
	var est Estimate
	est.Currency = "USD"
	for _, p := range b.Projects {
		for _, r := range p.Breakdown.Resources {
			rc := ResourceCost{Address: r.Name, ResourceType: r.ResourceType}
			rc.HourlyCost = parseCostString(r.HourlyCost)
			rc.MonthlyCost = parseCostString(r.MonthlyCost)
			if rc.Incomplete() {
				rc.UnsupportedNote = "infracost returned no price for this resource"
			}
			est.Resources = append(est.Resources, rc)
		}
		if h := parseCostString(p.Breakdown.TotalHourlyCost); h != nil {
			est.TotalHourly = h
		}
		if m := parseCostString(p.Breakdown.TotalMonthlyCost); m != nil {
			est.TotalMonthly = m
		}
	}
	return est, nil
}

// truncate bounds how much of a subprocess's own stderr this adapter will
// embed in an error message -- defense in depth against an unexpectedly
// verbose (or, in principle, sensitive) error from a binary this code does
// not control the output of.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "... (truncated)"
}

func parseCostString(s *string) *float64 {
	if s == nil || *s == "" {
		return nil
	}
	var v float64
	if _, err := fmt.Sscanf(*s, "%f", &v); err != nil {
		return nil
	}
	return &v
}
