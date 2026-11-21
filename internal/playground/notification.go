package playground

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

// NotificationKind distinguishes the three cleanup outcomes a human
// cares about hearing apart, matching SafetyField's own three-way split
// but named for the event rather than the verdict: a notification is
// about something that just happened, a SafetyField is about the
// current state.
type NotificationKind string

const (
	NotificationVerifiedClean     NotificationKind = "verified_clean"
	NotificationCleanupIncomplete NotificationKind = "cleanup_incomplete"
	NotificationAttentionRequired NotificationKind = "attention_required"
)

// notificationKindFor derives which (if any) notification a given
// report should raise. Returns ok == false for a report whose Outcome
// is still mid-flight/retryable with nothing new to tell a human yet
// (cleanup_pending, or any other status this function does not
// specifically recognize) -- not every CleanupReport is notification-
// worthy, only the three terminal-for-this-pass outcomes requirement 8
// names.
func notificationKindFor(report CleanupReport) (NotificationKind, bool) {
	switch report.Outcome {
	case StatusVerifiedClean:
		return NotificationVerifiedClean, true
	case StatusCleanupAttentionRequired:
		return NotificationAttentionRequired, true
	case StatusCleanupIncomplete, StatusCleanupUnverified:
		return NotificationCleanupIncomplete, true
	default:
		return "", false
	}
}

// notificationID is the STABLE idempotency key for one (environment,
// kind) notifiable event -- deliberately NOT derived from a report's
// GeneratedAt timestamp (an earlier version of this package did that;
// a second independent audit's P1 finding is why it no longer does).
// A timestamp-keyed ID produces a brand-new, never-before-seen ID every
// time a report is regenerated, which made "has this already been
// sent" undecidable from persisted state alone after a crash/restart --
// exactly the opposite of what an idempotency key must guarantee. Keyed
// only by (EnvironmentID, Kind), the SAME event always maps to the SAME
// record, forever, regardless of how many times or when a report
// happens to be regenerated -- which is what lets
// Controller.ReconcileCleanupNotifications (notification_reconcile.go)
// recover purely from persisted state after any crash, with no
// reliance on a particular pass's transient result list.
//
// episode, for kind == NotificationAttentionRequired only, is the
// environment's current escalation-episode counter
// (cloudCleanupAttemptState.Episode, cloud_retry.go) -- threaded into
// the key so that an environment which reaches
// StatusCleanupAttentionRequired, is reset via ResetCloudCleanupAttempts
// (which increments Episode), and later escalates a SECOND time gets a
// distinct, never-before-seen ID rather than silently colliding with
// the first escalation's already-"delivered" record. This closes the
// repeat-escalation limitation this doc comment used to describe as an
// accepted tradeoff; see
// TestResetCloudCleanupAttempts_SecondEscalationGetsItsOwnNotification.
//
// episode is ignored for every other kind: NotificationVerifiedClean
// and NotificationCleanupIncomplete have no comparable "distinct episode"
// concept -- a report regenerated mid-retry for the same still-ongoing
// cleanup_incomplete/cleanup_unverified condition is deliberately the
// SAME notifiable event, not a new one, so including episode there
// would reopen the exact "undecidable after a crash" problem the
// stable, timestamp-free key was introduced to solve.
func notificationID(environmentID string, kind NotificationKind, episode int) string {
	if kind == NotificationAttentionRequired {
		return fmt.Sprintf("%s:%s:%d", environmentID, kind, episode)
	}
	return fmt.Sprintf("%s:%s", environmentID, kind)
}

// Notification is one deliverable event, built from a CleanupReport.
type Notification struct {
	ID            string           `json:"id"`
	EnvironmentID string           `json:"environment_id"`
	Kind          NotificationKind `json:"kind"`
	Report        CleanupReport    `json:"report"`
	CreatedAt     time.Time        `json:"created_at"`
}

// Notifier is the minimal interface a future delivery mechanism (email,
// webhook, chat integration, paging system) must implement to receive
// cleanup notifications from this package. No implementation of this
// interface in this repository performs real delivery of any kind --
// see NoopNotifier (the safe default) and FakeNotifier
// (notification_test.go) for the only two implementations that exist
// today. Wiring a real one in (SMTP client, HTTP webhook POST, etc.) is
// explicitly out of scope for this increment -- "no live email/webhook
// delivery yet."
//
// DELIVERY SEMANTICS ARE AT-LEAST-ONCE, NEVER EXACTLY-ONCE. See
// SendCleanupNotification's doc comment for exactly why, and for every
// crash window this was deliberately analyzed against. A Notifier
// implementation that cannot tolerate receiving the same Notification.ID
// more than once must perform its own deduplication (e.g. keyed on
// Notify's own idempotency key, Notification.ID) -- this package's own
// durable bookkeeping reduces, but structurally cannot eliminate,
// duplicate delivery, for the same reason no local-only idempotency key
// can ever fully solve the distributed dual-write problem without a
// transactional handshake with the real delivery mechanism, which does
// not exist here.
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}

