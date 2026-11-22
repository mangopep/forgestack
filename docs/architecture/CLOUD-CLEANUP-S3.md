---
title: Cloud cleanup (S3), local-only increment
---

# Cloud cleanup (S3), local-only increment

> **Update, post-audit.** An independent audit of this increment found one
> CRITICAL, one HIGH, and two MEDIUM defects, all confirmed by deterministic
> or stress-tested local reproduction (never against real AWS). All four
> have been fixed, locally, with permanent regression tests. See "Audit
> findings and fixes" below for the complete account -- it is appended
> after the original design record rather than silently edited into it,
> so the history of what was found and changed stays visible.

**Zero AWS API calls, zero AWS authentication attempts, and zero cloud resources were used
anywhere in building or testing this.** Every claim below states exactly how it was verified,
per this repository's evidence-label convention. The existing `$4` AWS budget and
`ForgeStackTestRole` are untouched — neither was read, modified, or referenced by any code or
command in this increment.

> **What this is, in one sentence.** `internal/playground` gained the ability to *track* and
> *reconcile* S3 buckets against the same local playground lifecycle it already manages for
> local files and Docker resources — tested entirely against an in-memory fake. **A real
> AWS-SDK-backed `CloudClient` implementation (`S3Adapter`) now exists in this repository as
> of the "Real S3Adapter" section below**, but it has never been constructed with real
> credentials, never called a real AWS endpoint, and is proven only against a hand-written
> fake modeling S3's documented behavior. Proving it against a real account is a separately
> approved, not-yet-started increment (see "Live AWS validation plan" below, now partially
> superseded — see that section's update note).

## Scope decisions (given, not re-derived here)

1. **S3 bucket cleanup only.** No EC2, EKS, RDS, or NAT Gateway support in v1. Enforced in
   code, not just by convention: `ValidateCloudResourceKind` (`cloud.go`) rejects any
   `CloudResourceKind` other than `CloudS3Bucket` before it is ever persisted or reconciled —
   *confirmed from test*, `TestTrackCloudResourceRejectsUnsupportedKind`.
2. **Reuse the existing playground lifecycle and status model.** No parallel controller was
   built. `Controller` (already the single orchestrator for local + Docker cleanup) gained one
   more optional field, `Cloud CloudClient`, and `cleanupOne` gained one more additive phase
   (`removeCloudPhase`), structured identically to the existing `removeDockerPhase`. The status
   vocabulary (`active`/`expired`/`cleanup_pending`/`cleanup_incomplete`/`cleanup_unverified`/
   `verified_clean`) is unchanged — cloud failures downgrade to the same `cleanup_unverified`
   Docker failures already use.
3. **GitHub Actions scheduling is not represented as a guaranteed TTL or financial shutdown.**
   No scheduled workflow was added in this increment at all (see "Deferred" below) — there is
   nothing to mis-represent yet, and this document makes no claim that one exists.
4. **AWS-native EventBridge/Lambda is a documented alternative, not built.** See "Deferred."
5. **Cross-process coordination before live deletion.** The cloud phase reuses the *exact
   same* per-environment `flock` (`withEnvironmentLock`) the local and Docker phases already
   use — there is no new, separate locking primitive. This is genuinely sufficient only for
   processes sharing one filesystem; see "Limitations" for the real gap this does not close
   (multi-machine coordination against a durable, non-local store).
6. **Exact account/region/environment/owner/resource-ID identity.** `CloudResourceRef{Kind,
   Account, Region, Ref}` is the one persisted and compared unit throughout — never a bare
   name. `TrackCloudResource` validates the caller-supplied owner against the environment's
   own ownership record (same check `Cleanup`/`TrackDockerResource` already use) before
   persisting anything.
7. **Ownership verified via an independent read immediately before deletion.**
   `RemoveTrackedCloudResources` calls `CloudClient.VerifyOwnership` — a direct re-read, never
   the earlier `ListByTag` result — for every resource, immediately before calling `Remove`.
   *Confirmed from test*: `TestRemoveTrackedCloudResourcesVerifiesOwnershipBeforeEveryRemove`
   asserts exactly one `VerifyOwnership` call per resource, and
   `TestRemoveTrackedCloudResourcesRefusesWhenOwnershipDriftsBeforeRemoval` proves a resource
   whose tags changed in the window between listing and removal is never removed.
8. **IAM tag-condition capability is not assumed.** This increment does not rely on IAM
   resource-tag conditions working for the selected S3 operations at all — ownership
   enforcement is done entirely in application code (`VerifyOwnership`, checked against all
   three required tags before every `Remove`), specifically because this repository cannot
   verify real IAM behavior without AWS access, which this task forbids. See "A deliberately
   unverified assumption" below — this is recorded as **Inference requiring team
   confirmation**, not asserted as working.
9. **Bucket-not-empty, versions, delete markers, multipart uploads, throttling, missing tags,
   credential expiry, partial deletion** — each has an explicit, separately tested code path;
   see "What's tested" below.
10. **Durable metadata outside disposable playground state.** `cloud-resources.json` is
    written as a sibling of `environment.json`, inside the environment's own directory — never
    inside the `resources/` subdirectory `Cleanup` removes. *Confirmed from test*:
    `TestTrackCloudResourceSurvivesLocalCleanup`. This protects the record from this package's
    *own* `Cleanup` call; it does **not** protect it from the environment root directory itself
    being deleted out of band — see "Limitations."
11. **Unknown/stale/unverifiable inventory is always `CLEANUP_UNVERIFIED`, never success.**
    Every failure mode below downgrades to `cleanup_unverified` and reports a non-nil error;
    none infer success from a call simply not erroring.
12. **Emergency cleanup is a separate, auditable operation.** `ForceRemoveCloudResource`
    (`cloud_force.go`) is never called by `Controller`, `Worker`, or any automatic path — grep
    confirms this (`TestForceRemoveCloudResourceIsNeverCalledByTheAutomaticPath` exists as a
    deliberate tripwire for a future change). It requires a non-empty `reason`, still performs
    an ownership check, but records the result rather than gating on it — because the whole
    reason this path exists is to handle resources whose tags are wrong or missing.
13. **The `$4` budget and `ForgeStackTestRole` are unchanged.** Confirmed by inspection: no
    file in `infrastructure/terraform/`, no IAM policy, and no AWS configuration was read,
    created, or modified by this increment.
14. **Zero real AWS access.** Confirmed: no file in this increment imports an AWS SDK
    (`grep -rn aws-sdk internal/playground/*.go` → no matches), performs a subprocess call, or
    makes an HTTP request. `go.mod`/`go.sum` are unchanged (`git diff --stat go.mod go.sum` →
    empty).

## What was built

```
internal/playground/
  cloud.go              CloudResourceKind (S3 bucket only), CloudClient interface,
                         CloudResourceRef, tag constants, sentinel errors
  cloud_fake.go          FakeCloudClient -- deterministic in-memory test double modeling
                         object versions, delete markers, multipart uploads, throttling,
                         credential expiry, tag drift, and unemptiable buckets
  cloud_tracked.go       TrackCloudResource -- same ownership check + lock as
                         TrackDockerResource, persists cloud-resources.json
  cloud_reconcile.go     CloudReconcile / RemoveTrackedCloudResources -- same Clean()
                         semantics as the Docker Reconciliation, plus the independent
                         pre-removal VerifyOwnership step
  cloud_force.go         ForceRemoveCloudResource -- the separate emergency path
  controller.go (edited) Controller.Cloud field, WithCloud, removeDockerPhase/
                         removeCloudPhase extracted from cleanupOne (both domains now run
                         independently and combine their failures into one downgrade)
  worker.go (edited)     Worker.WithCloud -- library-level only, not wired into the CLI
                         (see "Deferred")
```

No file outside `internal/playground` was touched. `cmd/playground/main.go` was deliberately
**not** edited — see "Deferred."

## What's tested (evidence, not claims)

28 new tests, all passing, `-race` clean, `golangci-lint` 0 issues (repo-wide, not just this
package), `go vet` clean, `gofmt` clean. Every pre-existing test in `internal/playground`
(the 37 documented in `PLAYGROUND-LIFECYCLE.md`) still passes unmodified.

| Requirement | Test(s) |
|---|---|
| v1 scope boundary enforced in code | `TestTrackCloudResourceRejectsUnsupportedKind` |
| Ownership mismatch / invalid ID rejected before persisting | `TestTrackCloudResourceRejectsOwnershipMismatch`, `TestTrackCloudResourceRejectsInvalidID` |
| Metadata survives local cleanup (requirement 10) | `TestTrackCloudResourceSurvivesLocalCleanup` |
| Orphaned/missing drift surfaced, never silently clean | `TestCloudReconcileDetectsOrphanedAndMissingResources` |
| Unavailable (throttled / credential expired / generic) never reported clean | `TestCloudReconcileReportsUnavailableRatherThanAssumingClean` (subtests for all three) |
| Strict ownership — never touch a different environment's bucket | `TestRemoveTrackedCloudResourcesNeverTouchesAnotherEnvironment` |
| Non-empty bucket (versions + delete markers + multipart upload) successfully emptied and removed | `TestRemoveTrackedCloudResourcesHandlesNonEmptyBucketThenSucceeds` |
| Bucket that cannot be safely emptied is reported as a failure, never as removed | `TestRemoveTrackedCloudResourcesReportsUnemptiableBucketAsFailure` |
| Ownership re-verified immediately before removal (requirement 7); tag drift between list and removal is caught | `TestRemoveTrackedCloudResourcesRefusesWhenOwnershipDriftsBeforeRemoval`, `TestRemoveTrackedCloudResourcesVerifiesOwnershipBeforeEveryRemove` |
| Missing `managed-by` tag never treated as ours | `TestRemoveTrackedCloudResourcesRefusesWhenManagedByTagMissing` |
| Controller: clean cloud removal reaches `verified_clean` | `TestCleanupDueSucceedsWhenCloudResourcesAreCleanlyRemoved` |
| Controller: cloud failure downgrades to `cleanup_unverified` | `TestCleanupDueDowngradesWhenCloudResourcesCannotBeVerifiedAbsent` |
| Controller: Docker and cloud failures are independent and both reported together | `TestCleanupDueReportsBothDockerAndCloudFailuresTogether` |
| Transient failure (throttling) recovered by a later pass, not an internal retry loop | `TestCleanupDueRecoversFromTransientCloudFailureOnASubsequentPass` |
| Product/persistent environments never touched | `TestCleanupDueNeverAttemptsCloudForProductEnvironment` |
| Lost-update protection on concurrent tracking | `TestTrackCloudResourceConcurrentCallsNeverLoseAnUpdate` |
| Cross-environment isolation under concurrency | `TestConcurrentCleanupOfDifferentEnvironmentNeverAffectsThisOnesCloudResult` |
| Emergency path requires a reason, is never gated by the ownership check, and records the check's outcome for audit | `TestForceRemoveCloudResourceRequiresAReason`, `TestForceRemoveCloudResourceRemovesEvenOnOwnershipMismatch`, `TestForceRemoveCloudResourceRecordsAMatchedOwnershipCheck` |
| Emergency path is structurally unreachable from the automatic path | `TestForceRemoveCloudResourceIsNeverCalledByTheAutomaticPath` (tripwire) |
| Pre-existing expensive-resource / unapproved-deletion policy boundary still enforced (deployment-approval layer, not this cleanup layer) | Unmodified, still passing: `internal/costguard`'s `TestScanPlanFlagsNATGateway`, `TestScanPlanFlagsEKSClusterAndNodeGroup`, `TestEvaluateRejectsUnapprovedCostlyResource`, `TestEvaluateCannotBypassCostlyResourceCheckByOmittingExtraFlags` |

## Evidence matrix: LOCAL_VERIFIED vs. AWS_NOT_VERIFIED

| Capability | Status | Evidence |
|---|---|---|
| Tag-exact scoping (never name/pattern) | **LOCAL_VERIFIED** | `TestCloudReconcileDetectsOrphanedAndMissingResources` |
| Ownership re-verification immediately before delete | **LOCAL_VERIFIED** | `TestRemoveTrackedCloudResourcesRefusesWhenOwnershipDriftsBeforeRemoval` |
| Non-empty bucket (versions/delete-markers/multipart) handling | **LOCAL_VERIFIED** (against a fake that models the shape of the problem) | `TestRemoveTrackedCloudResourcesHandlesNonEmptyBucketThenSucceeds` / `...ReportsUnemptiableBucketAsFailure` |
| Throttling / credential-expiry handling | **LOCAL_VERIFIED** (against injected fake errors) | `TestCloudReconcileReportsUnavailableRatherThanAssumingClean` |
| Bounded-retry-by-next-pass recovery | **LOCAL_VERIFIED** | `TestCleanupDueRecoversFromTransientCloudFailureOnASubsequentPass` |
| Emergency removal audit trail | **LOCAL_VERIFIED** | `TestForceRemoveCloudResourceRecordsAMatchedOwnershipCheck` |
| Real AWS SDK call shape / actual S3 API error strings | **AWS_NOT_VERIFIED** | No AWS SDK client exists in this repo |
| Real IAM tag-condition enforcement for the selected S3 actions | **AWS_NOT_VERIFIED** | Explicitly not assumed; see decision 8 |
| Real throttling/backoff behavior, real credential-expiry error shapes | **AWS_NOT_VERIFIED** | Fake errors are hand-constructed, not observed from a live API |
| Real multipart-upload/versioning edge cases at scale | **AWS_NOT_VERIFIED** | Fake models the *shape* of the problem, not real S3 semantics |
| Cross-process coordination across more than one machine/filesystem | **AWS_NOT_VERIFIED / NOT IMPLEMENTED** | See "Limitations" — no durable multi-machine store exists yet |
| GitHub Actions scheduled execution, independent of this Mac | **NOT IMPLEMENTED** | No workflow file was added this increment |

## A deliberately unverified assumption, stated plainly

**Requirement 8 cannot be fully satisfied without real AWS access**, which this task
forbids. What this increment does instead: it does not *rely* on IAM tag conditions working
at all. Every ownership check that matters is enforced in Go code
(`VerifyOwnership`/`RemoveTrackedCloudResources`), re-read directly from the provider
immediately before every deletion. IAM should still be configured as tightly as the real S3
API allows (see the live validation plan below) as defense-in-depth, but this package's own
correctness does not depend on IAM conditions succeeding — only on its own application-level
check, which is fully tested above. **Whether S3's specific bucket-level actions
(`s3:DeleteBucket`, `s3:ListBucket`, `s3:GetBucketTagging`) actually support
`aws:ResourceTag/*` IAM conditions, and under what caveats, is recorded here as
Inference requiring team confirmation — to be settled in the live validation plan, never
assumed.**

## Limitations (reported honestly, not hidden)

- **No AWS-SDK-backed `CloudClient` exists.** This is a tested interface and a tested
  orchestration layer around it, not a working integration. Building the real adapter is
  separately approved work (see below).
- **No durable, multi-machine metadata store.** `cloud-resources.json` lives on this Mac's
  local filesystem, exactly like `environment.json` and `docker-resources.json` already do.
  The per-environment `flock` this increment reuses only serializes processes sharing that
  filesystem — it provides **no coordination at all** across two machines (e.g. a local
  worker and a future GitHub Actions run) racing on the same environment's cloud resources.
  **This must be designed and built before any live, multi-machine cloud deletion is
  attempted** — a real distributed primitive (e.g. DynamoDB conditional writes, or an S3
  object-lock-based lease) is needed, not assumed solved by analogy to `flock`.
- **The environment root directory itself is a single point of failure for cloud-resource
  knowledge.** If `.forgestack/environments/<id>/` is deleted out of band (accidental `rm -rf`,
  lost disk), this package has no remaining record that a real S3 bucket exists at all. Nothing
  in this increment backs up or replicates that directory. Treat it as itself requiring backup
  once it tracks real cloud resources — not yet implemented.
- **No real AWS error taxonomy.** `ErrCloudThrottled`/`ErrCloudCredentialExpired`/
  `ErrCloudBucketNotEmpty` are this package's own sentinel errors; a real adapter's job is to
  correctly map actual AWS SDK errors onto them, and that mapping is itself untested until a
  real adapter exists.
- **`CleanupDue`'s cloud phase has no internal retry loop** (matching Docker's existing
  behavior) — a transient failure surfaces as `cleanup_unverified` for one pass and is retried
  by the *next* `CleanupDue`/worker pass, never immediately. This is a deliberate reuse of the
  existing cadence, not a new gap, but it does mean "how quickly a transient AWS throttle
  recovers" depends entirely on how often `CleanupDue` runs — which, per the scheduling
  decision below, is not yet guaranteed at all outside a human running it by hand.
