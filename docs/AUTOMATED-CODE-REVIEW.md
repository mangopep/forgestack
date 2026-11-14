# Automated code review and security gates

Automated review is **multi-layered**, not one AI checkbox. Every tool listed below is free
and open-source — no paid subscription, no model API key, nothing that calls out to a hosted
LLM. See "Enforceable vs. advisory" below for which of these actually block a merge versus
which are informational only.

1. `go test -race`, `go vet`, `gofmt` and Go build: executable quality gates.
2. `golangci-lint` (via `golangci/golangci-lint-action` in `ci.yml`, **every PR including
   forks**) — see "Linter set" below for exactly what it checks and why each was added.
   `reviewdog/action-golangci-lint` (in `automated-review.yml`, same-repository PRs only)
   additionally posts inline Check annotations for a nicer review UX; the fork-safe job runs
   only `scripts/review_guard.py` to avoid giving untrusted fork code a privileged token.
3. `govulncheck`: detects known vulnerabilities reachable through Go dependencies.
4. `github/codeql-action`: security static analysis; availability and permissions depend on
   repository visibility and GitHub plan.
5. `scripts/review_guard.py`: explicit baseline secret and tracked-file checks.
6. `gitleaks`: broader secret scanning than the baseline script's narrow AWS-key/PEM
   patterns — but on a PR, it scans that PR's commits only, not the repository's full git
   history (confirmed from a live run); a real full-history scan runs locally/on demand, see
   `docs/security/VULNERABILITY-LIFECYCLE.md`.
7. Terraform `fmt`, `validate`, Trivy IaC scanning: blocks high/critical infrastructure
   misconfigurations. Trivy also scans the built container image.
8. Dependabot: dependency and GitHub Action update PRs.
9. `docgen` (`docs-check.yml`): fails the build if generated documentation is stale relative
   to source, or if a relative Markdown link is broken.
10. Human review / CODEOWNERS / GitHub branch rules: cannot be replaced by linting.

## Linter set (audited 2026-11-12)

No `.golangci.yml` existed before this audit — `golangci-lint` was running with only its
built-in defaults (`errcheck`, `govet`, `ineffassign`, `staticcheck`, `unused`). `.golangci.yml`
now makes the full set explicit and adds:

| Linter | Catches | Why added |
|---|---|---|
| `dupl` | Duplicated code fragments | Deterministic duplication detection, explicitly requested. Threshold tuned to **100** (not the tool's 150 default) — verified with an injected ~29-line duplicated function that 150 missed but 100 caught; still 0 false positives against this repo's real code. |
| `gocyclo` | High cyclomatic complexity | Threshold set to **15**, based on this repo's measured real maximum (12) at audit time, not a guess — headroom above what's already reviewed, not an arbitrary round number. |
| `gosec` | Security anti-patterns (file perms, command construction, weak crypto, etc.) | Complements CodeQL with a different, narrower rule engine; found 10 real issues on first run (file permissions, `exec.Command`/`os.ReadFile` with variable input in `docgen`), all fixed or documented with a justified `//nolint` and inline reasoning — never silently suppressed. |
| `bodyclose` | Unclosed `net/http` response bodies | Resource-leak correctness; verified with an injected leaking handler. |
| `contextcheck` | `context.Context` not propagated from the caller | Concurrency/cancellation correctness. |
| `errorlint` | `==`/`!=` comparison against a sentinel error instead of `errors.Is`/`errors.As` | Found 4 real instances in this repo's own test files (fragile once an error is ever wrapped) — fixed. |
| `govet`'s `copylocks` check (already on by default) | Copying a struct containing a `sync.Mutex` by value | The clearest deterministic concurrency-correctness check available without a dedicated race run; verified with an injected `Counter` struct passed by value. |

All of the above were verified against this repository with real injected violations (not
assumed to work from documentation) before being enabled — see this change's PR description
for the exact detection output.

## Enforceable vs. advisory

| Check | Runs on | Blocks merge? |
|---|---|---|
| `go vet`, `gofmt`, `go build`, `go test -race` (`ci.yml`) | Every push to `main` and every PR, including forks | **Enforceable** — fails the job, and GitHub branch protection is confirmed to require at least one approving review before merge (checked directly against the live repo). Whether `ci.yml`'s *specific job name* is additionally configured as a required status check could not be verified from this session (no authenticated `gh`/API access to read the branch protection rule's required-check list) — confirm directly in Settings → Branches. |
| `golangci-lint` via `golangci-lint-action` (`ci.yml`) | Every push/PR, including forks | Same caveat as above: the job fails on any issue, but whether it's *required* for merge depends on branch protection configuration not independently verified here. |
| `govulncheck`, Trivy (container + IaC), `gitleaks`, CodeQL | Every push/PR | Same caveat. |
| `reviewdog` inline annotations (`automated-review.yml`) | Same-repo PRs only | **Advisory presentation layer** — same underlying `golangci-lint` run as the enforceable one above, just a nicer UI (inline Check annotations instead of a plain job log). Not a second, independent check. |
| `docgen` staleness/link checks (`docs-check.yml`) | Every push/PR | Enforceable in the sense that the job fails; required-for-merge status not independently verified, same caveat. |
| Claude Code `/code-review`, `/security-review` (local) | On demand, locally | **Purely advisory.** Cannot block anything on GitHub, does not post to the PR, and must never be reported as equivalent to a required CI gate — see `CLAUDE.md`'s guardrails. |
| CODEOWNERS review requirement | PRs against `main` | **Enforceable** per the PR UI ("at least 1 approving review required by reviewers with write access") — but see the self-review limitation below. |

