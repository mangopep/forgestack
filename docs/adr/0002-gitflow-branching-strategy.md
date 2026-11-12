# ADR 0002: GitFlow-style branch promotion (feature/* → develop → staging → main)

- Status: Proposed — implemented and tested **locally only**. Nothing in "Not yet done" below
  has been applied to the live GitHub repository.
- Date: 2026-11-12

## Context

ForgeStack has grown past a single-branch workflow (everything merges straight to `main`) into
something with real CI gates, a multi-PR history, and a stated goal of being a public,
contributable project. The request was to implement a controlled GitFlow-style promotion path
— `feature/* → develop → staging → main` — with enforceable policy, not just a convention
written in a README that nothing checks.

## What was audited first (per instruction, before any implementation)

- **Branches**: confirmed via `git branch -vv` and `git ls-remote --heads origin`. At audit
  time, `main` was the only long-lived branch; all prior feature/fix branches from earlier work
  this session were already merged.
- **Open PRs**: none open at audit time (`gh api /pulls?state=open` → empty).
- **Workflow triggers**: every existing workflow (`ci.yml`, `codeql.yml`, `container-scan.yml`,
  `docs-check.yml`, `secret-scan.yml`) triggered only on `push: branches: [main]` plus
  `pull_request` — no `develop`/`staging` awareness anywhere.
- **Preserved work**: `docs/PRODUCTION_READINESS.md` and the full `internal/docgen` /
  `cmd/docgen` documentation-automation system were confirmed present on `main` (merged via
  earlier PRs) before any new work started — nothing was at risk of being lost.
- **Uncommitted work**: none found anywhere in the working tree at audit time.
- **Rulesets**: could not be enumerated — no authenticated GitHub API access in this session
  (`gh auth login` was never completed). The branch protection UI was previously confirmed (in
  an earlier session) to require "at least 1 approving review," but its exact required-status-
  check list could not be read. This ADR does not claim to know that list.

## Decision: the flow, and what enforces it

```
feature/*, fix/*  ──▶  develop  ──▶  staging  ──▶  main
                                                     ▲
                                        hotfix/*  ───┘  (documented exception,
                                                          must also backport to develop)
```

