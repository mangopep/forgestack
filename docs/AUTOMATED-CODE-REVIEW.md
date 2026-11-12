# Automated code review and security gates

Automated review is **multi-layered**, not one AI checkbox:

1. `go test -race`, `go vet`, `gofmt` and Go build: executable quality gates.
2. `reviewdog/action-golangci-lint`: annotates PRs with Go lint findings (same-repository PRs); the fork-safe job uses read-only repository checks.
3. `govulncheck`: detects known vulnerabilities reachable through Go dependencies.
4. `github/codeql-action`: security static analysis; availability and permissions depend on repository visibility and GitHub plan.
5. `scripts/review_guard.py`: explicit baseline secret and tracked-file checks.
6. Terraform `fmt`, `validate`, Trivy IaC scanning: blocks high/critical infrastructure misconfigurations.
7. Dependabot: dependency and GitHub Action update PRs.
8. Human review / CODEOWNERS / GitHub branch rules: cannot be replaced by linting.

## GitHub setup after creating the repository

- In Settings > Rules > Rulesets (or branch protection), protect `main` and require passing `CI and Quality Gates`, `Automated PR Code Reviewer` (where available), `Terraform Checks`, and CodeQL as appropriate.
- Configure repository security features and CodeQL availability in Settings > Security. Some features need paid plans or repository visibility settings.
- Require at least one human approval for production-bound changes and block self-approval if supported.
- Replace `.github/CODEOWNERS` placeholder with your own GitHub user and adjust reviewers; GitHub may not let sole maintainers obtain independent approval.
- Keep GitHub Actions default token permissions minimal; configure OIDC federation for future AWS deployments (do not store long-lived AWS keys in secrets).
- Action references are version tags for readability in this starter. Before production, pin actions to audited full-length immutable commit SHAs and automate updates.

## About AI review

Automated **static** reviewer is already integrated. An optional AI reviewer can be added later with an explicitly approved provider, separate API budget, strict read-only permissions, no untrusted secrets, and human approval. Do not give a PR-commenting bot AWS credentials or deployment permissions.

## Security exercise

Create a test branch, add a deliberately suspicious placeholder private key header in a test-only file, run `python3 scripts/review_guard.py --ci` and see failure. Remove it before merging; do not use a real secret. Later learn secret revocation and history cleaning.

## Claude Code local reviewer

Run `claude` from repository root and invoke `/code-review` or `/security-review`; optionally ask Claude to delegate to `.claude/agents/`. This gives AI review within an authenticated Claude Code session and does not automatically post comments to GitHub. Review findings still require human judgment. See `.claude/README.md`.
