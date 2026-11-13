# ADR 0006: F5 reusable templates, versioning, and trusted releases

- Status: Implemented locally only (`feature/f5-templates-releases`), not pushed.
- Date: 2026-11-13

## Context

F5 asked for reusable product templates, a SemVer/compatibility policy, and a trusted
release pipeline reusing F4's verified security components — without a large CLI
framework or premature abstraction.

## What was audited first

`cmd/api`/`internal/modules/posts`/`internal/platform` are deeply import-coupled to
`github.com/mangopep/forgestack` — not directly reusable as a template without rewriting
every import. `internal/platform/middleware.go` and `internal/platform/apierror` have
**zero** ForgeStack-specific imports — genuinely reusable verbatim. `apps/python-api`'s
`config.py`/`middleware.py`/`logging_config.py`/`errors.py` are equally generic; only
`items.py`/`store.py` are app-specific (not copied into the template).

## Decision: templates are plain file trees with a nested `go.mod`, not a code-gen library

`templates/go-service/` has its own `go.mod` (module path literally `__MODULE_PATH__`,
a placeholder, not a real importable path). Verified this doesn't break anything:
`go build/vet/test ./...` and `golangci-lint run ./...` at the repository root all
silently skip it — Go's module system excludes any directory containing its own
`go.mod` from a parent module's `./...` pattern, regardless of whether that nested
`go.mod`'s content is itself valid outside a generation context. This was the deciding
factor over keeping the template as `.tmpl`-suffixed files only: a real (if placeholder-
valued) `go.mod` is simpler and self-documents the module boundary.

## Decision: one bash generator script, not a framework

`scripts/new-service.sh <go|python> <dir> --name --port [--module] [--features] [--validate]`.
Explicit validation (name pattern, port range, non-empty output dir, language), explicit
feature list (`base,docker,ci`, independently selectable — `--features base` produces an
app with no Dockerfile/CI, verified), explicit `--validate` flag that builds/tests the
generated project in place. No plugin system, no templating engine dependency — `sed`
token substitution across the copied tree. This is deliberately the simplest thing that
satisfies "independently selectable capabilities without requiring unrelated modules" —
advanced module composition is explicitly F15's concern, not duplicated here.

## Verified by actually generating and running two projects, not just reading the script

`widgets-go` (Go, port 9090) and `widgets-py` (Python, port 9091): both generated, both
built/tested (`--validate`), both run standalone and respond correctly on `/healthz`/
`/readyz` with the generated service name baked in (not a placeholder leak), both Docker
images build and run as non-root. Six negative-input cases (bad name, bad port, missing
required `--module`, missing `--name`, non-empty existing output dir, unsupported
language) all rejected with a clear message and nonzero exit — tested, not assumed.

## Decision: four distinct things get versioned, not one blanket "project version"

Templates, the cross-language contract, published APIs, and releases each have their own
SemVer meaning (see `docs/VERSIONING.md`'s table) — collapsing them into one version
number would make "breaking" ambiguous (a template MAJOR bump and an API MAJOR bump are
different events with different consequences for different people).

## Decision: `cmd/semvercheck` validates and classifies, it does not gate every PR

It's a small, tested library (`internal/semver`, SemVer 2.0.0 grammar, 6 test cases
covering valid/invalid/breaking/compatible/patch/equal/downgrade) plus a CLI wrapper,
wired into `release.yml`'s `validate-tag` job (rejects a non-SemVer tag before anything
else runs) and available for manual use when deciding whether a template/contract change
needs a MAJOR bump. It is not a merge-blocking check on ordinary PRs — that would be
scope beyond what F5 asked for.

## Decision: generated projects are never auto-rewritten when a template updates

Recorded explicitly in `docs/VERSIONING.md`'s breaking-change policy #3. The alternative
(a "sync" command that reaches back into an already-generated project) was considered and
rejected: silently rewriting someone's code because the template changed is a worse
failure mode than requiring a manual, reviewed merge — matches "never silently rewrite
existing products" from F5's own instructions.

## Decision: `release.yml` reuses F4's exact SBOM/signing tools, applied to release artifacts

`cmd/sbomcheck`, `anchore/sbom-action`, `cosign`, `actions/attest-build-provenance` — the
same tools `sbom.yml`/`supply-chain-attest.yml` already use, not a second parallel
pipeline. The only new logic is `validate-tag`'s duplicate-release guard (`gh release
view` before anything builds) and signing the **pushed image's resolved digest**
(`docker inspect --format '{{index .RepoDigests 0}}'`) rather than the tag, so the
signature is bound to immutable content even if the tag were ever reused.

## Decision: privileged credentials live only in the `publish` job, gated by tag push

Same trust-boundary pattern already verified live in F4's `supply-chain-attest.yml`:
elevated `permissions:` (`contents: write`, `packages: write`, `id-token: write`,
`attestations: write`) are declared on the `publish` job only, and the workflow as a
whole has no `pull_request` trigger at all — a fork PR cannot reach this job under any
GitHub-supported circumstance.

## What's verified vs. not, stated honestly

**Verified this phase** (local fixtures, real tool runs, not CI): `cmd/semvercheck`
(unit tests + CLI), `cmd/sbomcheck` against a freshly built local image, `cosign
sign-blob`/`verify-blob` with a local key-pair (generated, used, deleted within the same
session) against the fixture SBOM — both the signing and verification succeeded, and the
key was never committed. Both example projects: generated, built, tested, run, and
Dockerized successfully.

**NOT YET VERIFIED** (requires a real tag push and registry/release credentials, neither
of which this phase performs): `.github/workflows/release.yml` end-to-end — the duplicate-
release guard, the actual GHCR push, the live keyless signing of a pushed digest, and
GitHub Release creation have never executed. This mirrors F4's own honest "not yet
verified" treatment of `supply-chain-attest.yml` before its first live run.

## Consequences

- New: `templates/go-service/`, `templates/python-service/`, `scripts/new-service.sh`.
- New: `cmd/semvercheck` + `internal/semver` (tested).
- New: `.github/workflows/release.yml` (not yet executed).
- New: `docs/VERSIONING.md`, `docs/RELEASES.md`.
- No existing F1-F4 file modified except documentation cross-references.
- Nothing pushed; no AWS, GHCR, or GitHub Release touched.