// NoopNotifier is the safe, zero-behavior default: it delivers nothing,
// anywhere, ever, and always succeeds. Using it is indistinguishable
// from not configuring a Notifier at all.
type NoopNotifier struct{}

func (NoopNotifier) Notify(context.Context, Notification) error { return nil }

var _ Notifier = NoopNotifier{}

// notificationClaimLease is how long a "claimed" (in-flight) record is
// honored before a DIFFERENT caller is allowed to treat it as abandoned
// and reclaim it -- see claimNotification. Generous on purpose: it only
// needs to be long enough that a genuinely still-in-progress Notify
// call (this package imposes no timeout of its own on notifier.Notify)
// is never reclaimed out from under its own caller in ordinary
// operation, while still being short enough that a process that
// crashed mid-delivery doesn't block that notification from ever being
// retried for an unreasonable length of real time.
const notificationClaimLease = 5 * time.Minute

// NotificationRecord is the durable, auditable delivery record for one
// notification, keyed by its stable idempotency ID -- persisted as a
// sibling of environment.json, exactly like cloud-cleanup-state.json's
// existing attempt-tracking pattern (cloud_retry.go), never inside the
// disposable resources/ subdirectory.
type NotificationRecord struct {
	ID            string           `json:"id"`
	Kind          NotificationKind `json:"kind"`
	EnvironmentID string           `json:"environment_id"`
	// Status is one of notificationStatusPending ("known, not currently
	// being attempted, and not yet delivered" -- including after one or
	// more failed attempts, and including a lease-expired abandoned
	// claim once reclaimed), notificationStatusClaimed ("a delivery
	// attempt is believed to be in flight right now -- or was, within
	// the last notificationClaimLease"), or notificationStatusDelivered
	// ("Notify returned nil at least once for this ID; never
	// intentionally re-sent after that, though see the Notifier
	// interface's doc comment for why at-least-once, not exactly-once,
	// is still the only guarantee this package actually provides").
	Status string `json:"status"`
	// ClaimToken is non-empty only while Status is "claimed" -- a
	// random value identifying WHICH caller's in-flight attempt this
	// is, so a caller finalizing a claim can detect (and refuse to
	// clobber) a DIFFERENT, later claim that has since superseded its
	// own lease-expired one.
	ClaimToken    string    `json:"claim_token,omitempty"`
	ClaimedAt     time.Time `json:"claimed_at,omitempty"`
	Attempts      int       `json:"attempts"`
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	DeliveredAt   time.Time `json:"delivered_at,omitempty"`
}

const (
	notificationStatusPending   = "pending"
	notificationStatusClaimed   = "claimed"
	notificationStatusDelivered = "delivered"
)

func notificationsPath(dir string) string { return filepath.Join(dir, "notifications.json") }

func loadNotificationRecords(dir string) ([]NotificationRecord, error) {
	data, err := os.ReadFile(notificationsPath(dir)) //nolint:gosec // G304: dir is built from a regex-validated environment ID under an operator-supplied root, not arbitrary input
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read notification records: %w", err)
	}
	var records []NotificationRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("parse notification records: %w", err)
	}
	return records, nil
}

func writeNotificationRecords(dir string, records []NotificationRecord) error {
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(notificationsPath(dir), append(data, '\n'))
}

// LoadNotificationRecords returns every notification record persisted
// for id, for audit/inspection -- read-only, never modifies anything.
func LoadNotificationRecords(root, id string) ([]NotificationRecord, error) {
	dir, err := environmentDir(root, id)
	if err != nil {
		return nil, err
	}
	return loadNotificationRecords(dir)
}

func randomClaimToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate claim token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func findRecord(records []NotificationRecord, id string) int {
	for i, r := range records {
		if r.ID == id {
			return i
		}
	}
	return -1
}

