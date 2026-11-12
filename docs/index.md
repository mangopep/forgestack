---
id: index
slug: /
title: ForgeStack docs
sidebar_position: 1
---

# ForgeStack

Go + Docker + AWS + DevSecOps engineering lab. This site renders the repository's own
`docs/` directory directly — nothing here is copied or duplicated, so it can never drift from
what's actually in the repo. See the root [README](https://github.com/mangopep/forgestack) for
the quickstart and current implementation status.

## Start here

- **[Roadmap](./ROADMAP.md)** — the full curriculum this lab works through.
- **[Automated code review and security gates](./AUTOMATED-CODE-REVIEW.md)** — every CI check,
  which are enforceable vs. advisory, and why.
- **[Production readiness gap analysis](./PRODUCTION_READINESS.md)** — an evidence-based audit
  of what's implemented vs. missing.
- **[Security audit](./SECURITY-AUDIT.md)** — pre-publication findings and remaining risks.

## Architecture

- **[Posts module architecture](./architecture/posts-module.md)** — the go-kit layered
  structure, with a diagram.
- **[ADR 0001: go-kit for the posts module](./adr/0001-go-kit-for-posts-module.md)**

<!--
  A branching-strategy guide and ADR 0002 exist on the not-yet-merged
  feature/gitflow-branch-policy branch. Add links here once that PR is
  reviewed and merged — do not link to content that doesn't exist on main
  yet (docgen's checklinks would correctly fail the build if we did).
-->

## Generated reference (always in sync with code)

These two pages are regenerated directly from source by `docgen`
(`go run ./cmd/docgen generate all`); CI fails if they ever drift from the code they describe —
see `docs/doc-impact-map.json` and `.github/workflows/docs-check.yml`.

- **[API reference](./generated/API.md)**
- **[CI/CD workflow inventory](./generated/CI_WORKFLOWS.md)**

## This site

- **[GitHub Pages setup](./GITHUB-PAGES-SETUP.md)** — the one manual repository setting
  required before this site can actually go live, and what's automatic after that.

## Operational logs

- **[Cost reports](./cost-reports/README.md)**
- **[Incident notes](./incidents/README.md)**
