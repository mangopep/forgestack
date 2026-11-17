package playground

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"
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
}

// NewController returns a Controller with real-world defaults: the
// standard Manager, wall-clock time.Now/time.Sleep, 3 cleanup attempts
// with a 2-second backoff between them, and no Docker client (call
// WithDocker to add one). Tests should not use this -- they construct a
// Controller literal directly so Now/Sleep/Docker are deterministic.
func NewController() Controller {
	return Controller{
		Manager:            NewManager(),
		Now:                func() time.Time { return time.Now().UTC() },
		Sleep:              time.Sleep,
		MaxCleanupAttempts: 3,
		RetryBackoff:       2 * time.Second,
	}
}

// WithDocker returns a copy of c with Docker set, for the common case of
// taking NewController()'s defaults and adding a real Docker client.
func (c Controller) WithDocker(d DockerClient) Controller {
	c.Docker = d
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
// restart finding that marker is how it knows to resume), or a prior
// attempt ran to completion but failed or couldn't verify success.
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
	return results, errors.Join(errs...)
}

func (c Controller) cleanupOne(ctx context.Context, root string, env Environment) (Result, error) {
	pending := env
	pending.Status = StatusCleanupPending
	if err := c.persist(root, pending); err != nil {
		return Result{Environment: env}, err
	}

	result, err := c.retryLocalCleanup(root, env.ID, env.Owner)
	if result.Environment.Status != StatusVerifiedClean {
		return result, err // local cleanup itself already recorded why (CLEANUP_INCOMPLETE/CLEANUP_UNVERIFIED) -- nothing more to attempt this round
	}

	if c.Docker == nil {
		return result, nil
	}
	rec, dockerErr := RemoveTrackedDockerResources(ctx, c.Docker, root, env.ID)
	if dockerErr != nil || !rec.Clean() {
		downgraded := result.Environment
		downgraded.Status = StatusCleanupUnverified
		if persistErr := c.persist(root, downgraded); persistErr != nil && dockerErr == nil {
			dockerErr = persistErr
		}
		result.Environment = downgraded
		result.Cleanup = "CLEANUP UNVERIFIED (docker resources remain or could not be checked)"
		if dockerErr == nil {
			dockerErr = errDockerResourcesRemain
		}
		return result, dockerErr
	}
	return result, nil
}

// retryLocalCleanup calls Manager.Cleanup up to MaxCleanupAttempts times,
// sleeping RetryBackoff between attempts, stopping as soon as one attempt
// reaches StatusVerifiedClean. A bounded retry count is deliberate: an
// environment whose resources genuinely cannot be removed (e.g. a
// permissions problem) must eventually surface as CLEANUP_INCOMPLETE for
// a human to look at, not retry forever.
func (c Controller) retryLocalCleanup(root, id, owner string) (Result, error) {
	attempts := c.MaxCleanupAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var result Result
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		result, err = c.Manager.Cleanup(root, id, owner)
		if result.Environment.Status == StatusVerifiedClean {
			return result, nil
		}
		if attempt < attempts && c.Sleep != nil {
			c.Sleep(c.RetryBackoff)
		}
	}
	return result, err
}

func (c Controller) persist(root string, env Environment) error {
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	return writeMetadata(filepath.Join(rootPath, env.ID, "environment.json"), env)
}

var errDockerResourcesRemain = dockerResourcesRemainError{}

type dockerResourcesRemainError struct{}

func (dockerResourcesRemainError) Error() string {
	return "docker resources for this environment remain or could not be independently verified absent"
}
