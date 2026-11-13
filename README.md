<p align="center">
  <img src="docs/assets/forgestack-banner.svg" alt="ForgeStack — build, test, observe, secure" width="760">
</p>

<h1 align="center">ForgeStack</h1>

<p align="center">
  <strong>A reusable, security-first engineering platform — not a toy, not a SaaS: a repository of real, reproducible CI/CD, supply-chain, and application-architecture practices you can inspect, run, and adopt.</strong>
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache_2.0-blue" alt="License: Apache 2.0"></a>
  <img src="https://img.shields.io/badge/Go-1.23%2B-00ADD8?logo=go&amp;logoColor=white" alt="Go 1.23 or newer">
  <img src="https://img.shields.io/badge/Python-3.11%2B-3776AB?logo=python&amp;logoColor=white" alt="Python 3.11 or newer">
  <img src="https://img.shields.io/badge/Docker-local_lab-2496ED?logo=docker&amp;logoColor=white" alt="Docker local lab">
  <img src="https://img.shields.io/badge/AWS-opt--in-FF9900" alt="AWS: opt-in exercises">
</p>

<p align="center">
  <a href="#quick-start">Quick start</a> ·
  <a href="#security-first-by-construction">Security</a> ·
  <a href="#implementation-status">Status</a> ·
  <a href="docs/ROADMAP.md">Roadmap</a> ·
  <a href="CONTRIBUTING.md">Contributing</a> ·
  <a href="SECURITY.md">Report a vulnerability</a>
</p>

---

## What this is

ForgeStack is a working reference implementation of the practices an enterprise engineering
team actually audits: branch-protected CI/CD, SAST/SCA/secret scanning on every change,
signed and validated SBOMs, documented vulnerability exceptions, and a modular, language-
agnostic service contract — demonstrated with real Go and Python reference services, not
diagrams. Everything below is either **verified** (built, run, and tested in this repository)
or explicitly marked **not yet verified** / **roadmap**. Nothing here claims a certification,
a security guarantee, or production deployment that hasn't actually happened.

It is also, honestly, a learning repository (see [`docs/ROADMAP.md`](docs/ROADMAP.md)'s full
25-module curriculum) — the two goals aren't in tension: the security/CI tooling below is the
same tooling a real product team would run, taught by actually running it.

## Security-first by construction

Every change to `develop`/`staging`/`main` runs through branch-protection rulesets (zero
bypass actors, 8 required status checks) and:

| Control | Tooling | Status |
|---|---|---|
| SAST | CodeQL (Go, Python, JavaScript/TypeScript) | **Verified** — 3 languages, see `.github/workflows/codeql.yml` |
| Dependency vulnerabilities (Go) | `govulncheck` (reachability-aware) | **Verified** — zero findings as of this writing |
| Dependency vulnerabilities (Python) | `pip-audit` | **Verified** — zero findings as of this writing |
| Dependency vulnerabilities (JS, build tooling) | `npm audit` against a reviewed, dated baseline | **Verified** — 26 pre-existing, documented, dev-tooling-only findings; no known safe fix yet |
| Secret scanning | `gitleaks` on every PR (that PR's commits); a deeper full-history scan is run locally/on demand, not automatically on every PR | **Verified** — zero leaks on every PR run to date; a full-history local scan also found zero |
| Container image scanning | Trivy (image + Dockerfile config) | **Verified** — Go image: 0 findings; Python image: 98 OS-package findings, all with no available upstream fix, tracked exception |
| SBOM generation | CycloneDX (Go module + both container images), validated against NTIA/CISA minimum elements | **Verified locally** — see `cmd/sbomcheck` |
| VEX vulnerability assessment | OpenVEX-shaped statements, evidence + reviewer + reassessment date required | **Verified locally** — see `cmd/secreport` |
| Artifact signing & provenance | Sigstore keyless signing, GitHub-native build attestation (SLSA-aligned) | **Signing/verify mechanics proven locally; the live keyless/OIDC flow has not yet run in GitHub Actions** |
| Supply-chain hardening | Digest-pinned base images, zero Linux capabilities, read-only filesystems, non-root execution | **Verified** — both services tested with `cap_drop: [ALL]` |

**Nothing above claims zero vulnerabilities, a passed audit, or a compliance certification.**
See [`docs/security/VULNERABILITY-LIFECYCLE.md`](docs/security/VULNERABILITY-LIFECYCLE.md)
for the full process and every tracked exception's exact evidence, owner, and review date,
and [`docs/architecture/SUPPLY-CHAIN-SECURITY.md`](docs/architecture/SUPPLY-CHAIN-SECURITY.md)
for trust boundaries, the compliance-evidence mapping (explicitly labeled evidence, not
certification), and this session's own honestly-stated limitations.

### Reproduce the security checks yourself

```bash
govulncheck ./...                                            # Go, reachability-aware
cd apps/python-api && pip-audit --requirement requirements-lock.txt
cd website && npm audit --json | python3 ../scripts/check-npm-audit-baseline.py /dev/stdin .security/npm-audit-baseline.json
go run ./cmd/secreport -govulncheck ... -pip-audit ... -json-out secreport.json -md-out secreport.md -vex-out vex.json
go run ./cmd/sbomcheck validate path/to/sbom.cdx.json
```

Full commands, including the container/secret scans, are in
[`docs/security/VULNERABILITY-LIFECYCLE.md`](docs/security/VULNERABILITY-LIFECYCLE.md#local-reproduction).

## Reusable, modular architecture

Two independent reference services, each idiomatic for its own language rather than forced
into a shared shape, both implementing the same documented cross-language contract
(`docs/platform/CONTRACTS.md`): health/readiness, structured JSON logging, request-correlation
IDs, and a consistent error-response shape.

- **Go** (`cmd/api`, `internal/modules/posts`, `internal/platform`) — go-kit layered,
  stdlib-first, in-memory storage, full unit/integration/concurrency test coverage.
- **Python** (`apps/python-api`) — FastAPI, typed config via `pydantic-settings`, the same
  contract implemented independently (not via a shared library — see
  `docs/platform/CONTRACTS.md` for why).
- **Adding a third language or service** doesn't require touching the first two — see
  `docs/platform/CONTRACTS.md`'s extension guidance.

### Start a new product from a template

```bash
scripts/new-service.sh go /path/to/new-project --name my-service --port 9090 \
  --module github.com/you/my-service --validate
scripts/new-service.sh python /path/to/new-project --name my-service --port 9090 --validate
```

Generates a complete, standalone project (health/readiness, correlation IDs, structured
logging, a hardened Dockerfile, self-contained CI) with **no dependency on ForgeStack's own
repository** — verified by actually generating, building, testing, and running example
projects from both templates. `--validate` builds and tests the generated project in
place before you touch it. See [`docs/VERSIONING.md`](docs/VERSIONING.md) for template
versioning, compatibility, and upgrade policy, and
[`docs/RELEASES.md`](docs/RELEASES.md) for how ForgeStack's own trusted-release pipeline
reuses the same SBOM/signing tooling (not yet exercised on a live tag push).

## Quick start

No AWS account, no cloud bill, no signup.

```bash
# Go reference service
go test ./...
go run ./cmd/api
curl -i http://localhost:8080/healthz

# Python reference service (in a second terminal)
cd apps/python-api && pip install -r requirements-lock.txt
uvicorn app.main:app --port 8081
curl -i http://localhost:8081/healthz

# both, containerized, hardened (non-root, read-only, zero capabilities)
docker compose up --build -d api python-api
curl -i http://localhost:8080/healthz && curl -i http://localhost:8081/healthz
docker compose down
```

`make test`, `make race`, `make vet`, `make lint`, `make smoke` also work for the Go service.
See [`docs/COMMANDS.md`](docs/COMMANDS.md) for every command this repository uses, explained.

## Implementation status

| Phase | Scope | Status |
|---|---|---|
| F1 | Branch-protected gitflow (`feature/* → develop → staging → main`), CI baseline | **Implemented**, live on GitHub (see `docs/adr/0002-gitflow-branching-strategy.md`) |
| F2 | Documentation-as-code (this Docusaurus site, generated references, link/diagram validation) | **Implemented**, live on GitHub Pages (`docs/adr/0003-documentation-as-code.md`) |
| F3 | Go + Python application foundation, shared service contract | **Implemented**, live on `main` (`docs/adr/0004-f3-application-foundation.md`) |
| F4 | Security & supply-chain foundation (this document's table above) | **Implemented and live on `main`**, including a verified live run of keyless signing + GitHub-native attestation (`docs/adr/0005-f4-security-supply-chain.md`) |
| F5 | Reusable Go/Python templates, SemVer policy, trusted-release pipeline | **Implemented locally**, not yet pushed; release pipeline verified with local fixtures only, not a live tag push (`docs/adr/0006-f5-templates-releases.md`) |
| F6–F20 | Future phases (not yet scoped in detail; see `docs/ROADMAP.md`) | **Not started** |
| Audit & Trust module | Evidence-collection expansion (infra/access/incident/backup), later a customer-facing Trust Center | **Roadmap only**, unscheduled against a specific F-number — see `docs/ROADMAP.md`'s "Audit & Trust" module |

ForgeStack never self-issues a certification. If an independent audit is ever obtained for a
product built on ForgeStack, it is tracked as an external fact (issuing authority, verified
scope, issuance/expiry dates) — never granted by this repository's own tooling.

## Repository layout

- `cmd/api`, `internal/modules/posts`, `internal/platform`: Go reference service and tests
- `apps/python-api`: Python (FastAPI) reference service
- `cmd/secreport`, `internal/secreport`: security report + VEX document generator
- `cmd/sbomcheck`, `internal/sbomcheck`: SBOM minimum-elements validator/augmenter
- `cmd/semvercheck`, `internal/semver`: SemVer validation and compatibility classification
- `templates/go-service`, `templates/python-service`: reusable project templates, each with
  its own nested `go.mod`/`pyproject.toml` so they're never part of this repository's own
  build
- `scripts/new-service.sh`: generates a new, standalone project from a template
- `cmd/docgen`: generates `docs/generated/*` directly from source; CI fails if these drift
- `compose.yaml`, `Dockerfile`, `apps/python-api/Dockerfile`: hardened local containers
  (digest-pinned, non-root, read-only, zero Linux capabilities)
- `infrastructure/terraform/aws-s3-sandbox`: safe-by-default opt-in Terraform example
- `website/`: Docusaurus site that publishes `docs/` to GitHub Pages
- `.github/workflows`: CI, lint, SAST, secret/container/IaC scanning, SBOM generation,
  signing/attestation, weekly deep security rescan, docs-check, Pages deploy, and a
  tag-gated trusted-release pipeline (`release.yml`, not yet exercised) — no cloud
  auto-deploy anywhere
- `docs/security/`: vulnerability lifecycle, findings register
- `docs/architecture/`: trust boundaries, supply-chain security, application architecture
- `docs/adr/`: every accepted architecture decision, with the tradeoffs considered
- `docs/ROADMAP.md`: the full learning curriculum and the Audit & Trust module plan

## PR safety, code review and CI

```bash
python3 scripts/review_guard.py --ci
make test && make race && make vet && make lint
```

Every PR to `develop`/`staging`/`main` runs the required checks (`go-quality`,
`security-baseline`, `analyze`, `gitleaks`, `trivy-image`, `validate`, `docs`, `check`) plus
Python quality/security gates, hadolint, and an npm-audit baseline check. See
[`docs/AUTOMATED-CODE-REVIEW.md`](docs/AUTOMATED-CODE-REVIEW.md) for exactly which checks are
required vs. advisory.

## Documentation site

`docs/` is published as a browsable Docusaurus site via GitHub Pages — nothing is duplicated,
so the site can't drift from the repository. Build it locally:

```bash
cd website
npm ci
npm run build
```

## AWS + Terraform (no automatic charges)

**No AWS account needed for anything above.** The Terraform sample in
`infrastructure/terraform/aws-s3-sandbox` creates zero resources unless explicitly opted in;
GitHub workflows never run `terraform apply`/`destroy`. See `docs/COST-SAFETY.md`.

## Community and license

Contributions are welcome — read the [contribution guide](CONTRIBUTING.md) and
[code of conduct](CODE_OF_CONDUCT.md). To report a vulnerability, use the private process in
[`SECURITY.md`](SECURITY.md) (GitHub Security Advisories) — not a public issue.

ForgeStack is licensed under the [Apache License 2.0](LICENSE).
