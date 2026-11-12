---
title: PR validation and documentation publishing
---

# PR validation and documentation publishing

**Status: implemented.** Both diagrams below reflect the actual workflow files and `cmd/docgen`
behavior as of this page's last regeneration-check — verified against `.github/workflows/*.yml`
and `docs/generated/CI_WORKFLOWS.md`, not hand-guessed.

## PR validation and required CI

```mermaid
flowchart TD
    PR["Pull request opened/updated"]
    PR --> policy["check (branch-policy.yml)<br/>internal/branchpolicy"]
    PR --> quality["go-quality, security-baseline (ci.yml)"]
    PR --> sec["analyze (codeql.yml), gitleaks (secret-scan.yml)"]
    PR --> container["trivy-image (container-scan.yml)"]
    PR --> iac["validate (terraform-checks.yml)"]
    PR --> docsCheck["docs (docs-check.yml)"]

    policy --> gate{"All 8 required<br/>checks green?"}
    quality --> gate
    sec --> gate
    container --> gate
    iac --> gate
    docsCheck --> gate

    gate -->|yes| mergeable["PR mergeable<br/>(mergeStateStatus: CLEAN)"]
    gate -->|no| blocked["PR blocked<br/>(mergeStateStatus: BLOCKED)"]

    classDef req fill:#c53030,color:#fff,stroke:#822727
    class policy,quality,sec,container,iac,docsCheck req
```

Every box under "required" is a real GitHub ruleset `required_status_checks` context on
`main`/`staging`/`develop` (see ADR 0002) — this isn't an aspirational diagram, it's what
`gh api repos/mangopep/forgestack/rulesets/<id>` actually returns as of the F1 migration.
`container-scan.yml` and `terraform-checks.yml` run unconditionally on every PR (no path
filter) specifically so they can never silently skip a required check — see ADR 0002's
"Required-check gap closed" section for why.

## Documentation generation and publishing

```mermaid
sequenceDiagram
    participant Dev as Contributor
    participant Docgen as cmd/docgen
    participant CI as docs-check.yml (required `docs` check)
    participant Pages as pages-deploy.yml

    Dev->>Docgen: go run ./cmd/docgen generate all
    Docgen->>Docgen: parse Go source (go/ast), Makefile, workflow YAML
    Docgen-->>Dev: docs/generated/*.md written
    Dev->>CI: open PR (any branch)
    CI->>Docgen: generate all, then git diff --exit-code
    alt generated docs stale
        CI-->>Dev: fail — "run generate all and commit"
    else links broken
        CI-->>Dev: fail — file:line of broken link
    else clean
        CI-->>Dev: docs check passes
    end
    Dev->>Dev: PR merges to main (only, via staging)
    Pages->>Pages: npm ci && npm run build (website/)
    Pages->>Pages: deploy build/ to GitHub Pages
    Note over Pages: Triggered only by push to main --<br/>never by an arbitrary PR branch.
```

The website build itself (`website` job in `docs-check.yml`) runs on every PR as a build-only
check — it never publishes from a PR. Only `pages-deploy.yml`, triggered by `push:
branches: [main]`, actually deploys to GitHub Pages, and it runs with `contents: read, pages:
write, id-token: write` — no broader token scope than the Pages deployment itself needs.
