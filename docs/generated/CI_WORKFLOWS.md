<!-- GENERATED FILE. Do not edit by hand. -->
<!-- Regenerate with: go run ./cmd/docgen generate ci -->

# CI/CD Workflow Inventory (generated)

Extracted directly from `.github/workflows/*.yml` by line-oriented parsing of `name:` and the trigger keys under `on:` — not a full YAML parse, but sufficient for an inventory. If a workflow's triggers aren't listed correctly here, check the raw file; this is a summary, not a substitute for reading it.

| Workflow file | Name | Triggers |
|---|---|---|
| `automated-review.yml` | Automated PR Code Reviewer | pull_request |
| `branch-policy.yml` | Branch Flow Policy | pull_request |
| `ci.yml` | CI and Quality Gates | push, pull_request |
| `codeql.yml` | CodeQL Security Analysis | push, pull_request, schedule |
| `container-scan.yml` | Container Image Scan (no push) | push, pull_request |
| `docs-check.yml` | Documentation Checks | push, pull_request |
| `pages-deploy.yml` | Deploy Docs to GitHub Pages | push, workflow_dispatch |
| `python-ci.yml` | Python API Quality Gates | push, pull_request |
| `release.yml` | Trusted Release | push |
| `sbom.yml` | SBOM Generation | push, workflow_dispatch |
| `secret-scan.yml` | Secret Scanning | push, pull_request |
| `security-report.yml` | Security Report | push, workflow_dispatch, schedule |
| `supply-chain-attest.yml` | Supply-Chain Signing & Provenance | push, workflow_dispatch |
| `terraform-checks.yml` | Terraform Checks (no deploy) | push, pull_request |