- **No CLI or worker wiring.** `cmd/playground/main.go` has no `track-cloud`/`--cloud`
  flags, and `Worker.WithCloud` is not called by the `worker` subcommand. Both are one-line
  additions once a real `CloudClient` exists — deliberately not added now, since a CLI flag
  with no safe real implementation behind it would be actively misleading.

## Deferred (explicitly, by instruction)

- **GitHub Actions as an independent backup scheduler.** No workflow file was added. If and
  when this is built, it must be documented — in its own PR, reviewed on its own — as a
  **best-effort backup**, never a guaranteed TTL enforcement or a financial-shutdown
  mechanism: GitHub Actions scheduling has well-known multi-minute slop, and a GitHub outage
  pauses it entirely. The honest framing is "this catches what the Mac-based worker missed
  while the Mac was off," not "this guarantees spend stops within N minutes of expiry."
- **AWS-native EventBridge Scheduler + Lambda**, as a tighter-grained, Mac-independent
  alternative. Documented as a design option in the prior architecture review, not built, and
  not deployed — it is itself new cloud infrastructure requiring its own IAM role, its own
  Terraform, and its own operational burden, which is a bigger step than this increment's
  "narrowly scoped S3 adapter" mandate.
- **A real `CloudClient` backed by the AWS SDK.** Not built. See the validation plan below for
  exactly what must happen, in what order, before this is attempted.

