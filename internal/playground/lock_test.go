package playground

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestWithEnvironmentLockBlocksConcurrentAcquisition proves mutual
// exclusion deterministically, not via a sleep-and-hope timing guess: while
// the first holder is inside its locked section, a second, independent,
// *non-blocking* flock attempt on the exact same lock file must fail
// (EWOULDBLOCK) -- which is only possible if the first holder's lock is
// genuinely held at the OS level, not merely "probably still running."
func TestWithEnvironmentLockBlocksConcurrentAcquisition(t *testing.T) {
	dir := t.TempDir()
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- withEnvironmentLock(dir, func() error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started

	lockPath := filepath.Join(dir, lockFileName)
	probe, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600) //nolint:gosec // G304: dir is t.TempDir(), not external input
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = probe.Close() }()
	if err := syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		_ = syscall.Flock(int(probe.Fd()), syscall.LOCK_UN)
		t.Fatal("expected a concurrent non-blocking lock attempt to fail while the first holder is active")
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("holder returned an error: %v", err)
	}

	// After release, a fresh non-blocking attempt must succeed immediately
	// -- the lock was actually released, not merely about to time out.
	if err := syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("expected the lock to be acquirable immediately after release, got: %v", err)
	}
	_ = syscall.Flock(int(probe.Fd()), syscall.LOCK_UN)
}

// TestWithEnvironmentLockRecoversAfterSimulatedCrash simulates a process
// that was killed while holding the lock: it acquires the flock and closes
// its file descriptor without ever calling LOCK_UN -- exactly what happens
// to a killed process's descriptors, never an orderly unlock. The kernel
// must release an flock the moment its owning descriptor closes, so a
// crash can never leave this lock stuck for every future caller.
func TestWithEnvironmentLockRecoversAfterSimulatedCrash(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, lockFileName)

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600) //nolint:gosec // G304: dir is t.TempDir(), not external input
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil { // no LOCK_UN -- the simulated crash
		t.Fatal(err)
	}

	acquired := make(chan error, 1)
	go func() {
		acquired <- withEnvironmentLock(dir, func() error { return nil })
	}()
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("expected the lock to be acquired cleanly after a simulated crash, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("lock was never acquired after a simulated crash -- it appears permanently stuck")
	}
}
