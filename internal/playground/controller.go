package playground

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

// Controller orchestrates automatic expiration and cleanup across every
// environment under a root, on top of Manager's existing Create/Cleanup
// (unchanged -- see lifecycle.go). Every time-dependent and I/O-dependent
// piece is an injected field specifically so tests can be deterministic:
// Now replaces time.Now(), Sleep replaces time.Sleep (a no-op in tests, so
// bounded-retry tests don't actually wait), and Docker replaces a real
// Docker daemon (nil means "don't attempt Docker reconciliation at all,"
// a FakeDockerClient means "verify against this in-memory state").
type Controller struct {
	Manager            Manager
	Now                func() time.Time
	Sleep              func(time.Duration)
	MaxCleanupAttempts int
	RetryBackoff       time.Duration
	// Docker is optional. A nil Docker means this Controller only manages
	// local filesystem state, exactly like Manager does on its own --
	// Docker reconciliation is additive, never required for local
	// cleanup to succeed.
	Docker DockerClient
	// Cloud is optional, exactly like Docker: a nil Cloud means this
	// Controller never attempts cloud reconciliation at all. Set via
	// WithCloud. See cloud.go/cloud_reconcile.go -- v1 supports S3
	// buckets only (ValidateCloudResourceKind enforces this).
	Cloud CloudClient
	// MaxCloudCleanupAttempts bounds how many real removal attempts
	// (across however many CleanupDue/worker passes it takes) are made
	// for one environment's tracked cloud resources before escalating to
	// StatusCleanupAttentionRequired and stopping automatic retries
	// entirely. Zero means defaultMaxCloudCleanupAttempts. Unlike Docker
	// (which retries unboundedly across passes, preserving this
	// package's original behavior), cloud failures are bounded because
	// an exhausted cloud resource keeps consuming real API calls/rate
	// limit budget on every pass, forever, with no human ever notified --
	// a materially worse consequence for a billable resource than for a
	// free local Docker container.
	MaxCloudCleanupAttempts int
	// CloudRetryBackoff, if nonzero, is the minimum time that must pass
	// between two real cloud removal attempts for the same environment.
	// A pass that finds a prior attempt too recent skips calling the
	// CloudClient entirely this round, rather than spending another API
	// call before it could plausibly help. A Controller literal's zero
	// value means no backoff gate at all -- every due pass attempts
	// immediately, which every deterministic test in this package relies
	// on. NewController sets a nonzero default instead (see
	// defaultCloudRetryBackoff, cloud_retry.go).
	//
	// This also happens to be this package's chosen defense against the
	// P2 finding from the second independent audit: two overlapping
	// CleanupDue passes for the same not-yet-exhausted environment (e.g.
	// a scheduled Worker pass overlapping an operator-triggered
	// `cleanup-due`) are serialized by withEnvironmentLock, never
	// concurrent, but without a backoff gate each one still makes its
	// own real removal attempt and consumes its own unit of the bounded
	// MaxCloudCleanupAttempts budget -- burning it roughly twice as fast
	// as an operator reading that setting would expect, though never
	// unsafely (no corruption, no false verified_clean; see
	// TestP2_ConcurrentCloudCleanupNeverReportsCleanWhileResourceRemains
	// and TestOverlappingCleanupDuePassesDoNotDoubleConsumeCloudAttemptBudget).
	// A nonzero CloudRetryBackoff closes this for real deployments
	// without inventing new dedup machinery: the second, lock-serialized
	// call re-reads the attempt state the first call just wrote and
	// finds itself still inside the backoff window, so it skips calling
	// the CloudClient at all for this round -- the exact mechanism
	// attemptCloudCleanup (controller.go) already has for an ordinary
	// solo retry, just also covering the overlapping-pass case for free.
	// A Controller built as a literal with CloudRetryBackoff left at its
	// zero value does not get this protection -- that is a deliberate,
	// documented tradeoff for test determinism, not an oversight.
	CloudRetryBackoff time.Duration

	// NOTE: this Controller deliberately has no CloudVerificationMode/
	// DockerVerificationMode field. An earlier version of this package
	// had one, letting a caller label a CleanupReport's verification
	// VerificationLive by simply setting a struct field -- a free-
	// floating assertion with no actual relationship to whether c.Cloud/
	// c.Docker was really a live, authenticated client. A second
	// independent audit flagged this as not acceptable ("LIVE
	// verification must require authenticated evidence from an approved
	// real adapter, not a caller-set enum"). See LiveCloudClient/
	// LiveDockerClient (cleanup_report.go): verification mode is now
	// derived strictly from whether c.Cloud/c.Docker's own concrete
	// type attests to being live, never from anything on this struct.

	// Metrics, if set, records Prometheus counters/gauges for every
	// cleanup/notification decision this Controller makes (see
	// telemetry.go). Left nil (the default -- NewController never sets
	// this), a Controller behaves with zero observability-related
	// behavior change, exactly like Worker.Notifier's own opt-in design:
	// every recordXxx/UpdateUnresolvedGauge call in telemetry.go is a
	// guaranteed no-op on a nil *Metrics, never a panic.
	Metrics *Metrics
}

