package playground

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// FakeNotifier is a deterministic, in-memory Notifier test double --
// never real email/webhook/paging delivery of any kind. Every test in
// this package that exercises notification delivery uses this.
type FakeNotifier struct {
	mu sync.Mutex

	// FailuresRemaining, if > 0, makes that many Notify calls fail
	// (decrementing each time) before a later call succeeds.
	FailuresRemaining int
	FailureErr        error

	// Block, if non-nil, is closed by the first Notify call right
	// before it returns, letting a test deterministically observe "the
	// call happened" without relying on timing. BeforeReturn, if
	// non-nil, runs synchronously inside Notify, before it returns --
	// used to simulate a crash exactly between a successful delivery
	// and this package's own bookkeeping of it.
	BeforeReturn func()

	received []Notification
}

func (f *FakeNotifier) Notify(_ context.Context, n Notification) error {
	f.mu.Lock()
	failRemaining := f.FailuresRemaining
	if failRemaining > 0 {
		f.FailuresRemaining--
	}
	failErr := f.FailureErr
	before := f.BeforeReturn
	f.mu.Unlock()

	if failRemaining > 0 {
		if failErr != nil {
			return failErr
		}
		return errors.New("simulated notification delivery failure")
	}
	f.mu.Lock()
	f.received = append(f.received, n)
	f.mu.Unlock()
	if before != nil {
		before()
	}
	return nil
}

func (f *FakeNotifier) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.received)
}

func (f *FakeNotifier) Received() []Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Notification(nil), f.received...)
}

var _ Notifier = (*FakeNotifier)(nil)

func reportWithOutcome(envID, outcome string, safety SafetyField, at time.Time) CleanupReport {
	return CleanupReport{
		EnvironmentID: envID,
		Owner:         "alice",
		GeneratedAt:   at,
		Outcome:       outcome,
		Safety:        safety,
		Simulated:     true,
	}
}

