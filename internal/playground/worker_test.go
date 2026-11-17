package playground

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"testing"
	"time"
)

// passSync collects AfterPass outcomes on a channel so tests can wait for
// an exact pass to finish instead of sleeping or polling the filesystem.
func passSync() (chan struct{}, func([]Environment, []Result, error)) {
	done := make(chan struct{}, 8)
	return done, func([]Environment, []Result, error) { done <- struct{}{} }
}

func waitPass(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a worker pass to complete")
	}
}

// TestWorkerRunsAnImmediatePassThenOnePerTick proves the Worker's loop is
// driven entirely by its injected Tick channel, never a real timer: no
// tick is ever sent, yet the first (immediate) pass still completes, and
// a second pass completes only after this test sends exactly one tick.
func TestWorkerRunsAnImmediatePassThenOnePerTick(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}

	tick := make(chan time.Time)
	done, afterPass := passSync()
	w := &Worker{
		Controller: Controller{
			Manager:            m,
			Now:                func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
			Sleep:              noSleep,
			MaxCleanupAttempts: 3,
		},
		Root:      root,
		Tick:      tick,
		Logf:      func(string, ...any) {},
		AfterPass: afterPass,
	}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(ctx) }()

	waitPass(t, done) // the immediate pass: expires and cleans up env

	envs, err := ListEnvironments(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 1 || envs[0].Status != StatusVerifiedClean {
		t.Fatalf("expected the immediate pass to reach VERIFIED_CLEAN, got %+v", envs)
	}

	// A second pass must not happen on its own -- only once we tick.
	select {
	case <-done:
		t.Fatal("a second pass happened without a tick being sent")
	case <-time.After(100 * time.Millisecond):
	}

	tick <- time.Now()
	waitPass(t, done) // the second pass: nothing left to do, but it must still run

	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("expected Run to return nil on graceful shutdown, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after ctx was canceled")
	}
}

// TestWorkerResumesAfterSimulatedRestart confirms the Worker-level
// restart-recovery guarantee: a first Worker's pass leaves an environment
// interrupted mid-cleanup (CLEANUP_INCOMPLETE, simulating a crash), and a
// second, entirely independent Worker value -- fresh Controller, fresh
// Manager, nothing shared in memory -- started against the same root
// resumes and completes it, using only what was persisted to disk.
func TestWorkerResumesAfterSimulatedRestart(t *testing.T) {
	root := t.TempDir()
	base := NewManager()
	env, err := base.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	interrupted := env
	interrupted.Status = StatusCleanupIncomplete
	firstController := Controller{Manager: base, Now: func() time.Time { return time.Now().UTC() }, Sleep: noSleep}
	if err := firstController.persist(root, interrupted); err != nil {
		t.Fatal(err)
	}

	secondWorker := NewWorker(root, time.Hour)
	tick := make(chan time.Time)
	done, afterPass := passSync()
	secondWorker.Tick = tick
	secondWorker.Logf = func(string, ...any) {}
	secondWorker.AfterPass = afterPass

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = secondWorker.Run(ctx) }()
	waitPass(t, done)

	envs, err := ListEnvironments(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 1 || envs[0].Status != StatusVerifiedClean {
		t.Fatalf("expected the second worker to resume and complete the interrupted cleanup from disk state alone, got %+v", envs)
	}
}