// NewController returns a Controller with real-world defaults: the
// standard Manager, wall-clock time.Now/time.Sleep, 3 cleanup attempts
// with a 2-second backoff between them, and no Docker client (call
// WithDocker to add one). Tests should not use this -- they construct a
// Controller literal directly so Now/Sleep/Docker are deterministic.
func NewController() Controller {
	return Controller{
		Manager:                 NewManager(),
		Now:                     func() time.Time { return time.Now().UTC() },
		Sleep:                   time.Sleep,
		MaxCleanupAttempts:      3,
		RetryBackoff:            2 * time.Second,
		MaxCloudCleanupAttempts: defaultMaxCloudCleanupAttempts,
		CloudRetryBackoff:       defaultCloudRetryBackoff,
	}
}

// WithDocker returns a copy of c with Docker set, for the common case of
// taking NewController()'s defaults and adding a real Docker client.
func (c Controller) WithDocker(d DockerClient) Controller {
	c.Docker = d
	return c
}

// WithCloud returns a copy of c with Cloud set, for the common case of
// taking NewController()'s defaults and adding a cloud client. No
// AWS-SDK-backed CloudClient exists in this repository yet -- callers
// pass a FakeCloudClient in tests; wiring a real one in is a separately
// approved increment (see docs/architecture/CLOUD-CLEANUP-S3.md).
func (c Controller) WithCloud(cl CloudClient) Controller {
	c.Cloud = cl
	return c
}

// ExpireDue scans every environment under root and transitions each
// StatusActive playground whose ExpiresAt is at or before c.Now() to
// StatusExpired, persisting the change. Product environments (no
// ExpiresAt) and environments already past StatusActive are left
// untouched. Returns the environments actually transitioned.
func (c Controller) ExpireDue(root string) ([]Environment, error) {
	envs, err := ListEnvironments(root)
	if err != nil {
		return nil, err
	}
	var expired []Environment
	now := c.Now()
	for _, env := range envs {
		if env.Status != StatusActive || env.Mode != "playground" || env.ExpiresAt.IsZero() {
			continue
		}
		if now.Before(env.ExpiresAt) {
			continue
		}
		env.Status = StatusExpired
		if err := c.persist(root, env); err != nil {
			return expired, err
		}
		expired = append(expired, env)
	}
	return expired, nil
}

// needsCleanup reports whether env's status means a cleanup attempt is
// still owed: it expired, a prior cleanup attempt was interrupted
// (CleanupPending, left on disk by exactly this situation -- a process
// restart finding that marker is how it knows to resume, including the
// case where local cleanup succeeded but an additive domain's
// verification was interrupted before it could run at all), or a prior
// attempt ran to completion but failed or couldn't verify success.
//
// StatusCleanupAttentionRequired is deliberately excluded: once cloud
// cleanup has exhausted its bounded retry budget for an environment, no
// further automatic pass spends another API call on it until a human
// explicitly calls ResetCloudCleanupAttempts -- see cloud_retry.go.
func needsCleanup(status string) bool {
	switch status {
	case StatusExpired, StatusCleanupPending, StatusCleanupIncomplete, StatusCleanupUnverified:
		return true
	default:
		return false
	}
}