func TestSendCleanupNotification_NoNotificationForNonTerminalOutcome(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	report := reportWithOutcome(env.ID, StatusCleanupPending, SafetyUnverified, now)
	notifier := &FakeNotifier{}

	sent, err := SendCleanupNotification(context.Background(), root, report, notifier, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sent {
		t.Fatal("expected cleanup_pending (not yet a terminal-for-this-pass outcome) to send nothing")
	}
	if notifier.Count() != 0 {
		t.Fatalf("expected zero Notify calls, got %d", notifier.Count())
	}
}

func TestSendCleanupNotification_VerifiedCleanDeliversExactlyOnce(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	report := reportWithOutcome(env.ID, StatusVerifiedClean, SafetyVerifiedClean, now)
	notifier := &FakeNotifier{}

	sent1, err := SendCleanupNotification(context.Background(), root, report, notifier, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !sent1 {
		t.Fatal("expected the first send to succeed")
	}
	sent2, err := SendCleanupNotification(context.Background(), root, report, notifier, now.Add(time.Hour), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !sent2 {
		t.Fatal("expected the second (idempotent) call to still report sent=true")
	}
	if got := notifier.Count(); got != 1 {
		t.Fatalf("expected exactly ONE real Notify call despite two SendCleanupNotification calls for the same report, got %d", got)
	}

	records, err := LoadNotificationRecords(root, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Status != notificationStatusDelivered {
		t.Fatalf("expected exactly one delivered record, got %+v", records)
	}
}

func TestSendCleanupNotification_StableIDAcrossRegeneratedReports(t *testing.T) {
	// Permanent regression test for the audit's P1 finding: the
	// idempotency key must be stable across report regeneration (no
	// GeneratedAt component), which is what lets restart-recovery work.
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	notifier := &FakeNotifier{}
	now := time.Now().UTC()

	first := reportWithOutcome(env.ID, StatusVerifiedClean, SafetyVerifiedClean, now)
	if _, err := SendCleanupNotification(context.Background(), root, first, notifier, now, nil); err != nil {
		t.Fatal(err)
	}
	// A completely different report (different GeneratedAt, different
	// struct value) for the SAME environment+kind must still map to
	// the SAME notification -- and therefore not re-deliver.
	second := reportWithOutcome(env.ID, StatusVerifiedClean, SafetyVerifiedClean, now.Add(999*time.Hour))
	sent, err := SendCleanupNotification(context.Background(), root, second, notifier, now.Add(999*time.Hour), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !sent {
		t.Fatal("expected the second, differently-timestamped report for the same event to still report sent=true")
	}
	if got := notifier.Count(); got != 1 {
		t.Fatalf("expected exactly one real delivery across both reports, got %d", got)
	}
}

func TestSendCleanupNotification_DistinctKindsForDistinctOutcomes(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	notifier := &FakeNotifier{}
	now := time.Now().UTC()

	attention := reportWithOutcome(env.ID, StatusCleanupAttentionRequired, SafetyActionRequired, now)
	incomplete := reportWithOutcome(env.ID, StatusCleanupIncomplete, SafetyActionRequired, now)
	verified := reportWithOutcome(env.ID, StatusVerifiedClean, SafetyVerifiedClean, now)

	for _, r := range []CleanupReport{attention, incomplete, verified} {
		if _, err := SendCleanupNotification(context.Background(), root, r, notifier, now, nil); err != nil {
			t.Fatal(err)
		}
	}

	kinds := map[NotificationKind]int{}
	for _, n := range notifier.Received() {
		kinds[n.Kind]++
	}
	if kinds[NotificationAttentionRequired] != 1 || kinds[NotificationCleanupIncomplete] != 1 || kinds[NotificationVerifiedClean] != 1 {
		t.Fatalf("expected exactly one of each distinct kind, got %+v", kinds)
	}
}

func TestSendCleanupNotification_FailedDeliveryIsRetryableAndAudited(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	report := reportWithOutcome(env.ID, StatusCleanupAttentionRequired, SafetyActionRequired, now)
	notifier := &FakeNotifier{FailuresRemaining: 1}

	sent, err := SendCleanupNotification(context.Background(), root, report, notifier, now, nil)
	if err == nil {
		t.Fatal("expected the first (forced-failing) delivery to report an error")
	}
	if sent {
		t.Fatal("expected sent=false on a failed delivery")
	}
	records, err := LoadNotificationRecords(root, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Status != notificationStatusPending || records[0].Attempts != 1 || records[0].LastError == "" {
		t.Fatalf("expected one pending, audited failed attempt, got %+v", records)
	}

	// Retry: same report, same stable ID, notifier now succeeds.
	sent, err = SendCleanupNotification(context.Background(), root, report, notifier, now.Add(time.Minute), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !sent {
		t.Fatal("expected the retry to succeed")
	}
	records, err = LoadNotificationRecords(root, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Status != notificationStatusDelivered || records[0].Attempts != 2 {
		t.Fatalf("expected the SAME record updated in place to delivered with 2 total attempts, got %+v", records)
	}
}

// TestSendCleanupNotification_NeverChangesCleanupState is the critical
// regression test for requirement 9: a notification failure (or success)
// must never alter env.Status or any cleanup-state sidecar file.
func TestSendCleanupNotification_NeverChangesCleanupState(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, env.ID)
	before, err := os.ReadFile(filepath.Join(dir, "environment.json")) //nolint:gosec // G304: test fixture path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	report := reportWithOutcome(env.ID, StatusCleanupAttentionRequired, SafetyActionRequired, now)
	alwaysFails := &FakeNotifier{FailuresRemaining: 1 << 30}
	for i := 0; i < 3; i++ {
		_, _ = SendCleanupNotification(context.Background(), root, report, alwaysFails, now.Add(time.Duration(i)*time.Minute), nil)
	}

	after, err := os.ReadFile(filepath.Join(dir, "environment.json")) //nolint:gosec // G304: test fixture path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("CRITICAL: environment.json was modified by notification delivery.\nbefore: %s\nafter: %s", before, after)
	}
	for _, name := range []string{"cloud-cleanup-state.json", "docker-resources.json", "cloud-resources.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("CRITICAL: notification delivery created/touched %s, which it must never read or write", name)
		}
	}
}

func TestSendCleanupNotification_ConcurrentCallsNeverDoubleDeliverOrCorrupt(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	report := reportWithOutcome(env.ID, StatusVerifiedClean, SafetyVerifiedClean, now)
	notifier := &FakeNotifier{}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = SendCleanupNotification(context.Background(), root, report, notifier, now, nil)
		}()
	}
	wg.Wait()

	if got := notifier.Count(); got != 1 {
		t.Fatalf("expected exactly one real delivery across 8 concurrent identical calls, got %d", got)
	}
	records, err := LoadNotificationRecords(root, env.ID)
	if err != nil {
		t.Fatalf("expected notifications.json to still parse as valid JSON after concurrent access: %v", err)
	}
	if len(records) != 1 || records[0].Status != notificationStatusDelivered {
		t.Fatalf("expected exactly one delivered record, got %+v", records)
	}
}

// TestSendCleanupNotification_OverlappingCallersNeverBothDeliver is the
// permanent regression test for the audit's P2 finding: two callers
// racing on the exact same not-yet-delivered notification must never
// BOTH call the real Notifier concurrently -- exactly one of them wins
// the claim per round; the other sees claimed=false and does nothing
// (not an error) this round.
func TestSendCleanupNotification_OverlappingCallersNeverBothDeliver(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	report := reportWithOutcome(env.ID, StatusCleanupAttentionRequired, SafetyActionRequired, now)

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	slow := &FakeNotifier{BeforeReturn: func() {
		once.Do(func() { close(started) })
		<-release
	}}

	done := make(chan struct {
		sent bool
		err  error
	}, 2)
	go func() {
		sent, err := SendCleanupNotification(context.Background(), root, report, slow, now, nil)
		done <- struct {
			sent bool
			err  error
		}{sent, err}
	}()
	<-started // first caller is inside Notify, claim already persisted

	sent2, err2 := SendCleanupNotification(context.Background(), root, report, slow, now, nil)
	if err2 != nil {
		t.Fatalf("expected the overlapping caller to return cleanly (not an error), got %v", err2)
	}
	if sent2 {
		t.Fatal("expected the overlapping caller to see the active claim and NOT deliver")
	}

	close(release)
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	if !result.sent {
		t.Fatal("expected the original caller to complete successfully")
	}
	if got := slow.Count(); got != 1 {
		t.Fatalf("expected exactly one real Notify call across both overlapping callers, got %d", got)
	}
}

// TestSendCleanupNotification_AbandonedClaimIsReclaimedAfterLeaseExpires
// is the permanent regression test for the audit's P2 "recovery of
// abandoned claims" requirement: a claim that was never finalized
// (simulating a crash during delivery) must become reclaimable once
// notificationClaimLease has elapsed, so the notification is not stuck
// forever.
func TestSendCleanupNotification_AbandonedClaimIsReclaimedAfterLeaseExpires(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	n := Notification{ID: notificationID(env.ID, NotificationAttentionRequired, 0), EnvironmentID: env.ID, Kind: NotificationAttentionRequired, CreatedAt: now}

	// Simulate a crash during delivery: claim it, then never finalize.
	_, delivered, claimed, _, err := claimNotification(root, n, now)
	if err != nil {
		t.Fatal(err)
	}
	if delivered || !claimed {
		t.Fatalf("test setup error: expected a fresh claim, got delivered=%v claimed=%v", delivered, claimed)
	}

	report := reportWithOutcome(env.ID, StatusCleanupAttentionRequired, SafetyActionRequired, now)
	notifier := &FakeNotifier{}

	// Immediately after: the claim is still fresh -- must not be able
	// to reclaim or deliver yet.
	sentTooSoon, err := SendCleanupNotification(context.Background(), root, report, notifier, now.Add(time.Second), nil)
	if err != nil {
		t.Fatal(err)
	}
	if sentTooSoon || notifier.Count() != 0 {
		t.Fatalf("expected the fresh claim to block delivery before the lease expires, sent=%v calls=%d", sentTooSoon, notifier.Count())
	}

	// After the lease has expired: must be reclaimable and deliverable.
	later := now.Add(notificationClaimLease + time.Minute)
	sent, err := SendCleanupNotification(context.Background(), root, report, notifier, later, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !sent {
		t.Fatal("expected the abandoned claim to be reclaimed and delivered after its lease expired")
	}
	if got := notifier.Count(); got != 1 {
		t.Fatalf("expected exactly one real delivery, got %d", got)
	}
	records, err := LoadNotificationRecords(root, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Status != notificationStatusDelivered || records[0].Attempts != 2 {
		t.Fatalf("expected the reclaimed record to show 2 total attempts (original abandoned claim + successful reclaim), got %+v", records)
	}
}

// TestSendCleanupNotification_IntentPersistedBeforeDelivery proves the
// audit's P1 ordering fix directly: even if the Notifier itself crashes
// (panics) mid-call, the claim -- the durable record that an attempt
// was made -- has already been persisted before Notify was ever called,
// so the audit trail is never silently empty the way it was before this
// fix (a write failure immediately after a successful Notify call used
// to lose the record entirely).
func TestSendCleanupNotification_IntentPersistedBeforeDelivery(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	report := reportWithOutcome(env.ID, StatusVerifiedClean, SafetyVerifiedClean, now)

	var sawClaimBeforeDelivery bool
	blocking := &FakeNotifier{BeforeReturn: func() {
		records, err := LoadNotificationRecords(root, env.ID)
		if err == nil && len(records) == 1 && records[0].Status == notificationStatusClaimed {
			sawClaimBeforeDelivery = true
		}
	}}

	if _, err := SendCleanupNotification(context.Background(), root, report, blocking, now, nil); err != nil {
		t.Fatal(err)
	}
	if !sawClaimBeforeDelivery {
		t.Fatal("expected a durable 'claimed' record to already exist WHILE Notify was running, before delivery completed")
	}
}

func TestSendCleanupNotification_RejectsInvalidEnvironmentID(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	report := reportWithOutcome("../escape", StatusVerifiedClean, SafetyVerifiedClean, now)
	if _, err := SendCleanupNotification(context.Background(), root, report, &FakeNotifier{}, now, nil); err == nil {
		t.Fatal("expected an invalid environment ID to be rejected")
	}
}

func TestSendCleanupNotification_NilNotifierIsSafeNoop(t *testing.T) {
	root := t.TempDir()
	m := NewManager()
	env, err := m.Create(root, "alice", "playground", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	report := reportWithOutcome(env.ID, StatusVerifiedClean, SafetyVerifiedClean, now)
	sent, err := SendCleanupNotification(context.Background(), root, report, nil, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !sent {
		t.Fatal("expected a nil Notifier to fall back to NoopNotifier and report sent=true")
	}
}