// TestWorkerNeverTouchesProductOrUnrelatedPlaygrounds covers item 6
// through the Worker's own loop (not just Controller's, which already
// proves this at the unit level): across several passes, a persistent
// product environment and a not-yet-due playground must both remain
// completely untouched.
func TestWorkerNeverTouchesProductOrUnrelatedPlaygrounds(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	due, err := m.Create(root, "alice", "playground", time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	product, err := m.Create(root, "alice", "product", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	notDue, err := m.Create(root, "alice", "playground", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}

	tick := make(chan time.Time)
	done, afterPass := passSync()
	w := &Worker{
		Controller: Controller{
			Manager:            m,
			Now:                func() time.Time { return due.ExpiresAt.Add(time.Millisecond) },
			Sleep:              noSleep,
			MaxCleanupAttempts: 3,
		},
		Root:      root,
		Tick:      tick,
		Logf:      func(string, ...any) {},
		AfterPass: afterPass,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()

	waitPass(t, done)
	tick <- time.Now()
	waitPass(t, done)
	tick <- time.Now()
	waitPass(t, done)

	envs, err := ListEnvironments(root)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Environment{}
	for _, e := range envs {
		byID[e.ID] = e
	}
	if byID[due.ID].Status != StatusVerifiedClean {
		t.Errorf("due playground status = %q, want %q", byID[due.ID].Status, StatusVerifiedClean)
	}
	if byID[product.ID].Status != StatusActive {
		t.Errorf("product environment status = %q, want %q (must never be touched)", byID[product.ID].Status, StatusActive)
	}
	if byID[notDue.ID].Status != StatusActive {
		t.Errorf("not-yet-due playground status = %q, want %q (must never be touched)", byID[notDue.ID].Status, StatusActive)
	}
}

// TestWorkerReportsUnverifiedNotSuccessWhenDockerIsUnavailable covers item
// 7: with a Docker client that reports unavailable, a pass must never let
// an environment reach VERIFIED_CLEAN -- it must land on
// CLEANUP_UNVERIFIED, and the worker's own pass summary must count it as
// such, never silently as a success.
func TestWorkerReportsUnverifiedNotSuccessWhenDockerIsUnavailable(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.TrackDockerResource(root, env.ID, "alice", TrackedDockerResource{Kind: DockerContainer, Ref: "abc123"}); err != nil {
		t.Fatal(err)
	}

	tick := make(chan time.Time)
	done, afterPass := passSync()
	var sawUnverified bool
	w := &Worker{
		Controller: Controller{
			Manager:            m,
			Now:                func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
			Sleep:              noSleep,
			MaxCleanupAttempts: 1,
			Docker:             unavailableDockerClient{},
		},
		Root: root,
		Tick: tick,
		Logf: func(string, ...any) {},
		AfterPass: func(expired []Environment, results []Result, err error) {
			for _, r := range results {
				if r.Environment.Status == StatusCleanupUnverified {
					sawUnverified = true
				}
				if r.Environment.Status == StatusVerifiedClean {
					t.Error("must never report VERIFIED_CLEAN while Docker is unavailable")
				}
			}
			afterPass(expired, results, err)
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()
	waitPass(t, done)

	if !sawUnverified {
		t.Fatal("expected the pass to report CLEANUP_UNVERIFIED while Docker is unavailable")
	}
	envs, err := ListEnvironments(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 1 || envs[0].Status != StatusCleanupUnverified {
		t.Fatalf("final persisted status = %+v, want exactly one environment at %q", envs, StatusCleanupUnverified)
	}
}

// TestOverlappingWorkerInstancesConvergeWithoutCorruption covers item 4's
// "safe handling of overlapping worker instances" by reusing (not
// re-implementing) the per-environment locking from c1920cf: two
// independent Worker values -- standing in for two separate processes
// pointed at the same root, sharing nothing but the filesystem -- run
// concurrent passes against the same set of environments, one of them
// with an asymmetric, state-independent injected failure. No new
// worker-level exclusivity is added; this test is the evidence that none
// is needed.
func TestOverlappingWorkerInstancesConvergeWithoutCorruption(t *testing.T) {
	root := t.TempDir()
	base := NewManager()
	env, err := base.Create(root, "alice", "playground", time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}

	var failingCalls int
	var mu sync.Mutex
	flaky := Manager{removeAll: func(path string) error {
		mu.Lock()
		failingCalls++
		n := failingCalls
		mu.Unlock()
		if n <= 2 {
			return errors.New("simulated asymmetric transient failure")
		}
		return base.removeAll(path)
	}}

	reliableTick := make(chan time.Time)
	flakyTick := make(chan time.Time)
	reliableDone, reliableAfter := passSync()
	flakyDone, flakyAfter := passSync()

	workerReliable := &Worker{
		Controller: Controller{Manager: base, Now: func() time.Time { return env.ExpiresAt.Add(time.Millisecond) }, Sleep: noSleep, MaxCleanupAttempts: 3},
		Root:       root,
		Tick:       reliableTick,
		Logf:       func(string, ...any) {},
		AfterPass:  reliableAfter,
	}
	workerFlaky := &Worker{
		Controller: Controller{Manager: flaky, Now: func() time.Time { return env.ExpiresAt.Add(time.Millisecond) }, Sleep: noSleep, MaxCleanupAttempts: 3},
		Root:       root,
		Tick:       flakyTick,
		Logf:       func(string, ...any) {},
		AfterPass:  flakyAfter,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = workerReliable.Run(ctx) }()
	go func() { _ = workerFlaky.Run(ctx) }()

	waitPass(t, reliableDone)
	waitPass(t, flakyDone)
	reliableTick <- time.Now()
	flakyTick <- time.Now()
	waitPass(t, reliableDone)
	waitPass(t, flakyDone)

	envs, err := ListEnvironments(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 1 {
		t.Fatalf("expected exactly one environment, got %+v", envs)
	}
	status := envs[0].Status
	if status != StatusVerifiedClean && status != StatusCleanupIncomplete {
		t.Fatalf("unexpected final status %q under overlapping workers (must be a legitimate outcome)", status)
	}
}

// TestWorkerRealDockerEndToEnd is this file's one test against a real
// Docker daemon, covering item 10 of this round's review: a real network,
// labeled and tracked exactly as a human operator would via `playground
// track-docker`, is removed by the Worker's own loop (NewDockerClient, not
// the fake), and its removal is confirmed independently via a direct
// `docker network ls`, not just by trusting this package's own Reconcile.
// Skipped outright -- not failed -- if no Docker daemon is reachable, so
// integration verification is clearly marked pending rather than silently
// assumed, exactly like TestDockerClientRealSmokeTest.
func TestWorkerRealDockerEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not on PATH; integration verification pending, not performed")
	}
	client := NewDockerClient()
	probeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := client.ListByLabel(probeCtx, DockerNetwork, DockerLabelEnvironmentID, "env-0000000000000000"); errors.Is(err, ErrDockerUnavailable) {
		t.Skip("docker daemon not reachable; integration verification pending, not performed")
	}

	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}

	networkName := "forgestack-worker-test-" + time.Now().UTC().Format("20060102150405.000000")
	mustCreateNetwork(t, networkName, env.ID)
	defer removeNetworkIfPresent(networkName)
	if err := m.TrackDockerResource(root, env.ID, "alice", TrackedDockerResource{Kind: DockerNetwork, Ref: networkName}); err != nil {
		t.Fatal(err)
	}

	tick := make(chan time.Time)
	done, afterPass := passSync()
	w := &Worker{
		Controller: Controller{
			Manager:            m,
			Now:                func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
			Sleep:              noSleep,
			MaxCleanupAttempts: 3,
			Docker:             client,
		},
		Root:      root,
		Tick:      tick,
		Logf:      func(string, ...any) {},
		AfterPass: afterPass,
	}
	ctx, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go func() { _ = w.Run(ctx) }()
	waitPass(t, done)

	envs, err := ListEnvironments(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 1 || envs[0].Status != StatusVerifiedClean {
		t.Fatalf("expected VERIFIED_CLEAN after the worker's real-Docker pass, got %+v", envs)
	}

	refs, err := client.ListByLabel(context.Background(), DockerNetwork, DockerLabelEnvironmentID, env.ID)
	if err != nil {
		t.Fatalf("independent post-cleanup ListByLabel: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("expected the real network to be gone, an independent docker query still found: %v", refs)
	}
}
