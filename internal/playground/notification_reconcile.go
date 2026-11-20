package playground

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// hasDeliveredNotification reports whether id already has a durably
// "delivered" NotificationRecord for kind -- a cheap, read-only check
// used by ReconcileCleanupNotifications to decide whether an
// environment still needs a report generated/notification attempted at
// all this pass.
func hasDeliveredNotification(root, id string, kind NotificationKind) (bool, error) {
	records, err := LoadNotificationRecords(root, id)
	if err != nil {
		return false, err
	}
	idx := findRecord(records, notificationID(id, kind))
	return idx >= 0 && records[idx].Status == notificationStatusDelivered, nil
}

// ReconcileCleanupNotifications is the fix for the second independent
// audit's P1 finding ("lost notifications"): Worker.runPass's earlier
// design only ever generated/sent a notification for environments
// present in ONE PASS's own transient CleanupDue results slice. Because
// needsCleanup (controller.go) permanently excludes both
// StatusVerifiedClean and StatusCleanupAttentionRequired from ever
// reappearing in a LATER pass's results, a crash between CleanupDue
// completing and that pass's notification step meant the notification
// for exactly those two statuses -- the two a human most needs to hear
// about -- could be silently, permanently lost with no automatic retry
// path at all.
//
// This function has no such dependency: it calls ListEnvironments
// (root) directly -- the exact same persisted-state-only scan
// ExpireDue/CleanupDue already use -- and, for every environment whose
// CURRENT status warrants a notification (notificationKindFor) and has
// no durable "delivered" record yet (hasDeliveredNotification), builds
// a fresh CleanupReport, persists it, and attempts to send it. Calling
// this again after ANY crash, from this process or a completely
// different one, with no in-memory handoff required, picks up exactly
// where the previous call left off -- the same restart-recovery
// guarantee Controller.CleanupDue already has
// (TestCleanupDueAfterSimulatedRestartUsesOnlyPersistedState), now
// extended to notifications
// (TestReconcileCleanupNotifications_RecoversAfterSimulatedCrash).
//
// A failure for one environment (report generation, persistence, or
// delivery) is collected and never stops the loop over the rest,
// matching CleanupDue's own "one environment's failure must not stop
// the others" rule.
func (c Controller) ReconcileCleanupNotifications(ctx context.Context, root string, notifier Notifier, now time.Time) ([]CleanupReport, error) {
	ctx, span := startSpan(ctx, "reconcile_notifications")
	defer span.End()

	envs, err := ListEnvironments(root)
	if err != nil {
		return nil, err
	}
	var reports []CleanupReport
	var errs []error
	for _, env := range envs {
		kind, ok := notificationKindFor(CleanupReport{Outcome: env.Status})
		if !ok {
			continue // not a notification-worthy status this pass
		}
		delivered, err := hasDeliveredNotification(root, env.ID, kind)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: check delivered notification: %w", env.ID, err))
			continue
		}
		if delivered {
			continue
		}

		report, err := c.GenerateCleanupReport(ctx, root, env, nil)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: generate cleanup report: %w", env.ID, err))
			continue
		}
		if err := PersistCleanupReport(root, report); err != nil {
			errs = append(errs, fmt.Errorf("%s: persist cleanup report: %w", env.ID, err))
			// Still attempt to notify below -- a persistence failure
			// must not also silently suppress the one notification a
			// human might actually be watching for.
		}
		if _, err := SendCleanupNotification(ctx, root, report, notifier, now, c.Metrics); err != nil {
			errs = append(errs, fmt.Errorf("%s: send cleanup notification (%s): %w", env.ID, report.Safety, err))
		}
		reports = append(reports, report)
	}
	return reports, errors.Join(errs...)
}