// CleanupDue finds every environment under root that needsCleanup and
// attempts to clean each one: local filesystem resources first (via
// Manager.Cleanup, retried up to MaxCleanupAttempts times with
// RetryBackoff between attempts), then -- only once local cleanup
// verifiably succeeds, and only if c.Docker is set -- removes and
// independently re-verifies any tracked Docker resources. Before the
// first attempt on each environment, its status is persisted as
// StatusCleanupPending: if this process is killed partway through, the
// next call to CleanupDue (by this process restarted, or a different one
// entirely -- it only ever reads from disk) finds that marker via
// needsCleanup and resumes, rather than the interruption going unnoticed.
func (c Controller) CleanupDue(ctx context.Context, root string) ([]Result, error) {
	ctx, span := startSpan(ctx, "cleanup_due_pass")
	defer span.End()

	envs, err := ListEnvironments(root)
	if err != nil {
		return nil, err
	}
	var results []Result
	var errs []error
	for _, env := range envs {
		if !needsCleanup(env.Status) {
			continue
		}
		result, cleanupErr := c.cleanupOne(ctx, root, env)
		results = append(results, result)
		if cleanupErr != nil {
			// Collected, not returned immediately: one environment's
			// failure must not stop the rest from being attempted.
			errs = append(errs, fmt.Errorf("%s: %w", env.ID, cleanupErr))
		}
	}
	if _, gaugeErr := UpdateUnresolvedGauge(c.Metrics, root); gaugeErr != nil {
		// Observability-only failure: never promoted to the function's
		// own return error, never allowed to affect cleanup.
		slog.Default().Warn("cleanup_unresolved_gauge_update_failed", "error", gaugeErr.Error())
	}
	return results, errors.Join(errs...)
}

// cleanupOne drives one environment through local cleanup and every
// configured additive domain (Docker, Cloud). The critical safety
// invariant it enforces: StatusVerifiedClean is persisted to disk if,
// and only if, every configured domain has been independently verified
// clean in this very call. Local cleanup alone reaching
// Manager.Cleanup's own internal success is never sufficient by itself
// when any additive domain is configured -- see localSuccessStatus
// below and finalizeCleanup's doc comment in lifecycle.go. This closes
// the exact crash window an earlier version of this package had: local
// cleanup's own persisted status and the controller's combined-domain
// decision are now never two separate terminal writes with a real gap
// between them -- the local phase persists a non-terminal, still-
// retryable status (StatusCleanupPending) whenever more verification is
// owed, and only verifyAdditionalDomains's own single locked write can
// ever persist the true StatusVerifiedClean.
func (c Controller) cleanupOne(ctx context.Context, root string, env Environment) (Result, error) {
	ctx, span := startSpan(ctx, "cleanup_one", attribute.String("environment_id", env.ID))
	defer span.End()

	pending := env
	pending.Status = StatusCleanupPending
	if err := c.persist(root, pending); err != nil {
		return Result{Environment: env}, err
	}

	hasAdditionalDomains := c.Docker != nil || c.Cloud != nil
	localSuccessStatus := StatusVerifiedClean
	if hasAdditionalDomains {
		localSuccessStatus = StatusCleanupPending
	}
	result, err := c.retryLocalCleanup(root, env.ID, env.Owner, localSuccessStatus)
	if result.Environment.Status != localSuccessStatus {
		// local cleanup itself already recorded why (CLEANUP_INCOMPLETE/
		// CLEANUP_UNVERIFIED) -- nothing more to attempt this round.
		recordCleanupAttempt(c.Metrics, "local", result.Environment.Status)
		logCleanupEvent(ctx, env.ID, result.Environment.Status, "local", 0, errString(err), "")
		return result, err
	}
	if !hasAdditionalDomains {
		// already verified_clean on disk; nothing further configured to check.
		recordCleanupAttempt(c.Metrics, "local", result.Environment.Status)
		logCleanupEvent(ctx, env.ID, result.Environment.Status, "local", 0, "", "")
		return result, nil
	}

	dir, err := environmentDir(root, env.ID)
	if err != nil {
		return result, err
	}

	// Every additive domain's own removal attempt AND the final combined
	// status decision all run inside this ONE lock acquisition -- not
	// one lock per domain plus an unlocked final write. That closes the
	// concurrent stale-write race a per-domain-lock-only design has: two
	// overlapping cleanupOne calls for the same environment can no
	// longer interleave their domain checks or their final status
	// writes, extending Manager.Cleanup's own already-proven
	// "whole read-decide-write cycle inside one lock" pattern to the
	// additive-domain phase.
	var final Result
	var finalErr error
	lockErr := withEnvironmentLock(dir, func() error {
		final, finalErr = c.verifyAdditionalDomains(ctx, root, env, result)
		return nil
	})
	if lockErr != nil {
		return result, lockErr
	}
	return final, finalErr
}