**Enforced by code, not just documentation**: `internal/branchpolicy` (pure Go, unit-tested
with every permitted/forbidden/hotfix scenario — see this change's test file) plus
`.github/workflows/branch-policy.yml`, which runs `cmd/branchpolicy` on every pull request
(including forks — it's read-only, no secrets, no elevated permissions) and fails the check if
the PR's head→base combination isn't on the allowed list.

**Rules, exactly as implemented and tested** (see `internal/branchpolicy/policy_test.go` for
the executable version of this table):

| Head | Base | Allowed? |
|---|---|---|
| `feature/*`, `fix/*`, anything else | `develop` | Yes — normal entry point |
| `develop` | `staging` | Yes — normal promotion |
| `release/*` | `staging` | Yes |
| `feature/*`, `fix/*`, arbitrary branch | `staging` | **No** |
| `staging` | `main` | Yes — normal promotion |
| `hotfix/*` | `main` | Yes — **documented exception**, see below |
| `hotfix/*` | `develop` | Yes — the required backport |
| anything else | `main` | **No** |
| `dependabot/*` | anything | Yes — exempt, see "Dependabot" below |

**Hotfix exception, explicit**: a `hotfix/*` branch may target `main` directly for urgent
production fixes, bypassing `develop`/`staging`. This is **not** a loophole — the policy
decision text returned to the PR author explicitly states the fix "must also be backported
into develop (and staging) in a follow-up PR — it does not happen automatically." Enforcing
that backport actually happens is a process responsibility, not something `branchpolicy` can
verify by itself (it would need to inspect git history across repos/time, out of scope for a
per-PR check); this is recorded as a known limitation, not hidden.

**Dependabot exemption**: Dependabot currently opens PRs against the repository's default
branch (`main`, until the default-branch migration below happens) and this policy does not
block those PRs. If `develop` becomes the default branch, `dependabot.yml`'s `target-branch`
should be set explicitly — tracked as a step in the migration plan below, not done yet.

## Ruleset specification (documented, NOT applied)

The following is the intended GitHub ruleset/branch-protection configuration for each branch.
**None of this has been created or changed on GitHub.** It is written out precisely so that
when you approve it, the exact `gh api` commands are ready, not improvised.

### `main`
- Require a pull request before merging; require 1 approving review.
- Require these status checks to pass before merging (exact job names from the existing
  workflow files): `go-quality`, `security-baseline` (`ci.yml`); `analyze` (`codeql.yml`);
  `gitleaks` (`secret-scan.yml`); `trivy-image` (`container-scan.yml`); `validate`
  (`terraform-checks.yml`); `docs` (`docs-check.yml`); `check` (`branch-policy.yml`).
- Require branches to be up to date before merging.
- Block force pushes. Block branch deletion. Require CODEOWNERS review (already active per
  ADR 0001's follow-up work).
- Allowed merge sources (once `branchpolicy` enforcement is a required check): `staging`,
  `hotfix/*`.

### `staging`
- Require a pull request before merging; require 1 approving review.
- Require the same CI status checks as `main` (identical code should pass identical checks at
  every promotion stage — that's the point of promoting a build, not re-building it
  differently per environment).
- Block force pushes. Block branch deletion.
- Allowed merge sources (enforced): `develop`, `release/*`.

### `develop`
- Require a pull request before merging; require 1 approving review.
- Require the same CI status checks (`go-quality`, `security-baseline`, `analyze`, `gitleaks`,
  `docs`, `check`, `trivy-image`, `validate` — all run unconditionally on every PR; see
  "Required-check gap closed" below for why `trivy-image`/`validate` no longer use `paths:`
  filters).
- Block force pushes. Block branch deletion.
- Allowed merge sources (enforced): `feature/*`, `fix/*`, `hotfix/*` (backport), anything else
  not otherwise restricted.

**Exact commands, not yet run** (require your explicit approval — see "Approval-gated steps"):

```bash
# Example for main; staging/develop follow the same shape with their own
# allowed-merge-source lists per the table above.
gh api repos/mangopep/forgestack/rulesets --method POST -f name='main-protection' \
  -f target='branch' -f enforcement='active' \
  -F conditions[ref_name][include][]='refs/heads/main' \
  -f rules[0][type]='pull_request' \
  -F rules[0][parameters][required_approving_review_count]=1 \
  -f rules[1][type]='required_status_checks' \
  -F 'rules[1][parameters][required_status_checks][][context]=go-quality' \
  # ... (one entry per required check; full command assembled at approval time,
  #     not guessed now — exact context strings will be re-verified against
  #     the live Checks API output at that time, not copied from this draft).
```

This ADR intentionally does not pretend to have the full, final `gh api` invocation ready
byte-for-byte — status check context names should be re-confirmed against a live run at
approval time, not trusted from this document months later.

## Migration plan: creating `develop` and `staging`

1. Verify `main` is green (all required checks passing) at the moment of migration — not
   assumed from this ADR's writing time.
2. Create both branches from that verified `main` tip:
   ```bash
   git checkout main && git pull --ff-only
   git checkout -b develop && git push -u origin develop
   git checkout -b staging && git push -u origin staging
   ```
   Both branches start **identical** to `main` — no drift, no cherry-picking, nothing lost.
3. Apply the rulesets above to all three branches (`gh api .../rulesets`, exact commands
   finalized at that time).
4. Confirm `branch-policy.yml`'s checks are green against a real test PR on GitHub (not just
   this session's local CLI simulation) before relying on it.
5. **Only after** steps 1–4 are verified: consider changing the default branch to `develop`
   (`gh api repos/mangopep/forgestack --method PATCH -f default_branch=develop`). This changes
   where new PRs default their base to, and where Dependabot opens PRs unless
   `dependabot.yml` is updated first — sequence those together, not this step alone.

## Approval-gated steps (nothing below has been done)

- Creating `develop` and `staging` branches on GitHub.
- Creating/updating any ruleset or branch protection rule.
- Changing the default branch.
- Updating `dependabot.yml`'s `target-branch`.
- Any release/publish/GHCR/SBOM/provenance pipeline actually running (see "Deferred" below).
- Creating GitHub "Environments" (development/staging/production).

## Deferred to a later milestone, not attempted in this PR

Building the actual versioned-artifact-promotion-by-digest release pipeline (build once, tag,
compute a content digest, promote that exact digest across environments), GHCR publishing,
SBOM/provenance generation, and GitHub Environments configuration are large enough to be their
own reviewable change, and depend on `develop`/`staging` actually existing first. Attempting
them now would violate "work incrementally" and "do not provision infrastructure or publish
releases without approval." Proposed as the next milestone once the branch migration above is
approved and executed.

## Required-check gap closed: missing/skipped checks must not silently satisfy a mandatory gate

`container-scan.yml` (`trivy-image`) and `terraform-checks.yml` (`validate`) originally used a
workflow-level `on: pull_request: paths: [...]` filter. That is a real gap for a **required**
status check: when a PR doesn't touch any of the filtered paths, GitHub never runs the workflow
at all, so the check context is never reported. Depending on repository/ruleset settings this
either blocks the merge forever waiting on a status that will never arrive, or — for rulesets
configured to not wait on checks that have no history on the PR — is treated as satisfied
despite never actually running. Neither behavior is acceptable for a mandatory gate.

Fix applied (uncommitted at ADR-writing time, included in this change): both workflows now
trigger on every `pull_request` and `push` to `[main, staging, develop]` with no path filter at
the `on:` level. Relevance is instead decided **inside** the job, in a "Detect relevant changes"
step that diffs the PR's base...head range (or push's before...after range) against the
same path list that used to live in `on.paths`. If the diff shows no relevant files changed, the
job runs an explicit "no relevant changes — explicit pass" step instead of the real
build/scan/validate steps, so the job still reports a real `success`, never a skip or a missing
check. If the diff itself cannot be computed (e.g. an unresolvable ref), the step fails safe by
scanning/validating anyway rather than silently passing. This means `trivy-image` and `validate`
are now unconditionally safe to list as required status checks on `main`, `staging`, and
`develop` — not contingent on path filters the way the earlier draft of this ADR assumed.

## Consequences

- New package: `internal/branchpolicy` (pure logic, zero dependencies, unit-tested).
- New workflow: `.github/workflows/branch-policy.yml`, runs on every PR including forks.
- Existing workflows (`ci.yml`, `codeql.yml`, `container-scan.yml`, `docs-check.yml`,
  `secret-scan.yml`) extended to also trigger on push to `develop`/`staging` — inert today
  since those branches don't exist yet, becomes active the moment they're created.
- `container-scan.yml` and `terraform-checks.yml` lost their workflow-level `on.paths` filters
  and gained an in-job "Detect relevant changes" step instead, so `trivy-image` and `validate`
  always report a real success/failure and can safely be required checks (see above).
- No change to `main`'s current behavior, protection, or required checks in this PR.