## Live AWS validation plan — NOT EXECUTED, approval-gated

Nothing below has been run. It is recorded here as the specific, ordered set of steps a human
must explicitly approve before any of this touches a real AWS account, so that "what real
verification would need to look like" is decided calmly, in advance, rather than improvised
under pressure once real resources are involved.

1. **Build the real `CloudClient`** (AWS SDK v2, a new dependency, added and reviewed in its
   own PR) implementing `ListByTag`/`VerifyOwnership`/`Remove` against the real S3 and
   Resource Groups Tagging APIs. Map real SDK errors onto `ErrCloudThrottled`/
   `ErrCloudCredentialExpired`/`ErrCloudBucketNotEmpty` explicitly, with a unit test per
   mapping using the SDK's own error types (no network call required for this step).
2. **Create one disposable, uniquely-named S3 bucket** (e.g.
   `forgestack-cleanup-validation-<random>`), tagged exactly as this package expects
   (`forgestack.environment-id`, `forgestack.owner`, `forgestack.managed-by=forgestack-cleanup`),
   under `ForgeStackTestRole`'s existing (currently empty) permission set, with a
   **separately reviewed, least-privilege IAM policy change** scoped to exactly
   `s3:ListBucket`, `s3:GetBucketTagging`, `s3:DeleteBucket`, and the object/version/
   multipart-upload actions needed to empty a bucket — on this one bucket only, via the
   `aws:ResourceTag` condition where it works, and explicitly tested to confirm whether it
   does (resolving the "deliberately unverified assumption" above).
3. **Run `CloudReconcile` read-only** against the real bucket first, confirming `Present`
   and `Tracked` match expectations, before any `Remove` call is ever attempted against a
   real resource.
4. **Run one real `RemoveTrackedCloudResources` call** against that one disposable bucket,
   then independently re-verify its absence via a *separate* real API call (`ListBuckets` or
   `HeadBucket`), not by trusting this call's own return value — the same "re-verify
   independently" standard this session's own `v0.2.0` release verification used.
5. **Deliberately inject each real failure mode once**: a bucket with real object versions
   and delete markers; a real throttled call (if reproducible safely); a real expired/revoked
   credential; a bucket whose tag is removed between listing and removal. Confirm each
   behaves as its corresponding local test already proved the orchestration logic would.
6. **Only after 1–5 succeed**, consider the multi-machine coordination gap (the real
   distributed-lock design named in "Limitations") and the scheduling question (GitHub
   Actions backup vs. EventBridge/Lambda) as their own, later, separately approved steps —
   neither is needed to validate the S3 adapter itself.
