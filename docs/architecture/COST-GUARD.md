---
title: Cost Guard
---

# Cost Guard

A local, vendor-independent cost-policy foundation (`internal/costguard`, `cmd/costguard`).
**No AWS credentials, AWS API call, cloud provisioning, or paid pricing service were used
anywhere in building or testing this** — every claim below states exactly how it was
verified, per this repo's evidence-label convention.

## What this actually is

`internal/costguard` answers one question: *given a cost estimate and a budget, should a
real deployment be approved?* It has no dependency on any pricing vendor to answer that
question — the core `Evaluate` function works from a plain `Estimate` value, however that
value was produced. **An `Estimate`'s `Source` field, not its numbers, gates approval**:

- `LIVE` — a real pricing lookup. Nothing in this repository's code, tests, or CI ever
  produces a `LIVE` estimate. The only way one exists is a caller explicitly running
  `costguard evaluate --live` with their own `INFRACOST_API_KEY` already set.
- `MOCK` — a committed, hand-written JSON fixture (`internal/costguard/testdata/*.json`).
  Used everywhere in this package's own tests and would be used in CI. **`Evaluate` refuses
  to approve a `MOCK` estimate no matter how cheap it claims to be** — this is enforced in
  code (`TestMockEstimateNeverApprovesEvenWellWithinBudget`), not left as a convention to
  remember.
- `UNAVAILABLE` — no pricing could be obtained. Also never approved.

This separation is why Cost Guard needed no API key, no signup, and no installed pricing
tool to build or test: the policy engine and the plan-risk scanner below are complete,
tested, and useful with zero pricing data at all.

## What's implemented and locally verified (evidence, not claims)

- **`ScanPlan`** parses Terraform's own `terraform show -json <planfile>` schema (not
  anything vendor-specific) and lists every resource that will exist after apply.
  *Confirmed from test*: `TestScanPlanExcludesPureDeletes`, `TestScanPlanRejectsMalformedJSON`
  (malformed plan JSON is a hard error, never a silent empty result).
- **`FlagCostlyResources`** flags a short, hand-curated list of resource *types* known to
  carry meaningful cost regardless of usage (NAT gateways, EKS clusters/node groups, load
  balancers, RDS/Aurora/ElastiCache/Redshift instances, EBS volumes, EFS) plus a pattern over
  large/accelerated EC2 instance-type names (`4xlarge` and up, `metal`, GPU/accelerator
  families). This is a classification list, not a pricing catalog — it names no dollar
  figure, only "this needs explicit sign-off." *Confirmed from test*:
  `TestScanPlanFlagsNATGateway`, `TestScanPlanFlagsEKSClusterAndNodeGroup`,
  `TestScanPlanFlagsLargeInstanceType`, `TestScanPlanDoesNotFlagSmallInstanceType`.
- **`Evaluate`** fails closed on every one of the required cases: MOCK/UNAVAILABLE source,
  stale estimate, incomplete/partial pricing, an unapproved costly resource, over-hourly-
  budget, over-monthly-budget, and a requested playground lifetime exceeding the budget's
  max. It reports *every* simultaneous reason, not just the first. *Confirmed from test*: see
  `evaluate_test.go` — 10 tests, one per failure mode plus one proving multiple reasons
  surface together (`TestEvaluateReportsEverySimultaneousReason`) and one proving the
  approval branch itself works when every condition is genuinely met
  (`TestSyntheticLiveEstimateCanApprove` — constructs a `Source: Live` value directly in the
  test; this does **not** exercise any real pricing lookup, and the test's own comment says
  so).
- **`cmd/costguard`**: `scan` (plan → flagged resources, no pricing involved) and `evaluate`
  (estimate + budget + optional plan → a `Decision`, printed as JSON, with the process exit
  code itself nonzero on any non-approval — a CI step can gate on exit code alone).
  *Confirmed from test*: `scripts/test-cost-guard.sh`, run directly in this session, exercises
  the real built CLI binary (via `go run`) against fixtures only, and asserts both the JSON
  content and the exit code.
- **Budget JSON**: human-readable duration strings (`"24h"`, not raw nanoseconds — Go's
  `time.Duration` JSON-encodes as a bare integer by default, which nobody should have to
  write by hand in a budget file). Custom `MarshalJSON`/`UnmarshalJSON` on `Budget`.
  *Confirmed from test*: `TestBudgetJSONRoundTripsHumanReadableDurations`,
  `TestBudgetJSONRejectsMalformedDuration`.

