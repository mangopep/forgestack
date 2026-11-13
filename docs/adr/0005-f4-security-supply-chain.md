# ADR 0005: F4 enterprise security and software supply chain

- Status: Implemented locally only (`feature/f4-security-supply-chain`), not pushed.
- Date: 2026-11-13

## Context

F4 asked for security scanning coverage, SBOM generation, artifact signing/provenance,
VEX-compatible vulnerability assessment, and a small report generator — reusing existing
tools rather than building parallel systems, and never claiming a capability the evidence
doesn't support.

## What was audited first

F1-F3 already had a solid baseline: `go-quality`/`security-baseline` (gofmt, vet,
golangci-lint, `go test -race`, `govulncheck`), CodeQL (Go only), `gitleaks` (full
history), Trivy (both container images, Terraform config), least-privilege `permissions:`
on every workflow, SHA-pinned actions, Dependabot (gomod/github-actions/docker/npm).
**Decision: extend this, not replace it** — real gaps closed, nothing working
restructured.

## Decision: CodeQL language expansion via new sibling jobs, not a matrix

The `analyze` job is a required status check on all three branch rulesets. A
`languages:` matrix would rename it to `analyze (go)`, which is a required-status-check
rename F4 itself says needs separate governance approval. Added `analyze-python` and
`analyze-javascript` as new, advisory (not required) sibling jobs instead — same
coverage goal, zero rename.

## Decision: npm audit via a reviewed, dated baseline, not a blanket gate

`website/`'s 26 pre-existing findings (issue #35) have no safe fix (npm's own suggestion
is a Docusaurus downgrade). A blanket `npm audit --audit-level=high` would permanently
fail every PR on findings nobody can fix yet. `scripts/check-npm-audit-baseline.py`
compares the real, full `npm audit --json` output against
`website/.security/npm-audit-baseline.json`: new findings beyond the baseline still fail
the build; an expired `review_by` also fails the build (forces re-review, doesn't let the
exception silently persist). Verified with 3 negative tests (new findings, tampered-
lower baseline, expired date) before wiring into CI.

## Decision: hadolint + Trivy config scan for both Dockerfiles, with one documented ignore

