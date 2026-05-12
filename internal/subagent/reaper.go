package subagent

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Default reaper tuning. Values picked to surface hangs quickly without
// hammering SQLite — the reaper does a handful of guarded UPDATEs per
// tick. See CW-20260512-0002 subtodos (b) + (c) for the originating
// audit data (UAT-c160) showing rows >20h past their timeouts.
const (
	// DefaultReaperInterval is the wall-clock cadence between reaper
	// sweeps. Matches subtodo (b)'s "~30s cycle" guidance.
	DefaultReaperInterval = 30 * time.Second

	// DefaultReaperOrphanGrace is the floor on how long a row with an
	// empty child_session_id may sit in `running` before being reaped
	// for never having spawned. Subtodo (c) calls for ~60s — short
	// enough to clean up real orphans, long enough that a slow
	// createChildSession + persistChildSessionID isn't preempted.
	DefaultReaperOrphanGrace = 60 * time.Second

	// Reason strings written to subagent_runs.error so audit / logs /
	// future dashboards can group failures by reaper cause.
	ReasonTimeoutReaper      = "timeout: runner reaper"
	ReasonOrphanReaper       = "timeout: orphan, no child session"
)

// Reaper periodically sweeps subagent_runs for rows that have outlived
// their wall-time budget or never managed to spawn a child session, and
// transitions them to `failed` so the parent's dispatch path unblocks
// instead of waiting forever.
//
// Lifecycle:
//   - NewReaper constructs the worker; nothing runs until Start.
//   - Start spawns a single background goroutine bound to the provided
//     context. The goroutine exits cleanly when ctx is cancelled OR
//     Stop is called, whichever happens first.
//   - Stop is idempotent and blocks until the goroutine exits, so
//     container shutdown can sequence the reaper before the DB closes.
//
// Concurrency contract:
//   - Reaper UPDATEs are guarded by `WHERE status = 'running'` so a
//     concurrent finalizeRun cannot have its terminal write clobbered.
//     RowsAffected==0 means another writer won the race; the reaper
//     simply moves on.
//   - SweepOnce is safe to call from tests directly; it returns the
//     counts of rows reaped per category for assertion.
type Reaper struct {
	db           *sql.DB
	interval     time.Duration
	orphanGrace  time.Duration

	// now is the clock surface. Defaults to time.Now; tests override it
	// to drive deterministic sweeps without sleeping.
	now func() time.Time

	// started flips to true when Start runs and dispatches (or short-circuits
	// in the nil-db branch). Stop reads it to decide whether to close doneCh
	// itself — if Start never ran, neither the loop nor the nil-db branch
	// will ever close doneCh, so Stop must do it or block forever.
	started atomic.Bool

	startOnce sync.Once
	stopOnce  sync.Once
	// doneOnce gates the close of doneCh so multiple paths (loop's defer,
	// the nil-db branch in Start, and the Stop-before-Start branch in
	// Stop) can all attempt the close without panicking on the second
	// attempt. Replaces the prior reliance on path-exclusivity, which
	// broke when Stop was called before Start (CW-20260512-0002 review #1).
	doneOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// ReaperOptions tweaks reaper cadence for tests + ops. Zero values fall
// back to the Default* constants.
type ReaperOptions struct {
	Interval    time.Duration
	OrphanGrace time.Duration
	// Now overrides the wall clock. Tests use this to step time without
	// real sleeps; production leaves it nil.
	Now func() time.Time
}

// NewReaper constructs a Reaper bound to db. Defaults for Interval,
// OrphanGrace, and Now are applied at construction time — pass non-zero
// values to override. db is required.
func NewReaper(db *sql.DB, opts ReaperOptions) *Reaper {
	interval := opts.Interval
	if interval <= 0 {
		interval = DefaultReaperInterval
	}
	grace := opts.OrphanGrace
	if grace <= 0 {
		grace = DefaultReaperOrphanGrace
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Reaper{
		db:          db,
		interval:    interval,
		orphanGrace: grace,
		now:         now,
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
	}
}

// Start spawns the reaper goroutine. ctx is the parent context; the
// reaper exits when ctx is cancelled OR Stop is called. Calling Start
// more than once is a no-op (the second call returns immediately).
//
// db nil is tolerated — the goroutine logs and exits, so wiring code
// can call Start unconditionally even in test harnesses that skip the
// reaper.
func (r *Reaper) Start(ctx context.Context) {
	r.startOnce.Do(func() {
		r.started.Store(true)
		if r.db == nil {
			slog.Warn("subagent reaper: nil db; reaper disabled")
			r.closeDone()
			return
		}
		go r.loop(ctx)
	})
}

// closeDone is the single safe path to close doneCh. Idempotent via
// doneOnce so the loop's defer, the nil-db Start branch, and the
// Stop-before-Start branch can each call it without coordination.
func (r *Reaper) closeDone() {
	r.doneOnce.Do(func() { close(r.doneCh) })
}

// Stop signals the reaper goroutine to exit and blocks until it has
// returned. Calling Stop twice is a no-op.
//
// Stop-before-Start safety: when Start was never invoked there is no
// loop goroutine to close doneCh and no nil-db branch will fire, so
// Stop must close doneCh itself or it would block forever waiting for
// a writer that does not exist. The doneOnce gate makes the close safe
// even if a Start call interleaves and reaches the nil-db branch (or
// loop defer) on a different path.
func (r *Reaper) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
		// If Start never ran, take ownership of doneCh closure here.
		// We also flip started=true and consume startOnce so a late
		// Start call short-circuits — no goroutine is ever spawned
		// against a closed stopCh, and the doneOnce gate is the final
		// belt-and-suspenders against a double close if a Start call
		// somehow raced past the started check.
		if !r.started.Load() {
			r.startOnce.Do(func() {
				r.started.Store(true)
				r.closeDone()
			})
		}
	})
	// Block until the goroutine has actually exited so DB close ordering
	// is safe. doneCh is closed by Start's nil-db branch, loop's defer,
	// or the Stop-before-Start branch above — all routed through
	// closeDone for idempotence.
	<-r.doneCh
}

