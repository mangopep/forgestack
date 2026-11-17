---
title: Playground lifecycle safety
---

# Playground lifecycle safety

Builds on `internal/playground`'s existing `Manager` (per-owner, unique-ID, TTL-bounded local
environments — see `docs/architecture/F6-LOCAL-FOUNDATION.md`) without changing it:
`Create`/`Cleanup` and every existing test still pass unmodified. This adds automatic
expiration, a bounded-retry cleanup controller, and independent reconciliation against real
local Docker resources.

> **Local-only, and that's a real boundary, not a disclaimer of convenience.** Everything on
> this page runs against the local filesystem and a local Docker daemon. **None of it proves
> anything about AWS.** A future AWS adapter needs its own independent reconciliation against
> real AWS inventory (Cost Explorer, resource tags, the AWS API) — passing every test on this
> page is not evidence that a cloud cleanup controller would be safe, and this document does
> not imply otherwise.

## States

| State | Meaning | Who sets it |
|---|---|---|
| `active` | Created, not yet due | `Manager.Create` |
| `expired` | Playground's TTL has passed; not yet cleaned up | `Controller.ExpireDue` |
| `cleanup_pending` | A cleanup attempt is about to start or was interrupted mid-attempt | `Controller.cleanupOne`, persisted *before* calling `Manager.Cleanup` |
| `cleanup_incomplete` | A cleanup attempt ran and failed to remove owned resources | `Manager.Cleanup` (unchanged) |
| `cleanup_unverified` | Resources were removed but independent re-verification couldn't confirm absence (local filesystem *or* Docker) | `Manager.Cleanup` (local) / `Controller.cleanupOne` (Docker) |
| `verified_clean` | Independently confirmed absent — local filesystem, and Docker if `--docker`/a `DockerClient` was supplied | `Manager.Cleanup` / `Controller.cleanupOne` |

`cleanup_pending` is the state that makes restart recovery work: it's written to disk
*before* any removal is attempted, so a process killed mid-cleanup leaves evidence a later
scan (by the same process restarted, or a different one — `needsCleanup` only ever reads
from disk) picks up and resumes, rather than the interruption going unnoticed.

## The controller (`internal/playground/controller.go`)

`Controller` wraps the existing `Manager` with three injected, deterministic-by-design
dependencies — `Now`, `Sleep`, and an optional `Docker` client — specifically so every test is
real logic under test, not a race against the wall clock or a real daemon:

- **`ExpireDue(root)`** scans every environment (`ListEnvironments`, new, read-only) and
  transitions `active` playgrounds past their `ExpiresAt` to `expired`. Product environments
  (no `ExpiresAt`) and not-yet-due playgrounds are left untouched.
- **`CleanupDue(ctx, root)`** finds every environment that `needsCleanup` (`expired`,
  `cleanup_pending`, `cleanup_incomplete`, or `cleanup_unverified`), marks it
  `cleanup_pending`, retries `Manager.Cleanup` up to `MaxCleanupAttempts` times with
  `RetryBackoff` between attempts (bounded — a genuinely broken environment must eventually
  surface as `cleanup_incomplete` for a human, not retry forever), and — only once local
  cleanup reaches `verified_clean`, and only if a `Docker` client is set — removes and
  independently re-verifies that environment's tracked Docker resources.

## Docker reconciliation (`docker.go`, `tracked.go`, `reconcile.go`)

This package never *creates* Docker resources itself (it manages local environment metadata;
whatever you run with `docker run`/`docker network create`/etc. is yours to label). What it
adds is strict, label-scoped tracking and reconciliation:

- **Tracking**: `Manager.TrackDockerResource(root, id, owner, resource)` records that a
  specific environment owns a specific Docker object — validated with the exact same
  ownership check `Cleanup` already uses, so a caller cannot attach a resource to an
  environment it doesn't own.
- **Labels, not names or patterns**: every list/remove operation is scoped to an exact value
  of `com.forgestack.playground-id` (plus `com.forgestack.playground-owner`, recorded for
  humans inspecting `docker ps`, not itself used for filtering). `DockerClient`'s real
  implementation (`execDockerClient`) has no code path that lists or removes "all containers,"
  by name, or by any pattern — only by that one exact label value. No `docker system prune`,
  no `-f`/`--force`, ever.
  - Real containers: `docker ps -a --filter label=... --format {{.ID}}` / `docker rm <id>`
    (no `-f` — a still-running container fails removal loudly rather than being force-killed).
  - Volumes/networks: the equivalent `ls --filter label=...` / `rm` pair.