`DL3025` (shell-form `ENTRYPOINT`) is ignored **only** on `apps/python-api/Dockerfile`,
because that shell form is required for `${PORT}` expansion — a real bug already caught
and fixed in F3 (ADR 0004). `failure-threshold: warning` on both Dockerfiles (not the
tool's own "info" default) so `DL3066` (non-numeric `USER`, intentional — matches
`gcr.io/distroless/static-debian12:nonroot`'s own convention) doesn't fail the build.
Verified locally with the `hadolint` CLI using the exact same flags before wiring into CI.

## Decision: base images pinned by digest, not just tag

A tag (`golang:1.27.4-alpine`, `python:3.12-slim-bookworm`) can be silently repointed; a
digest cannot. Both Dockerfiles now pin `@sha256:...` alongside the tag (kept as a
comment for readability and for Dependabot's existing docker-ecosystem PRs to still
target). Verified both images still build and both services still pass the existing
health/readiness smoke test after pinning.

## Decision: `cap_drop: [ALL]` on both Compose services

Neither the Go binary nor uvicorn needs any Linux capability (no setuid, no
privileged-port binding, no raw sockets). Verified both services still start, pass
health/readiness, and the Python service's Docker `HEALTHCHECK` reports `healthy` with
zero capabilities and a read-only filesystem.

## Decision: `cmd/secreport` is a small aggregator, not a dashboard

It parses real saved scanner output (govulncheck's multi-document JSON stream,
pip-audit JSON, Trivy JSON, gitleaks JSON, npm audit JSON + baseline) into one stable,
versioned schema (`internal/secreport.Report`) plus a Markdown rendering. It runs no
scanners itself. `computeOverallStatus` enforces F4's one named rule directly in code,
with a dedicated test: **never `PASS` when a required tool's evidence is missing** — a
tool with `StatusMissing`/`StatusParseFailed` always yields `BLOCKED`, never `PASS`,
regardless of what findings (or lack of findings) exist elsewhere.

A real bug was caught and fixed during this work: every parser set
`result.Status = StatusRan` **before** checking whether `readTool` had actually
succeeded, silently overwriting a legitimate `StatusMissing` on a missing-evidence path.
Caught by `TestParseGovulncheckMissingFileReportsMissing` failing on the first run, not
assumed correct from the code alone — fixed in all four affected parsers.

## Decision: VEX statements claim "affected," never "not_affected," without per-CVE analysis

`internal/secreport/vex.go`'s `BuildVEX` generates one OpenVEX-shaped statement per
suppressed/unfixed Trivy finding and per tracked Exception (#35, #40). It deliberately
never claims `not_affected` for the 98 individual CVEs under #40 — that per-CVE
reachability analysis was not actually done for each one individually (it was done,
narrowly, for the exception as a whole: no `kadmind`/KDC, `sqlite`, or `mount` usage in
this service). Overclaiming `not_affected` would be exactly the "automatic suppression
from VEX claims" F4 explicitly forbids. `ValidateVEX` rejects any statement missing
evidence, a reviewer, or a valid/unexpired reassessment date — proven with negative
tests (missing evidence, missing reviewer, expired date).

## Decision: SBOM validation treats syft's per-file entries correctly

The first SBOM validation run against a real, locally-generated SBOM
(`sbom-go-image.cdx.json`, 961 entries) failed `component_name_and_version` — caught
before assuming the validator was right: 950 of those 961 entries are syft's `type:
"file"` per-file provenance records (every file in the container image, tracked for
hashing), which legitimately have no version, license, or package identifier. Fixed
`internal/sbomcheck.Validate` to apply the name/version/identifier/license checks only to
actual package-type components (11 real components: `base-files`, `ca-certificates`,
3 Go modules, `media-types`, `netbase`, Go `stdlib`, `tzdata`, and `debian` itself), while
still counting hash coverage across every entry (files legitimately can and do have
hashes). Confirmed real, honest coverage gaps this surfaced: Go-module SBOM license
coverage is 1/422 (Go modules rarely carry machine-readable license metadata without a
deeper scan) — reported, not hidden.

## Decision: SBOM augmentation operates on the raw JSON tree, not a narrow typed struct

`internal/sbomcheck.Augment` injects `metadata.authors` (producer) and
`forgestack:repository`/`forgestack:commit` properties syft doesn't set on its own. The
first draft round-tripped through this package's own (necessarily incomplete) `CycloneDX`
struct, which would have silently dropped every CycloneDX field that struct doesn't
model (descriptions, external references, scopes, ...) on every augmented SBOM. Rewritten
to parse into `map[string]any` and mutate only the specific `metadata.*` paths needed,
verified with a dedicated test that an unmodeled field (`description`) survives
augmentation unchanged.

## Decision: signing/attestation scoped to `push: [main]` only, keyless, no stored keys

`supply-chain-attest.yml` never runs on `pull_request` — a fork PR must never receive
`id-token`/`attestations` write access. Uses Sigstore **keyless** signing (short-lived
OIDC identity per run) and GitHub's native `actions/attest-build-provenance`, so there is
no long-lived signing credential to leak, rotate, or accidentally commit. No container
image is pushed to any registry (unchanged from F3), so this attests the artifacts that
genuinely exist: the CycloneDX SBOMs, augmented and validated before signing.

The *mechanics* of `cosign sign-blob`/`verify-blob` were proven locally this session with
a throwaway local key-pair (generate, sign, verify positive case, verify tampered-content
negative case, verify wrong-key negative case — all behaved correctly), then the private
key material was deleted within the same session. This is **not** the same as verifying
the keyless/OIDC flow itself, which only a real GitHub Actions run can exercise — recorded
honestly as **not yet verified**, not glossed over.

## Decision: compliance mapping is evidence, explicitly not certification

`docs/architecture/SUPPLY-CHAIN-SECURITY.md`'s compliance table maps existing technical
controls to SOC 2/ISO 27001/NIST SSDF/PCI DSS control families, with an explicit,
un-missable disclaimer that no control has been independently audited and this is not a
compliance claim — per F4's own instruction to "distinguish technical evidence from
actual certification or compliance."

## Deferred, explicitly, per F4's own scope

Dependency-Track, CBOM, SaaSBOM, AI/ML-BOM — recorded as future integration points in
`SUPPLY-CHAIN-SECURITY.md`, not implemented (no current need justifies them). SPDX
format alongside CycloneDX — redundant with CycloneDX for this repository's needs, not
added (F4: "do not add redundant scanners/tools"). Any GitHub ruleset/settings change —
untouched this phase.

## Consequences

- New: `cmd/secreport` + `internal/secreport` (report, parsers, VEX — all tested).
- New: `cmd/sbomcheck` + `internal/sbomcheck` (CISA/NTIA validation + augmentation — all
  tested).
- New: `.github/workflows/sbom.yml`, `supply-chain-attest.yml`, `security-report.yml`.
- Modified: `codeql.yml` (new sibling jobs), `docs-check.yml` (npm audit baseline step),
  `container-scan.yml`/`python-ci.yml` (hadolint + Trivy config steps), both
  `Dockerfile`s (digest pinning), `compose.yaml` (`cap_drop: [ALL]`).
- New: `docs/security/VULNERABILITY-LIFECYCLE.md`, `docs/security/findings-register.json`,
  `website/.security/npm-audit-baseline.json`,
  `docs/architecture/SUPPLY-CHAIN-SECURITY.md`.
- No existing required status-check name changed. No GitHub ruleset/settings touched.
  Nothing pushed.
