---
title: Production-grade standards
---

# Production-grade standards

This page states the engineering policy every ForgeStack module — Terraform, observability,
application services, and whatever databases/AI-agents/MCP servers/Kubernetes workloads get
added later — must meet before being called production-grade, and then audits the modules
that actually exist today against it. **"Production-grade" is a verified capability, not a
label**: a module only gets marked Implemented below if it was actually run and checked, not
just configured to look right.

## Evidence labels used below

Same convention as this repo's ADRs: **Implemented** (built and locally verified — commands
run, output checked), **Policy-only** (the rule is written here; nothing exists yet to apply
it to), **Deferred** (needs cloud access, a second real module, or realistic load to verify —
named explicitly, not silently skipped), **Not applicable** (the module in question doesn't
exist in this repository).

## The policy

1. **Every module distinguishes local development, temporary playground, and persistent
   production profiles.** A module's own docs/config must say which profile a given default
   belongs to.
2. **Local defaults never silently become production defaults.** No environment variable,
   compose file, or Terraform variable may default to something unsafe for production without
   an explicit, documented opt-in.
3. **Production configuration includes real authentication, authorization, TLS, secrets
   handling, resource limits, network isolation, and least privilege** — not "it worked when
   I ran it locally."
4. **Pin compatible dependency/image/provider/chart/module versions; prefer immutable
   digests** for anything actually deployed.
5. **Preserve existing deployments across upgrades.** Breaking changes, data migrations, and
   infra modifications need explicit review, not an assumed-safe `latest`.
6. **Observability production profile** — see the per-component list in the audit below;
   this is the most concretely testable item today since the stack already exists locally.
7. **Expose logs/metrics/traces uniformly** across every kind of service, without forcing
   every application to run every monitoring backend.
8. **Health/readiness checks, graceful shutdown, bounded timeouts/retries, backup/recovery,
   rollback, and operational runbooks** where the module's nature calls for them.
9. **Measurable SLOs, verified with realistic workload/failure/recovery testing.**
10. **Temporary environments get ownership, isolation, TTL, cost safeguards, cleanup
    reconciliation, and independently verified deletion** — never inferred from a successful
    `destroy`/`down` alone.
11. **Preserve existing CI/security gates; add integration and negative tests** for every new
    production-facing configuration, not just the happy path.
12. **A successful `docker compose up`, `helm install`, or `terraform validate` is never, by
    itself, a production-readiness claim.**
13. **State explicitly, per capability: implemented / locally verified / cloud-verified /
    production-verified / still planned.** No capability gets the benefit of the doubt.
14. **Prefer established upstream deployment patterns over a large bespoke infra stack.**
15. **Implement incrementally.** No rewriting working modules, no installing every tool by
    default "to be safe."

## Audit: what exists today, against that policy

### Terraform (`infrastructure/terraform/aws-s3-sandbox`)

- Profiles (1–2): the sandbox module is explicitly local/playground-only
  (`enable_resources = false` by default, never applied in CI). **Implemented.**
- Pinned versions (4): bounded Terraform CLI constraint, bounded provider constraint,
  committed multi-platform lockfile. **Implemented** — see
  `docs/adr/0007-terraform-module-versioning-and-change-governance.md`.
- Change safety (11): `scripts/test-terraform-safety-checks.sh` proves checksum-mismatch,
  incompatible-provider-version, and unsupported-CLI-version are all rejected before any
  plan/apply. **Implemented, locally verified.**
- Remote state, locking, least-privilege access, recovery (3 for this module): **Deferred** —
  no backend exists; a fabricated one would be worse than the honest gap.
- SLOs, realistic load testing (9): **Not applicable** — nothing is deployed to have an SLO
  against.

### Observability (`internal/platform/telemetry`, `observability/`)

Audited against policy item 6's explicit sub-list:

| Sub-item | Status |
|---|---|
| Grafana auth/RBAC/dashboard provisioning | Dashboard provisioning: **Implemented** (real datasources + a working dashboard, verified by direct API query). Auth/RBAC: **explicitly not implemented** — anonymous Admin access, local-dev-only, documented in `observability/README.md`'s "Security posture" section. |
| Loki authenticated ingestion/query, retention, ingestion limits | **Not implemented.** Current config has no auth, no retention policy beyond the image default, no ingestion limits. |
| Tempo retention, sampling, access restriction | **Not implemented.** 24h block retention is the only policy set; no sampling strategy, no access control. |
| Prometheus durable metrics, HA, recording rules, alerting | **Not implemented.** Single instance, local disk, no recording rules, no Alertmanager. |
| OTel Collector backpressure/batching/memory limits/retries/encryption | **Partially implemented**: a `batch` processor is configured; memory limiter, retry policy tuning, and TLS between the app and the collector are **not implemented** (plaintext OTLP, acceptable for loopback-only local traffic, not for anything beyond it). |
| Alertmanager routing/escalation/tested delivery | **Not implemented** — no Alertmanager in the stack at all. |
| Loopback-only port binding | **Implemented, verified** — every published port confirmed bound to `127.0.0.1` via both `docker compose config` and live `docker ps` output. |

- Tracing/metrics work without a collector (7): **Implemented, locally verified** — spans are
  created/sampled and `/metrics` is served regardless of whether `observability/compose.yaml`
  is running.
- Preserving CI gates while adding this (11): **Implemented** — `golangci-lint`, `govulncheck`
  (which caught and required fixing 5 real CVEs pulled in by this exact work), and the full
  existing test suite all stayed green; no check was weakened to land this.

### Application services (Go API + `templates/go-service`)

- Health/readiness/graceful shutdown/bounded timeouts (8): **Implemented** —
  `GET /healthz`/`GET /readyz`, `http.Server` read/write/idle timeouts, signal-based graceful
  shutdown with a bounded shutdown context, mirrored in the template.
- Pinned, digest-referenced images (4): **Implemented** — `Dockerfile` pins both build and
  runtime base images by digest, not a mutable tag.

### Databases, AI/RAG/agents/MCP, Kubernetes

**Not applicable.** None of these exist in this repository. This document states the policy
they must meet when they're built; it does not pre-build placeholder config for them, per
policy items 14–15.

## Next approval gate

Each **Deferred** item above names exactly what it needs (cloud access, a second Terraform
module, realistic load, or a specific missing component like Alertmanager) rather than being
silently dropped. None of them is implied to be "coming soon" — each is its own explicitly
scoped, separately approved increment.