## What's an opt-in, unexercised adapter (Infracost)

`internal/costguard/infracost_adapter.go`'s `LoadLiveEstimateFromInfracost` is the one place
in this package that *could* call an external pricing vendor, and it was built — and stayed —
strictly opt-in, per explicit correction during this work (see "What changed mid-build"
below):

- It is **never called** by any other code in this package, by `cmd/costguard`'s default
  behavior, by any test in this repository, or by any CI workflow. The only way it runs is a
  human typing `--live` on the command line.
- With no `INFRACOST_API_KEY` set, it returns `UNAVAILABLE` **without invoking the
  `infracost` binary at all** — confirmed by the one test that touches this file,
  `TestLiveAdapterIsUnavailableWithoutAnAPIKey`, which asserts this without a real binary call
  succeeding or failing either way.
- It operates on an already-generated Terraform plan JSON file, never a live directory with
  a configured provider block — see "A real hazard found and avoided" below for exactly why
  that distinction matters.
- **No Infracost account was created, no API key was ever obtained or used, and no live
  pricing lookup was ever attempted to completion in building this.** The `infracost` CLI
  binary itself was installed locally via Homebrew (free, open-source, Apache-2.0 licensed —
  distinct from Infracost's paid Cloud product) to test the fail-closed "no key" path above;
  it was never registered, never given a key, and no network call to Infracost's pricing API
  ever completed. It remains installed and unused; removing it (`brew uninstall infracost`)
  is a one-line, zero-risk cleanup step if you'd rather it not be there.

## A real hazard found and avoided

While exploring how Infracost actually works, `infracost breakdown --path
infrastructure/terraform/aws-s3-sandbox` (pointed at a *directory*, not a plan file) was run
with a placeholder/invalid API key, to see what error it produced. **It did not fail
quickly — it hung.** Infracost's default behavior for a Terraform directory is to run
`terraform init`/`terraform plan` itself, and this sandbox module configures the
`hashicorp/aws` provider; a `plan` against a real provider block risks the provider's own
credential-chain resolution (STS calls, IMDS probing) even with zero resources actually
targeted for creation. The command was killed before it could complete, and a direct test of
`terraform plan` itself (to check whether the AWS provider needs credentials when
`enable_resources=false` means zero resources are planned) was separately blocked by this
environment's own safety classifier as an unauthorized action — correctly, since it's
exactly the kind of command that could silently attempt real AWS API calls.

**This is why `LoadLiveEstimateFromInfracost` requires a pre-generated plan JSON file, and
will never be extended to accept a live Terraform directory with a configured provider
block.** Pointing Infracost at a plan file Terraform already produced (`terraform plan
-out=plan.bin && terraform show -json plan.bin > plan.json`, run by whoever has real
credentials and real authorization, not by this tool) is the documented, safe Infracost usage
pattern that avoids this entirely — Infracost then only parses JSON and calls its own pricing
API, no provider, no credentials, no AWS.

## Distinguishing pre-deployment estimates from actual billing

An `Estimate` from this package — MOCK or (hypothetically) LIVE — is a **pre-deployment
prediction**, produced before anything is created. Real AWS billing data (Cost Explorer, the
Cost and Usage Report, AWS Budgets' actual-spend tracking) is **delayed, metered, and
authoritative** — it reflects what actually happened, hours to a day later, not what was
predicted. Nothing in this package treats an `Estimate` as if it were billing data, and
nothing here claims to reconcile the two; that reconciliation is explicitly deferred (see
below) and would need real AWS Cost Explorer access to build and verify.

## AWS Budgets / cost allocation tags / Cost Explorer — designed, not built

Per this work's explicit scope, these are **future adapter points, not implemented, and no
AWS API was contacted to even check their shape**:

- **AWS Budgets** would be the natural place to enforce a *monthly account-level* ceiling
  independent of any single playground's own budget — a defense-in-depth backstop, not a
  replacement for this package's pre-deployment gate.
- **Cost allocation tags**: `infrastructure/terraform/README.md` already requires every
  future cloud resource to carry an environment ID, owner, and mode tag (from F6's playground
  work). Cost Explorer's own cost-by-tag views would key off exactly those tags — no new
  tagging scheme needed, just a consumer of the existing one.
- **Cost Explorer** would be the source of truth for reconciling a `LIVE` pre-deployment
  estimate against what a playground actually cost once real usage-based charges (data
  transfer, request volume, etc. — see `UsageBased` on `ResourceCost`) are known.

None of these were designed in more detail than this, because doing so without real AWS
access to verify against would be guessing, not design.

## Expired-playground cleanup, orphan detection, independent verification — design only

This reuses the pattern `internal/playground` already implements and tests for *local*
resources (own environment ID, owner, TTL, `CLEANUP INCOMPLETE`/`CLEANUP UNVERIFIED` states
rather than inferring success) — see `internal/playground/lifecycle_test.go`. A Terraform-
aware version of the same controller, reconciling Terraform state against real AWS resource
inventory (to catch an orphan — something AWS billed for that Terraform's own state lost
track of) is **not implemented**: it needs a real cloud account to write against and verify,
which this task's restrictions forbid. Recorded as a named gap (also tracked in
`docs/adr/0007-terraform-module-versioning-and-change-governance.md`'s item 13), not assumed
solved by analogy to the local controller.

## Tests (what was actually run)

| Case required | Test |
|---|---|
| Rejected over-budget plan | `TestEvaluateRejectsOverHourlyBudget`, `TestEvaluateRejectsOverMonthlyBudget` |
| Missing estimate / pricing | `TestEvaluateRejectsMissingPricing` |
| Partial estimate | `TestEvaluateRejectsPartialEstimate` |
| Stale pricing | `TestEvaluateRejectsStaleEstimate` |
| Incorrect/unapproved ownership of a costly resource type | `TestEvaluateRejectsUnapprovedCostlyResource`, `TestEvaluateApprovesExplicitlyApprovedCostlyResource` |
| Unsupported configuration (malformed plan JSON) | `TestScanPlanRejectsMalformedJSON` |
| TTL exceeding budget | `TestEvaluateRejectsTTLExceedingMaxLifetime` |
| Live adapter with no key | `TestLiveAdapterIsUnavailableWithoutAnAPIKey` |
| Mock can never approve | `TestMockEstimateNeverApprovesEvenWellWithinBudget` |
| End-to-end CLI, fixtures only | `scripts/test-cost-guard.sh` |

22 unit tests in `internal/costguard`, all passing; `golangci-lint` 0 issues; `gofmt`/`go
vet` clean.

## Operating assumptions and remaining risks

- **AWS credits are never assumed to make a resource free.** Nothing in this package checks
  for or references credits; a `LIVE` estimate's numbers (once that path is ever actually
  used, by a human with their own key) are the real pricing-API numbers, full price.
- **This does not estimate data-transfer, request-volume, or other genuinely usage-metered
  charges precisely** — `ResourceCost.UsageBased` flags when a number is a floor, not a
  ceiling, but does not attempt to predict actual usage.
- **The costly-resource-type list is hand-curated and will go stale** as AWS ships new
  expensive resource types; it is a known, bounded, documented list, not a claim of
  completeness.
- **No cloud-side verification exists for any of this.** Every number, every test, and every
  claim above is local-only. The real test of a `LIVE` estimate — accuracy, latency, API
  reliability — can only happen once a human explicitly opts in with their own Infracost
  account, which is deliberately outside this work's scope.

## How to avoid unintended charges

1. Never run `costguard evaluate --live` without having deliberately decided to; it does
   nothing without your own `INFRACOST_API_KEY`.
2. Never treat a `MOCK` decision's `"approved": false` as a bug to work around — it's the
   policy working as designed. There is no flag or configuration that makes a `MOCK`
   estimate approve a deployment.
3. A `LIVE` approval from this tool is still only a pre-deployment estimate, not a guarantee
   of final cost — reconcile against real billing once anything is actually deployed.
4. Keep `approved_costly_resource_types` in any real budget file as short and deliberate as
   possible; every entry is an explicit decision to skip this tool's own warning for that
   resource type.

## Next approval gate

Real Infracost usage (an actual `--live` run with a real key), AWS Budgets/Cost
Explorer/cost-allocation-tag integration, and a Terraform-aware cleanup/orphan-reconciliation
controller each require either a human's own pricing-vendor account or real AWS access —
each is its own separately-approved increment, not an automatic next step of this one.
