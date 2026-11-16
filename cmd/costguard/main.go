// Command costguard is a local, vendor-independent cost-policy CLI. By
// default it never contacts any pricing vendor: `costguard evaluate` reads
// a committed fixture estimate (always stamped MOCK) unless --live is
// passed explicitly, and a MOCK or unavailable estimate can never approve
// a deployment (see internal/costguard.Evaluate). `costguard scan` reports
// known-costly resource types from a Terraform plan JSON with no pricing
// lookup at all.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/mangopep/forgestack/internal/costguard"
)

func newFlagSet(name string) *flag.FlagSet {
	return flag.NewFlagSet(name, flag.ContinueOnError)
}

// liveEstimateTimeout bounds how long `evaluate --live` will wait for the
// infracost subprocess before giving up. Fixed, not a flag, to keep this
// CLI's surface minimal -- raise it here if a real Infracost lookup
// genuinely needs longer.
const liveEstimateTimeout = 60 * time.Second

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "scan":
		err = runScan(os.Args[2:])
	case "evaluate":
		err = runEvaluate(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "costguard:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  costguard scan --plan <plan.json> [--budget <budget.json>]
  costguard evaluate --estimate <path> --budget <budget.json> [--plan <plan.json>] --ttl <duration> [--live]

"scan" is informational only: it always exits 0, even when it flags a
costly resource. It never approves or blocks anything. "evaluate" is the
one gate -- it exits nonzero on any non-approval, and that exit code is
designed to be the thing a CI step actually checks before proceeding.
Nothing in this repository currently wires "evaluate"'s exit code into an
actual deployment step (no terraform apply anywhere calls this first) --
see docs/architecture/COST-GUARD.md's "Next approval gate."

--estimate's path means two different things depending on --live, which is
deliberate but easy to misuse, so read this carefully:
  - without --live (the default): --estimate is a hand-written MOCK fixture
    JSON file (internal/costguard.LoadFixtureEstimate). No pricing vendor is
    ever contacted.
  - with --live: --estimate is instead a pre-generated Terraform plan JSON
    file (terraform show -json <planfile> > plan.json), fed to the real
    infracost CLI. It must be a FILE, never a directory -- passing a
    directory is rejected outright, because infracost would otherwise run
    terraform init/plan itself against it. --live requires
    INFRACOST_API_KEY already set in your own environment and the infracost
    CLI on PATH; see docs/architecture/COST-GUARD.md.

--plan (separate from --estimate) is always a Terraform plan JSON file, used
to additionally detect large/accelerated instance *sizes* that an estimate's
own resource list can't express. Costly resource *types* (NAT gateways, EKS,
load balancers, RDS, ...) are always checked directly from the estimate's
own resources, with or without --plan -- there is no way to skip that check
by omitting --plan.

A MOCK or UNAVAILABLE estimate can never approve a deployment, regardless
of --live or how cheap it looks.`)
}

func runScan(args []string) error {
	fs := newFlagSet("scan")
	planPath := fs.String("plan", "", "path to a Terraform plan JSON file (terraform show -json <planfile>)")
	budgetPath := fs.String("budget", "", "optional path to a budget JSON file, for approved_costly_resource_types")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *planPath == "" {
		return fmt.Errorf("--plan is required")
	}
	planData, err := os.ReadFile(*planPath)
	if err != nil {
		return fmt.Errorf("read plan: %w", err)
	}
	resources, err := costguard.ScanPlan(planData)
	if err != nil {
		return fmt.Errorf("scan plan: %w", err)
	}
	budget, err := loadBudget(*budgetPath)
	if err != nil {
		return err
	}
	flags := costguard.FlagCostlyResources(resources, budget)
	return printJSON(map[string]any{"resources": len(resources), "flagged": flags})
}

func runEvaluate(args []string) error {
	fs := newFlagSet("evaluate")
	estimatePath := fs.String("estimate", "", "path to an estimate JSON file (loaded as MOCK unless --live)")
	budgetPath := fs.String("budget", "", "path to a budget JSON file")
	planPath := fs.String("plan", "", "optional path to a Terraform plan JSON file, to detect costly resource types")
	ttl := fs.Duration("ttl", 0, "requested playground lifetime, e.g. 4h")
	live := fs.Bool("live", false, "opt in to a real pricing lookup via the Infracost adapter (requires your own INFRACOST_API_KEY; never enabled by default)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *estimatePath == "" || *budgetPath == "" {
		return fmt.Errorf("--estimate and --budget are required")
	}

	budget, err := loadBudget(*budgetPath)
	if err != nil {
		return err
	}

	var est costguard.Estimate
	if *live {
		// Bounded, not context.Background(): an unbounded context would let
		// a hung infracost invocation (or any subprocess) block this
		// process forever -- exactly the hazard documented in
		// docs/architecture/COST-GUARD.md, where a prior exploratory call
		// hung rather than failing fast.
		ctx, cancel := context.WithTimeout(context.Background(), liveEstimateTimeout)
		defer cancel()
		est, err = costguard.LoadLiveEstimateFromInfracost(ctx, *estimatePath)
		if err != nil {
			return fmt.Errorf("live estimate: %w", err)
		}
	} else {
		data, readErr := os.ReadFile(*estimatePath)
		if readErr != nil {
			return fmt.Errorf("read estimate: %w", readErr)
		}
		est, err = costguard.LoadFixtureEstimate(data)
		if err != nil {
			return fmt.Errorf("load fixture estimate: %w", err)
		}
	}

	var flags []costguard.CostlyResourceFlag
	if *planPath != "" {
		planData, readErr := os.ReadFile(*planPath)
		if readErr != nil {
			return fmt.Errorf("read plan: %w", readErr)
		}
		resources, scanErr := costguard.ScanPlan(planData)
		if scanErr != nil {
			return fmt.Errorf("scan plan: %w", scanErr)
		}
		flags = costguard.FlagCostlyResources(resources, budget)
	}

	decision := costguard.Evaluate(est, budget, flags, *ttl, time.Now().UTC())
	if err := printJSON(decision); err != nil {
		return err
	}
	if !decision.Approved {
		os.Exit(1)
	}
	return nil
}

func loadBudget(path string) (costguard.Budget, error) {
	if path == "" {
		return costguard.Budget{}, nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is this CLI's own operator-supplied flag, not untrusted input
	if err != nil {
		return costguard.Budget{}, fmt.Errorf("read budget: %w", err)
	}
	var b costguard.Budget
	if err := json.Unmarshal(data, &b); err != nil {
		return costguard.Budget{}, fmt.Errorf("parse budget: %w", err)
	}
	return b, nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
