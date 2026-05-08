package agent

import "context"

// SweepOrphans runs at daemon startup. For every store row in
// state="running" or state="launching", it probes the persisted PID with
// syscall.Kill(pid, 0). Live PIDs stay; dead PIDs get the row closed
// to state="orphaned" with a captured reason.
//
// Phase 3 implements the actual sweep against the store + a darwin/linux
// liveness probe. This entry point exists so the daemon-bootstrap caller
// can be wired in early.
func SweepOrphans(ctx context.Context, deps *Dependencies) error {
	_ = ctx
	_ = deps
	// Phase 2 stub: no-op. Phase 3 implements.
	return nil
}