// verifyAdditionalDomains runs every configured additive domain's
// removal-and-reconcile step and persists the single, final combined
// status. Must be called with the environment's lock already held (see
// cleanupOne). Each domain runs regardless of another domain's outcome --
// a Docker failure must not prevent attempting, and independently
// reporting, cloud cleanup, and vice versa.
//
// SAFETY INVARIANT (fixed after a third independent audit): a domain
// with NO client configured on this Controller is never silently
// skipped. checkUnconfiguredDomain below reuses
// trackedDockerResourcesUnverifiable/trackedCloudResourcesUnverifiable
// (lifecycle.go) -- the exact same tracked-file check
// guardVerifiedCleanStatus already uses for Manager.cleanup's own
// direct-call path -- so a tracked-but-unconfigured domain counts as a
// failed (never escalated -- there is nothing to retry or reset; the
// fix is configuring the missing client) domain here too, before this
// function's own direct c.persist call can ever claim
// StatusVerifiedClean.
func (c Controller) verifyAdditionalDomains(ctx context.Context, root string, env Environment, localResult Result) (Result, error) {
	dir, dirErr := environmentDir(root, env.ID)
	if dirErr != nil {
		return localResult, dirErr
	}

	var failedDomains []string
	var escalatedDomains []string
	var firstErr error
	note := func(domain string, domainErr error, escalated bool) {
		failedDomains = append(failedDomains, domain)
		if escalated {
			escalatedDomains = append(escalatedDomains, domain)
		}
		if firstErr == nil {
			firstErr = domainErr
		}
	}

	dockerErr, dockerEscalated := c.verifyDockerDomain(ctx, root, env, dir)
	if dockerErr != nil {
		note("docker", dockerErr, dockerEscalated)
	}
	cloudErr, cloudEscalated := c.verifyCloudDomain(ctx, root, env, dir)
	if cloudErr != nil {
		note("cloud", cloudErr, cloudEscalated)
	}

	// Per-domain telemetry: recorded only for a domain that was actually
	// exercised this call -- either a real client attempted it, or an
	// unconfigured-but-tracked domain's check (checkUnconfiguredDomain)
	// produced a failure. A domain genuinely not configured AND with
	// nothing tracked contributes no metric/log line at all: there was
	// nothing for this pass to say about it.
	if c.Docker != nil || dockerErr != nil {
		outcome := domainOutcome(dockerErr, false)
		recordCleanupAttempt(c.Metrics, "docker", outcome)
		logCleanupEvent(ctx, env.ID, outcome, "docker", 0, errString(dockerErr), dockerVerificationMode(c.Docker))
	}
	if c.Cloud != nil || cloudErr != nil {
		outcome := domainOutcome(cloudErr, cloudEscalated)
		recordCleanupAttempt(c.Metrics, "cloud", outcome)
		logCleanupEvent(ctx, env.ID, outcome, "cloud", 0, errString(cloudErr), cloudVerificationMode(c.Cloud))
	}
	if len(escalatedDomains) > 0 {
		recordAttentionRequired(c.Metrics)
	}

	result := localResult
	if len(failedDomains) == 0 {
		result.Environment.Status = StatusVerifiedClean
		result.Cleanup = "VERIFIED CLEAN"
		if err := c.persist(root, result.Environment); err != nil {
			return localResult, err
		}
		return result, nil
	}

	if len(escalatedDomains) > 0 {
		result.Environment.Status = StatusCleanupAttentionRequired
		result.Cleanup = fmt.Sprintf(
			"CLEANUP ATTENTION REQUIRED (%s resources remain after exhausting automatic retries; tracked resources were not discarded -- see ResetCloudCleanupAttempts for manual recovery)",
			strings.Join(escalatedDomains, ", "),
		)
	} else {
		result.Environment.Status = StatusCleanupUnverified
		result.Cleanup = fmt.Sprintf("CLEANUP UNVERIFIED (%s resources remain or could not be checked)", strings.Join(failedDomains, ", "))
	}
	if err := c.persist(root, result.Environment); err != nil {
		return localResult, err
	}
	return result, firstErr
}

