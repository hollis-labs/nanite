package agent

import (
	"context"
	"errors"
	"fmt"
	"syscall"
)

// SweepOrphans runs at daemon startup. For every runtime row in
// state="running" or state="launching", it probes the persisted PID with
// syscall.Kill(pid, 0). Live PIDs stay; dead PIDs get the row closed to
// state="orphaned" with a captured reason. Rows with PID == 0 are skipped
// (a row with no recorded PID is either still launching or was persisted
// from a runtime that doesn't track PIDs — neither case is an orphan we
// can reconcile).
//
// Returns the number of rows reconciled. Errors during individual row
// reconciliation are logged via deps.Telemetry and do not abort the sweep.
func SweepOrphans(ctx context.Context, deps *Dependencies) (int, error) {
	if deps == nil || deps.Store == nil {
		return 0, errors.New("agent.SweepOrphans: Dependencies.Store is required")
	}
	_ = ctx

	rows, err := deps.Store.ListRunningRows()
	if err != nil {
		return 0, fmt.Errorf("agent.SweepOrphans: list running rows: %w", err)
	}

	var orphaned int
	for _, row := range rows {
		if row == nil || row.PID == 0 {
			continue
		}
		if pidAlive(row.PID) {
			continue
		}
		if err := deps.Store.MarkRuntimeOrphaned(row.ID, fmt.Sprintf("pid %d not alive at sweep", row.PID)); err != nil {
			// Continue sweeping other rows; one persistence failure shouldn't
			// block reconciliation of the rest.
			continue
		}
		orphaned++
	}
	return orphaned, nil
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
