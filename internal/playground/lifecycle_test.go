package playground

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func createTestSentinel(t *testing.T, root string) string {
	t.Helper()
	if err := os.Mkdir(filepath.Join(root, "unrelated"), 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(root, "unrelated", "keep.txt")
	if err := os.WriteFile(sentinel, []byte("preserve me"), 0600); err != nil {
		t.Fatal(err)
	}
	return sentinel
}

func assertSentinelPreserved(t *testing.T, sentinel string) {
	t.Helper()
	got, err := os.ReadFile(sentinel) //nolint:gosec // G304: test-only path built from t.TempDir(), not external input
	if err != nil || string(got) != "preserve me" {
		t.Fatalf("unrelated resource changed: %q, %v", got, err)
	}
}

func TestCreateAndCleanupIsScopedAndIdempotent(t *testing.T) {
	root := t.TempDir()
	sentinel := createTestSentinel(t, root)
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Hour, []string{"go-api", "docker-compose"})
	if err != nil {
		t.Fatal(err)
	}
	if env.ID == "" || env.ExpiresAt.IsZero() || env.Status != "active" {
		t.Fatalf("bad environment metadata: %#v", env)
	}
	if _, err := os.Stat(filepath.Join(root, env.ID, "resources")); err != nil {
		t.Fatal(err)
	}
	result, err := m.Cleanup(root, env.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if result.Cleanup != "VERIFIED CLEAN" || result.Environment.Status != "verified_clean" {
		t.Fatalf("bad cleanup result: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(root, env.ID, "resources")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("resources remain: %v", err)
	}
	assertSentinelPreserved(t, sentinel)
	again, err := m.Cleanup(root, env.ID, "alice")
	if err != nil || again.Cleanup != "VERIFIED CLEAN (already absent)" {
		t.Fatalf("repeat cleanup: %#v, %v", again, err)
	}
}

func TestProductEnvironmentCannotExpireOrAutoCleanup(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "product", 0, []string{"go-api"})
	if err != nil {
		t.Fatal(err)
	}
	if !env.ExpiresAt.IsZero() {
		t.Fatal("product environment must not expire")
	}
	if _, err := m.Cleanup(root, env.ID, "alice"); err == nil {
		t.Fatal("product environment cleanup should be rejected")
	}
	if _, err := os.Stat(filepath.Join(root, env.ID, "resources")); err != nil {
		t.Fatalf("product resources were touched: %v", err)
	}
}

func TestCleanupFailureIsReportedAndRecorded(t *testing.T) {
	root := t.TempDir()
	base := NewManager()
	env, err := base.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	failed := Manager{removeAll: func(string) error { return errors.New("simulated remove failure") }}
	result, err := failed.Cleanup(root, env.ID, "alice")
	if err == nil || result.Cleanup != "CLEANUP INCOMPLETE" {
		t.Fatalf("failure was hidden: %#v, %v", result, err)
	}
	if result.Environment.Status != "cleanup_incomplete" {
		t.Fatalf("failure status not recorded: %#v", result.Environment)
	}
	if _, err := os.Stat(filepath.Join(root, env.ID, "resources")); err != nil {
		t.Fatalf("failure fixture unexpectedly removed: %v", err)
	}
}

func TestCleanupRejectsWrongOwnerAndMalformedID(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Cleanup(root, env.ID, "bob"); err == nil {
		t.Fatal("expected owner mismatch")
	}
	if _, err := m.Cleanup(root, "../unrelated", "alice"); err == nil {
		t.Fatal("expected invalid ID rejection")
	}
}

func TestCreateRejectsTTLForPersistentProductAndOverlongPlayground(t *testing.T) {
	m := NewManager()
	root := t.TempDir()
	if _, err := m.Create(root, "alice", "product", time.Hour, nil); err == nil {
		t.Fatal("product TTL must be rejected")
	}
	if _, err := m.Create(root, "alice", "playground", 25*time.Hour, nil); err == nil {
		t.Fatal("overlong playground TTL must be rejected")
	}
}