// claimNotification durably records INTENT TO DELIVER n -- a short,
// lock-held, non-blocking operation that completes and persists BEFORE
// this package ever calls the real (possibly slow, possibly blocking)
// Notifier -- see SendCleanupNotification. This is the fix for the
// second independent audit's P1 ordering finding ("persist notification
// intent before attempting delivery") and its P2 locking finding ("do
// not perform blocking notification I/O while holding the environment
// cleanup lock"): claimNotification itself never calls notifier.Notify;
// it only ever touches notifications.json.
//
// Returns delivered=true if n.ID is already durably marked delivered
// (the common, cheap, no-op-safe case for a caller that calls this
// repeatedly). Returns claimed=false, with NO change made at all, if a
// DIFFERENT, still-active (lease not expired) claim already exists for
// this ID -- letting an overlapping/concurrent caller safely skip this
// round rather than racing a duplicate delivery; see
// TestSendCleanupNotification_OverlappingCallersNeverBothDeliver.
// Otherwise it claims the record (creating it if this is the first
// attempt ever, or reclaiming it if the existing claim has lease-
// expired -- recovering an abandoned claim, see
// TestSendCleanupNotification_AbandonedClaimIsReclaimedAfterLeaseExpires)
// and returns claimed=true with a fresh, random token the caller must
// present back to finalizeNotification. recovered=true specifically
// marks the "reclaimed an abandoned, lease-expired claim" case -- as
// opposed to an ordinary fresh claim from pending/absent -- so a
// caller can record cleanup_claim_recovery_total (telemetry.go) only
// for the case that actually indicates a notification got stuck.
func claimNotification(root string, n Notification, now time.Time) (token string, delivered, claimed, recovered bool, err error) {
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return "", false, false, false, err
	}
	dir := filepath.Join(rootPath, n.EnvironmentID)

	lockErr := withEnvironmentLock(dir, func() error {
		records, loadErr := loadNotificationRecords(dir)
		if loadErr != nil {
			return loadErr
		}
		idx := findRecord(records, n.ID)
		if idx >= 0 {
			existing := records[idx]
			if existing.Status == notificationStatusDelivered {
				delivered = true
				return nil
			}
			if existing.Status == notificationStatusClaimed {
				if now.Sub(existing.ClaimedAt) < notificationClaimLease {
					// A different, still-active claim -- never double-claim.
					claimed = false
					return nil
				}
				recovered = true // reclaiming an abandoned, lease-expired claim
			}
		}

		tok, genErr := randomClaimToken()
		if genErr != nil {
			return genErr
		}
		var rec NotificationRecord
		if idx >= 0 {
			rec = records[idx]
		} else {
			rec = NotificationRecord{ID: n.ID, Kind: n.Kind, EnvironmentID: n.EnvironmentID, CreatedAt: n.CreatedAt}
		}
		rec.Status = notificationStatusClaimed
		rec.ClaimToken = tok
		rec.ClaimedAt = now
		rec.Attempts++
		rec.LastAttemptAt = now
		if idx >= 0 {
			records[idx] = rec
		} else {
			records = append(records, rec)
		}
		if writeErr := writeNotificationRecords(dir, records); writeErr != nil {
			return writeErr
		}
		token = tok
		claimed = true
		return nil
	})
	if lockErr != nil {
		return "", false, false, false, lockErr
	}
	return token, delivered, claimed, recovered, nil
}

// finalizeNotification re-acquires the lock and records the outcome of
// a delivery attempt this caller previously claimed with token -- but
// ONLY if that exact claim is still the current one on disk. If a
// different claim has since superseded it (this caller's own lease
// expired and someone else reclaimed it in the meantime), this
// function makes NO change at all: the later claimer's own eventual
// finalize call is authoritative instead, never this stale one.
func finalizeNotification(root string, n Notification, token string, now time.Time, deliverErr error) error {
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	dir := filepath.Join(rootPath, n.EnvironmentID)

	return withEnvironmentLock(dir, func() error {
		records, loadErr := loadNotificationRecords(dir)
		if loadErr != nil {
			return loadErr
		}
		idx := findRecord(records, n.ID)
		if idx < 0 {
			return fmt.Errorf("notification record %s vanished before it could be finalized", n.ID)
		}
		rec := records[idx]
		if rec.ClaimToken != token {
			// Superseded by a later claim -- not ours to finalize.
			return nil
		}
		if deliverErr != nil {
			rec.Status = notificationStatusPending
			rec.LastError = deliverErr.Error()
		} else {
			rec.Status = notificationStatusDelivered
			rec.DeliveredAt = now
			rec.LastError = ""
		}
		rec.ClaimToken = ""
		records[idx] = rec
		return writeNotificationRecords(dir, records)
	})
}

