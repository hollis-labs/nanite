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

	// DefaultHardCeiling is the non-resetting backstop applied to every
	// running row regardless of activity (CW-20260816-0004, porting
	// Torque's proven two-timer model — internal/runtime/agent/timeout.go
	// in the Torque repo, defaultHardCeiling = 12h, production-validated
	// there). A run that keeps heartbeating forever is still not allowed
	// to run forever; this is the last-resort kill.
	//
	// Deliberately NOT maxTimeoutSeconds (7200s / 2h, service.go) — that
	// constant is the override ceiling on the OLD single wall-clock knob
	// (timeout_seconds), a considered bound for how long a single
	// unconfigured attempt may run, not a considered bound for "how long
	// can a run legitimately keep making genuine progress." 12h matches
	// what Torque actually runs in production without complaint.
	DefaultHardCeiling = 12 * time.Hour

	// Reason strings written to subagent_runs.error so audit / logs /
	// future dashboards can group failures by reaper cause.
	//
	// ReasonTimeoutReaper is reserved for the hard-ceiling branch only
	// (status=failed — a genuine, no-more-chances kill). Before
	// CW-20260816-0004 this string also covered the inactivity branch;
	// that branch now uses ReasonInactivityReaper and lands on
	// status=stalled instead, since "went quiet for a while" and "hit
	// the absolute backstop" are different failure classes and the
	// second one no longer exists for a run that's still heartbeating.
	ReasonTimeoutReaper    = "timeout: runner reaper"
	ReasonInactivityReaper = "stalled: inactivity reaper (no activity observed within threshold)"
	ReasonOrphanReaper     = "timeout: orphan, no child session"
)

// Reaper periodically sweeps subagent_runs for rows that have outlived
// their hard ceiling, gone silent past their inactivity threshold, or
// never managed to spawn a child session, and transitions them to a
// terminal state (`failed` or `stalled`, see SweepOnce) so the parent's
// dispatch path unblocks instead of waiting forever.
//
// Lifecycle:
//   - NewReaper constructs the worker; nothing runs until Start.
//   - Start spawns a single background goroutine bound to the provided
//     context. The goroutine exits cleanly when ctx is canceled OR
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
	db          *sql.DB
	interval    time.Duration
	orphanGrace time.Duration
	// hardCeiling is the non-resetting backstop duration (CW-20260816-0004).
	// See DefaultHardCeiling for the reasoning behind the default value.
	hardCeiling time.Duration

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
	// HardCeiling overrides DefaultHardCeiling when non-zero
	// (CW-20260816-0004). Tests use small values to exercise the branch
	// without a fake 12h clock jump; production leaves it zero.
	HardCeiling time.Duration
	// Now overrides the wall clock. Tests use this to step time without
	// real sleeps; production leaves it nil.
	Now func() time.Time
}

// NewReaper constructs a Reaper bound to db. Defaults for Interval,
// OrphanGrace, HardCeiling, and Now are applied at construction time —
// pass non-zero values to override. db is required.
func NewReaper(db *sql.DB, opts ReaperOptions) *Reaper {
	interval := opts.Interval
	if interval <= 0 {
		interval = DefaultReaperInterval
	}
	grace := opts.OrphanGrace
	if grace <= 0 {
		grace = DefaultReaperOrphanGrace
	}
	hardCeiling := opts.HardCeiling
	if hardCeiling <= 0 {
		hardCeiling = DefaultHardCeiling
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Reaper{
		db:          db,
		interval:    interval,
		orphanGrace: grace,
		hardCeiling: hardCeiling,
		now:         now,
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
	}
}

// Start spawns the reaper goroutine. ctx is the parent context; the
// reaper exits when ctx is canceled OR Stop is called. Calling Start
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
		"hard_ceiling", r.hardCeiling.String(),
	)

	for {
		select {
		case <-ctx.Done():
			slog.Info("subagent reaper: stopping (ctx canceled)")
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
					"hard_ceiling", counts.HardCeiling,
					"inactivity", counts.Inactivity,
					"orphans", counts.Orphans,
				)
			}
		}
	}
}

// SweepCounts reports how many rows each reaper branch transitioned out
// of `running` on a single sweep. Tests read these to assert behavior.
type SweepCounts struct {
	// HardCeiling counts rows reaped by the non-resetting backstop
	// branch (status -> failed, ReasonTimeoutReaper). Fires regardless
	// of activity.
	HardCeiling int
	// Inactivity counts rows reaped because no activity was observed
	// within the inactivity threshold (status -> stalled,
	// ReasonInactivityReaper). A run that keeps heartbeating never
	// trips this branch no matter how long it runs.
	Inactivity int
	Orphans    int
}

// Total reports the sum across categories.
func (s SweepCounts) Total() int { return s.HardCeiling + s.Inactivity + s.Orphans }

