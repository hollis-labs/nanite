package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hollis-labs/nanite/internal/recovery/orphansweep"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestSmoke_OrphanSweep_LogsRealEventLogRow exercises the full production
// path — the real *agentRuntimeStore adapter (the RuntimeStore wired into
// runtimeagent.Dependencies by BuildAgentDependencies) against a real
// *store.Store — end to end: seed a dead-PID agent_runtime row, run
// orphansweep.SweepOrphans (the exact function the daemon's RuntimeReaper
// calls on a real restart), and confirm the row lands in the real
// event_log table with the enriched metadata this task adds.
//
// TASKS/phase-3/03-extend-event-log-to-recovery-mechanisms.md's Done means
// requires triggering a REAL occurrence, not just a fake-store unit test
// (see the orphansweep package's own TestSweepOrphans_LogsEventOnReconciliation
// for the fake-store coverage of the decision matrix) — this is that real
// trigger.
func TestSmoke_OrphanSweep_LogsRealEventLogRow(t *testing.T) {
	st := newConfigTestStore(t)
	runtimeStore := &agentRuntimeStore{store: st}

	const runtimeID = "smoke-orphan-runtime-1"
	// A PID that is virtually certain to be dead (well past any real
	// system's pid space) — mirrors orphansweep's own TestPidAlive_Dead
	// fixture.
	const deadPID = 1<<22 + 98765

	if err := runtimeStore.CreateRuntimeRow(&runtimeagent.RuntimeRow{
		ID:           runtimeID,
		AgentProfile: "smoke-agent",
		Provider:     "claude",
		Mode:         "long_lived",
		State:        "running",
		PID:          deadPID,
	}); err != nil {
		t.Fatalf("CreateRuntimeRow: %v", err)
	}

	orphaned, err := orphansweep.SweepOrphans(context.Background(), &runtimeagent.Dependencies{
		Store: runtimeStore,
	})
	if err != nil {
		t.Fatalf("SweepOrphans: %v", err)
	}
	if orphaned != 1 {
		t.Fatalf("orphaned = %d, want 1", orphaned)
	}

	events, err := st.ListEvents("recovery", 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var logged *store.EventLog
	for i := range events {
		if events[i].EventType == "orphan_sweep_reconciled" && events[i].SessionID == runtimeID {
			logged = &events[i]
			break
		}
	}
	if logged == nil {
		t.Fatalf("event_log missing orphan_sweep_reconciled row for %s; got %d recovery events", runtimeID, len(events))
	}

	var meta map[string]any
	if err := json.Unmarshal([]byte(logged.Metadata), &meta); err != nil {
		t.Fatalf("event_log metadata not JSON: %v\nblob: %s", err, logged.Metadata)
	}
	if meta["runtime_id"] != runtimeID {
		t.Errorf("metadata.runtime_id = %v, want %v", meta["runtime_id"], runtimeID)
	}
	if meta["state_before"] != "running" {
		t.Errorf("metadata.state_before = %v, want running", meta["state_before"])
	}
	if meta["reason"] != orphansweep.ReasonReconcileDeadPid {
		t.Errorf("metadata.reason = %v, want %q", meta["reason"], orphansweep.ReasonReconcileDeadPid)
	}
	if got, want := meta["pid"], float64(deadPID); got != want {
		t.Errorf("metadata.pid = %v, want %v", got, want)
	}

	// Confirm the row also actually flipped to orphaned in agent_runtime —
	// this is a real reconciliation, not just a logging side-effect.
	rows, err := st.ListRunningAgentRuntimeRows()
	if err != nil {
		t.Fatalf("ListRunningAgentRuntimeRows: %v", err)
	}
	for _, r := range rows {
		if r.ID == runtimeID {
			t.Errorf("runtime row %s still in running state; want reconciled to orphaned", runtimeID)
		}
	}
}
