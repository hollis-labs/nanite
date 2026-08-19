// Package orphansweep is the Orphan/Runtime Reaper — one of the four
// recovery mechanisms grouped under internal/recovery/*. It reconciles
// stale agent_runtime rows against actually-dead PIDs, most commonly
// after a daemon restart. See internal/recovery/broker for the
// in-process crash-recovery mechanism and internal/recovery/pack for
// cold-boot context replay — those answer different "what went wrong"
// questions and are not consolidated with this one.
package orphansweep

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/hollis-labs/nanite/internal/runtime/agent"
)

// Default tunings for the agent-runtime reaper. Values picked to surface
// post-restart and mid-run process deaths quickly without hammering SQLite —
// SweepOnce does at most one query per tick and one UPDATE per orphan.
const (
	// DefaultRuntimeReaperInterval is the wall-clock cadence between
	// reaper sweeps once the service is running. Matches the subagent
	// reaper's 30s cycle so operators reason about one observability
	// budget across both sweepers.
	DefaultRuntimeReaperInterval = 30 * time.Second

	// DefaultRuntimeReaperPidZeroGrace is the floor on how long a row
	// with PID == 0 may sit in launching/running before being treated as
	// stale by the sweep. Codex-style runtimes never persist a pid, so
	// signal-0 liveness can't speak for them; this grace window lets a
	// launch finish and a heartbeat update updated_at before the
	// staleness check fires. Generous because a slow boot
	// (sandbox setup, MCP plant, provider auth refresh) can legitimately
	// keep updated_at unchanged for tens of seconds.
	DefaultRuntimeReaperPidZeroGrace = 5 * time.Minute

	// Reason strings written to agent_runtime.failure_reason so audit
	// queries can group reconciliations by sweep cause.
	ReasonReconcileDeadPid       = "dead_pid"        // signal-0 probe failed
	ReasonReconcilePidZeroStale  = "pid_zero_stale"  // pid=0, updated_at older than grace
	ReasonReconcileNoLiveSession = "no_live_session" // pid=0 + no in-memory session entry
)

// SweepOrphans runs at daemon startup and on each periodic reaper tick.
// For every runtime row in state="running" or state="launching" it
// reconciles the row against three signals:
//
//  1. PID > 0:  signal-0 probe via syscall.Kill(pid, 0). Live PIDs stay.
//     Dead PIDs flip to state="orphaned" with reason="dead_pid".
//
//  2. PID == 0: codex-style adapters never persist a pid, so liveness-by-pid
//     can't speak for them. The fallback is the in-memory session checker
//     (Dependencies.LiveSessions) plus an updated_at staleness threshold —
//     if no live session AND updated_at is older than pidZeroGrace, flip
//     to state="orphaned" with reason="no_live_session" (checker available)
//     or reason="pid_zero_stale" (no checker, fallback path).
//
// Idempotent: a row already in state="orphaned" is not enumerated by
// ListRunningRows, and MarkRuntimeOrphaned on an already-orphaned id is a
// safe no-op upstream.
//
// Returns the number of rows reconciled. Per-row persistence failures are
// logged via slog and skipped; a single failure does not abort the sweep.
func SweepOrphans(ctx context.Context, deps *agent.Dependencies) (int, error) {
	return sweepOrphansAt(ctx, deps, DefaultRuntimeReaperPidZeroGrace, time.Now)
}