// domainOutcome maps one domain's own err/escalated result onto the
// bounded outcome label set recordCleanupAttempt's metric uses.
func domainOutcome(err error, escalated bool) string {
	switch {
	case escalated:
		return "attention_required"
	case err != nil:
		return "unverified"
	default:
		return "verified_clean"
	}
}

// verifyDockerDomain is verifyAdditionalDomains' Docker branch, split
// out purely to keep that function's own branching readable. Returns a
// non-nil error whenever this domain must count as failed: either a
// real removal attempt found resources remaining (c.Docker != nil), or
// no Docker client is configured at all on this Controller AND dir has
// a tracked Docker resource this call therefore has no way to verify
// (checkUnconfiguredDomain, reusing trackedDockerResourcesUnverifiable,
// lifecycle.go) -- never escalated, since there is nothing to retry or
// reset; the fix for that case is configuring the missing client.
func (c Controller) verifyDockerDomain(ctx context.Context, root string, env Environment, dir string) (err error, escalated bool) {
	ctx, span := startSpan(ctx, "verify_docker_domain", attribute.String("environment_id", env.ID))
	defer span.End()
	if c.Docker == nil {
		return checkUnconfiguredDomain("docker", trackedDockerResourcesUnverifiable, dir, errDockerResourcesRemain), false
	}
	rec, dockerErr := RemoveTrackedDockerResources(ctx, c.Docker, root, env.ID)
	if dockerErr == nil && rec.Clean() {
		return nil, false
	}
	if dockerErr == nil {
		dockerErr = errDockerResourcesRemain
	}
	// Docker retries unboundedly across passes, preserving this
	// package's original, already-shipped behavior exactly -- only
	// Cloud gets the bounded-attempt escalation below.
	return dockerErr, false
}

// verifyCloudDomain is verifyAdditionalDomains' cloud equivalent.
func (c Controller) verifyCloudDomain(ctx context.Context, root string, env Environment, dir string) (err error, escalated bool) {
	ctx, span := startSpan(ctx, "verify_cloud_domain", attribute.String("environment_id", env.ID))
	defer span.End()
	if c.Cloud == nil {
		return checkUnconfiguredDomain("cloud", trackedCloudResourcesUnverifiable, dir, errCloudResourcesRemain), false
	}
	return c.attemptCloudCleanup(ctx, root, env)
}

// attemptCloudCleanup makes (at most) one real removal attempt against
// c.Cloud for env's tracked cloud resources, gated by a persisted,
// per-environment backoff and bounded by MaxCloudCleanupAttempts. Must
// be called with the environment's lock already held (see cleanupOne),
// so its own read-decide-write of the attempt-state sidecar file never
// races a concurrent caller's.
//
// Returns (nil, false) on success (and clears any accumulated attempt
// history, so a human reading it later never sees a stale failure
// record for a resource that is, in fact, gone). Returns (err, false) on
// an ordinary not-yet-exhausted failure (the caller downgrades to
// StatusCleanupUnverified, exactly as before this change). Returns
// (err, true) once this failure pushes the attempt count to the
// configured bound -- the caller escalates to
// StatusCleanupAttentionRequired instead, and no further automatic pass
// will call c.Cloud for this environment again until a human calls
// ResetCloudCleanupAttempts.
func (c Controller) attemptCloudCleanup(ctx context.Context, root string, env Environment) (err error, escalated bool) {
	dir, dirErr := environmentDir(root, env.ID)
	if dirErr != nil {
		return dirErr, false
	}
	state, loadErr := loadCloudCleanupAttemptState(dir)
	if loadErr != nil {
		return loadErr, false
	}

	now := c.Now()
	if state.Attempts > 0 && c.CloudRetryBackoff > 0 && now.Sub(state.LastAttemptAt) < c.CloudRetryBackoff {
		// Not due for another attempt yet -- skip calling the
		// CloudClient entirely this pass rather than spending an API
		// call before it could plausibly help.
		return fmt.Errorf("cloud cleanup backoff in effect until %s (attempt %d/%d, last error: %s): %w",
			state.LastAttemptAt.Add(c.CloudRetryBackoff), state.Attempts, c.maxCloudCleanupAttempts(), state.LastError, errCloudResourcesRemain), false
	}

	rec, cloudErr := RemoveTrackedCloudResources(ctx, c.Cloud, root, env.ID, env.Owner)
	if cloudErr == nil && rec.Clean() {
		if clearErr := clearCloudCleanupAttemptState(dir); clearErr != nil {
			return clearErr, false
		}
		return nil, false
	}
	if cloudErr == nil {
		// CloudAvailable == false (unavailable/throttled/credential
		// failure) or resources genuinely remain present -- either way,
		// never treated as success.
		cloudErr = errCloudResourcesRemain
	}

	state.Attempts++
	state.LastAttemptAt = now
	state.LastError = cloudErr.Error()
	if saveErr := saveCloudCleanupAttemptState(dir, state); saveErr != nil {
		return saveErr, false
	}

	if state.Attempts >= c.maxCloudCleanupAttempts() {
		return cloudErr, true
	}
	return cloudErr, false
}

