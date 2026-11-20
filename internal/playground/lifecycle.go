// Package playground manages isolated, local-only ForgeStack environments.
package playground

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const maxTTL = 24 * time.Hour

var safeID = regexp.MustCompile(`^env-[a-f0-9]{16}$`)

// Status values an Environment can carry. These are the exact strings
// already written to disk and asserted by this package's existing tests
// (e.g. lifecycle_test.go checks literal "active"/"cleanup_incomplete"/
// "verified_clean") -- naming them here doesn't change a single value,
// it just gives the controller (controller.go) named constants instead of
// repeating the magic strings, and adds the two states that previously had
// no representation at all: StatusExpired and StatusCleanupPending.
const (
	StatusActive            = "active"
	StatusExpired           = "expired"
	StatusCleanupPending    = "cleanup_pending"
	StatusCleanupIncomplete = "cleanup_incomplete"
	StatusCleanupUnverified = "cleanup_unverified"
	StatusVerifiedClean     = "verified_clean"
	// StatusCleanupAttentionRequired marks a cleanup that has exhausted
	// its bounded automatic retry budget for at least one additive
	// domain (today: cloud -- see Controller.MaxCloudCleanupAttempts).
	// Deliberately excluded from needsCleanup's retry set: once an
	// environment reaches this status, no further automatic pass
	// attempts it (no more API calls are spent on it), and its tracked
	// resources are left exactly as they were -- never discarded --
	// until a human explicitly calls ResetCloudCleanupAttempts.
	StatusCleanupAttentionRequired = "cleanup_attention_required"
)

