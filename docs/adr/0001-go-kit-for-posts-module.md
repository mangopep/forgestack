# ADR 0001: Adopt go-kit (endpoint + transport/http) for the posts module

- Status: Accepted, with conditions (see Risks)
- Date: 2026-11-12

## Context

ForgeStack's Go API currently implements the one real resource (`posts`) as a single file
(`internal/api/api.go`) mixing HTTP routing, request decoding, validation, business logic, and
an in-memory store together. This milestone's instruction is to refactor it into a modular,
layered architecture — domain / service / endpoint / transport / repository — using **go-kit**
as the architectural framework, explicitly selected in advance.

Before writing any code, this ADR records the verification the instruction required: current
go-kit compatibility, dependency security, and API stability.

## Verification performed (evidence-based, this session)

1. **Release/maintenance activity** (confirmed via GitHub API, `api.github.com/repos/go-kit/kit`):
   latest tag `v0.13.0`, published 2023-08-25. Last push to the repository: 2024-07-19. Not
   archived. 62 open issues. License: MIT. As of this ADR's date, that is roughly 2.3 years
   since the last commit.
2. **API stability**: the module has never progressed past `v0.x` across 13 years of history
   (`v0.1.0` through `v0.13.0`). Under semver, a `v0.x` module carries no API-stability
   guarantee. This is the one concrete risk this ADR cannot resolve by inspection — it's a
   property of the project, not something we can verify away.
3. **Dependency footprint — verified empirically, not assumed**: go-kit/kit's own `go.mod`
   lists dozens of heavyweight dependencies (AWS SDK, Consul, NATS, RabbitMQ, etcd, gRPC,
   Zipkin, Casbin, Hystrix, ...) because the module bundles every optional integration
   go-kit offers. To check whether using it would actually pull those in, a throwaway module
   was created (outside this repository) importing only `github.com/go-kit/kit/endpoint` and
   `github.com/go-kit/kit/transport/http`, then `go mod tidy` was run against it. Result:
   **3 modules total** — `go-kit/kit`, `go-kit/log`, `go-logfmt/logfmt`. None of the heavy
   dependencies were pulled in, because Go's module graph pruning (Go ≥1.17) only resolves
   the dependencies of packages actually imported, not every package in the target module.
4. **Known vulnerabilities**: `govulncheck` against that same probe module found 18 findings —
   all 18 traced to the *local Go toolchain's standard library* (`go1.26.1`, several patch
   versions behind what this repo's CI already pins — `1.27.4` in `ci.yml`), none attributed
   to `go-kit/kit`, `go-kit/log`, or `go-logfmt/logfmt` themselves (confirmed by filtering
   verbose output for "Found in" lines not naming the standard library). Cross-checked
   against the OSV database (`api.osv.dev`) for `github.com/go-kit/kit`: zero known advisories.
   **Unrelated finding, noted for awareness, not acted on in this PR**: this developer
   machine's local `go` binary is behind the CI-pinned version; not a go-kit risk.

## Decision

Adopt go-kit, but narrowly: only `github.com/go-kit/kit/endpoint` and
`github.com/go-kit/kit/transport/http` — the oldest, most structurally stable part of the
library (the core request/response/endpoint abstraction, present since the project's earliest
versions), not any of the newer or integration-heavy packages (circuit breakers, service
discovery, metrics backends, tracing adapters). Domain and service-layer code depend on
**plain Go interfaces only**, never on go-kit types — go-kit types appear exclusively in the
`endpoint` and `transport` layers. This bounds the blast radius of the staleness risk in
"Context": if go-kit becomes unmaintained or a replacement is needed later, only those two
thin layers need rewriting, not business logic or tests for it.

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| No semver API-stability guarantee (perpetual v0.x) | Pin to an exact version (not `latest`); only use the two most structurally unchanged packages; domain/service code has zero go-kit imports, so an upstream breaking change is contained to the transport/endpoint files. |
| Maintenance has slowed (last push mid-2024) | Dependabot (already configured in this repo) will surface any future release or advisory; govulncheck (already in CI) will catch any future vulnerability. Re-evaluate this ADR if go-kit is archived or a CVE appears. |
| Heavy transitive dependency surface *if* more go-kit packages are used later | Verified empirically that the two packages used today do not pull in the heavy dependencies; any future addition of another go-kit package must repeat this same empirical check before merging, not assume it's as lightweight. |

## Alternatives considered

- **No framework, plain `net/http` with hand-rolled layering**: avoids the dependency
  entirely, but the instruction explicitly selected go-kit as "the architectural framework" in
  advance — not this ADR's call to override.
- **A newer, actively maintained alternative** (e.g. `go-chi` for routing, or a hand-rolled
  endpoint abstraction): not evaluated in depth, since go-kit was pre-selected; noted here in
  case a future re-evaluation is triggered by one of the risks above.

## Consequences

- New dependency: `github.com/go-kit/kit` (plus transitive `go-kit/log`, `go-logfmt/logfmt`).
- `internal/api/api.go` is replaced by a layered structure under `internal/modules/posts/` and
  `internal/platform/` (see this milestone's PR description for the exact layout); the old
  file and its test are removed, not left as dead code alongside the new implementation.
- `docgen` (the documentation-automation tool from the prior milestone) is updated to read
  routes from the new composition root rather than the old file — see that PR's note in
  section 9 of the originating instructions.
