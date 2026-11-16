# ADR 0007: Terraform module versioning, governance, and change safety

- Status: Partially implemented locally on the F6 feature branch. Items below are each
  labeled with their actual evidence status; this ADR does not claim cloud-verified
  behavior anywhere.
- Date: 2026-11-17

## Context

ForgeStack's Terraform surface today is exactly one opt-in root module
(`infrastructure/terraform/aws-s3-sandbox`), `enable_resources = false` by default, never
applied in CI. The request behind this ADR is to treat Terraform as a versioned product —
pinned toolchain, versioned reusable modules, governed changes, safe rollback — not editable
deployment scripts. That request is written for a platform with a module catalog and live
environments; this repository doesn't have those yet. Writing an elaborate SemVer/registry
policy for modules that don't exist would be fabricating capability, which this repo's other
ADRs (0002, 0005, 0006) have consistently refused to do. Instead, this ADR: (a) fixes the
concrete gaps that exist right now in the one real module, (b) states the policy a future
module catalog must follow, and (c) marks every item honestly as Implemented, Policy-only
(written but nothing to apply it to yet), or Deferred (genuinely needs cloud access or a
second module to test).

## Decisions, by requirement

### 1. Pin supported Terraform CLI versions

**Implemented.** `main.tf`'s `required_version` was `>= 1.7.0` (an unbounded lower bound —
would silently accept an untested future Terraform 2.x). Changed to
`">= 1.7.0, < 2.0.0"`. `.github/workflows/terraform-checks.yml`'s `hashicorp/setup-terraform`
step previously passed no `terraform_version` input, defaulting to whatever is "latest" at
each CI run — unpinned, not reproducible. Pinned to the exact version this ADR's changes were
verified against (`1.14.3`). *Confirmed from configuration and local test
(`scripts/test-terraform-safety-checks.sh`'s "unsupported Terraform CLI version constraint is
rejected" case proves the bound is actually enforced, not just written).*

### 2. Bounded provider version constraints and committed multi-platform lockfile

**Implemented** (carried over from F6, re-verified here). `main.tf` already uses
`version = "~> 6.0"` for the AWS provider (bounded, not unbounded or exact-pinned to the
point of blocking patch releases). `.terraform.lock.hcl` was regenerated via
`terraform providers lock` for `linux_amd64`, `darwin_amd64`, `darwin_arm64`, and
`linux_arm64` during the earlier F6 work (see PR #63's history) — never hand-edited.
*Confirmed from configuration.*

### 3–5. Independently versioned reusable modules, SemVer, immutable references, preventing silent replacement of releases

**Policy-only — not applicable yet.** There is one root module, not consumed by anything
else, so "independent versioning," "supported upgrade paths," and "preventing silent
replacement of a published release" describe a module *catalog* that doesn't exist. The
policy this repo commits to, for the day a second consumer or a child-module catalog exists:

- Each reusable module gets its own directory and its own SemVer tag
  (`infrastructure/terraform/modules/<name>` tagged `<name>/vX.Y.Z`, mirroring how
  `docs/VERSIONING.md` already versions the Go/Python service templates — one scheme,
  not two).
- A consumer references a module by an exact tag or commit SHA
  (`source = "git::https://.../modules//<name>?ref=<name>/v1.2.3"`), never a branch name.
  A branch reference is rejected the same way `internal/branchpolicy` already rejects an
  unapproved merge source — enforced the day a module registry's `source =` strings are
  worth linting.
- A published module tag is immutable: re-tagging an existing version is a force-push to a
  Git ref and is blocked the same way force-pushes to `main`/`staging`/`develop` already are
  blocked by the live rulesets (confirmed `non_fast_forward` rule present on all three,
  2026-11-17). A genuinely broken release gets a new patch version, never a silent
  retag — the identical principle `docs/VERSIONING.md` already states for container images
  and templates.

### 6. Backward compatibility within minor/patch; approval + migration docs for breaking changes

**Policy-only — not applicable yet**, same reasoning as above: nothing currently consumes a
versioned module, so there is no compatibility promise to keep yet. Policy: follow SemVer's
actual contract (a minor/patch release must not force a consumer's `.tfvars` or outputs to
change; a breaking change is a major version bump, ships with a dedicated migration doc
under `docs/migrations/`, and requires the same PR-review gate as any other protected-branch
change — no separate "infra approval" process invented, reusing what already exists).

### 7. Detect incompatible module/provider upgrades before deployment

**Implemented, locally verified.** `scripts/test-terraform-safety-checks.sh` proves, without
touching AWS, that: a provider version constraint outside the committed lockfile's version is
rejected by `terraform init -lockfile=readonly` before any plan/apply would run (this is the
exact mechanism — not a new one — that caught PR #63's real lockfile-checksum bug in CI).
*Confirmed from test.*

### 8. Require fmt, validate, lint, security scanning, tests, and reviewed plan output in the change workflow

**Implemented, with one honest gap.** `terraform-checks.yml` runs `fmt -check`, backend-free
`init -lockfile=readonly`, `validate`, the new change-safety test script, and Trivy config
scanning (HIGH/CRITICAL fails the job) on every PR and push to `main`/`staging`/`develop`.
**Not implemented: a dedicated linter (tflint) and reviewed `plan` output** — there is no
`plan` step because there is no backend/credentials to plan against safely in CI, and adding
tflint for one module with no custom resources beyond `aws_s3_bucket`/
`aws_s3_bucket_public_access_block` would be the "install every tool by default" this task's
own instructions say not to do. Revisit tflint when the module catalog grows past what Trivy
and `validate` already cover. *Confirmed from configuration (workflow) and test (safety-check
script); the gap is recorded, not hidden.*

### 9. PRs, documented approvals, and environment-specific deployment permissions

**Implemented** (inherited from existing repo governance, reverified, not duplicated).
`main`, `staging`, and `develop` already carry active GitHub rulesets requiring a PR,
required status checks, and resolved review threads (see ADR 0002 and its 2026-11-17
amendment for the exact live state and the honest correction of an earlier false claim about
CODEOWNERS enforcement). Terraform changes go through the same PRs as everything else — no
separate "Terraform approval" workflow was invented, because one PR-and-CI gate per branch is
what this repo already has and a second one would just be two places the same rule could
drift out of sync. *Confirmed from configuration (rulesets API, 2026-11-17).*

### 10. Separate state per account/environment/deployment; encrypted remote state, locking, least-privilege, versioning, recovery

**Deferred — genuinely needs cloud access to implement and verify.** There is no remote
state today (no backend block; `enable_resources = false`; nothing has ever been applied).
`infrastructure/terraform/README.md` already documents the target design (separate root +
state key per environment, encrypted/versioned/private state bucket, native locking,
short-lived OIDC) as a target, not a claim. This ADR does not add fake backend configuration
to satisfy this item on paper — an untested backend block is worse than an honest "not yet."

### 11. Never commit state, credentials, secrets, or sensitive plan artifacts

**Implemented, verified.** `.gitignore` already excluded `.terraform/`, `*.tfstate`,
`*.tfstate.*`, and `*.tfvars` (with a tracked `.terraform.tfvars.example`). This ADR adds the
one real gap found: `*.tfplan`/`*.plan` and `.terraformrc`/`terraform.rc` were **not**
previously excluded — a `terraform plan -out=plan.tfplan` run locally, or a CLI config
holding a credentials block, could have been committed by accident. *Confirmed from
configuration (`.gitignore` diff); confirmed from `git log` that no `.tfstate`/`.tfplan`/
credentials file has ever been committed to this repository.*

### 12. Prevent concurrent apply/destroy on the same environment

**Deferred — not applicable without a remote backend.** Terraform's native state locking
requires a backend that supports it (S3+DynamoDB, Terraform Cloud, etc.); with no backend
configured there is no state to lock and nothing to race on. Revisit when item 10 is
implemented against a real backend — the two are the same piece of work, not sequential
ADR items to track separately.

### 13. Safe state migration and rollback procedures; reverting code ≠ restoring infrastructure

**Policy-only, documented, not independently testable without real state.**
`infrastructure/terraform/README.md` already states this explicitly: "A successful
`terraform destroy` alone is insufficient," cleanup must reconcile against independent
provider inventory, and a cleanup process must report `CLEANUP INCOMPLETE`/
`CLEANUP UNVERIFIED` rather than infer success — the exact pattern `internal/playground`
already implements and tests for local resources (see `internal/playground/lifecycle_test.go`:
`TestCleanupFailureIsReportedAndRecorded`). The Terraform-specific version of that controller
(reconciling Terraform state against real AWS inventory) is explicitly **not implemented**:
it needs a real cloud account to write against and verify, which this task forbids. Recorded
as a named gap, not assumed solved by analogy to the local controller.

### 14. Keep existing deployments pinned until explicit opt-in upgrade

**Policy-only — not applicable yet**, no deployment exists to pin. Policy: a module consumer
upgrades its `ref=` pin in its own PR, reviewed like any other change; nothing upgrades a
consumer's pinned version for it.

### 15. Test rollback, version incompatibility, provider checksum failures, unauthorized-change rejection

**Partially implemented, locally verified; rollback explicitly not testable here.**
`scripts/test-terraform-safety-checks.sh` (new, run in CI as of this ADR) proves three of the
four, against a throwaway copy of the real module, never touching AWS:

1. A corrupted lockfile checksum (every `h1:`/`zh:` entry mutated) is rejected by
   `terraform init -lockfile=readonly` before any plan/apply.
2. A provider version constraint outside the committed lockfile is rejected the same way.
3. An unsatisfiable `required_version` (CLI incompatibility) is rejected the same way.

**Rollback** is not tested here and is explicitly called out as not assumed: there is no
state to roll back (nothing has ever been applied), and fabricating a fake rollback drill
against no real infrastructure would prove nothing. **Unauthorized-change rejection** at the
repository-governance layer (not plan is attempted without my passing CI) is covered by the
existing GitHub rulesets (item 9) — that mechanism was not re-tested here since it has no
Terraform-specific behavior, it's the same PR gate as any other file in this repo.
*Confirmed from test (the script, run directly in this session: all three checks reject with
the expected `Error:` line, and a clean baseline copy still passes); rollback and
cloud-governed unauthorized-apply rejection remain Deferred.*

### 16. How external developers propose infrastructure changes without affecting existing users

**Policy-only, documented.** Added to `infrastructure/terraform/README.md`: a contributor
proposes a change the same way as any other ForgeStack change — a PR against `develop`,
passing `fmt`/`init -lockfile=readonly`/`validate`/the new safety-check script/Trivy — and,
for the future module catalog, by adding a *new* versioned module directory or a new tag
rather than editing an existing published version's files, so existing consumers pinned to
an older tag are structurally unaffected by someone else's in-flight change.

## Consequences

- `infrastructure/terraform/aws-s3-sandbox/main.tf`: bounded `required_version`.
- `.github/workflows/terraform-checks.yml`: pinned `terraform_version`; new change-safety
  test step.
- `.gitignore`: closes the `*.tfplan`/CLI-config gap.
- New `scripts/test-terraform-safety-checks.sh`, run in CI on every relevant PR/push.
- `infrastructure/terraform/README.md`: contributor-proposal section added (see diff).
- No ruleset, CODEOWNERS, or repository-setting change. No AWS credential use, no cloud API
  call, no `plan`/`apply`/`destroy` against a real account anywhere in this work.
- Deferred, by name, not by omission: remote state, state locking, a Terraform-aware cleanup
  reconciliation controller, rollback testing, and a real module-versioning/registry
  implementation — each requires either cloud access or a second module to exist, both out
  of scope for this increment.