**Self-review limitation**: GitHub does not let a PR author satisfy their own
"code owner review required" rule by approving their own PR, even when they are the listed
owner. For a solo-maintainer repo this means every PR is permanently blocked on a review from
somewhere else, until a second collaborator is added or branch protection is explicitly
adjusted — not done as part of this audit; flagged for a separate decision.

## Avoiding duplicate bot comments

Only one tool posts anything PR-visible today: `reviewdog` in `github-pr-check` mode, which
uses GitHub's Checks API (inline annotations tied to a check run) rather than posting
conversation comments. Re-running the same PR replaces that check run's annotations in place —
it does not accumulate duplicate comments across re-runs. If a second commenting tool is ever
added (e.g. the PR-Agent evaluation below), it must either also use Checks-API-style annotation
(not comments) or explicitly update/delete its own prior comment before posting a new one, to
avoid duplicate bot noise. Not an issue today because there is exactly one PR-commenting tool.

## GitHub setup after creating the repository

- In Settings > Rules > Rulesets (or branch protection), protect `main` and require passing
  `CI and Quality Gates`, `Automated PR Code Reviewer` (where available), `Terraform Checks`,
  `Documentation Checks`, `Secret Scanning`, and CodeQL as appropriate — this session could not
  verify which of these are actually marked "required" versus merely present; check directly.
- Configure repository security features and CodeQL availability in Settings > Security. Some
  features need paid plans or repository visibility settings.
- Require at least one human approval for production-bound changes and block self-approval if
  supported.
- Replace `.github/CODEOWNERS` placeholder with your own GitHub user and adjust reviewers;
  GitHub may not let sole maintainers obtain independent approval (see "self-review limitation"
  above).
- Keep GitHub Actions default token permissions minimal; configure OIDC federation for future
  AWS deployments (do not store long-lived AWS keys in secrets).
- All third-party Actions in this repo are already pinned to full-length immutable commit
  SHAs (not version tags) — Dependabot opens a PR for every update, so pins stay current
  without losing reproducibility.

## Evaluating PR-Agent (Qodo Merge, open-source) + local Ollama — NOT enabled

Evaluated as an **optional, later** integration per explicit request. Not installed, not
enabled, nothing changed in this PR.

**What it is**: [PR-Agent](https://github.com/qodo-ai/pr-agent) (Apache-2.0) is an open-source
AI PR-review tool that can target a local model server instead of a paid API — including
Ollama, which can serve an open-weight model entirely offline.

**Why this evaluation leans against enabling it automatically on this repo, as-is**:

- PR-Agent needs to run somewhere with both (a) access to the PR diff and (b) network access
  to the Ollama server. On a public GitHub-hosted runner, option (b) means either exposing
  Ollama to the internet (explicitly ruled out) or running on a **self-hosted runner** so the
  runner can reach `localhost`/a LAN-local Ollama instance.
- A self-hosted runner that executes on `pull_request` from a **public** repository is a
  well-known supply-chain risk: any fork can open a PR whose workflow-triggered code runs with
  your runner's network access, including reaching your local Ollama instance and anything
  else on that network — explicitly ruled out by this task's own constraints ("do not... add an
  insecure self-hosted GitHub runner").
- The safe version of this integration restricts the self-hosted runner to `workflow_dispatch`
  (a maintainer manually triggers it, never automatically on an untrusted PR) or runs PR-Agent
  **locally** on a maintainer's own machine against a checked-out PR branch, never as an
  automatic GitHub Actions trigger on public PRs.

**Recommendation**: do not wire PR-Agent into automatic PR triggers on this public repository.
If wanted later, the safe shape is a manually-invoked local script (maintainer runs
`pr_agent` against a PR number from their own machine, talking to their own local Ollama) —
not a GitHub Actions workflow. Revisit if/when this repo gets a dedicated, isolated self-hosted
runner with no network path to anything sensitive, used only for maintainer-triggered,
non-automatic runs.

## About AI review

Automated **static** reviewer (the linter/scanner set above) is already integrated and runs
automatically. Any AI reviewer (PR-Agent or otherwise) is optional, evaluated above, not yet
enabled, and must never be treated as equivalent to the deterministic checks — see "Enforceable
vs. advisory." Do not give a PR-commenting bot AWS credentials or deployment permissions.

## Security exercise

Create a test branch, add a deliberately suspicious placeholder private key header in a test-only file, run `python3 scripts/review_guard.py --ci` and see failure. Remove it before merging; do not use a real secret. Later learn secret revocation and history cleaning.

## Claude Code local reviewer

Run `claude` from repository root and invoke `/code-review` or `/security-review`; optionally ask Claude to delegate to `.claude/agents/`. This gives AI review within an authenticated Claude Code session and does not automatically post comments to GitHub. Review findings still require human judgment. See `.claude/README.md`.
