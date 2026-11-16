# Terraform module and environment conventions

Terraform remains an opt-in adapter. CI never runs `plan`, `apply`, or `destroy` against a
cloud account. The existing sandbox root defaults to no resources and is validated with a
backend-free init, `terraform fmt -check`, `terraform validate`, and Trivy configuration
scanning.

## Root modules and state

- Put independently operated environments in separate root directories and separate state
  keys. Reuse child modules; do not make a shared root select `dev`, `playground`, staging
  and production through a variable.
- Every root must set a Terraform CLI version constraint and provider source/version
  constraints. Commit the generated `.terraform.lock.hcl`; do not edit checksums by hand.
- Generate/update provider selections intentionally with `terraform init` and review the
  lockfile diff. CI uses `terraform init -backend=false -lockfile=readonly` so it fails if
  provider selections were not committed.
- Never commit `.tfstate`, plan files, credentials or `.terraform/`. For a future remote
  backend, use an encrypted, versioned, private state bucket, strict IAM, a separate key per
  environment and backend-native locking. Prefer short-lived OIDC credentials when a
  deployment workflow has been separately reviewed and authorized.
- Pin the Terraform CLI with a *bounded* `required_version` (e.g. `">= 1.7.0, < 2.0.0"`, not
  just a lower bound) and match it with an explicit `terraform_version` in
  `hashicorp/setup-terraform` in CI — an unpinned "latest" CLI drifts silently between runs.
  See `docs/adr/0007-terraform-module-versioning-and-change-governance.md`.

## Module versioning (policy for the future module catalog)

There is currently one root module (`aws-s3-sandbox`), not a reusable module catalog, so this
section is policy for when that changes, not a description of something that exists:

- A reusable module gets its own directory and SemVer git tag (`<name>/vX.Y.Z`). A consumer
  references an exact tag or commit SHA — never a mutable branch.
- A published tag is immutable. A broken release gets a new patch version, never a silent
  retag of an existing one.
- A breaking change is a major version bump with a migration doc under `docs/migrations/` and
  the same PR review every other change here gets — no separate approval process.

### Proposing an infrastructure change (including as an external contributor)

Open a PR against `develop`, same as any other ForgeStack change. It must pass `terraform fmt
-check`, backend-free `init -lockfile=readonly`, `validate`, `scripts/test-terraform-safety-
checks.sh`, and the Trivy config scan — all required status checks on the live branch
rulesets, not optional. To add a new reusable module without affecting existing consumers,
add a *new* module directory/tag rather than editing an already-published version's files;
existing consumers pinned to an older tag are then structurally unaffected by your change.

## Resource ownership and cleanup

Any future cloud resource must be tagged with its ForgeStack environment ID, owner, mode
(`playground` or `product`) and manager. Expiration applies only to temporary playground
environments. A cleanup process must reconcile Terraform state with independent provider
inventory, limit deletion to positively owned resources, preserve persistent product
environments, and report `CLEANUP INCOMPLETE` or `CLEANUP UNVERIFIED` when it cannot prove
the result. A successful `terraform destroy` alone is insufficient.

The current AWS sample has no remote state and no cleanup controller. Its lockfile pins the
provider for the opt-in sample only; no AWS resource was created to generate or validate
that file.