- **`Reconcile(ctx, docker, root, id)`** independently compares what's *tracked* against what
  Docker *actually reports* for that exact label, producing `Orphaned` (present, never
  tracked) and `Missing` (tracked, not present) — drift surfaced, never silently assumed
  clean. If Docker itself is unavailable, `Reconciliation.DockerAvailable` is `false` and
  `Clean()` is **never** `true` for it — absence of evidence (couldn't check) is never treated
  as evidence of absence (nothing's there).
- **`RemoveTrackedDockerResources`** removes everything currently `Present` under that label,
  then calls `Reconcile` again to independently confirm — the Docker-resource equivalent of
  `removeOwnedResources`/`finalizeCleanup`'s existing local-filesystem pattern.

## Test evidence

22 tests in `internal/playground`, `go test -race` clean, `golangci-lint` 0 issues. The 5
pre-existing tests are unchanged and still pass. New:

| Requirement | Test(s) |
|---|---|
| Automatic expiration, deterministic clock | `TestExpireDueTransitionsOnlyPastDeadlinePlaygrounds`, `TestExpireDueIsIdempotent` |
| Idempotent cleanup, bounded retries | `TestCleanupDueRetriesUpToBoundThenGivesUp`, `TestCleanupDueSucceedsAfterTransientFailures`, `TestCleanupDueIsIdempotentAcrossRepeatedInvocations` |
| Recovery after interrupted cleanup | `TestCleanupDueResumesAfterInterruptedCleanup` |
| Resume after a process restart | `TestCleanupDueAfterSimulatedRestartUsesOnlyPersistedState` (a fresh `Controller`/`Manager` value, nothing shared in memory) |
| Strict ownership — never touch an unrelated environment | `TestCleanupDueNeverTouchesAnUnrelatedActiveEnvironment`, `TestRemoveTrackedDockerResourcesNeverTouchesAnotherEnvironment` |
| Ownership mismatch rejected | `TestTrackDockerResourceRejectsOwnershipMismatch`, `TestTrackDockerResourceRejectsInvalidID` |
| Partial deletion / failed verification | `TestCleanupDueDowngradesWhenDockerResourcesCannotBeVerifiedAbsent` |
| Missing / orphaned resource detection | `TestReconcileDetectsOrphanedAndMissingResources` |
| Duplicate cleanup | `TestCleanupDueIsIdempotentAcrossRepeatedInvocations`, plus the pre-existing `TestCreateAndCleanupIsScopedAndIdempotent` |
| Docker unavailable fails closed | `TestReconcileReportsDockerUnavailableRatherThanAssumingClean` |
| Real Docker, not just the fake | `TestDockerClientRealSmokeTest` (skipped if no daemon reachable; network-only, no image pull; proves the real label filter is as strict as the fake's by seeding a second network under a different label and confirming it survives) |

**End-to-end CLI evidence** (`cmd/playground`, run directly, not just unit-tested): `create`
with a 2-second TTL → `expire-due` → `cleanup-due` reaches `verified_clean`; separately,
`track-docker` against a real `docker network create`d network, then `reconcile` correctly
shows it `tracked`+`present` with no drift; then `expire-due` + `cleanup-due --docker` against
that same setup reached `verified_clean` and an independent `docker network ls` confirmed the
network actually gone.

## Limitations (reported honestly, not hidden)

- `Manager.Create`'s own `time.Now()` call is not clock-injectable (preserving its existing,
  tested behavior rather than rewriting it) — tests that need a deterministic "past the
  deadline" clock capture the real `ExpiresAt` `Create` returns and set the controller's
  `Now` relative to that, rather than controlling creation time itself.
- Docker reconciliation only covers containers, volumes, and networks — not images (shared
  across environments by design, never this package's business to remove) and not any other
  Docker object kind.
- `CleanupDue`'s bounded retries are synchronous and sequential per environment within one
  call; this is a library a caller invokes (by hand, or from a scheduler/cron the caller sets
  up), not a standalone daemon process. No daemon/scheduler is implemented here.
- Still true from `F6-LOCAL-FOUNDATION.md`: no remote state, no AWS adapter, no cost-reconciled
  cleanup against real cloud billing. Cost Guard's own design-only section on this
  (`docs/architecture/COST-GUARD.md`) is the closest existing documentation of what an AWS
  version would need, and it is explicitly design-only there too.
- No cross-process lock on an environment's directory: `Controller.cleanupOne` writes
  `environment.json` with no compare-and-set, so two overlapping `CleanupDue` invocations
  (two processes, or two goroutines) against the same environment both proceed and the
  chronologically last metadata write wins. A 100-iteration × 6-goroutine stress probe against
  this exact scenario (real `os.RemoveAll`, no injected failures) found every run converging
  to `verified_clean` with no corruption, because every concurrent attempt performs the same
  idempotent operation against the same real filesystem state. That is evidence for this
  specific test shape, not a guarantee: a genuinely asymmetric partial failure (one concurrent
  attempt hits a transient error the others don't, e.g. a permissions race) could still let a
  slower failed write overwrite a faster successful one, regressing the on-disk status.
  Production hardening should add a per-environment advisory lock (e.g. an `flock` on a lock
  file in the environment directory) around `cleanupOne` before relying on concurrent
  `CleanupDue` callers.