// loop is the goroutine body. Exits on ctx.Done or stopCh.
func (r *Reaper) loop(ctx context.Context) {
	defer r.closeDone()

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	slog.Info("subagent reaper: started",
		"interval", r.interval.String(),
		"orphan_grace", r.orphanGrace.String(),
	)

	for {
		select {
		case <-ctx.Done():
			slog.Info("subagent reaper: stopping (ctx cancelled)")
			return
		case <-r.stopCh:
			slog.Info("subagent reaper: stopping (Stop called)")
			return
		case <-ticker.C:
			counts, err := r.SweepOnce(ctx)
			if err != nil {
				slog.Warn("subagent reaper: sweep error", "err", err)
				continue
			}
			if counts.Total() > 0 {
				slog.Info("subagent reaper: reaped",
					"timeouts", counts.Timeouts,
					"orphans", counts.Orphans,
				)
			}
		}
	}
}

// SweepCounts reports how many rows each reaper branch transitioned to
// `failed` on a single sweep. Tests read these to assert behavior.
type SweepCounts struct {
	Timeouts int
	Orphans  int
}

// Total reports the sum across categories.
func (s SweepCounts) Total() int { return s.Timeouts + s.Orphans }

// SweepOnce executes a single reaper pass: timeouts first, then orphans.
// Returns the per-category counts of rows transitioned to `failed`.
//
// Both sweeps use UPDATE … WHERE status = 'running' so they never
// clobber a row that finalizeRun (or Cancel, or a competing reaper) has
// already moved to a terminal state. SQLite UPDATE is atomic per
// statement, so there's no need for an explicit transaction.
//
// SQL details:
//   - Timeout branch matches rows whose started_at is non-empty AND
//     started_at + timeout_seconds < now. The non-empty guard avoids
//     reaping a row that's still in the orphan-grace window solely
//     because the runner hadn't set started_at yet (defense in depth —
//     in practice all rows that reach status=running have started_at
//     set, since Spawn writes both in the same INSERT or UPDATE).
//   - Orphan branch matches rows with empty child_session_id AND
//     created_at older than orphan_grace. created_at — not started_at —
//     because an orphan never reached the spawn path where started_at
//     gets stamped past the requested→running transition.
func (r *Reaper) SweepOnce(ctx context.Context) (SweepCounts, error) {
	if r.db == nil {
		return SweepCounts{}, nil
	}
	now := r.now().UTC()
	nowRFC := now.Format(time.RFC3339Nano)

	var counts SweepCounts

	// --- Timeout branch: rows past started_at + timeout_seconds. ---
	//
	// SQLite datetime() math:
	//   datetime(started_at, '+' || timeout_seconds || ' seconds')
	// returns SQLite's canonical "YYYY-MM-DD HH:MM:SS" format (no 'T',
	// no 'Z'). The RHS is also wrapped in datetime() so both sides use
	// the same normalized representation — lexicographic comparison
	// between "2026-05-12 04:29:50" and "2026-05-12T04:25:00Z" yields
	// the wrong answer (' ' (0x20) sorts before 'T' (0x54)).
	//
	// The nowRFC argument is the deterministic clock surface — tests
	// override Now() to a fixed time; production uses time.Now().
	timeoutSQL := `UPDATE subagent_runs
	   SET status = 'failed',
	       error = ?,
	       completed_at = ?
	 WHERE status = 'running'
	   AND started_at != ''
	   AND datetime(started_at, '+' || timeout_seconds || ' seconds') < datetime(?)`

	res, err := r.db.ExecContext(ctx, timeoutSQL, ReasonTimeoutReaper, nowRFC, nowRFC)
	if err != nil {
		return counts, fmt.Errorf("reaper timeout sweep: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil {
		counts.Timeouts = int(n)
	}

	// --- Orphan branch: status=running, empty child_session_id, older
	// than orphan_grace by created_at. ---
	//
	// The timeout sweep above already catches rows that DO have
	// started_at past their timeout, so this branch only fires for rows
	// that never even reached spawn (child session creation failed
	// silently, or the runner crashed before persistChildSessionID).
	// Both sides wrapped in datetime() for the same canonicalization
	// reason as the timeout branch above.
	orphanCutoff := now.Add(-r.orphanGrace).Format(time.RFC3339Nano)
	orphanSQL := `UPDATE subagent_runs
	   SET status = 'failed',
	       error = ?,
	       completed_at = ?
	 WHERE status = 'running'
	   AND child_session_id = ''
	   AND datetime(created_at) < datetime(?)`

	res, err = r.db.ExecContext(ctx, orphanSQL, ReasonOrphanReaper, nowRFC, orphanCutoff)
	if err != nil {
		return counts, fmt.Errorf("reaper orphan sweep: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil {
		counts.Orphans = int(n)
	}

	return counts, nil
}
