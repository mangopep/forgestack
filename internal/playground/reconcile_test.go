package playground

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestTrackDockerResourceRejectsOwnershipMismatch(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.TrackDockerResource(root, env.ID, "bob", TrackedDockerResource{Kind: DockerContainer, Ref: "abc"}); err == nil {
		t.Fatal("expected tracking a resource under the wrong owner to be rejected")
	}
	tracked, err := loadTrackedDockerResources(filepath.Join(root, env.ID))
	if err != nil {
		t.Fatal(err)
	}
	if len(tracked) != 0 {
		t.Errorf("expected no resource to be tracked after a rejected ownership mismatch, got %+v", tracked)
	}
}

func TestTrackDockerResourceRejectsInvalidID(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	if err := m.TrackDockerResource(root, "../escape", "alice", TrackedDockerResource{Kind: DockerContainer, Ref: "abc"}); err == nil {
		t.Fatal("expected an invalid environment ID to be rejected")
	}
}

func TestReconcileDetectsOrphanedAndMissingResources(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Tracked but will not exist in Docker -> Missing.
	if err := m.TrackDockerResource(root, env.ID, "alice", TrackedDockerResource{Kind: DockerContainer, Ref: "gone"}); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeDockerClient()
	// Present under this exact environment's label but never tracked -> Orphaned.
	fake.Seed(DockerVolume, "untracked-vol", map[string]string{DockerLabelEnvironmentID: env.ID})
	// A completely different environment's resource, present in the fake
	// at all, must never show up in this environment's reconciliation.
	fake.Seed(DockerContainer, "other-env-container", map[string]string{DockerLabelEnvironmentID: "env-0000000000000000"})

	rec, err := Reconcile(context.Background(), fake, root, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.DockerAvailable {
		t.Fatal("expected DockerAvailable to be true for a working fake")
	}
	if len(rec.Missing) != 1 || rec.Missing[0].Ref != "gone" {
		t.Errorf("expected 'gone' to be reported missing, got %+v", rec.Missing)
	}
	if len(rec.Orphaned) != 1 || rec.Orphaned[0].Ref != "untracked-vol" {
		t.Errorf("expected 'untracked-vol' to be reported orphaned, got %+v", rec.Orphaned)
	}
	for _, p := range rec.Present {
		if p.Ref == "other-env-container" {
			t.Error("a different environment's resource must never appear in this environment's Present list")
		}
	}
}

// TestRemoveTrackedDockerResourcesNeverTouchesAnotherEnvironment is the
// strict-ownership test for Docker specifically: two environments each
// have a tracked, present resource; removing one environment's resources
// must leave the other's completely untouched, verified both by the fake's
// internal state and by independently reconciling the untouched one.
func TestRemoveTrackedDockerResourcesNeverTouchesAnotherEnvironment(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	envA, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	envB, err := m.Create(root, "bob", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.TrackDockerResource(root, envA.ID, "alice", TrackedDockerResource{Kind: DockerContainer, Ref: "a-container"}); err != nil {
		t.Fatal(err)
	}
	if err := m.TrackDockerResource(root, envB.ID, "bob", TrackedDockerResource{Kind: DockerContainer, Ref: "b-container"}); err != nil {
		t.Fatal(err)
	}
	fake := NewFakeDockerClient()
	fake.Seed(DockerContainer, "a-container", map[string]string{DockerLabelEnvironmentID: envA.ID})
	fake.Seed(DockerContainer, "b-container", map[string]string{DockerLabelEnvironmentID: envB.ID})

	after, err := RemoveTrackedDockerResources(context.Background(), fake, root, envA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Clean() {
		t.Fatalf("expected env A to be clean after removal, got %+v", after)
	}
	if fake.Has("a-container") {
		t.Error("env A's container should have been removed")
	}
	if !fake.Has("b-container") {
		t.Fatal("env B's container must survive env A's cleanup untouched")
	}

	// Independently re-verify B is still intact via its own reconciliation.
	recB, err := Reconcile(context.Background(), fake, root, envB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recB.Present) != 1 || recB.Present[0].Ref != "b-container" {
		t.Errorf("expected env B to still show its container present, got %+v", recB)
	}
}

type unavailableDockerClient struct{}

func (unavailableDockerClient) ListByLabel(context.Context, DockerResourceKind, string, string) ([]string, error) {
	return nil, ErrDockerUnavailable
}
func (unavailableDockerClient) Remove(context.Context, DockerResourceKind, string) error {
	return ErrDockerUnavailable
}

func TestReconcileReportsDockerUnavailableRatherThanAssumingClean(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := Reconcile(context.Background(), unavailableDockerClient{}, root, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.DockerAvailable {
		t.Fatal("expected DockerAvailable to be false when the client reports unavailable")
	}
	if rec.Clean() {
		t.Fatal("an unavailable Docker check must never report Clean() == true -- absence of evidence is not evidence of absence")
	}
}

func TestReconcilePropagatesARealListError(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("simulated real docker error, not unavailability")
	_, err = Reconcile(context.Background(), failingDockerClient{err: boom}, root, env.ID)
	if err == nil {
		t.Fatal("expected a non-unavailability docker error to be propagated, not swallowed")
	}
}

type failingDockerClient struct{ err error }

func (f failingDockerClient) ListByLabel(context.Context, DockerResourceKind, string, string) ([]string, error) {
	return nil, f.err
}
func (f failingDockerClient) Remove(context.Context, DockerResourceKind, string) error { return f.err }