// SweepOnce executes a single reaper pass: hard-ceiling first, then
// inactivity, then orphans. Returns the per-category counts of rows
// transitioned out of `running`.
//
// CW-20260816-0004 — activity-reset inactivity timer + separate,
// non-resetting hard ceiling (Torque parity; ports the mechanism from
// Torque's executor_longlived.go awaitLongLivedCompletion, not literal
// code). Before this change the sole branch here compared pure elapsed
// time (started_at + timeout_seconds < now) with zero regard for
// whether the run was still doing anything — a subagent whose own 30s
// heartbeat ticker was firing the entire time got reaped at exactly the
// 30-minute mark, discarding genuinely in-flight work. Now:
//
//   - The hard-ceiling branch is the only one that still measures pure
//     elapsed time from started_at, and it runs FIRST. It is the
//     backstop of last resort — even a run that never stops
//     heartbeating is eventually killed here. Firing this branch is a
//     genuine failure (status=failed, ReasonTimeoutReaper).
//   - The inactivity branch runs SECOND, only against rows the
//     hard-ceiling branch didn't already claim (its own WHERE
//     status='running' guard naturally excludes them once the first
//     UPDATE lands). It measures elapsed time from the more recent of
//     last_activity_at / started_at — a run that keeps stamping
//     activity (the heartbeat ticker, service.go emitHeartbeat) never
//     trips it. Firing this branch is NOT a crash: the run genuinely
//     went silent, so it's parked at status=stalled
//     (ReasonInactivityReaper) rather than failed — mirrors Torque's
//     canceled-vs-failed split, and reuses Nanite's existing
//     StatusStalled semantics ("an inactivity watchdog fired") rather
//     than inventing a new status.
//
// Ordering matters: if the inactivity branch ran first, a row that
// satisfies BOTH conditions (past the hard ceiling AND currently quiet)
// would already be flipped to 'stalled' by the time the hard-ceiling
// branch's WHERE status='running' guard runs, permanently
// under-reporting hard-ceiling kills. Running hard-ceiling first makes
// the more severe outcome win on overlap, which also matches the
// operational meaning: "no more chances" outranks "let's park it."
//
// All three UPDATEs use WHERE status = 'running' so they never clobber
// a row that finalizeRun (or Cancel, or a competing reaper) has already
// moved to a terminal state. SQLite UPDATE is atomic per statement, so
// there's no need for an explicit transaction.
//
// SQL details:
//   - Hard-ceiling branch matches rows whose started_at is non-empty
//     AND started_at + hardCeiling < now. hardCeiling is a
//     reaper-level duration (DefaultHardCeiling unless overridden via
//     ReaperOptions), not a per-row column — unlike timeout_seconds,
//     no per-run override exists yet.
//   - Inactivity branch matches rows whose started_at is non-empty AND
//     COALESCE(NULLIF(last_activity_at, ”), started_at) +
//     timeout_seconds < now. last_activity_at defaults to ” (migration
//     092) until the first heartbeat stamps it (service.go
//     stampActivity); NULLIF converts that empty default to NULL so
//     COALESCE falls back to started_at — a run with no heartbeat
//     signal yet behaves exactly like the pre-fix elapsed-time check.
//     Reuses the existing timeout_seconds column (still populated by
//     Spawn via resolveDefaultTimeoutSeconds, default 1800s/30min) as
//     the inactivity threshold rather than adding a second per-row
//     column — this is the same number that already governed the old
//     single-branch sweep, now reinterpreted as a silence window
//     instead of a total-runtime cap.
//   - Both sides of each comparison are wrapped in datetime() so SQLite
//     canonicalizes to the same "YYYY-MM-DD HH:MM:SS" representation —
//     lexicographic comparison between that and a raw
//     "2026-05-12T04:25:00Z" RFC3339 string yields the wrong answer
//     (' ' (0x20) sorts before 'T' (0x54)).
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

	// --- Hard-ceiling branch: rows past started_at + hardCeiling,
	// unconditionally (no activity check). Runs first — see the
	// ordering rationale in the doc comment above. ---
	hardCeilingSeconds := int64(r.hardCeiling.Seconds())
	hardCeilingSQL := `UPDATE subagent_runs
	   SET status = 'failed',
	       error = ?,
	       completed_at = ?
	 WHERE status = 'running'
	   AND started_at != ''
	   AND datetime(started_at, '+' || ? || ' seconds') < datetime(?)`

	res, err := r.db.ExecContext(ctx, hardCeilingSQL, ReasonTimeoutReaper, nowRFC, hardCeilingSeconds, nowRFC)
	if err != nil {
		return counts, fmt.Errorf("reaper hard-ceiling sweep: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil {
		counts.HardCeiling = int(n)
	}

	// --- Inactivity branch: rows whose most recent activity signal
	// (last_activity_at, falling back to started_at) is past
	// timeout_seconds. A row already claimed by the hard-ceiling branch
	// above is no longer status='running', so this UPDATE naturally
	// skips it. ---
	inactivitySQL := `UPDATE subagent_runs
	   SET status = 'stalled',
	       error = ?,
	       completed_at = ?
	 WHERE status = 'running'
	   AND started_at != ''
	   AND datetime(
	         COALESCE(NULLIF(last_activity_at, ''), started_at),
	         '+' || timeout_seconds || ' seconds'
	       ) < datetime(?)`

	res, err = r.db.ExecContext(ctx, inactivitySQL, ReasonInactivityReaper, nowRFC, nowRFC)
	if err != nil {
		return counts, fmt.Errorf("reaper inactivity sweep: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil {
		counts.Inactivity = int(n)
	}

	// --- Orphan branch: status=running, empty child_session_id, older
	// than orphan_grace by created_at. ---
	//
	// The hard-ceiling and inactivity branches above already catch rows
	// that DO have started_at set, so this branch only fires for rows
	// that never even reached spawn (child session creation failed
	// silently, or the runner crashed before persistChildSessionID).
	// Both sides wrapped in datetime() for the same canonicalization
	// reason as the branches above.
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
