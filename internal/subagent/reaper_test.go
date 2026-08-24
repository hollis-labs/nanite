package subagent

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// insertRunForReaper is a convenience helper for tests that inserts a
// minimal subagent_runs row with the canonical column set the reaper
// needs. Keep
// in sync with internal/store/migrations/017_subagent_runs.sql + later
// additive migrations: any new NOT NULL column without a DEFAULT must be
// supplied here. Fields not relevant to reaper logic stay at SQL DEFAULT.
//
// last_activity_at (migration 092, CW-20260816-0004) defaults to "" —
// tests that need to simulate a heartbeat-stamped run pass it explicitly
// via fields.
func insertRunForReaper(t *testing.T, db *sql.DB, id string, fields map[string]any) {
	t.Helper()
	defaults := map[string]any{
		"id":                id,
		"parent_session_id": "sess-parent",
		"child_session_id":  "child-" + id,
		"role":              "worker",
		"prompt":            "test prompt",
		"mode":              "sync",
		"status":            StatusRunning,
		"inputs_json":       "{}",
		"result_json":       "{}",
		"error":             "",
		"timeout_seconds":   300,
		"created_at":        time.Now().UTC().Format(time.RFC3339Nano),
		"started_at":        time.Now().UTC().Format(time.RFC3339Nano),
		"completed_at":      "",
		"last_activity_at":  "",
	}
	for k, v := range fields {
		defaults[k] = v
	}
	_, err := db.Exec(
		`INSERT INTO subagent_runs
		    (id, parent_session_id, child_session_id, role, prompt,
		     mode, status, inputs_json, result_json, error,
		     timeout_seconds, created_at, started_at, completed_at,
		     last_activity_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		defaults["id"], defaults["parent_session_id"], defaults["child_session_id"],
		defaults["role"], defaults["prompt"], defaults["mode"], defaults["status"],
		defaults["inputs_json"], defaults["result_json"], defaults["error"],
		defaults["timeout_seconds"], defaults["created_at"], defaults["started_at"],
		defaults["completed_at"], defaults["last_activity_at"],
	)
	if err != nil {
		t.Fatalf("insert reaper row %q: %v", id, err)
	}
}

func readRunStatusError(t *testing.T, db *sql.DB, id string) (status, errMsg string) {
	t.Helper()
	if err := db.QueryRow(
		`SELECT status, error FROM subagent_runs WHERE id = ?`, id,
	).Scan(&status, &errMsg); err != nil {
		t.Fatalf("scan run %q: %v", id, err)
	}
	return status, errMsg
}

// TestReaper_InactivitySweep_FallsBackToStartedAt verifies that a row
// with no last_activity_at yet (the pre-heartbeat / never-heartbeated
// case) is reaped using started_at as the activity baseline — i.e. the
// COALESCE(NULLIF(last_activity_at, ”), started_at) fallback behaves
// exactly like the old started_at-only comparison when no heartbeat has
// landed. Lands on status=stalled (CW-20260816-0004), not failed — see
// TestReaper_InactivityBranch_LandsOnStalledNotFailed for the dedicated
// assertion on that status split. CW-20260512-0002 subtodo (b) is the
// origin of this test; CW-20260816-0004 changed its expected outcome.
func TestReaper_InactivitySweep_FallsBackToStartedAt(t *testing.T) {
	db, _ := newTestDB(t)

	// Fixed clock: T0. Row started 10 minutes ago with a 60s inactivity
	// threshold — 9 minutes past — and never heartbeated
	// (last_activity_at == "").
	t0 := time.Date(2026, 5, 12, 4, 25, 0, 0, time.UTC)
	insertRunForReaper(t, db, "run-late", map[string]any{
		"started_at":      t0.Add(-10 * time.Minute).Format(time.RFC3339Nano),
		"timeout_seconds": 60,
	})

	r := NewReaper(db, ReaperOptions{Now: func() time.Time { return t0 }})
	counts, err := r.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if counts.Inactivity != 1 {
		t.Errorf("Inactivity = %d, want 1", counts.Inactivity)
	}
	if counts.HardCeiling != 0 {
		t.Errorf("HardCeiling = %d, want 0 (10 minutes is nowhere near the 12h default)", counts.HardCeiling)
	}
	if counts.Orphans != 0 {
		t.Errorf("Orphans = %d, want 0 (row had a child_session_id)", counts.Orphans)
	}

	status, errMsg := readRunStatusError(t, db, "run-late")
	if status != StatusStalled {
		t.Errorf("status = %q, want %q", status, StatusStalled)
	}
	if errMsg != ReasonInactivityReaper {
		t.Errorf("error = %q, want %q", errMsg, ReasonInactivityReaper)
	}
}

// TestReaper_TimeoutGuardDoesNotClobberCompleted verifies that a row
// that has already been marked completed is NOT overwritten by the
// reaper — the WHERE status='running' guard wins the race.
// CW-20260512-0002 subtodo (b): "UPDATE … WHERE status='running' guard".
func TestReaper_TimeoutGuardDoesNotClobberCompleted(t *testing.T) {
	db, _ := newTestDB(t)
	t0 := time.Date(2026, 5, 12, 4, 25, 0, 0, time.UTC)
	insertRunForReaper(t, db, "run-finished", map[string]any{
		"started_at":      t0.Add(-10 * time.Minute).Format(time.RFC3339Nano),
		"timeout_seconds": 60,
		"status":          "completed", // late completion landed BEFORE the reaper tick
		"result_json":     `{"summary":"done"}`,
	})

	r := NewReaper(db, ReaperOptions{Now: func() time.Time { return t0 }})
	counts, err := r.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if counts.Total() != 0 {
		t.Errorf("Total = %d, want 0 — completed rows must not be touched", counts.Total())
	}

	status, errMsg := readRunStatusError(t, db, "run-finished")
	if status != "completed" {
		t.Errorf("status = %q, want \"completed\" (preserved)", status)
	}
	if errMsg != "" {
		t.Errorf("error = %q, want \"\" — reaper must not stamp error onto a completed row", errMsg)
	}
}

// TestReaper_TimeoutLeavesUnexpiredRowsAlone verifies that a row whose
// inactivity threshold has NOT yet elapsed is left at status=running.
// Defensive: any reaper that fires early would prematurely fail healthy
// long-running subagents.
func TestReaper_TimeoutLeavesUnexpiredRowsAlone(t *testing.T) {
	db, _ := newTestDB(t)
	t0 := time.Date(2026, 5, 12, 4, 25, 0, 0, time.UTC)
	insertRunForReaper(t, db, "run-fresh", map[string]any{
		"started_at":      t0.Add(-10 * time.Second).Format(time.RFC3339Nano),
		"timeout_seconds": 300,
	})

	r := NewReaper(db, ReaperOptions{Now: func() time.Time { return t0 }})
	counts, err := r.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if counts.Total() != 0 {
		t.Errorf("Total reaps = %d, want 0", counts.Total())
	}
	status, _ := readRunStatusError(t, db, "run-fresh")
	if status != StatusRunning {
		t.Errorf("status = %q, want %q", status, StatusRunning)
	}
}

// TestReaper_ActivityResetPreventsInactivityReap reproduces the exact
// bug shape from CW-20260816-0004's live incident: a run whose
// last_activity_at keeps advancing (simulating the 30s heartbeat
// ticker, service.go emitHeartbeat/stampActivity) past the point where
// the OLD pure-elapsed-time check would have killed it must NOT be
// reaped. started_at is 40 minutes in the past — well past the old
// 30-minute wall clock — but last_activity_at is only 5 seconds old, so
// the inactivity threshold (30 min) hasn't actually elapsed since real
// activity was last observed.
func TestReaper_ActivityResetPreventsInactivityReap(t *testing.T) {
	db, _ := newTestDB(t)
	t0 := time.Date(2026, 5, 12, 4, 25, 0, 0, time.UTC)
	insertRunForReaper(t, db, "run-still-active", map[string]any{
		"started_at":       t0.Add(-40 * time.Minute).Format(time.RFC3339Nano),
		"last_activity_at": t0.Add(-5 * time.Second).Format(time.RFC3339Nano),
		"timeout_seconds":  DefaultTimeoutSeconds, // 1800s / 30min, the real inactivity default
	})

	r := NewReaper(db, ReaperOptions{Now: func() time.Time { return t0 }})
	counts, err := r.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if counts.Total() != 0 {
		t.Errorf("Total reaps = %d, want 0 — a run with a 5s-old heartbeat must survive past the old 30-minute mark (started_at was 40 minutes ago)", counts.Total())
	}
	status, errMsg := readRunStatusError(t, db, "run-still-active")
	if status != StatusRunning {
		t.Errorf("status = %q, want %q — genuinely active work must not be discarded", status, StatusRunning)
	}
	if errMsg != "" {
		t.Errorf("error = %q, want \"\"", errMsg)
	}
}

// TestReaper_HardCeilingReapsRegardlessOfActivity verifies the
// non-resetting backstop branch: a row past the hard ceiling is reaped
// to status=failed even when its last_activity_at is fresh. This is the
// scenario the inactivity branch alone can never catch — a run that
// keeps heartbeating forever must still eventually terminate.
func TestReaper_HardCeilingReapsRegardlessOfActivity(t *testing.T) {
	db, _ := newTestDB(t)
	t0 := time.Date(2026, 5, 12, 4, 25, 0, 0, time.UTC)
	insertRunForReaper(t, db, "run-forever-active", map[string]any{
		"started_at":       t0.Add(-2 * time.Hour).Format(time.RFC3339Nano),
		"last_activity_at": t0.Add(-1 * time.Second).Format(time.RFC3339Nano), // heartbeat just ticked
		"timeout_seconds":  DefaultTimeoutSeconds,                             // 30min inactivity window — nowhere near tripped
	})

	// HardCeiling overridden to 1h (well under the 2h elapsed) so the
	// test doesn't need a fake 12h clock jump to exercise the branch.
	r := NewReaper(db, ReaperOptions{
		HardCeiling: 1 * time.Hour,
		Now:         func() time.Time { return t0 },
	})
	counts, err := r.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if counts.HardCeiling != 1 {
		t.Errorf("HardCeiling = %d, want 1", counts.HardCeiling)
	}
	if counts.Inactivity != 0 {
		t.Errorf("Inactivity = %d, want 0 — the hard-ceiling branch must claim the row first", counts.Inactivity)
	}

	status, errMsg := readRunStatusError(t, db, "run-forever-active")
	if status != StatusFailed {
		t.Errorf("status = %q, want %q — hard-ceiling kills are genuine failures", status, StatusFailed)
	}
	if errMsg != ReasonTimeoutReaper {
		t.Errorf("error = %q, want %q", errMsg, ReasonTimeoutReaper)
	}
}

// TestReaper_InactivityBranch_LandsOnStalledNotFailed pins the status
// split the ticket calls out explicitly: the inactivity branch (a run
// that genuinely went silent, as opposed to hitting the hard ceiling)
// must land on StatusStalled, reserving StatusFailed + ReasonTimeoutReaper
// for the hard-ceiling branch only.
func TestReaper_InactivityBranch_LandsOnStalledNotFailed(t *testing.T) {
	db, _ := newTestDB(t)
	t0 := time.Date(2026, 5, 12, 4, 25, 0, 0, time.UTC)
	insertRunForReaper(t, db, "run-gone-quiet", map[string]any{
		"started_at":       t0.Add(-45 * time.Minute).Format(time.RFC3339Nano),
		"last_activity_at": t0.Add(-40 * time.Minute).Format(time.RFC3339Nano), // silent for 40min
		"timeout_seconds":  DefaultTimeoutSeconds,                              // 30min inactivity window — tripped
	})

	r := NewReaper(db, ReaperOptions{Now: func() time.Time { return t0 }})
	counts, err := r.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if counts.Inactivity != 1 {
		t.Errorf("Inactivity = %d, want 1", counts.Inactivity)
	}
	if counts.HardCeiling != 0 {
		t.Errorf("HardCeiling = %d, want 0 (45 minutes is nowhere near the 12h default)", counts.HardCeiling)
	}

	status, errMsg := readRunStatusError(t, db, "run-gone-quiet")
	if status != StatusStalled {
		t.Errorf("status = %q, want %q — a silent run is parked, not failed", status, StatusStalled)
	}
	if status == StatusFailed {
		t.Error("inactivity branch must never land on StatusFailed — that's reserved for the hard-ceiling branch")
	}
	if errMsg != ReasonInactivityReaper {
		t.Errorf("error = %q, want %q", errMsg, ReasonInactivityReaper)
	}
}

// TestReaper_OrphanSweep verifies that a row with empty child_session_id
// and created_at older than the orphan grace is reaped with the orphan
// error string. CW-20260512-0002 subtodo (c).
func TestReaper_OrphanSweep(t *testing.T) {
	db, _ := newTestDB(t)
	t0 := time.Date(2026, 5, 12, 4, 25, 0, 0, time.UTC)
	insertRunForReaper(t, db, "run-orphan", map[string]any{
		"child_session_id": "",
		"created_at":       t0.Add(-2 * time.Minute).Format(time.RFC3339Nano),
		// started_at intentionally NON-empty so the inactivity branch
		// would also match if we had a low timeout — verify the orphan
		// branch stamps the orphan-specific error string. timeout_seconds
		// is 300 so the inactivity sweep does NOT match.
		"started_at":      t0.Add(-2 * time.Minute).Format(time.RFC3339Nano),
		"timeout_seconds": 300,
	})

	r := NewReaper(db, ReaperOptions{
		OrphanGrace: 60 * time.Second,
		Now:         func() time.Time { return t0 },
	})
	counts, err := r.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if counts.Orphans != 1 {
		t.Errorf("Orphans = %d, want 1", counts.Orphans)
	}
	if counts.Inactivity != 0 {
		t.Errorf("Inactivity = %d, want 0 — orphan branch should own this row", counts.Inactivity)
	}
	if counts.HardCeiling != 0 {
		t.Errorf("HardCeiling = %d, want 0 — orphan branch should own this row", counts.HardCeiling)
	}

	status, errMsg := readRunStatusError(t, db, "run-orphan")
	if status != "failed" {
		t.Errorf("status = %q, want \"failed\"", status)
	}
	if errMsg != ReasonOrphanReaper {
		t.Errorf("error = %q, want %q", errMsg, ReasonOrphanReaper)
	}
}

// TestReaper_OrphanGraceFloorPreservesFreshRows verifies that an orphan
// row whose created_at is INSIDE the grace window is left alone — the
// child session creation may still complete.
func TestReaper_OrphanGraceFloorPreservesFreshRows(t *testing.T) {
	db, _ := newTestDB(t)
	t0 := time.Date(2026, 5, 12, 4, 25, 0, 0, time.UTC)
	insertRunForReaper(t, db, "run-fresh-orphan", map[string]any{
		"child_session_id": "",
		"created_at":       t0.Add(-10 * time.Second).Format(time.RFC3339Nano),
		"started_at":       "", // not yet started
	})

	r := NewReaper(db, ReaperOptions{
		OrphanGrace: 60 * time.Second,
		Now:         func() time.Time { return t0 },
	})
	counts, err := r.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if counts.Total() != 0 {
		t.Errorf("Total = %d, want 0 — row is inside orphan grace", counts.Total())
	}
	status, _ := readRunStatusError(t, db, "run-fresh-orphan")
	if status != StatusRunning {
		t.Errorf("status = %q, want %q", status, StatusRunning)
	}
}

// TestReaper_StartStopNoGoroutineLeak — the reaper goroutine must exit
// when ctx is cancelled (subtodo (b): "Reaper must stop on context
// cancel — no goroutine leak"). Test by cancelling and asserting Stop
// returns within a tight bound.
func TestReaper_StartStopNoGoroutineLeak(t *testing.T) {
	db, _ := newTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	r := NewReaper(db, ReaperOptions{Interval: 50 * time.Millisecond})
	r.Start(ctx)

	// Let one tick happen so the goroutine is observably in its loop.
	time.Sleep(75 * time.Millisecond)

	cancel()
	// Stop must return promptly because loop sees ctx.Done.
	stopDone := make(chan struct{})
	go func() {
		r.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(1 * time.Second):
		t.Fatal("reaper Stop did not return within 1s — goroutine leak")
	}
}

// TestReaper_StopWithoutCtxCancel verifies that Stop alone (without a
// ctx cancel) also unwinds the goroutine — exercise the stopCh path.
func TestReaper_StopWithoutCtxCancel(t *testing.T) {
	db, _ := newTestDB(t)
	r := NewReaper(db, ReaperOptions{Interval: 50 * time.Millisecond})
	r.Start(context.Background())

	stopDone := make(chan struct{})
	go func() {
		r.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(1 * time.Second):
		t.Fatal("reaper Stop did not return within 1s — stopCh path broken")
	}
}

// TestReaper_StopIsIdempotent verifies multiple Stop calls don't panic.
func TestReaper_StopIsIdempotent(t *testing.T) {
	db, _ := newTestDB(t)
	r := NewReaper(db, ReaperOptions{Interval: 50 * time.Millisecond})
	r.Start(context.Background())
	r.Stop()
	r.Stop() // must not panic on second close
}

// TestReaper_StopBeforeStart — a reaper that's never started can still
// be Stop'd without deadlocking. Covers the "wired but never reached"
// path where container init aborts before the start sequence.
//
// Regression test for PR #138 review #1: previously Stop would block
// forever on <-doneCh because neither the loop nor the nil-db branch
// had run to close it.
func TestReaper_StopBeforeStart(t *testing.T) {
	db, _ := newTestDB(t)
	r := NewReaper(db, ReaperOptions{Interval: 50 * time.Millisecond})

	stopDone := make(chan struct{})
	go func() {
		r.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Stop-before-Start hung — doneCh was never closed by any path")
	}
}

// TestReaper_StopBeforeStart_NilDB also covers the nil-db reaper variant
// that's used in test harnesses skipping the reaper. Even with no
// goroutine to spawn, Stop-before-Start must not deadlock.
func TestReaper_StopBeforeStart_NilDB(t *testing.T) {
	r := NewReaper(nil, ReaperOptions{})

	stopDone := make(chan struct{})
	go func() {
		r.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("nil-db Stop-before-Start hung")
	}
}

// TestReaper_StartAfterStopIsNoop verifies the inverse order: a Start
// call that lands after Stop must not spawn a goroutine that would try
// to close doneCh a second time (which would panic). Belt-and-suspenders
// for the late-init / late-shutdown race in container teardown.
func TestReaper_StartAfterStopIsNoop(t *testing.T) {
	db, _ := newTestDB(t)
	r := NewReaper(db, ReaperOptions{Interval: 50 * time.Millisecond})

	r.Stop()
	// Must not panic ("close of closed channel") and must not spawn a
	// goroutine that would race the already-closed doneCh.
	r.Start(context.Background())

	// A second Stop must also be safe.
	r.Stop()
}
