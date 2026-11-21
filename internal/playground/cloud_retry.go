package playground

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// defaultMaxCloudCleanupAttempts is used when Controller.MaxCloudCleanupAttempts
// is zero (the Go zero value, used by every test Controller literal that
// doesn't explicitly set it) -- chosen so a Controller{} literal still
// behaves sensibly rather than escalating on the very first failure.
const defaultMaxCloudCleanupAttempts = 5

// defaultCloudRetryBackoff is the CloudRetryBackoff NewController sets
// (controller.go), applied only via NewController -- a Controller{}
// literal's zero value still means no backoff gate, which every
// deterministic test in this package relies on. One minute is short
// enough that a genuinely transient failure (throttling, a brief
// credential hiccup) still recovers within a few worker intervals, but
// comfortably longer than the real-world gap between two overlapping,
// lock-serialized CleanupDue passes for the same environment -- closing
// the P2 double-attempt-consumption finding (see CloudRetryBackoff's doc
// comment) without inventing new dedup machinery.
const defaultCloudRetryBackoff = time.Minute

// cloudCleanupAttemptState is the persisted record of how many times
// automatic cleanup has attempted (and failed) to remove an environment's
// tracked cloud resources, and when it last tried. Stored as a sibling of
// environment.json/cloud-resources.json -- never inside the disposable
// resources/ subdirectory -- so it survives exactly as long as the
// environment's own ownership record does, same as cloud-resources.json.
type cloudCleanupAttemptState struct {
	Attempts      int       `json:"attempts"`
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	// Episode counts distinct escalation cycles for this environment:
	// it starts at 0 and is incremented only by ResetCloudCleanupAttempts
	// (never by an ordinary attempt or an ordinary success). It survives
	// a success-clear (clearCloudCleanupAttemptState preserves it rather
	// than deleting it once non-zero) specifically so that notificationID
	// can key the attention_required notification to the CURRENT
	// episode -- see that function's doc comment for the repeat-
	// escalation bug this closes.
	Episode int `json:"episode,omitempty"`
}

func cloudCleanupAttemptStatePath(dir string) string {
	return filepath.Join(dir, "cloud-cleanup-state.json")
}

func loadCloudCleanupAttemptState(dir string) (cloudCleanupAttemptState, error) {
	data, err := os.ReadFile(cloudCleanupAttemptStatePath(dir)) //nolint:gosec // G304: dir is built from a regex-validated environment ID under an operator-supplied root, not arbitrary input
	if errors.Is(err, fs.ErrNotExist) {
		return cloudCleanupAttemptState{}, nil
	}
	if err != nil {
		return cloudCleanupAttemptState{}, fmt.Errorf("read cloud cleanup attempt state: %w", err)
	}
	var state cloudCleanupAttemptState
	if err := json.Unmarshal(data, &state); err != nil {
		return cloudCleanupAttemptState{}, fmt.Errorf("parse cloud cleanup attempt state: %w", err)
	}
	return state, nil
}

func saveCloudCleanupAttemptState(dir string, state cloudCleanupAttemptState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(cloudCleanupAttemptStatePath(dir), append(data, '\n'))
}

// clearCloudCleanupAttemptState is called after a SUCCESSFUL cleanup
// (attemptCloudCleanup's rec.Clean() branch). It always clears Attempts/
// LastAttemptAt/LastError, but preserves Episode once it is non-zero
// (an environment that has escalated and been reset at least once)
// rather than deleting the whole record -- so that if this SAME
// environment later tracks a new cloud resource that also escalates,
// that escalation still gets a fresh, never-before-reused episode
// number rather than silently reusing episode 0's already-delivered
// notification ID. An environment that has never escalated (Episode
// still 0) keeps today's exact behavior: the file is removed entirely.
func clearCloudCleanupAttemptState(dir string) error {
	state, loadErr := loadCloudCleanupAttemptState(dir)
	if loadErr != nil {
		return loadErr
	}
	if state.Episode == 0 {
		err := os.Remove(cloudCleanupAttemptStatePath(dir))
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("clear cloud cleanup attempt state: %w", err)
	}
	return saveCloudCleanupAttemptState(dir, cloudCleanupAttemptState{Episode: state.Episode})
}

// currentCloudEscalationEpisode returns id's current escalation episode
// counter (cloudCleanupAttemptState.Episode), or 0 for an environment
// that has never escalated and/or has no persisted attempt state at
// all. Read-only; used by notification.go/notification_reconcile.go/
// cleanup_report.go to key the attention_required notification ID to
// the environment's CURRENT episode.
func currentCloudEscalationEpisode(root, id string) (int, error) {
	dir, err := environmentDir(root, id)
	if err != nil {
		return 0, err
	}
	state, err := loadCloudCleanupAttemptState(dir)
	if err != nil {
		return 0, err
	}
	return state.Episode, nil
}

// ResetCloudCleanupAttempts is the explicit, separately invoked recovery
// path for an environment whose cloud cleanup has escalated to
// StatusCleanupAttentionRequired (automatic retries exhausted). It is
// never called by Controller, Worker, or any automatic path -- the only
// caller is a human (or an operator script acting on a human's explicit
// decision), exactly like ForceRemoveCloudResource. It clears the
// persisted attempt-count history and, if the environment's status is
// StatusCleanupAttentionRequired, moves it back to StatusCleanupUnverified
// so the next automatic CleanupDue/worker pass retries it with a fresh
// attempt budget. It does not retry removal itself and does not touch
// cloud-resources.json -- the resource stays tracked throughout.
//
// It also increments the persisted escalation Episode counter
// (cloudCleanupAttemptState.Episode) -- unconditionally, even if called
// on an environment that was not actually in StatusCleanupAttentionRequired,
// which is harmless since no notification would exist yet for an episode
// that was never reached. This is the fix for the documented repeat-
// escalation limitation in notificationID's doc comment: without a
// generation counter, a SECOND escalation to StatusCleanupAttentionRequired
// after this reset reused the FIRST escalation's already-"delivered"
// notification ID and was silently suppressed. Bumping Episode here
// means the next escalation (if any) is keyed to a distinct, never-
// before-seen ID, while every notification already delivered for an
// earlier episode remains in notifications.json untouched -- see
// TestResetCloudCleanupAttempts_SecondEscalationGetsItsOwnNotification.
func ResetCloudCleanupAttempts(root, id, owner string) error {
	if !safeID.MatchString(id) {
		return errors.New("invalid environment ID")
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	dir := filepath.Join(rootPath, id)
	metadataPath := filepath.Join(dir, "environment.json")
	return withEnvironmentLock(dir, func() error {
		env, err := loadOwnedEnvironment(metadataPath, id, owner)
		if err != nil {
			return err
		}
		state, err := loadCloudCleanupAttemptState(dir)
		if err != nil {
			return err
		}
		state = cloudCleanupAttemptState{Episode: state.Episode + 1}
		if err := saveCloudCleanupAttemptState(dir, state); err != nil {
			return err
		}
		if env.Status != StatusCleanupAttentionRequired {
			return nil
		}
		env.Status = StatusCleanupUnverified
		return writeMetadata(metadataPath, env)
	})
}
