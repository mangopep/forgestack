package playground

import (
	"context"
	"errors"
	"testing"
	"time"
)

func noSleep(time.Duration) {}

func testController(now time.Time) Controller {
	return Controller{
		Manager:            NewManager(),
		Now:                func() time.Time { return now },
		Sleep:              noSleep,
		MaxCleanupAttempts: 3,
		RetryBackoff:       0,
	}
}

func TestExpireDueTransitionsOnlyPastDeadlinePlaygrounds(t *testing.T) {
	root := t.TempDir()
	m := NewManager()

	due, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	notDue, err := m.Create(root, "alice", "playground", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	product, err := m.Create(root, "alice", "product", 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	// "due" was created with a 1-second TTL; evaluating slightly after its
	// own real creation time is enough to put it past ExpiresAt without
	// needing Create itself to be clock-injectable (it isn't, by design --
	// see controller.go's doc comment on why that's fine).
	c := testController(due.ExpiresAt.Add(time.Millisecond))
	expired, err := c.ExpireDue(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 || expired[0].ID != due.ID {
		t.Fatalf("expected exactly %s to expire, got %+v", due.ID, expired)
	}

	envs, err := ListEnvironments(root)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, e := range envs {
		statuses[e.ID] = e.Status
	}
	if statuses[due.ID] != StatusExpired {
		t.Errorf("due environment status = %q, want %q", statuses[due.ID], StatusExpired)
	}
	if statuses[notDue.ID] != StatusActive {
		t.Errorf("not-yet-due environment status = %q, want %q (untouched)", statuses[notDue.ID], StatusActive)
	}
	if statuses[product.ID] != StatusActive {
		t.Errorf("product environment status = %q, want %q (never auto-expires)", statuses[product.ID], StatusActive)
	}
}

func TestExpireDueIsIdempotent(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := testController(env.ExpiresAt.Add(time.Millisecond))
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	again, err := c.ExpireDue(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Errorf("expected a second ExpireDue call to find nothing new to expire, got %+v", again)
	}
}

func TestCleanupDueResumesAfterInterruptedCleanup(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a process that crashed mid-cleanup: it had already marked
	// the environment CleanupIncomplete (the same thing a real failed
	// Manager.Cleanup attempt would leave behind) and the owned resources
	// directory genuinely still exists on disk, exactly as it would after
	// an interrupted removal.
	c := testController(env.CreatedAt)
	interrupted := env
	interrupted.Status = StatusCleanupIncomplete
	if err := c.persist(root, interrupted); err != nil {
		t.Fatal(err)
	}

	results, err := c.CleanupDue(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Environment.Status != StatusVerifiedClean {
		t.Fatalf("expected the interrupted environment to be resumed and verified clean, got %+v", results)
	}
}

func TestCleanupDueRetriesUpToBoundThenGivesUp(t *testing.T) {
	root := t.TempDir()
	base := NewManager()
	env, err := base.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	var sleeps int
	alwaysFails := Manager{removeAll: func(string) error { return errors.New("simulated persistent removal failure") }}
	c := Controller{
		Manager:            alwaysFails,
		Now:                func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:              func(time.Duration) { sleeps++ },
		MaxCleanupAttempts: 3,
		RetryBackoff:       0,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	results, err := c.CleanupDue(context.Background(), root)
	if err == nil {
		t.Fatal("expected CleanupDue to report an error when every retry fails")
	}
	if len(results) != 1 || results[0].Environment.Status != StatusCleanupIncomplete {
		t.Fatalf("expected the environment to end as CLEANUP_INCOMPLETE after exhausting retries, got %+v", results)
	}
	if sleeps != 2 {
		t.Errorf("expected exactly 2 backoff sleeps between 3 attempts, got %d", sleeps)
	}
}

func TestCleanupDueSucceedsAfterTransientFailures(t *testing.T) {
	root := t.TempDir()
	base := NewManager()
	env, err := base.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	flaky := Manager{removeAll: func(path string) error {
		calls++
		if calls < 2 {
			return errors.New("simulated transient failure")
		}
		return base.removeAll(path)
	}}
	c := Controller{
		Manager:            flaky,
		Now:                func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:              noSleep,
		MaxCleanupAttempts: 3,
		RetryBackoff:       0,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	results, err := c.CleanupDue(context.Background(), root)
	if err != nil {
		t.Fatalf("expected the second attempt to succeed within the retry bound, got error: %v", err)
	}
	if len(results) != 1 || results[0].Environment.Status != StatusVerifiedClean {
		t.Fatalf("expected eventual success, got %+v", results)
	}
}

func TestCleanupDueIsIdempotentAcrossRepeatedInvocations(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := testController(env.ExpiresAt.Add(time.Millisecond))
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	first, err := c.CleanupDue(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Environment.Status != StatusVerifiedClean {
		t.Fatalf("expected the first CleanupDue to succeed, got %+v", first)
	}
	second, err := c.CleanupDue(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Errorf("expected a second CleanupDue call to find nothing left needing cleanup, got %+v", second)
	}
}

// TestCleanupDueNeverTouchesAnUnrelatedActiveEnvironment is the strict-
// ownership/isolation test at the controller level: two environments
// exist, only one is due for cleanup, and the other's owned resources
// must survive completely untouched.
func TestCleanupDueNeverTouchesAnUnrelatedActiveEnvironment(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	due, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := m.Create(root, "bob", "playground", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}

	c := testController(due.ExpiresAt.Add(time.Millisecond))
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CleanupDue(context.Background(), root); err != nil {
		t.Fatal(err)
	}

	envs, err := ListEnvironments(root)
	if err != nil {
		t.Fatal(err)
	}
	var otherAfter Environment
	for _, e := range envs {
		if e.ID == other.ID {
			otherAfter = e
		}
	}
	if otherAfter.Status != StatusActive {
		t.Errorf("unrelated environment %s status = %q, want %q (must be completely untouched)", other.ID, otherAfter.Status, StatusActive)
	}
}

// TestCleanupDueAfterSimulatedRestartUsesOnlyPersistedState constructs a
// brand-new Controller value (nothing shared in memory with whatever
// created the environment -- simulating a process restart) and confirms
// it can resume and complete cleanup purely from what's on disk.
func TestCleanupDueAfterSimulatedRestartUsesOnlyPersistedState(t *testing.T) {
	root := t.TempDir()
	func() { // the "before restart" process
		m := NewManager()
		env, err := m.Create(root, "alice", "playground", time.Second, nil)
		if err != nil {
			t.Fatal(err)
		}
		c := testController(env.ExpiresAt.Add(time.Millisecond))
		if _, err := c.ExpireDue(root); err != nil {
			t.Fatal(err)
		}
	}()

	// "after restart": a fresh Controller, fresh Manager, fresh clock
	// closure -- the only thing connecting it to the environment above is
	// the root directory on disk.
	restarted := Controller{
		Manager:            NewManager(),
		Now:                func() time.Time { return time.Now().UTC() },
		Sleep:              noSleep,
		MaxCleanupAttempts: 3,
		RetryBackoff:       0,
	}
	results, err := restarted.CleanupDue(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Environment.Status != StatusVerifiedClean {
		t.Fatalf("expected the restarted controller to resume and complete cleanup from disk state alone, got %+v", results)
	}
}

func TestCleanupDueDowngradesWhenDockerResourcesCannotBeVerifiedAbsent(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.TrackDockerResource(root, env.ID, "alice", TrackedDockerResource{Kind: DockerContainer, Ref: "abc123"}); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeDockerClient()
	fake.Seed(DockerContainer, "abc123", map[string]string{DockerLabelEnvironmentID: env.ID})
	fake.RemoveErr = errors.New("simulated docker removal failure")

	c := Controller{
		Manager:            m,
		Now:                func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:              noSleep,
		MaxCleanupAttempts: 1,
		RetryBackoff:       0,
		Docker:             fake,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	results, err := c.CleanupDue(context.Background(), root)
	if err == nil {
		t.Fatal("expected an error when a tracked docker resource cannot be removed/verified absent")
	}
	if len(results) != 1 || results[0].Environment.Status != StatusCleanupUnverified {
		t.Fatalf("expected the local-clean-but-docker-dirty environment to be downgraded to CLEANUP_UNVERIFIED, got %+v", results)
	}
	if !fake.Has("abc123") {
		t.Error("the simulated-failed-removal resource should still be present in the fake")
	}
}

func TestCleanupDueSucceedsWhenDockerResourcesAreCleanlyRemoved(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.TrackDockerResource(root, env.ID, "alice", TrackedDockerResource{Kind: DockerVolume, Ref: "env-vol"}); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeDockerClient()
	fake.Seed(DockerVolume, "env-vol", map[string]string{DockerLabelEnvironmentID: env.ID})

	c := Controller{
		Manager:            m,
		Now:                func() time.Time { return env.ExpiresAt.Add(time.Millisecond) },
		Sleep:              noSleep,
		MaxCleanupAttempts: 1,
		RetryBackoff:       0,
		Docker:             fake,
	}
	if _, err := c.ExpireDue(root); err != nil {
		t.Fatal(err)
	}
	results, err := c.CleanupDue(context.Background(), root)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if len(results) != 1 || results[0].Environment.Status != StatusVerifiedClean {
		t.Fatalf("expected VERIFIED_CLEAN once Docker resources are cleanly removed, got %+v", results)
	}
	if fake.Has("env-vol") {
		t.Error("expected the tracked volume to actually be removed from the fake")
	}
}
