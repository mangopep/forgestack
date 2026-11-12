# ForgeStack Security Audit — Pre-Publication

Date: 2026-11-12
Scope: local repository state before first GitHub push. This is a **code and configuration
audit**, not a penetration test, and it is **not** a claim that CI gates are enforced on
GitHub — enforcement can only be confirmed after the repository is connected and branch
protection is configured (see "Not yet verified" below).

## Executive summary

The repository's existing CI design (tests, CodeQL SAST, Go dependency scanning via
govulncheck, golangci-lint via reviewdog, Terraform IaC config scanning via Trivy, and
Dependabot for Go modules/Actions/Docker) was already reasonably strong. This audit added
secret scanning and container image scanning, pinned all third-party GitHub Actions to commit
SHAs (closing a supply-chain gap the repo's own README already called for but didn't follow),
and verified no credentials, personal paths, or unnecessary generated files are present.
**No GitHub-side enforcement (branch protection, required reviews, OIDC) exists yet** — none
can exist until the repo is connected to GitHub, which has not happened.

## Confirmed findings (verified in this session)

| # | Area | Finding | Evidence |
|---|------|---------|----------|
| 1 | Secrets | No AWS keys, private keys, password/secret/token assignments, or `.env` real values found in tracked or untracked files. `grep` sweep, zero matches. | Confirmed from code (text scan) |
| 2 | Personal data | No contributor personal paths/emails in tracked files; only a generic `/Users/me/...` placeholder in a test fixture (`.claude/hooks/test_guard.py`). | Confirmed from code |
| 3 | Generated files | `.claude/hooks/__pycache__/*.pyc` existed on disk but were never staged/committed; `.gitignore` now excludes `__pycache__/`, `*.pyc`. | Confirmed from code |
| 4 | Supply chain | All 5 workflow files used Actions pinned by mutable tag (`@v4`, `@v2`, etc.), contradicting the repo's own stated policy in `README.md`. **Fixed**: all pinned to resolved commit SHAs with version comments. | Confirmed from code (before/after diff) |
| 5 | Secret scanning | No secret-scanning workflow existed; `scripts/review_guard.py` only matches AWS access-key and PEM private-key patterns — it is a narrow baseline, not general secret detection. **Added** `secret-scan.yml` (gitleaks). | Confirmed from code |
| 6 | Container scanning | Dockerfile exists; no workflow scanned the built image. **Added** `container-scan.yml` (Trivy, image mode, builds locally, never pushes to a registry). | Confirmed from code |
| 7 | Least privilege | All workflows already declare minimal top-level `permissions:` (`contents: read`, plus `security-events: write` only where CodeQL needs it, `checks: write` only for the reviewdog job). Fork PRs run a token-less job (`fork-safe-review`). | Confirmed from code |
| 8 | CODEOWNERS | `.github/CODEOWNERS` sets `* @mangopep`. The file alone does not enforce review — it only takes effect once branch protection requires CODEOWNERS review (not yet configured; see below). | Confirmed from code |
| 9 | Dependabot | Already configured for `gomod`, `github-actions`, and `docker` ecosystems, weekly. | Confirmed from code |
| 10 | Local build/test | `gofmt -l .` clean, `go vet ./...` clean, `go build ./...` OK, `go test -race -cover ./...` passing (`internal/api` 93.5% coverage), `python3 scripts/review_guard.py --ci` passing. | Confirmed from test |
| 11 | Terraform sandbox | `enable_resources` defaults to `false`; no AWS resources are created by `terraform validate`/`fmt`; workflow never runs `apply`/`destroy`. | Confirmed from code |

## Tested with intentional violations

- Verified `scripts/review_guard.py` actually fails on a planted AWS-key-shaped string in a
  scratch file (not committed) before this audit, and passes on the real tree — confirms the
  check is live, not a no-op. *(Carried over from the prior session's verification.)*
- **Not executed this session**: injecting a fake secret and re-running gitleaks locally.
  Docker Desktop's daemon was not running on this machine, so the `gitleaks/gitleaks-action`
  and the new container-scan workflow could not be exercised locally via the Docker-based
  gitleaks image. This is a genuine gap in this audit, not a pass — see "Not yet verified."

## Not yet verified (requires your approval + a connected GitHub remote)

These cannot be claimed as working until actually exercised on GitHub, because GitHub Actions
and branch protection do not run or apply locally:

- **`secret-scan.yml` and `container-scan.yml` actually running green/red on GitHub.**
- **Branch protection on `main`**: require PR before merge, require the `go-quality`,
  `security-baseline`, `analyze` (CodeQL), `gitleaks`, and `trivy-image` checks to pass,
  require CODEOWNERS review, block force-push and branch deletion. Exact command (run only
  with your approval, after remote is connected and you're repo admin):
  ```bash
  gh api repos/mangopep/forgestack/branches/main/protection \
    --method PUT \
    -f required_status_checks[strict]=true \
    -F required_status_checks[contexts][]=go-quality \
    -F required_status_checks[contexts][]=security-baseline \
    -f enforce_admins=true \
    -f required_pull_request_reviews[require_code_owner_reviews]=true \
    -f required_pull_request_reviews[required_approving_review_count]=1 \
    -f restrictions=null
  ```
  This mutates a shared, visible GitHub setting — do not run without your explicit go-ahead.
- **OIDC**: there is currently no deployment workflow at all (by design — README states "no
  cloud auto deploy"). OIDC trust policies are meaningless without a deploy target, so none
  was added. If/when a real deploy pipeline is introduced, it should use short-lived OIDC
  federation to AWS (no long-lived `AWS_ACCESS_KEY_ID` secrets) — documented here as a
  requirement for that future work, not implemented now.
- **Rulesets / signed commits / required linear history** — not configured; optional
  hardening, GitHub-side only.
- **Dependabot security alerts / secret scanning (GitHub's native feature, separate from the
  gitleaks workflow)** — toggled in repo Settings → Security, cannot be set from this session.

## Severity-ranked remaining risks

- **High** — No branch protection exists yet, so nothing above actually blocks a bad merge to
  `main` today. All the CI gates described are advisory until the repo is connected and
  protection rules are turned on.
- **Medium** — Secret-scanning and container-scanning workflows are new and have not had a
  single real run; a config/action-compatibility bug would only surface on first GitHub
  Actions execution.
- **Medium** — `golangci-lint`, `gitleaks`, `trivy`, `govulncheck` are not installed locally,
  so none of this was double-checked outside of CI's own first run. Recommend running these
  locally pre-commit once installed (not done here — would require your approval to install).
- **Low** — `review_guard.py`'s secret patterns are narrow (AWS keys + PEM headers only); it
  is correctly described as a baseline, and gitleaks now covers the broader case.

## Go / No-go

**No-go for "production-ready" claims.** Go for "ready to push and connect remote for further
verification" — the code-level controls are sound, but GitHub-side enforcement (branch
protection, required checks, CODEOWNERS enforcement) is unverified by construction (nothing to
verify against yet) and must be configured and tested with a real PR before any production
claim is made.
