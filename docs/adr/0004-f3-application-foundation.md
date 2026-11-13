# ADR 0004: F3 production application foundation (Go + Python)

- Status: Implemented, local branch only (`feature/f3-application-foundation`).
- Date: 2026-11-13

## Context

F3 asked for two independent reference services (Go, Python) demonstrating the same
operational conventions, plus a lightweight, documented, language-independent contract
other languages/services can join later — without inventing a heavyweight shared
framework, prematurely adding auth/databases/Kubernetes, or restructuring the existing
repository.

## What was audited first

- The existing Go API (`cmd/api`, `internal/modules/posts`, `internal/platform`) already
  satisfies most of F3's requirements: go-kit layering, health/readiness, graceful
  shutdown (`signal.NotifyContext` + `Server.Shutdown`), structured `slog` logging,
  consistent `{"error": "..."}` responses (`apierror`), request body limits, unit +
  integration tests. **Decision: extend it, don't replace it** — "using the existing
  project conventions" in the mission statement is read as "reuse this as the Go
  reference service," not "build a second, parallel Go service."
- Real gaps found in the existing Go service, fixed as part of this phase (not a
  rewrite): no correlation-ID propagation, no service-metadata (name/version) in
  `/healthz`. Both added as small, additive changes
  (`internal/platform/middleware.CorrelationID`, `internal/platform/buildinfo`) with new
  tests — nothing existing was restructured.
- No Python code existed anywhere in the repository. This phase adds it fresh.

## Decision: where the Python service lives

