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
	Logf func(format string, args ...any)

	// AfterPass, if set, is called synchronously at the end of every
	// pass -- including the immediate first one, before Run ever reads
	// from Tick -- with that pass's outcome. Tests use it to know
	// precisely when a pass has finished, with no sleep and no polling.
	AfterPass func(expired []Environment, results []Result, err error)
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
	expired, expireErr := w.Controller.ExpireDue(w.Root)
	if expireErr != nil {
		logf("playground worker: expire-due: %v", expireErr)
	}

	results, cleanupErr := w.Controller.CleanupDue(ctx, w.Root)
	if cleanupErr != nil {
		logf("playground worker: cleanup-due: %v", cleanupErr)
	}

	unverified := 0
	for _, r := range results {
		if r.Environment.Status != StatusVerifiedClean {
			unverified++
		}
	}
	logf("playground worker: pass complete: expired=%d cleanup_attempted=%d cleanup_unverified_or_incomplete=%d", len(expired), len(results), unverified)

	if w.AfterPass != nil {
		err := expireErr
		if err == nil {
			err = cleanupErr
		}
		w.AfterPass(expired, results, err)
	}
}