// sweepOrphansAt is the testable core of SweepOrphans. Tests inject a
// fixed clock + a tighter grace window to drive deterministic sweeps.
func sweepOrphansAt(ctx context.Context, deps *agent.Dependencies, pidZeroGrace time.Duration, now func() time.Time) (int, error) {
	if deps == nil || deps.Store == nil {
		return 0, errors.New("orphansweep.SweepOrphans: Dependencies.Store is required")
	}
	_ = ctx

	rows, err := deps.Store.ListRunningRows()
	if err != nil {
		return 0, fmt.Errorf("orphansweep.SweepOrphans: list running rows: %w", err)
	}

	var orphaned int
	nowTS := now().UTC()
	for _, row := range rows {
		if row == nil {
			continue
		}
		reason, drop := classifyForReconciliation(row, deps.LiveSessions, nowTS, pidZeroGrace)
		if !drop {
			continue
		}
		if err := deps.Store.MarkRuntimeOrphaned(row.ID, reason); err != nil {
			slog.Warn("agent_runtime: orphan reconciliation persist failed",
				"runtime_id", row.ID,
				"provider", row.Provider,
				"mode", row.Mode,
				"reason", reason,
				"err", err,
			)
			continue
		}
		slog.Info("agent_runtime: reconciled orphan",
			"runtime_id", row.ID,
			"provider", row.Provider,
			"mode", row.Mode,
			"state_before", row.State,
			"pid", row.PID,
			"reason", reason,
			"updated_at_age", nowTS.Sub(row.UpdatedAt).String(),
		)
		orphaned++
	}
	return orphaned, nil
}

// classifyForReconciliation is the pure decision function: given a row
// snapshot and the runtime helpers, return the failure_reason to record
// (empty when the row is healthy) and whether to write it. Pulled out so
// the test surface can exercise the matrix without standing up a fake
// store.
func classifyForReconciliation(row *agent.RuntimeRow, live agent.LiveSessionChecker, now time.Time, pidZeroGrace time.Duration) (string, bool) {
	if row == nil {
		return "", false
	}
	if row.PID > 0 {
		if pidAlive(row.PID) {
			return "", false
		}
		return ReasonReconcileDeadPid, true
	}
	// PID == 0 path — codex / any adapter that doesn't surface a pid.
	if live != nil {
		if live.IsLive(row.ID) {
			return "", false
		}
		// No live session AND we have a checker → the in-memory registry
		// is authoritative for the current process. Either we restarted
		// (empty registry; every pre-restart pid=0 row is by definition
		// orphaned) or the session genuinely never registered.
		// Honor the staleness grace so a row mid-launch (updated_at fresh,
		// registry not yet populated) isn't reaped on the first sweep.
		if !row.UpdatedAt.IsZero() && now.Sub(row.UpdatedAt) < pidZeroGrace {
			return "", false
		}
		return ReasonReconcileNoLiveSession, true
	}
	// No live-session checker wired (tests, minimal harness). Fall back
	// to updated_at staleness alone. updated_at being zero means the row
	// is new-ish; skip rather than mis-classify.
	if row.UpdatedAt.IsZero() {
		return "", false
	}
	if now.Sub(row.UpdatedAt) < pidZeroGrace {
		return "", false
	}
	return ReasonReconcilePidZeroStale, true
}

// pidAlive returns true if a signal-0 probe succeeds against pid. On
// darwin/linux this is the canonical liveness check that doesn't side-
// effect the target. EPERM still means the process exists (we just don't
// own it), so treat that as alive too.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}

// RuntimeReaper periodically sweeps agent_runtime for rows whose
// underlying process has died — either because the service was restarted
// (pre-restart rows orphan-en-masse) or because a long-lived session's
// PTY/child exited without persisting a terminal state.
//
// Pairs with the subagent reaper (internal/subagent.Reaper) but operates
// on a different table: that one sweeps subagent_runs for budget /
// orphan-spawn timeouts; this one sweeps agent_runtime for dead
// processes. Both run on their own goroutines with independent cadence.
//
// Lifecycle:
//   - NewRuntimeReaper constructs; nothing runs until Start.
//   - Start spawns a single background goroutine bound to ctx. The
//     goroutine exits on ctx.Done() or Stop, whichever happens first.
//   - Stop is idempotent and blocks until the goroutine exits, so
//     container shutdown can sequence the reaper before the DB closes.
//
// Concurrency: SweepOrphans is safe to invoke from multiple goroutines
// (each UPDATE is guarded by `WHERE state IN ('launching','running')` at
// the SQL layer in MarkRuntimeOrphaned via state column). The reaper
// owns a single goroutine in production; tests may call SweepOnce
// directly.
type RuntimeReaper struct {
	deps         *agent.Dependencies
	interval     time.Duration
	pidZeroGrace time.Duration
	now          func() time.Time

	started   atomic.Bool
	startOnce sync.Once
	stopOnce  sync.Once
	doneOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
}

