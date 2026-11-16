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