// SendCleanupNotification delivers (at-least-once -- see the Notifier
// interface's doc comment for why never exactly-once) the notification
// a report's Outcome warrants, via notifier, at the given current time
// now (caller-supplied, never time.Now() internally, so every part of
// this is deterministically testable).
//
// Returns (false, nil) for a report whose Outcome does not yet warrant
// any notification at all (see notificationKindFor).
//
// ORDERING, PER THE SECOND INDEPENDENT AUDIT'S P1 FINDING: this
// function ALWAYS durably persists intent to deliver (claimNotification)
// BEFORE ever calling notifier.Notify, and that persistence happens
// under the per-environment lock -- but notifier.Notify itself runs
// completely OUTSIDE any lock (the P2 locking fix), so a slow or
// blocking real Notifier can never stall any other cleanup/report/
// notification activity for this environment.
//
// EVERY CRASH WINDOW, HANDLED EXPLICITLY:
//   - Crash before claimNotification persists: no record exists/changed
//     yet; a later call starts clean, exactly as if this call never
//     happened. Safe, no duplicate, no loss.
//   - Crash during notifier.Notify (claim persisted, outcome unknown):
//     the record is left "claimed" with this attempt's token. A later
//     call (from ANY caller, any process) sees the claim once its
//     notificationClaimLease expires and reclaims/retries. This can
//     cause a duplicate real delivery if the original Notify call had
//     actually reached the real notifier server-side despite this
//     process dying -- an accepted, documented at-least-once tradeoff,
//     never silently hidden.
//   - Crash after notifier.Notify succeeds but before finalizeNotification
//     persists "delivered": identical to the case above -- the claim
//     looks abandoned, gets reclaimed after the lease, and is retried,
//     again causing a possible duplicate. What this fix DOES guarantee,
//     unlike the pre-fix version, is that the claim record itself (the
//     fact an attempt happened) is never silently lost to zero records
//     the way a write failure immediately after a successful Notify
//     call used to lose it entirely.
//
// A failed attempt (claimed, Notify returned an error, finalized as
// "pending") is always retryable: calling this function again with the
// same report re-claims the same ID and tries again, same semantics as
// any other retry in this package.
//
// CRITICAL SAFETY PROPERTY, UNCHANGED: this function never reads or
// writes environment.json, cloud-cleanup-state.json,
// docker-resources.json, or cloud-resources.json -- only its own
// notifications.json sidecar. A Notifier that always fails cannot
// change env.Status, cannot re-trigger cleanup, and cannot mark
// anything verified_clean or otherwise.
// metrics, if non-nil, records cleanup_notification_total{kind,outcome}
// and cleanup_claim_recovery_total (telemetry.go) for this call -- safe
// to pass nil for zero observability-related behavior change.
func SendCleanupNotification(ctx context.Context, root string, report CleanupReport, notifier Notifier, now time.Time, metrics *Metrics) (sent bool, err error) {
	kind, ok := notificationKindFor(report)
	if !ok {
		return false, nil
	}
	ctx, span := startSpan(ctx, "send_notification",
		attribute.String("environment_id", report.EnvironmentID),
		attribute.String("notification_kind", string(kind)),
	)
	defer span.End()

	if notifier == nil {
		notifier = NoopNotifier{}
	}
	if !safeID.MatchString(report.EnvironmentID) {
		return false, errors.New("invalid environment ID")
	}

	n := Notification{
		ID:            notificationID(report.EnvironmentID, kind, report.CloudEscalationEpisode),
		EnvironmentID: report.EnvironmentID,
		Kind:          kind,
		Report:        report,
		CreatedAt:     now,
	}

	token, delivered, claimed, recovered, err := claimNotification(root, n, now)
	if err != nil {
		return false, err
	}
	if recovered {
		recordClaimRecovery(metrics)
	}
	if delivered {
		recordNotification(metrics, kind, "already_delivered")
		return true, nil
	}
	if !claimed {
		// A different, still-active claim exists -- not an error, just
		// "not this round" (see TestSendCleanupNotification_
		// OverlappingCallersNeverBothDeliver).
		recordNotification(metrics, kind, "skipped_claimed")
		return false, nil
	}

	notifyErr := notifier.Notify(ctx, n) // deliberately OUTSIDE any lock

	if finalizeErr := finalizeNotification(root, n, token, now, notifyErr); finalizeErr != nil {
		recordNotification(metrics, kind, "finalize_failed")
		logNotificationEvent(ctx, report.EnvironmentID, kind, "finalize_failed", 0)
		if notifyErr != nil {
			return false, notifyErr
		}
		return false, finalizeErr
	}
	outcome := "delivered"
	if notifyErr != nil {
		outcome = "failed"
	}
	recordNotification(metrics, kind, outcome)
	logNotificationEvent(ctx, report.EnvironmentID, kind, outcome, 0)
	if notifyErr != nil {
		return false, notifyErr
	}
	return true, nil
}
