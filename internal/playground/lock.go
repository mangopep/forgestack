package playground

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockFileName is the per-environment advisory lock file. It is never read
// as data -- only flock'd -- so its own contents (if any) are irrelevant.
const lockFileName = ".cleanup.lock"

// withEnvironmentLock runs fn while holding an exclusive, blocking,
// cross-process advisory lock (flock) on dir's lock file. It serializes
// every mutation of one environment's persisted state -- metadata writes,
// tracked-resource writes, and Docker removal attempts -- across both
// goroutines and separate processes (a scheduled cleanup-due run and a
// human-invoked `playground cleanup` can target the same environment at
// the same time), so whatever fn observes by reading the environment's
// files is never stale by the time it decides what to write.
//
// The OS releases the lock automatically when the holding file descriptor
// closes -- including when the holding process is killed outright -- so a
// lock can never be left stuck by a crash. The next caller acquires it as
// soon as the kernel notices the fd is gone and proceeds from whatever was
// actually persisted, never from a release signal a crashed process failed
// to send.
func withEnvironmentLock(dir string, fn func() error) error {
	lockPath := filepath.Join(dir, lockFileName)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600) //nolint:gosec // G304: dir is always this package's own, regex-ID-derived environment directory, never arbitrary input
	if err != nil {
		return fmt.Errorf("open environment lock: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("acquire environment lock: %w", err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	return fn()
}