7. **Cost and cleanup of the validation itself**: the one disposable bucket from step 2 must
   be deleted (by this very pipeline, proving it works, or by hand if it doesn't) before this
   validation is considered closed. Expected cost: effectively zero (S3 has no charge for an
   empty bucket beyond negligible API call costs); this is explicitly the cheapest real AWS
   resource available to validate against, which is why it was chosen as the v1 scope.

Each of these steps requires its own explicit approval before being run — this plan is a
specification for that future conversation, not a thing already in motion.

## Audit findings and fixes

An independent audit (read-only, local-only, zero AWS calls) found one CRITICAL, one HIGH,
and two MEDIUM defects in the original implementation above. All four were fixed in a
follow-up, also entirely local and offline. This section records root cause, exact fix, and
regression evidence for each — nothing above was edited to hide what changed; this is the
addendum.

### CRITICAL (fixed): `verified_clean` could be persisted before every domain was verified

**Root cause.** `Manager.Cleanup` → `finalizeCleanup` persisted `StatusVerifiedClean` to
`environment.json` immediately upon local removal succeeding — before `Controller.cleanupOne`
ever reached the Docker/Cloud phases. `needsCleanup` excludes `verified_clean` from its retry
set. A crash in that window (realistic: any process termination between local success and
additive-domain completion) left the on-disk status permanently claiming "clean" while tracked
Docker/cloud resources were never checked, with no automatic recovery path.

**Confirmed, not assumed:** reproduced directly (call `retryLocalCleanup` alone, inspect
`environment.json` — it already read `verified_clean`; start a brand-new `Controller`/`Manager`
with no shared memory and call `CleanupDue` — it returned zero results, resource remained
present in the fake forever). Also confirmed the identical gap pre-dated this session's cloud
work (applies to Docker alone) — this increment's cloud phase widened the exposed window, it
did not create the pattern.

**Fix — `lifecycle.go`, `controller.go`.** `Manager.cleanup` (new, unexported) takes an explicit
`successStatus` parameter; `Manager.Cleanup` (public, unchanged contract) always passes
`StatusVerifiedClean`. `Controller.cleanupOne` passes `StatusCleanupPending` instead whenever
any additive domain (`Docker`/`Cloud`) is configured — `cleanup_pending` is already in
`needsCleanup`'s retry set. Only `Controller.verifyAdditionalDomains`, once every configured
domain is independently confirmed clean, persists the true `StatusVerifiedClean`. A crash at
any point before that single write leaves a retryable status on disk, never a false "done."

**Regression tests** (`cloud_crash_recovery_test.go`): one each for Docker-only, cloud-only,
and combined Docker+cloud crash points, plus a control test proving a local-only `Controller`
(no additive domains) still reaches `verified_clean` in one call exactly as before.

### HIGH (fixed): unbounded cloud retries, no escalation, no "stop spending API calls" signal

**Root cause.** The cloud phase retried on every `CleanupDue`/worker pass, forever, for a
resource that could never actually be removed (e.g. a real permissions problem) — no counter,
no backoff, no terminal state for a human to notice, consuming API calls indefinitely.

**Fix — `cloud_retry.go`, `controller.go`.** A new persisted sidecar,
`cloud-cleanup-state.json` (sibling of `environment.json`/`cloud-resources.json`, same
durability properties), tracks `Attempts`/`LastAttemptAt`/`LastError` per environment.
`Controller.MaxCloudCleanupAttempts` (default 5) bounds real attempts; `Controller.CloudRetryBackoff`
(default 0, opt-in) gates how soon a new attempt may run, skipping the API call entirely when
not yet due. Exceeding the bound transitions the environment to the new
`StatusCleanupAttentionRequired` status — deliberately excluded from `needsCleanup`, so no
further automatic pass touches it — while `cloud-resources.json` is left completely untouched
(resources are never discarded). `ResetCloudCleanupAttempts(root, id, owner)` is the explicit,
ownership-checked, separately invoked manual recovery path (never called automatically),
mirroring `ForceRemoveCloudResource`'s existing "separate, auditable" philosophy. Docker's
retry behavior is unchanged (unbounded, exactly as originally shipped) — this fix is scoped to
cloud, per instruction, since an exhausted Docker container has no ongoing billing/API-quota
consequence the way an exhausted cloud resource does.

**Regression tests** (`cloud_escalation_test.go`): escalation after exhausting the bound,
backoff preventing an unnecessary immediate API call (confirmed via call-count, not just
timing), attempt-state surviving a simulated restart (fresh `Controller`/`Manager`), manual
reset restoring the automatic retry set, manual reset rejecting a non-owning caller, and
attempt history being cleared on eventual success.

### MEDIUM (fixed): concurrent cleanup could leave a stale `cleanup_unverified` for an already-removed resource

**Root cause.** The per-domain lock covered only each domain's own removal attempt; the final
downgrade-to-`cleanup_unverified` write ran *outside* any lock. Two concurrent `cleanupOne`
calls for the same environment could interleave such that a losing caller's failure-write
landed after a winning caller's (unwritten, since success wrote nothing) success — leaving a
stale `cleanup_unverified` for a resource that was, in fact, already gone. Confirmed (not just
theorized) via deterministic failure injection: self-healed on the next pass, and the dangerous
direction (false "clean" while a resource remains) was never observed across 200+ stress
iterations — bounding it to a reliability/noise issue, not a safety violation.

**Fix — `controller.go`.** The entire additive-domain decide-and-persist cycle
(`verifyAdditionalDomains`) now runs inside **one** `withEnvironmentLock` acquisition per
`cleanupOne` call, extending `Manager.Cleanup`'s own already-proven "whole read-decide-write
cycle inside one lock" pattern to the additive-domain phase. Two concurrent calls for the same
environment can no longer interleave their domain checks or their final status write at all.

**Regression tests** (`cloud_p2_regressions_test.go`): the exact deterministic interleaving
from the audit, run 20 times, now always converges to `verified_clean` with the resource
actually gone — the stale-write regression no longer reproduces. A 50-iteration stress variant
re-confirms the dangerous direction still never occurs.

### MEDIUM (fixed): blank `Account`/`Region`/`Ref` accepted, breaking identity matching permanently

**Root cause.** `TrackCloudResource` validated resource `Kind` but not `Account`/`Region`/`Ref`
non-emptiness. A blank-fielded tracked entry could never match a real `ListByTag` result,
producing a permanent, incorrect `Missing`+`Orphaned` pair instead of an immediate, actionable
rejection. Confirmed by direct reproduction.

**Fix — `cloud.go`, `cloud_tracked.go`.** `ValidateCloudResourceRef` rejects a blank `Account`,
`Region`, or `Ref` (in addition to the existing `Kind` check) before `TrackCloudResource`
persists anything. `FakeCloudClient` was also upgraded to key buckets by the full
`(Kind, Account, Region, Ref)` tuple rather than `Ref` alone, so cross-account and cross-region
negative tests are meaningful rather than vacuously passing.

**Regression tests** (`cloud_p2_regressions_test.go`): blank `Account`/`Region`/`Ref` each
rejected individually; two same-named buckets in different accounts, and two same-named buckets
in different regions, proven never confused by either the fake or `RemoveTrackedCloudResources`.

### Positive finding, now a permanent regression test: ownership mismatch can never authorize deletion

The audit's confirmed positive result — a tampered `cloud-resources.json` naming an unrelated,
real bucket is never removed, because removal is driven by the live tag query (`Present`),
never by the `Tracked` list's contents — is now a permanent test
(`TestP2_CorruptedTrackedFileCannotAuthorizeUnrelatedDeletion`), plus a concurrent-pressure
variant (`TestP2_OwnershipMismatchNeverAuthorizesDeletionUnderConcurrentPressure`, 8 concurrent
attempts against a resource with a deliberately mismatched owner tag, all rejected).

### Residual, documented, not fixed: `VerifyOwnership` → `Remove` TOCTOU

`RemoveTrackedCloudResources` closes the gap between the earlier `ListByTag` and the
pre-removal check via `VerifyOwnership`, called immediately before `Remove`. **A smaller window
still remains between `VerifyOwnership` returning `true` and the subsequent `Remove` call
itself** — two separate round-trips, not one atomic provider-side operation. S3 has no
"delete-if-tag-matches" primitive to close this fully. This is believed to be close to the best
achievable without such a primitive, and is recorded here explicitly as a residual limitation
rather than implied solved — any real adapter built in the future live-validation phase should
re-examine whether a conditional-delete-equivalent exists for the specific S3 actions in use.

### Test-quality fix

`TestRemoveTrackedCloudResourcesHandlesNonEmptyBucketThenSucceeds`'s doc comment was corrected
to state explicitly that it proves orchestration tolerance of a successful `Remove` call, not
real S3 object/version/delete-marker/multipart-upload removal semantics — which do not exist in
this codebase and remain `AWS_NOT_VERIFIED` until a real adapter is built.

### Full verification after all fixes

`gofmt`, `go vet`, `golangci-lint` (repo-wide, 0 issues), and `go test ./... -race` all pass —
every pre-existing test (including the original 23 cloud tests) plus 27 new permanent
regression tests, all green. No AWS API call, credential access, or SDK network client was
used at any point during this audit-and-fix cycle; `go.mod`/`go.sum` remain unchanged.

## Real `S3Adapter` — strictly offline implementation

This section records the increment that added a real, AWS-SDK-backed `CloudClient`
implementation, `S3Adapter`. **It was built and tested with zero AWS API calls, zero AWS
authentication attempts, zero credential access (`~/.aws` and `AWS_*` environment variables
were never read), and zero cloud resource creation/deletion.** The only network access used
was two official Go module downloads, from `proxy.golang.org`, with every checksum verified
against `sum.golang.org` (`go mod verify` → `all modules verified`) — explicitly authorized,
and explicitly distinct from any AWS network access, which never occurred.

### SIMULATED vs. AWS-LIVE — read this before trusting any claim below

Every test in `cloud_s3_adapter_test.go` runs `S3Adapter` against `FakeS3API`
(`cloud_s3_fake_test.go`) — an entirely in-memory, hand-written model of S3's **documented**
behavior (pagination markers, `BucketNotEmpty`, per-item `DeleteObjects` failures, AWS error
codes like `SlowDown`/`ExpiredToken`/`NoSuchTagSet`). A PASS proves `S3Adapter`'s own
orchestration logic — pagination loops, 1000-item batching, error-code-to-sentinel mapping,
scope checks, never-touch-unrelated-bucket — behaves correctly **against that model**. It
does **not** prove a real AWS account's S3 API behaves identically to the model, nor that the
model's error codes/response shapes exactly match what a real `*s3.Client` call returns in
every circumstance (eventual consistency timing, exact throttling backoff behavior, and real
IAM-condition enforcement in particular — see "A deliberately unverified assumption," still
unresolved). Anywhere this document says **LOCAL_VERIFIED (SIMULATED)**, read it as "proven
against the hand-written model, not against AWS." Anywhere it says **AWS_NOT_VERIFIED**, read
it as "no run against this increment has ever touched a real AWS endpoint, so there is no
live evidence at all, positive or negative."

### What was added

```
go.mod / go.sum (edited)
  github.com/aws/aws-sdk-go-v2/service/s3 v1.116.0   (direct)
  github.com/aws/smithy-go v1.28.9                    (direct, for error-type classification)
  + their transitive internal/presigned-url, internal/checksum, aws/protocol/eventstream,
    internal/v4a, internal/configsources, internal/endpoints/v2, service/internal/s3shared,
    service/internal/accept-encoding (all // indirect, pulled automatically by `go get`)
  -- deliberately NOT added: aws-sdk-go-v2/config, aws-sdk-go-v2/credentials, or any other
     credential-resolution module. Confirmed: `grep -i aws go.mod` lists only the above.

internal/playground/
  cloud_s3_api.go          S3API -- the 7-method subset of *s3.Client this adapter needs,
                           with each method's exact signature copied from the real SDK.
                           var _ S3API = (*s3.Client)(nil) is a compile-time-only proof that
                           a real client satisfies this interface -- the nil pointer is never
                           constructed, dereferenced, or dialed.
  cloud_s3_adapter.go      S3Adapter -- the real CloudClient implementation: ListByTag,
                           VerifyOwnership, Remove, plus pagination (deleteAllObjectVersions,
                           abortAllMultipartUploads, batchDelete) and classifyS3Error (maps
                           AWS error codes onto this package's existing ErrCloudThrottled /
                           ErrCloudCredentialExpired / ErrCloudBucketNotEmpty / ErrCloudUnavailable
                           sentinels -- cloud.go, unchanged).
  cloud_s3_fake_test.go    FakeS3API -- test-only, in-memory model of the 7 S3API methods,
                           supporting pagination markers, object versions, delete markers,
                           multipart uploads, per-item DeleteObjects failures, and injectable
                           AWS-shaped errors via smithy.GenericAPIError.
  cloud_s3_adapter_test.go 24 tests against FakeS3API -- see "What's tested" below.
```

No file outside `internal/playground` and `go.mod`/`go.sum` was touched. `cmd/playground/main.go`
still has no `--cloud`/`track-cloud` flags — wiring a real client into the CLI is explicitly
not part of this increment (constructing one needs `config`/credential resolution, which this
increment deliberately does not add; see "Who constructs the real client" below).

### Who constructs the real client (and who never does)

`S3Adapter` never constructs an `*s3.Client` and never resolves a credential. `NewS3Adapter(api
S3API, account, region string) *S3Adapter` takes `api` as a parameter — it must be supplied by
calling code that is *not* in this package. A real deployment would, somewhere outside
`internal/playground` (not written in this increment):

1. Import `github.com/aws/aws-sdk-go-v2/config` (not currently a dependency of this repo).
2. Call `config.LoadDefaultConfig(ctx, ...)` to resolve credentials from whatever source is
   approved (environment, shared config file, instance role, etc.).
3. Construct `s3.NewFromConfig(cfg)`.
4. Pass that `*s3.Client` as `S3API` to `NewS3Adapter`.

None of steps 1–4 exist anywhere in this repository. This is a deliberate scope boundary, not
an oversight: the task authorizing this increment explicitly said "do not add SDK config or
credentials modules unless technically necessary," and they are not necessary to implement or
test the adapter's own logic — only to eventually run it for real, which is exactly the
separately-approved live validation plan's job.

### Exact resource identity, ownership, and the `ExpectedBucketOwner` defense-in-depth

Every one of the seven `S3API` calls `S3Adapter` makes that supports it (all seven do) sets
`ExpectedBucketOwner` to `a.Account`. This is a real, independent, server-side AWS check: S3
itself refuses the request with `AccessDenied` if the bucket's actual owning account does not
match — entirely apart from this package's own tag-based `VerifyOwnership` check. `checkScope`
additionally rejects, before any API call at all, a `CloudResourceRef` whose `Account`/`Region`
does not match this adapter's own configured scope — `TestS3AdapterRemove_RejectsCrossScopeRefBeforeAnyAPICall`
and `TestS3AdapterVerifyOwnership_RejectsCrossScopeRef` confirm zero API calls occur in that
case.

`Account` itself remains **operator-asserted, not independently confirmed** by any call this
adapter makes — confirming it from AWS itself would require an `sts:GetCallerIdentity` call
and the STS service module, which this increment does not add (not technically necessary for
S3 cleanup). This is safe, not merely convenient: a wrong `Account` value can only ever cause
`ExpectedBucketOwner` to correctly **refuse** a call (fewer successful removals, never more) —
it can never grant unintended access, because every real call is authenticated by whatever
credentials `API` was constructed with, never by the `Account` string. Recorded here as
**Inference requiring team confirmation** only insofar as nobody has yet independently proven
`ExpectedBucketOwner` is enforced the way AWS's documentation says, against a real account —
see the live validation plan.

### Supported and unsupported cases

| Case | Supported | How |
|---|---|---|
| Live object versions | Yes | `ListObjectVersions` (paginated via `KeyMarker`/`VersionIdMarker`) → batched `DeleteObjects` |
| Delete markers | Yes | Same `ListObjectVersions` response, `DeleteMarkers` field, same batched delete |
| In-progress multipart uploads | Yes | `ListMultipartUploads` (paginated via `KeyMarker`/`UploadIdMarker`) → `AbortMultipartUpload` per upload |
| `>1000` versions/delete-markers in one bucket | Yes | `batchDelete` splits into ≤`maxDeleteObjectsBatch` (1000) per `DeleteObjects` call — `TestS3AdapterRemove_BatchesDeleteObjectsOverTheThousandItemLimit` |
| `BucketNotEmpty` after this adapter's own emptying pass (e.g. eventual consistency) | Yes, surfaced, never swallowed | `DeleteBucket`'s own error is authoritative; mapped straight to `ErrCloudBucketNotEmpty` regardless of what this adapter's own enumeration believed — `TestS3AdapterRemove_DeleteBucketBucketNotEmptyIsNeverSwallowed` |
| A per-item `DeleteObjects` failure (e.g. Object Lock / legal hold) | Yes | Any non-empty `Errors` in a `DeleteObjects` response stops the batch and reports `ErrCloudBucketNotEmpty`; `DeleteBucket` is never even attempted — `TestS3AdapterRemove_PerItemDeleteFailureReportsBucketNotEmptyNeverDeletesBucket` |
| Throttling (`SlowDown`, `ThrottlingException`, `RequestLimitExceeded`, `TooManyRequestsException`) | Yes | Mapped to `ErrCloudThrottled` at every call site, via `classifyS3Error` |
| Credential/token expiry (`ExpiredToken`, `InvalidClientTokenId`, `SignatureDoesNotMatch`, etc.) | Yes | Mapped to `ErrCloudCredentialExpired` |
| Access denied (real permission problem, or `ExpectedBucketOwner` mismatch) | Yes, fails closed | Mapped to `ErrCloudUnavailable` — this adapter cannot distinguish the two causes from the error alone, so it never guesses; both are "could not check," never "confirmed absent" |
| Bucket already gone (`NoSuchBucket`) at any stage of `Remove` | Yes, idempotent success | Matches `FakeCloudClient`/`DockerClient`'s existing "already absent is not an error" contract — `TestS3AdapterRemove_AlreadyGoneBucketIsSuccessNotError` |
| No tags at all (`NoSuchTagSet`) | Yes | Treated as "doesn't match any tag filter" / "not owned" — never an error — matching `FakeCloudClient`'s existing contract |
| Any AWS error code this adapter does not specifically recognize | Yes, fails closed | `classifyS3Error`'s `default` case maps to `ErrCloudUnavailable` — never treated as "confirmed absent," never silently swallowed |
| Tag-based `ListByTag` across an account | Yes, client-side filtered | S3 has **no native "list buckets by tag" API** — `ListByTag` calls `ListBuckets` (server-side filtered by `BucketRegion`, paginated via `ContinuationToken`), then reads each candidate bucket's tags directly via `GetBucketTagging` and filters locally. This means `ListByTag`'s cost scales with the number of buckets in the account+region, not the number actually tagged for this package — a real-world scale concern noted here, not yet load-tested (no real account exists to load-test against) |
| EC2, EKS, RDS, NAT Gateway, or any non-S3 resource kind | **No, by design** | `ValidateCloudResourceKind` rejects anything but `CloudS3Bucket` before any API call — unchanged v1 scope decision, now also enforced inside `S3Adapter` itself (`TestS3AdapterListByTag_RejectsUnsupportedKind`) |
| S3 Object Lock / legal hold forcing a real empty-override | **No** | `Remove` never passes `BypassGovernanceRetention: true` — a bucket genuinely protected by governance-mode retention will correctly fail as `ErrCloudBucketNotEmpty` rather than this adapter forcing it open. Deliberate: bypassing a legal hold automatically is a human decision, not a cleanup-automation one |
| Cross-account STS role assumption / resource sharing | **No** | Out of scope — one `S3Adapter` is one account+region, matching `CloudClient`'s existing documented contract |
| Real eventual-consistency timing (how long after a real `DeleteObjects` call a real `ListObjectVersions` reliably reflects it) | **Unknown — AWS_NOT_VERIFIED** | `FakeS3API` is strongly consistent by construction (it's a Go map under a mutex); real S3's actual consistency timing for this specific sequence has never been observed by this increment |

### What's tested (evidence, not claims)

24 new tests, all passing, `-race` clean. Every pre-existing cloud test (`FakeCloudClient`-based)
also still passes unmodified — `S3Adapter` is additive, not a replacement.

| Requirement (from the authorizing task) | Test(s) |
|---|---|
| Injected API interface, never a constructed live client | `TestNewS3AdapterTouchesNothingAtConstruction` |
| Exact account/region scope enforced before any call | `TestS3AdapterVerifyOwnership_RejectsCrossScopeRef`, `TestS3AdapterRemove_RejectsCrossScopeRefBeforeAnyAPICall` |
| Pagination — `ListBuckets` | `TestS3AdapterListByTag_PaginatesAcrossMultipleListBucketsCalls` |
| Pagination — `ListObjectVersions` / `ListMultipartUploads` | `TestS3AdapterRemove_PaginatesVersionsAndMultipartUploads` |
| `DeleteObjects` 1000-item batching | `TestS3AdapterRemove_BatchesDeleteObjectsOverTheThousandItemLimit` |
| Object versions, delete markers, multipart uploads all emptied before `DeleteBucket` | `TestS3AdapterRemove_EmptiesVersionsDeleteMarkersAndUploadsThenDeletesBucket` |
| Partial failure (per-item `DeleteObjects` error) never silently treated as success | `TestS3AdapterRemove_PerItemDeleteFailureReportsBucketNotEmptyNeverDeletesBucket` |
| `BucketNotEmpty` from `DeleteBucket` itself (simulated eventual-consistency surprise) never swallowed | `TestS3AdapterRemove_DeleteBucketBucketNotEmptyIsNeverSwallowed` |
| Throttling retried by a later pass, not silently treated as success | `TestS3AdapterRemove_TransientThrottlingOnDeleteBucketIsRetryableNotSuccess` |
| Throttling / credential-expiry error-code mapping | `TestS3AdapterListByTag_ThrottlingMapsToErrCloudThrottled`, `TestS3AdapterListByTag_CredentialExpiryMapsToErrCloudCredentialExpired` |
| Unrecognized AWS error fails closed, never "confirmed absent" | `TestS3AdapterListByTag_UnrecognizedErrorFailsClosedAsUnavailable` |
| Never deletes an unrelated bucket | `TestS3AdapterRemove_NeverTouchesAnUnrelatedBucket` |
| Idempotent on an already-gone bucket | `TestS3AdapterRemove_AlreadyGoneBucketIsSuccessNotError` |
| Untagged bucket skipped, not an error | `TestS3AdapterListByTag_UntaggedBucketIsSkippedNotError` |
| Region-scoped `ListByTag` (never cross-region) | `TestS3AdapterListByTag_MatchesOnlyTaggedBucketsInOwnRegion` |
| Unsupported resource kind rejected before any call | `TestS3AdapterListByTag_RejectsUnsupportedKind` |
| Drop-in integration with the existing `Controller`/`RemoveTrackedCloudResources` orchestration — `verified_clean` invariant, retry, escalation all unchanged | `TestS3AdapterIntegratesWithExistingCloudCleanupOrchestration`, `TestS3AdapterIntegration_ThrottlingEscalatesExactlyLikeFakeCloudClient` |

### Required future IAM permissions (documented now, **not applied**)

Nothing below has been created, attached, or modified. This is a specification for the
separately-approved live validation plan, scoped as tightly as the real S3 API's documented
support for `aws:ResourceTag` conditions allows — see "A deliberately unverified assumption"
above for what remains actually unconfirmed about that scoping.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "ForgeStackCleanupS3ListAndTag",
      "Effect": "Allow",
      "Action": [
        "s3:ListAllMyBuckets",
        "s3:GetBucketTagging",
        "s3:GetBucketLocation"
      ],
      "Resource": "*"
    },
    {
      "Sid": "ForgeStackCleanupS3EmptyAndDelete",
      "Effect": "Allow",
      "Action": [
        "s3:ListBucketVersions",
        "s3:ListMultipartUploadParts",
        "s3:ListBucketMultipartUploads",
        "s3:DeleteObject",
        "s3:DeleteObjectVersion",
        "s3:AbortMultipartUpload",
        "s3:DeleteBucket"
      ],
      "Resource": "arn:aws:s3:::forgestack-*",
      "Condition": {
        "StringEquals": {
          "aws:ResourceTag/forgestack.managed-by": "forgestack-cleanup"
        }
      }
    }
  ]
}
```

Limitations of this IAM specification, stated plainly:

- `s3:ListAllMyBuckets` cannot be scoped by resource tag at all (it is an account-level,
  bucket-name-listing action in IAM's model) — this is why `S3Adapter.ListByTag` must
  additionally filter by `GetBucketTagging` in application code (see "Supported and
  unsupported cases" above); IAM alone cannot narrow `ListBuckets` to only tagged buckets.
- Whether `aws:ResourceTag` actually works as a condition key for the specific bucket-level
  actions listed under `ForgeStackCleanupS3EmptyAndDelete` is **still the same open question**
  recorded in "A deliberately unverified assumption" above — AWS's own documentation has
  historically been inconsistent about which S3 bucket (as opposed to object) actions support
  resource-tag conditions. This policy is a starting proposal for the live validation plan to
  test and correct, not a confirmed-working policy.
- The `Resource` ARN pattern `arn:aws:s3:::forgestack-*` assumes every bucket this package
  manages is named with a `forgestack-` prefix. This is a naming convention this package does
  not itself enforce anywhere in code — it must be adopted and enforced operationally (e.g. at
  bucket-creation time, outside this package) before this IAM policy can be relied upon as a
  real boundary; until then, the `ExpectedBucketOwner` check and this package's own
  `VerifyOwnership` are the only enforced boundaries.
- This policy grants nothing toward constructing the client itself (no `sts:GetCallerIdentity`,
  no KMS permissions for an encrypted bucket, etc.) — it is scoped exactly to what `S3Adapter`'s
  current seven API calls need, nothing more.

### Remaining safety limitations (in addition to the pre-existing ones above)

- **No live evidence this adapter's error-code mapping matches real AWS responses.**
  `classifyS3Error` was written against AWS's published error-code documentation and
  `smithy.GenericAPIError`-constructed fakes, not against errors actually observed from a real
  S3 API call. A real error could use a code this mapping does not recognize — which is why
  the `default` case fails closed to `ErrCloudUnavailable` rather than guessing, but the exact
  set of codes a real account produces for each failure mode remains unconfirmed.
- **`ListByTag`'s cost scales with total buckets in the account+region**, not just this
  package's own tagged buckets (see "Supported and unsupported cases" above) — a real account
  with many buckets could make this adapter's reconciliation pass slow or call-heavy. Not
  load-tested; no real account exists to load-test against in this increment.
- **The residual `VerifyOwnership` → `Remove` TOCTOU window (documented above, pre-existing)
  is unchanged by this adapter** — `S3Adapter` does not close it, and S3 has no
  delete-if-tag-matches primitive this adapter uses to close it. Still recorded as a residual,
  not-fully-closeable limitation.
- **`ExpectedBucketOwner` as a defense-in-depth mechanism is itself `AWS_NOT_VERIFIED`** —
  this adapter sets it on every call, and AWS's documentation says a mismatch causes
  `AccessDenied`, but no real call has ever confirmed this behavior.
- **No retry/backoff logic inside `S3Adapter` itself.** Exactly like the existing Docker
  phase and the original cloud-phase design, a transient throttling failure surfaces as an
  error from this one call and is retried by the *next* `CleanupDue`/worker pass (via
  `Controller`'s existing bounded-retry/backoff machinery in `controller.go`/`cloud_retry.go`),
  never by an internal retry loop inside the adapter. This is a deliberate reuse of the
  existing, already-tested cadence, not a new gap.

### Live AWS validation plan — update note

The "Live AWS validation plan" section above (written for the local-only increment) listed
"Build the real `CloudClient`" as step 1. **That step is now done** — `S3Adapter` exists — but
every other step in that plan (steps 2–7: creating a real disposable bucket, running real
`CloudReconcile`/`RemoveTrackedCloudResources` calls against it, real failure injection, real
IAM policy attachment and testing) remains **entirely NOT EXECUTED**, approval-gated, and
unchanged by this increment. This increment does not advance past step 1 of that plan — it
only completes step 1's prerequisite (the adapter to validate), with the exact same strict
"nothing below has been run" framing the original plan stated for every subsequent step.

## CLI integration (`playground cleanup-due`/`worker`), simulated-only

`internal/playground/cloud_provider.go` adds `playground.NewCloudClient(provider, mode,
account, region, root)` -- the single choke point `cmd/playground`'s `--cloud-provider`/
`--cloud-mode`/`--cloud-account`/`--cloud-region`/`--cloud-retry-backoff` flags (on
`cleanup-due` and `worker`) go through, and a new `playground track-cloud` CLI subcommand
(mirroring the existing `track-docker`) to actually record a tracked `CloudResourceRef`
through the CLI, which previously had no way to do so at all.

**Explicit, future-proof provider selection -- no S3 hardcoded into the CLI.**
`cloudProviderRegistry` (`cloud_provider.go`) is a `map[string]cloudProviderFactory`; `"s3"` is
the only entry today. Adding a second AWS service later means writing one more
`newXxxCloudProvider` function with the same signature and adding one more map entry --
nothing in `cmd/playground`'s flag parsing, dispatch, or help text changes, and
`KnownCloudProviders()` keeps CLI help text in sync automatically. `--cloud-provider` left at
its default `""` means `cloudFlags.apply` (`cmd/playground/main.go`) returns the `Controller`
completely unchanged -- `playground.NewCloudClient` is never called, no provider-specific code
runs, and behavior is byte-for-byte identical to before this flag existed (*confirmed from
test*, `TestCLICloudProvider_NoCloudOptionsNeverInitializesCloudComponent`).

**Simulation mode, deterministic fixtures.** `--cloud-mode=sim` (the default) builds an
in-memory `FakeCloudClient`, seeded by `seedSimulatedS3Fixtures` purely from what is already
tracked under `root` via `track-cloud` -- never randomness, never the wall clock -- then wraps
it in `scopedSimulatedCloudClient`, which actively refuses (errors on) any ref outside the
client's own configured account+region, mirroring the real `S3Adapter.checkScope` exactly
(*confirmed from code*: `errSimulatedScopeMismatch`; *confirmed from test*,
`TestNewCloudClient_CrossScopeRefIsRejectedByReturnedClient`). Three bucket-name conventions
select fixture behavior (documented fully on `seedSimulatedS3Fixtures`): no suffix (clean,
owned, cleans up immediately), `-missing-tag` (tag drift, never verifies), `-flaky-N` (fails
`N` attempts, computed from the already-persisted `cloud-cleanup-state.json` attempt count --
not an in-process counter -- so it converges correctly across separate `cleanup-due` process
restarts, not just within one long-running `worker`).

**Labeling is never a caller-set flag.** Every `CleanupReport`/log line's `SIMULATED`/`LIVE`
label is derived by the pre-existing, independently-audited `cloudVerificationMode`
(`cleanup_report.go`) from the returned `CloudClient`'s own concrete type -- this increment
adds zero new labeling logic, and the simulated client it builds has no way to ever report
`LIVE` (*confirmed from test*, `TestNewCloudClient_SimulatedReturnsScopedClientLabeledSimulated`).

**Real/live mode fails closed, unconditionally.** `--cloud-mode=live` returns
`errLiveCloudModeNotAvailable` as the very first statement in `newS3CloudProvider`, before even
checking account/region. `cloud_provider.go` does not import
`github.com/aws/aws-sdk-go-v2/config` or `.../credentials`, and a dedicated build-graph-level
test (`TestNoAWSCredentialOrConfigPackageIsEverCompiledIn`, `cloud_no_aws_access_test.go`) fails
the build if either package ever becomes a transitive dependency of this package again --
*confirmed from test*, not just from a comment. A second test
(`TestCLICloudProvider_LiveModeFailsClosedWithNoAWSEnvironment`) runs the real compiled binary
with every `AWS_*` and `HOME` environment variable stripped and asserts the exact static
fail-closed error message, never an AWS-SDK-shaped error, confirming no credential resolution
was attempted.

**Everything downstream of `WithCloud` is untouched.** `Controller.CleanupDue`/
`attemptCloudCleanup`/`cloud_retry.go`'s bounded-retry, backoff, and escalation state machine,
`CloudReconcile`/`RemoveTrackedCloudResources`'s ownership re-verification, and
`VerifyOwnership`-then-`Remove` ordering are not modified by this CLI integration at all --
only a `CloudClient` is now reachable to feed them from the CLI. `--cloud-retry-backoff`
(default 1 minute, matching `NewController`'s own default) is the one new tunable, letting an
operator shrink or disable the existing safety gate without touching library code; proven
end-to-end, including a full 2-failure-then-success convergence across three separate
`cleanup-due` process restarts (not three passes of one process), by
`TestCLICloudProvider_RetriesEscalationAndRestartRecovery`.

**Remaining real-AWS integration gaps -- explicit, not implied.** Wiring a real, credentialed
`S3Adapter` into the CLI is a separately approved, not-yet-started increment, and will require,
at minimum, beyond what this CLI integration does:

1. **Independent account identity verification.** `S3Adapter.Account` is still
   operator-asserted (see "Real S3Adapter" above) -- a live CLI path would need an `sts`
   `GetCallerIdentity` call (and the `aws-sdk-go-v2/service/sts` + `.../config`/`.../credentials`
   modules this repository deliberately does not have today) to confirm the account a real
   client actually authenticates as matches the `--cloud-account` an operator typed, rather than
   trusting the string.
2. **Restricted IAM, scoped to exactly this adapter's 8 S3 API calls** (`ListBuckets`,
   `GetBucketTagging`, `ListObjectVersions`, `ListMultipartUploads`, `DeleteObjects`,
   `AbortMultipartUpload`, `DeleteBucket`, `HeadBucket`) -- never a broader `s3:*` grant -- and
   reviewed against this repository's existing `$4` AWS budget/`ForgeStackTestRole` constraints
   before any credential is ever generated.
3. **Explicit, separate approval before the live branch is implemented at all** -- not implied
   by this CLI integration shipping `--cloud-mode=live` as an accepted flag value. That value
   exists today only so the CLI surface and `cloudProviderRegistry` already have a place for it;
   accepting the flag value is not authorization to build what it would do.
4. **Independent deletion evidence beyond this package's own `CleanupReport`.** A real deletion
   should be corroborated by something this process did not itself write -- e.g. an AWS
   CloudTrail event, or a separate read-only listing pass run by different tooling/credentials --
   so "we believe we deleted it" and "it is independently confirmed gone" are never the same
   claim resting on the same code path.

### Full verification after this increment

`gofmt` clean, `go vet ./...` clean, `go build ./...` clean, `golangci-lint run ./...` → 0
issues (repo-wide), `go test ./... -race -count=1` → all packages pass, including the 24 new
`S3Adapter` tests and every pre-existing test in this repository, unmodified. `go mod verify`
→ `all modules verified`. No AWS API call (including read-only), no AWS CLI invocation, no
`~/.aws` or `AWS_*` environment variable read, no Terraform execution, no IAM/budget change,
and no cloud resource creation/mutation/deletion occurred at any point in building or testing
this increment. No commit, push, PR, merge, or release was made.