`apps/python-api/` — a new top-level `apps/` directory, used for exactly this one
service. This matches the target repository architecture discussed in earlier phases
(an `apps/` directory for application implementations) without forcing the *existing* Go
code into that structure too — `cmd/`/`internal/` stay exactly where they are. Per F3's
own instruction ("use clear language-specific conventions instead of forcing Go and
Python into identical directory structures") and "do not restructure the entire
repository," this phase introduces the new convention only for the new service, and
leaves the question of whether to eventually move `cmd/api` under `apps/go-api/` as a
distinct, future, separately-reviewable decision — not bundled into this one.

## Decision: Python packaging — plain `pyproject.toml`, no Poetry/uv

`pip install -e .` plus a `requirements-lock.txt` (generated via `pip freeze`, the exact
transitive closure) is sufficient for a single reference service with no publishing
requirement. Poetry or uv would add a second packaging tool and its own lockfile format
to a repository that doesn't otherwise use either — "minimize dependencies... and
cognitive overhead" favors the stdlib-adjacent option here.

## Decision: a real dependency-security finding, fixed during this phase

`pip-audit` against the first-drafted pin set (`fastapi==0.120.1`) found 6 known
vulnerabilities (`PYSEC-2026-161/248/249/2280/2281` in `starlette`, `PYSEC-2026-1845` in
`pytest`). Investigated before acting: `fastapi==0.120.1` constrains `starlette` to
`<0.50.0`, and **no version in that range fixes the CVEs** — the fix requires crossing
into `starlette` 1.x, which requires a newer `fastapi` (`>=0.162.5`, the lowest version
requiring `starlette>=0.46.0`, confirmed by inspecting `fastapi`'s own published
dependency metadata, not guessed). Verified this upgrade is actually compatible before
adopting it: full test suite (11/11), `ruff check`/`ruff format --check`, and
`mypy --strict` all pass unchanged against the upgraded stack. `pip-audit` against the
resulting lockfile: zero findings. This is "a safe, compatible fix," not a forced/
breaking upgrade accepted blindly — the distinction mattered enough to verify explicitly
rather than assume either way.

## Decision: structured logging without a third-party library

Both services emit one JSON log line per request with the same field names
(`method`, `path`, `status`, `duration_ms`, `request_id`). Go uses the standard
library's `log/slog`, already in use. Python uses a ~20-line `logging.Formatter`
subclass (`app/logging_config.py`) rather than `structlog` or `python-json-logger` —
the stdlib already does everything this service needs; a dependency for 20 lines of code
is the overengineering F3's own instructions explicitly warn against.

## Decision: correlation IDs, independently generated, same contract

`X-Request-Id`: reused if the caller supplies one, generated (16 random bytes, hex) if
not, echoed on the response, attached to every log line for that request. Implemented
independently per language (Go: `crypto/rand` + `context.Context`; Python:
`secrets.token_hex` + `contextvars.ContextVar`, the async-safe equivalent) — not via a
shared library, deliberately (see `docs/platform/CONTRACTS.md`'s "why independent
implementations" section). Verified via dedicated tests in both languages, including
that two requests with no inbound header get two *different* generated IDs (Go) and that
an inbound ID is reused rather than overwritten (both languages).

## Decision: error-response shape is the contract; status codes are not, fully

Both return `{"error": "<message>"}` for every error. One documented, deliberate
difference: a client-input error is `400` in Go, `422` in Python (FastAPI's native
validation-error status) — forcing identical status codes across frameworks would mean
fighting each framework's own idiom for no real benefit, so the contract specifies the
body shape, not every status code. See `docs/platform/CONTRACTS.md` §3 for the full
reasoning, written out so a future contributor doesn't "fix" this into false uniformity.

## Decision: graceful shutdown — explicit in Go, inherited in Python

Go's `net/http.Server` has no built-in signal handling, so `cmd/api/main.go` already
wires `signal.NotifyContext` + `Server.Shutdown(ctx)` explicitly (pre-existing, unchanged
by this phase). `uvicorn` (Python) already handles `SIGINT`/`SIGTERM` gracefully out of
the box; the `lifespan` hook added in `apps/python-api/app/main.py` only logs the
start/stop transition — reimplementing what uvicorn already does would be the
unnecessary-abstraction F3 explicitly warns against.

## Decision: a real config/Docker bug caught and fixed, not shipped

The first draft of `apps/python-api/Dockerfile` hardcoded `--port 8081` in its
exec-form `ENTRYPOINT`, meaning `config.py`'s validated `port` setting and the
`PORT` environment variable were silently never actually used to bind the server —
dead configuration. Fixed by switching to a shell-form `ENTRYPOINT` that expands
`${PORT}` (exec-form `ENTRYPOINT` cannot perform environment-variable substitution).
Recorded here because it's exactly the kind of inconsistency "review the complete
folder/file structure... for... accidental complexity" is meant to catch, and because a
configuration field that's validated but never read is worse than no field at all — it
implies a guarantee the code doesn't keep.

## Deferred, explicitly, per F3's own scope

Authentication, a real (non-in-memory) datastore, message brokers, Kubernetes, an AI
gateway, and full observability infrastructure (OpenTelemetry, Prometheus, Grafana) —
all out of scope per F3's own instructions, not omitted by oversight. A hand-authored
OpenAPI document for the Go API (Python already has one, auto-generated by FastAPI) —
deferred to avoid introducing a second, parallel, potentially-drifting source of truth
for the same route information `docs/generated/API.md` already covers deterministically;
generating a real OpenAPI document *from* the same source `cmd/docgen` already parses is
a reasonable future addition, not attempted here.

## Consequences

- New: `apps/python-api/` (full service, tests, Dockerfile, README).
- New: `internal/platform/middleware.CorrelationID`, `internal/platform/buildinfo`
  (+ tests) — small, additive changes to the existing Go service.
- New: `docs/platform/CONTRACTS.md` (the cross-language contract specification).
- New: `.github/workflows/python-ci.yml` — `python-quality`, `python-security`,
  `python-container` jobs. **Not added as required status checks** — that's a live
  ruleset change requiring its own approval, out of scope for this phase (per F1's
  established discipline: code/CI additions happen first, required-check-list changes
  are a separate, explicit, approval-gated step).
- `compose.yaml`: new `python-api` service, same security posture as the existing `api`
  service (`read_only`, `no-new-privileges`, non-root).
- No existing Go file was restructured; no existing required CI check was renamed.