func (c Controller) maxCloudCleanupAttempts() int {
	if c.MaxCloudCleanupAttempts <= 0 {
		return defaultMaxCloudCleanupAttempts
	}
	return c.MaxCloudCleanupAttempts
}

// retryLocalCleanup calls Manager's internal cleanup up to
// MaxCleanupAttempts times, sleeping RetryBackoff between attempts,
// stopping as soon as one attempt reaches successStatus. A bounded retry
// count is deliberate: an environment whose resources genuinely cannot
// be removed (e.g. a permissions problem) must eventually surface as
// CLEANUP_INCOMPLETE for a human to look at, not retry forever.
//
// successStatus is StatusVerifiedClean when cleanupOne has no additive
// domain configured (nothing further to verify, so local success really
// is the final word), or StatusCleanupPending when it does -- see
// cleanupOne and finalizeCleanup's doc comment in lifecycle.go for why
// the terminal StatusVerifiedClean must never be written here in that
// case.
func (c Controller) retryLocalCleanup(root, id, owner, successStatus string) (Result, error) {
	attempts := c.MaxCleanupAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var result Result
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		result, err = c.Manager.cleanup(root, id, owner, successStatus)
		if result.Environment.Status == successStatus {
			return result, nil
		}
		if attempt < attempts && c.Sleep != nil {
			c.Sleep(c.RetryBackoff)
		}
	}
	return result, err
}

func (c Controller) persist(root string, env Environment) error {
	dir, err := environmentDir(root, env.ID)
	if err != nil {
		return err
	}
	return writeMetadata(filepath.Join(dir, "environment.json"), env)
}

func environmentDir(root, id string) (string, error) {
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(rootPath, id), nil
}

var errDockerResourcesRemain = dockerResourcesRemainError{}

type dockerResourcesRemainError struct{}

func (dockerResourcesRemainError) Error() string {
	return "docker resources for this environment remain or could not be independently verified absent"
}

var errCloudResourcesRemain = cloudResourcesRemainError{}

type cloudResourcesRemainError struct{}

func (cloudResourcesRemainError) Error() string {
	return "cloud resources for this environment remain or could not be independently verified absent"
}

// checkUnconfiguredDomain returns a non-nil, wrapped baseErr when dir
// has any tracked resource in a domain this Controller has no client
// for -- or that domain's tracking file could not even be read (fails
// closed, via unverifiable) -- so verifyAdditionalDomains can treat a
// tracked-but-unconfigured domain exactly like a failed one, never a
// silently-skipped one.
func checkUnconfiguredDomain(domain string, unverifiable func(dir string) (bool, error), dir string, baseErr error) error {
	isUnverifiable, err := unverifiable(dir)
	if err != nil {
		return fmt.Errorf("%w (tracked %s resources could not be verified -- no %s client is configured on this Controller): %w", baseErr, domain, domain, err)
	}
	if isUnverifiable {
		return fmt.Errorf("%w: tracked %s resource(s) remain and no %s client is configured on this Controller", baseErr, domain, domain)
	}
	return nil
}
