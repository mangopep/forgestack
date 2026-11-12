# CI troubleshooting guide

What each required check actually does, what a failure usually means, and how to reproduce it
locally before pushing again. All commands below assume you're in the repository root with Go
installed (`go version` should print `1.27.4` or compatible).

| Check (job name) | Workflow | What it verifies |
|---|---|---|
| `go-quality` | `ci.yml` | `gofmt`, `go vet`, `golangci-lint`, `go test -race`, `go build ./cmd/api` |
| `security-baseline` | `ci.yml` | `scripts/review_guard.py`, `govulncheck` |
| `analyze` | `codeql.yml` | CodeQL static analysis (Go) |
| `gitleaks` | `secret-scan.yml` | Committed-secret scanning across full history |
| `trivy-image` | `container-scan.yml` | Built container image vulnerability scan (HIGH/CRITICAL) |
| `validate` | `terraform-checks.yml` | Terraform `fmt`/`validate` + Trivy IaC config scan |
| `docs` | `docs-check.yml` | Generated docs freshness, broken relative links, doc-impact report |
| `check` | `branch-policy.yml` | This PR's head→base combination is a permitted promotion |

## `go-quality` fails

- **`gofmt` step fails**: your code isn't formatted. Fix locally, don't argue with the check:
  ```bash
  gofmt -w cmd internal tests
  ```
  `-w` writes the formatted result back to the files (as opposed to `-l`, which only lists
  which files are wrong, used by CI).
- **`go vet` fails**: a real suspicious-construct finding (e.g. a `Printf` format mismatch).
  Read the specific line vet points at; these are rarely false positives.
- **`golangci-lint` fails**: run it locally with the same version pinned in `.golangci.yml`:
  ```bash
  golangci-lint run ./...
  ```
  If it reports something that looks wrong for a specific, justified reason, use a scoped
  `//nolint:<linter> // reason` comment on that line — never a blanket disable.
- **`go test -race` fails**: the race detector found a real data race, or a test is flaky. Run
  the specific failing package directly for a clearer trace: `go test -race -run
  <TestName> ./path/to/package`.

## `security-baseline` fails

- **`review_guard.py` fails**: it found something matching a baseline secret/risky-config
  pattern. Read its exact output — it tells you the file and pattern matched. Do not disable
  this check to get past a real finding; redact/remove the secret and rotate it if it was ever
  live.
- **`govulncheck` fails**: a dependency has a known vulnerability *reachable* by your code
  (govulncheck does call-graph analysis, not just version matching — it won't flag a
  vulnerable function your code never calls). Run `govulncheck ./...` locally; usually fixed by
  `go get -u <module>` to a patched version, then `go mod tidy`.

## `analyze` (CodeQL) fails

Appears as a GitHub "Code scanning alert," not a simple pass/fail line in the Actions log.
Check the PR's "Checks" tab or the repository's Security tab for the specific alert. CodeQL
findings tend to be real; treat a dismissal as something to justify in the PR description, not
a default response.

## `gitleaks` fails

A secret pattern was found in the diff or history. **Do not just delete the line and
re-push** — if the secret was ever committed, it's in git history and should be treated as
compromised: rotate it at the source (AWS console, the provider that issued it, etc.), then
remove it from the current code. Rewriting history to scrub it is a separate, higher-risk
operation — don't attempt it without discussing scope and impact first.

## `trivy-image` fails

A HIGH/CRITICAL vulnerability was found in the built container image (base image or an
installed package). Check the Trivy output for the specific CVE and package; usually fixed by
bumping the base image tag in `Dockerfile` or updating the vulnerable package. This check now
always runs (see "Required-check gap closed" in `docs/adr/0002-gitflow-branching-strategy.md`)
— it will report an explicit pass if your PR genuinely touches nothing image-relevant, never a
skip.

## `validate` (Terraform) fails

- **`fmt -check` fails**: run `terraform -chdir=infrastructure/terraform/aws-s3-sandbox fmt`
  locally and commit the result — `fmt` with no `-check` rewrites files in place.
- **`validate` fails**: a real syntax/reference error in the Terraform config. Run the same
  `init -backend=false` + `validate` sequence locally to reproduce without touching any real
  cloud state.
- **Trivy IaC scan fails**: a HIGH/CRITICAL misconfiguration (e.g. an overly permissive IAM
  policy, a public S3 bucket). Fix the configuration; this is a sandbox example, not production
  infra, but the check is intentionally strict anyway so bad patterns don't get copied later.

## `docs` fails

- **"Generated documentation is out of sync"**: run `go run ./cmd/docgen generate all` locally
  and commit the result. This check exists so generated docs can never silently drift from
  their source.
- **Broken relative Markdown link**: run `go run ./cmd/docgen checklinks .` locally; it prints
  the exact file and broken link.
- The documentation-impact report (manual-kind areas) is advisory and does not fail the build —
  if you see it flag something, review it as a human judgment call, not a required fix.

## `check` (branch-policy) fails

This means your PR's source branch and target branch aren't on the permitted list. The failure
message always states which combination was blocked and why — see
`docs/handbook/BRANCHING.md` for the full table, or `docs/handbook/EMERGENCY-HOTFIX.md` if this
is a genuine production emergency. **Retarget the PR's base branch**, don't ask for this check
to be disabled.

### The one expected exception: the bootstrap PR

The very first PR that introduces `branch-policy.yml` cannot itself pass this check, because
its source branch is neither `staging` nor `hotfix/*` (there's no "proper" predecessor branch
yet — that's exactly what this PR is setting up). This is expected and documented in ADR 0002.
As long as `branch-policy.yml` has not yet been added as a **required** ruleset check (true at
the time of writing), this failure is advisory and does not block the maintainer from merging
that one bootstrap PR. It will not happen again for any subsequent PR once `develop`/`staging`
exist.

## A failing required check must never look like a pass

If you ever see a workflow report "skipped" for a check that's marked required in branch
protection, treat that as a bug report, not a convenience — a skipped required check can mean
GitHub either blocks the merge forever (safe but confusing) or, depending on ruleset
configuration, treats it as satisfied despite never running (unsafe). This repository's
`container-scan.yml` and `terraform-checks.yml` were fixed specifically to avoid ever reporting
"skipped" for this reason — see the "Required-check gap closed" section of ADR 0002.
<!-- F1 Phase D2 harmless develop->staging promotion test content -->
