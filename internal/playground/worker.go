package playground

import (
	"context"
	"log"
	"time"
)

// Worker periodically invokes Controller.ExpireDue and Controller.CleanupDue
// (which itself performs Docker reconciliation when Controller.Docker is
// set) against one root directory. It adds no new state of its own -- it
// reads and writes only the same on-disk environment.json/docker-resources.json
// files Manager and Controller already use, through the same per-environment
// locks (c1920cf) and the same ownership-scoped sibling verification
// (c5156fd). That is deliberate: a Worker is purely a scheduler for calls
// this package already knows how to make safely, not a new place to
// re-derive safety from scratch. Because of that, a Worker is independent
// of whatever process originally created the playgrounds it manages: start
// it in a brand-new process against an existing root and it picks up
// exactly where persisted state left off, with no in-memory handoff
// required -- the same restart-recovery guarantee Controller.CleanupDue
// already has (TestCleanupDueAfterSimulatedRestartUsesOnlyPersistedState),
// now exercised through the Worker's own loop
// (TestWorkerResumesAfterSimulatedRestart).
type Worker struct {
	Controller Controller
	Root       string
	Interval   time.Duration

	// Tick, if set, replaces the real interval-driven ticker with a
	// caller-controlled channel: tests send on it directly instead of
	// waiting on a real timer, so every test in worker_test.go is
	// deterministic and sleep-free. Production code (NewWorker) leaves
	// this nil and Run wires up a real time.Ticker from Interval.
	Tick <-chan time.Time

	// Logf receives one line per completed pass and one line for any
	// pass-level error. Defaults to log.Printf when nil.
	//
	// NOT a notification/alerting mechanism: this package implements no
	// remote delivery of any kind -- no paging, no webhook, no metrics
	// export, no email. log.Printf's default destination is the
	// process's own stderr; nothing reads it unless an operator is
	// watching that stream or has wired their own log-shipping/alerting
	// on top of it. In particular, a StatusCleanupAttentionRequired
	// environment (an exhausted, human-actionable cloud-cleanup failure
	// -- see ResetCloudCleanupAttempts) is called out by name in this
	// line (see runPass), but that is visibility, not notification: a
	// caller who needs a human reliably paged must consume AfterPass (or
	// poll ListEnvironments for StatusCleanupAttentionRequired) and wire
	// it to a real alerting system themselves. This package never claims
	// otherwise.
	Logf func(format string, args ...any)

	// AfterPass, if set, is called synchronously at the end of every
	// pass -- including the immediate first one, before Run ever reads
	// from Tick -- with that pass's outcome. Tests use it to know
	// precisely when a pass has finished, with no sleep and no polling.
	AfterPass func(expired []Environment, results []Result, err error)

	// Notifier, if set, makes runPass call
	// Controller.ReconcileCleanupNotifications (notification_reconcile.go)
	// once per pass. That function scans every environment under Root
	// from persisted state alone (ListEnvironments), independent of
	// this pass's own CleanupDue results -- so a crash at any point is
	// always recoverable by simply running another pass, including
	// recovering a notification for an environment that reached
	// StatusVerifiedClean or StatusCleanupAttentionRequired in some
	// EARLIER pass and will never appear in CleanupDue's results again
	// (see needsCleanup, controller.go). Left nil (the default --
	// NewWorker never sets this), a Worker behaves exactly as it always
	// has: no report is generated, nothing is persisted beyond what
	// CleanupDue already writes, and no Notify call is ever made. This
	// is strictly additive and opt-in -- see cleanup_report.go/
	// notification.go for why neither reporting nor notifying can ever
	// change env.Status or any cleanup-state file, regardless of
	// outcome.
	//
	// A report/notification failure for one environment is logged
	// (never silently dropped) and never stops the pass or affects any
	// other environment's report/notification -- matching this
	// package's existing "one environment's failure must not stop the
	// rest" philosophy (see CleanupDue).
	Notifier Notifier
}

// NewWorker returns a Worker with real-world defaults: NewController(),
// the given root and interval, and a real time.Ticker-driven Tick (left
// nil here; Run creates it). Call WithDocker to add Docker reconciliation.
func NewWorker(root string, interval time.Duration) *Worker {
	return &Worker{
		Controller: NewController(),
		Root:       root,
		Interval:   interval,
	}
}

// WithDocker returns w with its Controller's Docker client set, for the
// common case of taking NewWorker's defaults and adding a real Docker
// client (see Controller.WithDocker).
func (w *Worker) WithDocker(d DockerClient) *Worker {
	w.Controller = w.Controller.WithDocker(d)
	return w
}

// WithCloud returns w with its Controller's Cloud client set (see
// Controller.WithCloud). Wired into the playground worker CLI's
// --cloud-provider flag (cmd/playground/main.go) via
// playground.NewCloudClient -- simulated mode only in this build; live
// mode fails closed before any credential or network access (see
// cloud_provider.go). A real, credentialed CloudClient remains a
// separately approved increment (see
// docs/architecture/CLOUD-CLEANUP-S3.md).
func (w *Worker) WithCloud(c CloudClient) *Worker {
	w.Controller = w.Controller.WithCloud(c)
	return w
}