// RuntimeReaperOptions tweaks reaper cadence + clock. Zero values fall
// back to the Default* constants; production leaves Now nil.
type RuntimeReaperOptions struct {
	Interval     time.Duration
	PidZeroGrace time.Duration
	Now          func() time.Time
}

// NewRuntimeReaper constructs a RuntimeReaper bound to deps. deps must
// carry a non-nil Store; otherwise Start logs and exits without ever
// sweeping. Defaults for Interval and PidZeroGrace are applied here;
// pass non-zero values to override.
func NewRuntimeReaper(deps *agent.Dependencies, opts RuntimeReaperOptions) *RuntimeReaper {
	interval := opts.Interval
	if interval <= 0 {
		interval = DefaultRuntimeReaperInterval
	}
	grace := opts.PidZeroGrace
	if grace <= 0 {
		grace = DefaultRuntimeReaperPidZeroGrace
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &RuntimeReaper{
		deps:         deps,
		interval:     interval,
		pidZeroGrace: grace,
		now:          now,
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
}

// Start spawns the reaper goroutine. ctx is the parent context; the
// reaper exits when ctx is cancelled OR Stop is called. Calling Start
// more than once is a no-op.
//
// deps.Store nil is tolerated — the goroutine logs and exits, so wiring
// code can call Start unconditionally even in test harnesses without an
// agent_runtime backend.
func (r *RuntimeReaper) Start(ctx context.Context) {
	r.startOnce.Do(func() {
		r.started.Store(true)
		if r.deps == nil || r.deps.Store == nil {
			slog.Warn("agent_runtime reaper: nil store; reaper disabled")
			r.closeDone()
			return
		}
		go r.loop(ctx)
	})
}

func (r *RuntimeReaper) closeDone() {
	r.doneOnce.Do(func() { close(r.doneCh) })
}

// Stop signals the reaper goroutine to exit and blocks until it has
// returned. Calling Stop twice is a no-op. Safe to call before Start
// (the call closes doneCh itself so the wait doesn't deadlock).
func (r *RuntimeReaper) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
		if !r.started.Load() {
			r.startOnce.Do(func() {
				r.started.Store(true)
				r.closeDone()
			})
		}
	})
	<-r.doneCh
}

// SweepOnce runs a single reconciliation pass. Exposed for tests +
// for the synchronous startup-sweep call site so the composition root
// can run one sweep at boot and then hand the goroutine to Start.
func (r *RuntimeReaper) SweepOnce(ctx context.Context) (int, error) {
	return sweepOrphansAt(ctx, r.deps, r.pidZeroGrace, r.now)
}

// loop is the goroutine body. Exits on ctx.Done or stopCh.
func (r *RuntimeReaper) loop(ctx context.Context) {
	defer r.closeDone()

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	slog.Info("agent_runtime reaper: started",
		"interval", r.interval.String(),
		"pid_zero_grace", r.pidZeroGrace.String(),
	)

	for {
		select {
		case <-ctx.Done():
			slog.Info("agent_runtime reaper: stopping (ctx cancelled)")
			return
		case <-r.stopCh:
			slog.Info("agent_runtime reaper: stopping (Stop called)")
			return
		case <-ticker.C:
			n, err := r.SweepOnce(ctx)
			if err != nil {
				slog.Warn("agent_runtime reaper: sweep error", "err", err)
				continue
			}
			if n > 0 {
				slog.Info("agent_runtime reaper: reconciled", "count", n)
			}
		}
	}
}