type Environment struct {
	ID        string    `json:"id"`
	Owner     string    `json:"owner"`
	Mode      string    `json:"mode"`
	Modules   []string  `json:"modules"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	Status    string    `json:"status"`
}

type Result struct {
	Environment Environment `json:"environment"`
	Cleanup     string      `json:"cleanup,omitempty"`
}

type Manager struct{ removeAll func(string) error }

func NewManager() Manager { return Manager{removeAll: os.RemoveAll} }

func validateCreateInputs(owner, mode string, ttl time.Duration, modules []string) error {
	if strings.TrimSpace(owner) == "" {
		return errors.New("owner is required")
	}
	if mode != "playground" && mode != "product" {
		return errors.New("mode must be playground or product")
	}
	if mode == "playground" && (ttl <= 0 || ttl > maxTTL) {
		return fmt.Errorf("playground TTL must be between 1s and %s", maxTTL)
	}
	if mode == "product" && ttl != 0 {
		return errors.New("product environments cannot have an expiration")
	}
	for _, module := range modules {
		if strings.TrimSpace(module) == "" {
			return errors.New("module names cannot be empty")
		}
	}
	return nil
}

// realRootDir creates root if needed and returns its absolute path, rejecting
// a symlinked root so cleanup's ownership checks cannot be redirected.
func realRootDir(root string) (string, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", fmt.Errorf("create environment root: %w", err)
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rootInfo, err := os.Lstat(rootPath)
	if err != nil {
		return "", err
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("environment root must be a real directory, not a symlink")
	}
	return rootPath, nil
}

func (m Manager) Create(root, owner, mode string, ttl time.Duration, modules []string) (Environment, error) {
	if err := validateCreateInputs(owner, mode, ttl, modules); err != nil {
		return Environment{}, err
	}
	rootPath, err := realRootDir(root)
	if err != nil {
		return Environment{}, err
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Environment{}, fmt.Errorf("generate environment ID: %w", err)
	}
	now := time.Now().UTC()
	env := Environment{ID: "env-" + hex.EncodeToString(random[:]), Owner: owner, Mode: mode, Modules: append([]string(nil), modules...), CreatedAt: now, Status: StatusActive}
	if mode == "playground" {
		env.ExpiresAt = now.Add(ttl)
	}
	sort.Strings(env.Modules)
	dir := filepath.Join(rootPath, env.ID)
	if err := os.Mkdir(dir, 0700); err != nil {
		return Environment{}, fmt.Errorf("create environment: %w", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "resources"), 0700); err != nil {
		_ = os.Remove(dir)
		return Environment{}, fmt.Errorf("create owned resource directory: %w", err)
	}
	if err := writeMetadata(filepath.Join(dir, "environment.json"), env); err != nil {
		_ = m.removeAll(dir)
		return Environment{}, fmt.Errorf("write environment ownership record: %w", err)
	}
	return env, nil
}

// cleanupRootState validates root is a real, non-symlinked directory and
// snapshots its entries before any removal, so a later inventory comparison
// can detect anything outside the owned environment changing.
func cleanupRootState(root string) (rootPath string, entriesBefore []os.DirEntry, err error) {
	rootPath, err = filepath.Abs(root)
	if err != nil {
		return "", nil, err
	}
	rootInfo, err := os.Lstat(rootPath)
	if err != nil {
		return "", nil, fmt.Errorf("inspect environment root: %w", err)
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", nil, errors.New("environment root must be a real directory")
	}
	entriesBefore, err = os.ReadDir(rootPath)
	if err != nil {
		return "", nil, err
	}
	return rootPath, entriesBefore, nil
}

// loadOwnedEnvironment reads and validates the ownership record at
// metadataPath, confirming the caller-supplied id and owner match it and
// that the environment is a playground (cleanup is forbidden for product).
func loadOwnedEnvironment(metadataPath, id, owner string) (Environment, error) {
	metaInfo, err := os.Lstat(metadataPath)
	if err != nil {
		return Environment{}, errors.New("environment ownership record is missing")
	}
	if !metaInfo.Mode().IsRegular() {
		return Environment{}, errors.New("environment ownership record is not a regular file")
	}
	var env Environment
	data, err := os.ReadFile(metadataPath) //nolint:gosec // G304: path is built from a regex-validated environment ID, not arbitrary input
	if err != nil {
		return Environment{}, err
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return Environment{}, fmt.Errorf("read environment ownership record: %w", err)
	}
	if env.ID != id || env.Owner != owner {
		return Environment{}, errors.New("environment ID or owner does not match ownership record")
	}
	if env.Mode != "playground" {
		return Environment{}, errors.New("automatic cleanup is forbidden for persistent product environments")
	}
	return env, nil
}

// removeOwnedResources removes dir's resources subdirectory (if present) and
// verifies its absence afterward, recording cleanup_incomplete or
// cleanup_unverified on env rather than ever inferring success.
func (m Manager) removeOwnedResources(dir, metadataPath string, env Environment) (Result, bool, error) {
	resources := filepath.Join(dir, "resources")
	resourceInfo, err := os.Lstat(resources)
	alreadyAbsent := errors.Is(err, fs.ErrNotExist)
	if err != nil && !alreadyAbsent {
		return Result{}, false, err
	}
	if !alreadyAbsent {
		if !resourceInfo.IsDir() || resourceInfo.Mode()&os.ModeSymlink != 0 {
			return Result{}, false, errors.New("owned resources path is not a real directory")
		}
		if err := m.removeAll(resources); err != nil {
			env.Status = StatusCleanupIncomplete
			_ = writeMetadata(metadataPath, env)
			return Result{Environment: env, Cleanup: "CLEANUP INCOMPLETE"}, false, fmt.Errorf("remove owned local resources: %w", err)
		}
	}
	if _, err := os.Lstat(resources); !errors.Is(err, fs.ErrNotExist) {
		env.Status = StatusCleanupUnverified
		_ = writeMetadata(metadataPath, env)
		return Result{Environment: env, Cleanup: "CLEANUP UNVERIFIED"}, false, errors.New("owned resources remain or cannot be verified absent")
	}
	return Result{Environment: env}, alreadyAbsent, nil
}

// errTrackedResourcesUnverifiable is returned (wrapped, with the
// offending domain named) when cleanup is about to persist
// StatusVerifiedClean but dir still has a tracked Docker and/or cloud
// resource that this call has no client to verify or remove -- see
// guardVerifiedCleanStatus.
var errTrackedResourcesUnverifiable = errors.New("tracked additive resources remain and could not be verified absent by this cleanup call")

// hasUnverifiableAdditionalResources reports whether dir's
// docker-resources.json or cloud-resources.json names any tracked
// resource -- or could not be read at all -- that a caller with no
// corresponding client has no way to confirm is actually gone.
//
// Fails closed deliberately: a read error (malformed JSON, a partially
// written file caught mid-write, a permissions problem) is treated
// exactly like "yes, resources are tracked," never like "file's not
// there, so there's nothing to track." The alternative -- silently
// treating an unreadable tracking file as empty -- is precisely the gap
// that would let a corrupted docker-resources.json or cloud-resources.json
// authorize a false verified_clean.
func hasUnverifiableAdditionalResources(dir string) (domain string, err error) {
	if unverifiable, dockerErr := trackedDockerResourcesUnverifiable(dir); dockerErr != nil {
		return "docker", dockerErr
	} else if unverifiable {
		return "docker", nil
	}
	if unverifiable, cloudErr := trackedCloudResourcesUnverifiable(dir); cloudErr != nil {
		return "cloud", cloudErr
	} else if unverifiable {
		return "cloud", nil
	}
	return "", nil
}

// trackedDockerResourcesUnverifiable reports whether dir's
// docker-resources.json names any tracked resource, or could not be
// read at all (fails closed: a read error counts as "yes, unverifiable",
// never as "nothing tracked"). Shared by hasUnverifiableAdditionalResources
// above (Manager.cleanup's own direct-call guard) AND by
// Controller.verifyAdditionalDomains (controller.go) -- a third
// independent audit found verifyAdditionalDomains had no equivalent
// guard of its own: a Controller configured with only Cloud (not
// Docker) could persist StatusVerifiedClean via its own direct
// c.persist call while a tracked Docker resource remained completely
// unverified, because that write never went through Manager.cleanup/
// guardVerifiedCleanStatus at all. Both call sites now share this one
// tracked-file check -- not two independently-maintained copies of the
// same safety decision.
func trackedDockerResourcesUnverifiable(dir string) (bool, error) {
	docker, err := loadTrackedDockerResources(dir)
	if err != nil {
		return true, fmt.Errorf("tracked docker resources could not be read: %w", err)
	}
	return len(docker) > 0, nil
}

// trackedCloudResourcesUnverifiable is
// trackedDockerResourcesUnverifiable's cloud equivalent.
func trackedCloudResourcesUnverifiable(dir string) (bool, error) {
	cloud, err := loadTrackedCloudResources(dir)
	if err != nil {
		return true, fmt.Errorf("tracked cloud resources could not be read: %w", err)
	}
	return len(cloud) > 0, nil
}

// guardVerifiedCleanStatus is the single choke point enforcing this
// package's core safety invariant at the Manager boundary, not only
// inside Controller.cleanupOne: StatusVerifiedClean must never be
// persisted by ANY public cleanup entry point -- Manager.Cleanup (the
// `playground cleanup` CLI command calls this directly, with no
// Docker/Cloud client at all) or Controller's own local-only path (no
// Docker/Cloud client configured on that Controller) -- while dir still
// has a tracked additive resource this call cannot itself verify absent.
//
// Called only when the caller's requested successStatus is
// StatusVerifiedClean; any other successStatus (e.g. Controller's
// StatusCleanupPending while it still has its own configured-client
// domains left to check) already means "not yet claiming verified
// clean," so there is nothing for this guard to downgrade.
//
// On a block, it downgrades to StatusCleanupPending rather than
// StatusCleanupIncomplete/StatusCleanupUnverified: StatusCleanupPending
// is already in needsCleanup's retry set (lifecycle.go), so the very
// next CleanupDue/worker pass -- run by this process or a completely
// different one, since nothing here is in-memory state -- picks this
// environment back up and resumes, exactly like any other interrupted
// attempt. It never touches docker-resources.json/cloud-resources.json:
// the tracked-resource evidence this environment's additive domains
// still need is left completely untouched for that next pass to act on.
func guardVerifiedCleanStatus(dir, successStatus string) (string, error) {
	if successStatus != StatusVerifiedClean {
		return successStatus, nil
	}
	domain, err := hasUnverifiableAdditionalResources(dir)
	if err != nil {
		return StatusCleanupPending, fmt.Errorf("%w (left cleanup_pending, retryable): %w", errTrackedResourcesUnverifiable, err)
	}
	if domain != "" {
		return StatusCleanupPending, fmt.Errorf("%w: tracked %s resource(s) remain and no %s client is configured on this cleanup call -- left cleanup_pending for a Controller/CleanupDue pass with that client configured to resume", errTrackedResourcesUnverifiable, domain, domain)
	}
	return successStatus, nil
}

// finalizeCleanup compares the root's sibling inventory against its
// pre-removal snapshot and only then records successStatus.
//
// successStatus is never hardcoded to StatusVerifiedClean here: Cleanup
// (below) always passes StatusVerifiedClean, since a direct Cleanup call
// has no further domain to wait for. Controller's internal cleanup
// variant (controller.go) passes StatusCleanupPending instead whenever it
// has Docker/Cloud domains still to verify -- so the on-disk status
// never claims "verified clean" before every configured domain has
// actually been checked. cleanup_pending is already in needsCleanup's
// retry set, so a crash in the window between local success and the
// Controller's own later domain checks is recovered exactly like any
// other interrupted attempt, never silently treated as done.
//
// This is the second, independent line of defense for that same
// invariant: even if a caller passes StatusVerifiedClean directly (every
// caller in this package now runs it through guardVerifiedCleanStatus
// first -- see cleanup below), finalizeCleanup itself does not know or
// care who called it, so it simply records whatever successStatus it was
// actually given.
func finalizeCleanup(rootPath, id, metadataPath string, env Environment, entriesBefore []os.DirEntry, alreadyAbsent bool, successStatus string) (Result, error) {
	entriesAfter, err := os.ReadDir(rootPath)
	if err != nil {
		return Result{Environment: env, Cleanup: "CLEANUP UNVERIFIED"}, fmt.Errorf("verify environment inventory: %w", err)
	}
	if !priorSiblingsStillPresent(entriesBefore, entriesAfter, id) {
		return Result{Environment: env, Cleanup: "CLEANUP UNVERIFIED"}, errors.New("a sibling environment present before cleanup is missing or changed afterward")
	}
	env.Status = successStatus
	if err := writeMetadata(metadataPath, env); err != nil {
		return Result{Environment: env, Cleanup: "CLEANUP UNVERIFIED"}, fmt.Errorf("persist cleanup evidence: %w", err)
	}
	status := "LOCAL RESOURCES VERIFIED REMOVED"
	if successStatus == StatusVerifiedClean {
		status = "VERIFIED CLEAN"
	}
	if alreadyAbsent {
		status += " (already absent)"
	}
	return Result{Environment: env, Cleanup: status}, nil
}

// Cleanup removes only the verified environment's resources directory. It retains
// the ownership record as evidence and never traverses a caller-selected resource path.
//
// Everything from the ownership read onward runs under withEnvironmentLock,
// so a concurrent call for the same id -- from another goroutine or an
// entirely separate process, e.g. a scheduled cleanup-due run overlapping a
// human-invoked `playground cleanup` -- cannot interleave its own read and
// write with this one. Without that, a caller whose own attempt genuinely
// fails (for a reason unrelated to whether the resources are actually
// still there) can persist CLEANUP_INCOMPLETE *after* a concurrent caller
// has already removed everything and persisted VERIFIED_CLEAN, silently
// regressing a true success to a stale failure. Serializing the whole
// read-decide-write cycle rules that out: whichever caller runs second
// always re-reads fresh state under the same lock, so by the time it acts,
// "already absent" is exactly what it sees, and it never calls removeAll
// at all.
func (m Manager) Cleanup(root, id, owner string) (Result, error) {
	return m.cleanup(root, id, owner, StatusVerifiedClean)
}

// cleanup is Cleanup's shared implementation, parameterized by
// successStatus (see finalizeCleanup's doc comment for why). Identical to
// the former, unparameterized Cleanup when called with successStatus ==
// StatusVerifiedClean AND dir has no tracked Docker/cloud resource this
// call cannot verify -- the common case, so every existing local-only
// test against it is unchanged. When dir DOES have such a resource,
// guardVerifiedCleanStatus (above) downgrades the persisted status to
// StatusCleanupPending and this returns a non-nil error instead of
// silently claiming verified_clean; see guardVerifiedCleanStatus's doc
// comment for why.
func (m Manager) cleanup(root, id, owner, successStatus string) (Result, error) {
	if !safeID.MatchString(id) {
		return Result{}, errors.New("invalid environment ID")
	}
	if strings.TrimSpace(owner) == "" {
		return Result{}, errors.New("owner is required")
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return Result{}, err
	}
	dir := filepath.Join(rootPath, id)
	dirInfo, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		status := "VERIFIED CLEAN (already absent)"
		if successStatus != StatusVerifiedClean {
			status = "LOCAL RESOURCES VERIFIED REMOVED (already absent)"
		}
		return Result{Environment: Environment{ID: id, Owner: owner, Status: successStatus}, Cleanup: status}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("inspect environment: %w", err)
	}
	if !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		return Result{}, errors.New("environment path is not a real directory")
	}
	metadataPath := filepath.Join(dir, "environment.json")

	var result Result
	err = withEnvironmentLock(dir, func() error {
		lockedRootPath, entriesBefore, lockErr := cleanupRootState(root)
		if lockErr != nil {
			return lockErr
		}
		env, lockErr := loadOwnedEnvironment(metadataPath, id, owner)
		if lockErr != nil {
			return lockErr
		}
		removed, alreadyAbsent, lockErr := m.removeOwnedResources(dir, metadataPath, env)
		if lockErr != nil {
			result = removed
			return lockErr
		}
		finalStatus, guardErr := guardVerifiedCleanStatus(dir, successStatus)
		result, lockErr = finalizeCleanup(lockedRootPath, id, metadataPath, removed.Environment, entriesBefore, alreadyAbsent, finalStatus)
		if lockErr != nil {
			return lockErr
		}
		if guardErr != nil {
			result.Cleanup = "CLEANUP PENDING (local resources removed; " + guardErr.Error() + ")"
			return guardErr
		}
		return nil
	})
	return result, err
}

// ListEnvironments reads every environment.json directly under root and
// returns the Environment it describes, skipping (not failing on) any
// sibling entry that isn't a safeID-shaped directory with a readable,
// well-formed ownership record -- a controller scanning for expired
// environments must not abort entirely because one neighboring entry is
// unrelated or corrupted. Read-only: never creates, modifies, or removes
// anything.
func ListEnvironments(root string) ([]Environment, error) {
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(rootPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list environment root: %w", err)
	}
	var envs []Environment
	for _, entry := range entries {
		if !entry.IsDir() || !safeID.MatchString(entry.Name()) {
			continue
		}
		metadataPath := filepath.Join(rootPath, entry.Name(), "environment.json")
		data, readErr := os.ReadFile(metadataPath) //nolint:gosec // G304: path is built from a regex-validated environment ID under an operator-supplied root, not arbitrary input
		if readErr != nil {
			continue
		}
		var env Environment
		if jsonErr := json.Unmarshal(data, &env); jsonErr != nil {
			continue
		}
		if env.ID != entry.Name() {
			continue
		}
		envs = append(envs, env)
	}
	sort.Slice(envs, func(i, j int) bool { return envs[i].ID < envs[j].ID })
	return envs, nil
}

// priorSiblingsStillPresent reports whether every root-level entry that
// existed in before (other than id, this environment's own directory)
// still exists in after. It deliberately does not require the reverse: a
// brand-new sibling appearing is not collateral damage from this
// environment's cleanup, it's simply a concurrent Manager.Create for a
// different, independently-generated random ID landing in the same
// window -- entirely ownership-scoped to environments this snapshot
// pair already knew about, never to anything created afterward. What
// this must still catch, and does, is any PRIOR sibling going missing or
// being altered, which is the actual thing cleanup must never cause.
func priorSiblingsStillPresent(before, after []os.DirEntry, id string) bool {
	afterByName := make(map[string]os.DirEntry, len(after))
	for _, entry := range after {
		afterByName[entry.Name()] = entry
	}
	for _, entry := range before {
		if entry.Name() == id {
			continue
		}
		afterEntry, stillPresent := afterByName[entry.Name()]
		if !stillPresent || afterEntry.IsDir() != entry.IsDir() {
			return false
		}
	}
	return true
}

func writeMetadata(path string, env Environment) error {
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(path, append(data, '\n'))
}

// atomicWriteFile writes data to path via a uniquely-named temporary file
// in the same directory, then renames it into place. The temp name must be
// unique per call -- a single fixed "<path>.tmp" is unsafe under concurrent
// writers: two callers racing to write the same path would both write to
// and rename the identical temp file, and whichever rename runs second
// fails with "no such file or directory" because the first rename already
// consumed it. os.CreateTemp's randomized suffix rules that out.
func atomicWriteFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