// Run blocks, performing one pass immediately and then one pass per tick,
// until ctx is canceled. Canceling ctx is the only way to stop a Worker --
// there is no separate Stop method -- and that is graceful shutdown in
// its entirety: Run returns only after its current pass finishes, never
// leaving a pass half-applied, and returns nil rather than ctx.Err(),
// since cancellation is the expected, designed way to stop it, not a
// failure a caller needs to handle specially.
//
// A pass-level error (from ExpireDue or CleanupDue) is logged and never
// stops the loop -- a transient error on one pass (e.g. a bad permission
// on one environment's files) must not permanently prevent every future
// pass from running. Run itself only returns a non-nil error if ctx was
// never valid to begin with in a way that prevents even one pass; in
// practice this only happens if the very first synchronous pass's own
// setup fails in a way the pass can't recover from, which today it never
// does -- ExpireDue/CleanupDue report their failures per-pass, not by
// aborting Run.
//
// Limitation, stated plainly: a pass that is blocked acquiring a
// per-environment lock (withEnvironmentLock, c1920cf) held by another
// process does not return early just because ctx was canceled --
// syscall.Flock's blocking acquisition has no context support, matching
// Manager.Cleanup's existing behavior, unchanged here. In practice this
// bounds "graceful" shutdown latency to however long the other holder's
// own local-filesystem critical section takes, which is always a bounded
// local I/O operation (see lifecycle.go's Cleanup), not an unbounded wait.
func (w *Worker) Run(ctx context.Context) error {
	logf := w.Logf
	if logf == nil {
		logf = log.Printf
	}
	tick := w.Tick
	if tick == nil {
		ticker := time.NewTicker(w.Interval)
		defer ticker.Stop()
		tick = ticker.C
	}

	w.runPass(ctx, logf)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick:
			w.runPass(ctx, logf)
		}
	}
}

func (w *Worker) runPass(ctx context.Context, logf func(format string, args ...any)) {
	ctx, span := startSpan(ctx, "worker_pass")
	defer span.End()

	expired, expireErr := w.Controller.ExpireDue(w.Root)
	if expireErr != nil {
		logf("playground worker: expire-due: %v", expireErr)
	}

	results, cleanupErr := w.Controller.CleanupDue(ctx, w.Root)
	if cleanupErr != nil {
		logf("playground worker: cleanup-due: %v", cleanupErr)
	}

	// unverified counts every ordinary, still-retryable failure
	// (cleanup_incomplete/cleanup_unverified/cleanup_pending): the next
	// pass will attempt it again on its own, no human action needed yet.
	// attentionRequired is counted separately -- it is the one status
	// this Worker will NEVER retry again on its own (see needsCleanup,
	// controller.go): a human must call ResetCloudCleanupAttempts before
	// any further progress is possible. Conflating the two in one
	// counter would hide exactly the environments most in need of a
	// human looking at them behind the much larger, self-resolving
	// "ordinary retry in progress" count.
	//
	// IMPORTANT, read literally: logf writing this line is NOT a
	// notification. It is exactly as reliable as whatever sink logf's
	// caller wired it to (NewWorker's default is log.Printf -- stderr --
	// which nobody is paged on). No alerting, paging, metrics-export, or
	// any other delivery mechanism exists anywhere in this package. An
	// operator must either watch this log, scrape/poll it into a real
	// alerting system themselves, or call ListEnvironments/`reconcile`
	// and look for StatusCleanupAttentionRequired. Treat
	// attentionRequired > 0 in this line as "there is something to look
	// at," never as "someone has already been told."
	unverified := 0
	attentionRequired := 0
	for _, r := range results {
		switch r.Environment.Status {
		case StatusVerifiedClean:
			// fully resolved, nothing to count
		case StatusCleanupAttentionRequired:
			attentionRequired++
		default:
			unverified++
		}
	}
	logf("playground worker: pass complete: expired=%d cleanup_attempted=%d cleanup_unverified_or_incomplete=%d cleanup_attention_required=%d (attention_required needs a human -- see ResetCloudCleanupAttempts; this log line is not a notification, nothing pages on it)",
		len(expired), len(results), unverified, attentionRequired)

	if w.Notifier != nil {
		w.reconcileNotifications(ctx, logf)
	}

	if w.AfterPass != nil {
		err := expireErr
		if err == nil {
			err = cleanupErr
		}
		w.AfterPass(expired, results, err)
	}
}

// reconcileNotifications calls Controller.ReconcileCleanupNotifications
// (notification_reconcile.go) once -- scanning every environment under
// Root from persisted state, independent of this pass's own CleanupDue
// results (see the Notifier field's doc comment for why this matters:
// it is what makes a notification for StatusVerifiedClean/
// StatusCleanupAttentionRequired recoverable after a crash, even though
// neither status will ever reappear in a LATER pass's transient
// results). Only called when w.Notifier is explicitly set.
func (w *Worker) reconcileNotifications(ctx context.Context, logf func(format string, args ...any)) {
	now := time.Now().UTC()
	if w.Controller.Now != nil {
		now = w.Controller.Now()
	}
	reports, err := w.Controller.ReconcileCleanupNotifications(ctx, w.Root, w.Notifier, now)
	if err != nil {
		logf("playground worker: reconcile cleanup notifications: %v", err)
	}
	for _, report := range reports {
		logf("playground worker: cleanup report for %s: safety=%s simulated=%v", report.EnvironmentID, report.Safety, report.Simulated)
	}
}
