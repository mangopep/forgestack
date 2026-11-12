# ForgeStack Production Readiness — Gap Analysis

Date: 2026-11-12. This is Phase 1 only (audit + roadmap), per instruction: **no infrastructure
changes or pushes until explicitly approved.** Nothing in this document has been implemented
yet unless marked "Implemented."

Evidence labels used throughout: **Confirmed from code**, **Confirmed from CI** (live GitHub
check run), **Confirmed from config**, **Design/documentation only** (planned, not built),
**Missing**.

## 1. Current CI status (confirmed from CI, live as of this audit)

| Workflow | Job | Last known state |
|---|---|---|
| CI and Quality Gates | go-quality, security-baseline | success (in progress on latest commit at time of writing — actions/checkout was just bumped to v7) |
| CodeQL Security Analysis | analyze | success |
| Terraform Checks | validate (incl. Trivy IaC scan) | success — previously broken, root cause and fix documented in `docs/SECURITY-AUDIT.md` (PR #6, merged) |
| Container Image Scan | trivy-image | success |
| Secret Scanning | gitleaks | success |
| Automated PR Code Reviewer | reviewdog, fork-safe-review | success / correctly skipped on non-fork PRs |

Branch protection on `main` exists (confirmed: unauthenticated API call to
`/branches/main/protection` returns `401` rather than `404`, and the PR UI shows "At least 1
approving review is required by reviewers with write access"). **Who configured it is not
confirmed** — not done by this assistant; flagged previously, still unconfirmed.

PRs #1, #2, #3, #5, #6 are merged. PR #4 (`hashicorp/setup-terraform` 3.1.2→4.0.1) is still open.

## 2. What's already implemented (confirmed from code)

| Area | Status | Evidence |
|---|---|---|
| Go quality gates | Implemented | `ci.yml`: gofmt, go vet, go build, `go test -race -cover` |
| SAST | Implemented (CodeQL only) | `codeql.yml` |
| Lint | Implemented | `automated-review.yml` via reviewdog/golangci-lint |
| Dependency vuln scan (Go) | Implemented | `ci.yml` govulncheck-action |
| Secret scanning | Implemented (2 layers) | `secret-scan.yml` (gitleaks) + `scripts/review_guard.py` (narrow baseline: AWS keys, PEM headers, `.env`/`.tfstate` tracking) |
| Container image scanning | Implemented | `container-scan.yml`, Trivy, builds locally, no registry push |
| IaC scanning | Implemented | `terraform-checks.yml`, Trivy config mode |
| Dependency update automation | Implemented | `.github/dependabot.yml`: gomod, github-actions, docker, weekly |
| Supply-chain: action pinning | Implemented | all 5 workflows pin third-party Actions to commit SHA |
| CODEOWNERS | Implemented | `.github/CODEOWNERS` → `@mangopep` |
| OSS hygiene files | Implemented | LICENSE (Apache-2.0), CONTRIBUTING.md, SECURITY.md, CODE_OF_CONDUCT.md, issue templates |
| Container hardening | Implemented | `Dockerfile`: multi-stage, `CGO_ENABLED=0`, distroless `nonroot` base, no shell in final image |
| Compose hardening | Implemented | `compose.yaml`: `read_only: true`, `no-new-privileges:true`, ports bound to `127.0.0.1` only |
| Basic app-level input validation | Implemented | `internal/api/api.go`: body size cap (4096 bytes), strict JSON decoding (`DisallowUnknownFields`, single-object check), field length limits, bounded pagination (`limit` 1–100) |
| Structured logging | Implemented (partial) | `slog` JSON-shaped request logs in `api.go`; no redaction policy defined, no correlation/trace IDs |
| Graceful shutdown | Implemented | `cmd/api/main.go`: `signal.NotifyContext`, 10s shutdown timeout |
| Health checks | Implemented (minimal) | `/healthz`, `/readyz` — both static, don't check real dependencies (there are none wired yet) |

## 3. Partially implemented / present but shallow

| Area | Gap |
|---|---|
| SAST | CodeQL only — **no Semgrep**, despite being requested. Semgrep adds different rule coverage (OWASP-style custom rules) CodeQL doesn't cover well for small Go services. |
| Filesystem/dependency scanning via Trivy | Trivy currently used for container image + Terraform config only. **Not run in filesystem mode against the repo itself** (would catch, e.g., vulnerable vendored files or misconfigured Dockerfiles beyond what's scanned now). |
| Metrics | `/metrics-demo` is a fake counter explicitly labeled "demo"; **not Prometheus-format, not real metrics**. |
| Secret detection | Two baseline layers exist, but narrow. No scanning of git *history* (gitleaks is configured with `fetch-depth: 0` so it does scan history — this is actually covered, correcting an undersell). |

## 4. Missing entirely (confirmed from code — absent)

### Security / supply chain
- **Semgrep** — not configured anywhere.
- **SBOM generation** — no `syft`/`cyclonedx`/similar step anywhere.
- **Artifact signing / provenance (SLSA, cosign, in-toto)** — none. There's also no publish/release pipeline yet for anything to sign.
- **License/dependency policy checks** — none (e.g. no `go-licenses`, no OSS license allowlist).
- **API contract tests, authN/authZ tests, OWASP API Top 10 test suite** — none. (Consistent with reality: the API has no authentication at all — intentional per `README.md`, documented as "do not expose publicly.")

### Compliance engineering
- **No `compliance/` directory, no control registry, no framework mappings** (SOC 2, ISO 27001, ISO 27701, DPDP, GDPR, ASVS, NIST SSDF, CIS, SLSA, ISO 42001) — none exist.
- **No evidence collection automation** beyond the CI artifacts GitHub already stores.

### Observability
- **No OpenTelemetry instrumentation or Collector.**
- **No Loki, Prometheus, Tempo, or Grafana** — `compose.yaml` only has Postgres/Redis/ClickHouse (and those aren't wired into the API either).
- **No correlation/trace IDs**, no redaction policy for structured logs.

### Delivery environments / release engineering
- **No staging/production environment distinction** — this is a single local/CI lab today.
- **No Helm charts, no Argo CD, no Kubernetes manifests at all.**
- **No release process**: no semantic versioning, no CHANGELOG, no GitHub Releases, no immutable GHCR image publish. (This matches your earlier Phase 2 ask from a previous message in this session — not started.)
- **No OIDC federation** — there is no deploy workflow to federate from yet; AWS access, if it existed, would currently have to use long-lived keys, which is exactly why none exists.

### Reliability
- **No rate limiting / backpressure.**
- **No retry/idempotency patterns** (there's nothing downstream yet to retry against).
- **No SLOs/error budgets, no alerting, no incident runbooks** beyond `docs/incidents/README.md` (currently a stub).
- **No load testing, no chaos/failure-injection tooling wired up** (the `/break-and-fix` Claude skill exists as a *process* for doing this manually, but no automated chaos tooling).
- **No backup/restore testing** — nothing persists yet (in-memory store), so there's nothing to back up.

## 5. Why some of these are correctly absent, not failures

Several "missing" items are missing **because their prerequisite doesn't exist yet**, not because of an oversight:
- No OIDC/deploy secrets/rollback procedure — because there is no deploy pipeline (by explicit design, per `README.md` and `CLAUDE.md`).
- No backup/restore testing — because there is no persisted data yet (Postgres/Redis/ClickHouse are running but unconnected).
- No API auth tests — because there is intentionally no auth yet; adding auth tests before auth exists would test nothing real.

This matters because the roadmap below sequences things in dependency order, not in the order they were listed.

## 6. Prioritized roadmap (proposed, not started)

1. **Observability foundation** — wire real Prometheus-format `/metrics` (replace the demo counter), add OpenTelemetry SDK instrumentation for HTTP handlers (latency, error rate, in-flight requests), add trace/correlation ID propagation into the existing `slog` logs. This unlocks everything in reliability (SLOs need real metrics first).
2. **Reliability on top of real metrics** — rate limiting middleware, request-scoped timeouts (partially present via `http.Server` timeouts already), basic SLO definitions once metrics exist, a documented local failure-injection experiment proving logs/metrics/traces actually surface a real induced fault.
3. **Expand SAST/scanning breadth** — add Semgrep (OWASP-focused ruleset) alongside CodeQL; add Trivy filesystem-mode scan of the repo itself; add `go-licenses` for dependency license checks.
4. **Supply-chain provenance** — only meaningful once there's something to publish. Sequence this with the release pipeline (SBOM + cosign signing + SLSA provenance generated at the same time a GHCR image is first published).
5. **Release engineering** — semantic versioning, CHANGELOG, GitHub Releases, immutable GHCR image, built only from protected-branch commits after mandatory checks pass.
6. **Compliance control registry** — build the `compliance/` structure with a *small, real* set of controls mapped to what's actually implemented by step 3 (e.g., "secret scanning" → gitleaks evidence from CI run URLs), explicitly marking every unimplemented control as **Missing**, not partially done. Full framework coverage (all 10 frameworks listed) is unrealistic as a single milestone — proposed as several small milestones, one framework-family at a time, starting with OWASP ASVS/API Top 10 (most directly tied to code that exists) and SLSA (most directly tied to the release pipeline being built anyway in step 5).
7. **Staging environment + Argo CD/Helm** — deferred until there's a real deployable artifact (step 5) and a reason to have more than one environment (a lab API with no users doesn't need staging yet; proposed as a learning exercise once the release pipeline exists, using a local kind/minikube cluster, not paid AWS infra).

## 7. Proposed first implementation milestone

**Milestone 1: real metrics + minimal OpenTelemetry tracing, replacing the fake `/metrics-demo`.**

Why this one first: it's small, testable, touches no infrastructure, requires no new AWS/cloud
resources, and is the direct prerequisite for the reliability and SRE work (SLOs, alerting,
load-test interpretation) requested later. Concretely, if approved:

- Add `prometheus/client_golang` (one new, well-known dependency; justified per `go.md` rule).
- Replace `/metrics-demo` with a real `/metrics` endpoint exposing request count, latency
  histogram, and in-flight gauge, labeled by route and status code.
- Add minimal OpenTelemetry SDK setup (trace-only, local stdout/OTLP exporter — no new
  infrastructure required to try it locally; Tempo/Grafana wiring is a separate, later step).
- Add a unit test asserting `/metrics` returns valid Prometheus exposition format after a few
  requests.
- Add a short `docs/OBSERVABILITY.md` explaining what was added, why, how to view it locally
  (`curl localhost:8080/metrics`), and how to clean it up (it's just a Go dependency + code —
  removing it is a revert).
- Open as a small PR, same verification bar as the CI-repair PR (tests, gofmt, vet, race,
  review_guard, local reproduction) — not merged without approval.

**Not started. Awaiting your go-ahead before writing any code.**
