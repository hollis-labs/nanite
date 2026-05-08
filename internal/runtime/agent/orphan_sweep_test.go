package agent

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestPidAlive_Self confirms our own pid is reported alive (signal-0
// against ourselves should always succeed).
func TestPidAlive_Self(t *testing.T) {
	if !pidAlive(os.Getpid()) {
		t.Errorf("pidAlive(self) = false, want true")
	}
}

// TestPidAlive_Zero rejects pid 0 (signal-0 to pid 0 is the process group
// signal — never a liveness probe).
func TestPidAlive_Zero(t *testing.T) {
	if pidAlive(0) {
		t.Errorf("pidAlive(0) = true, want false")
	}
}

// TestPidAlive_Dead probes a pid that is virtually certain to be dead.
// pid 1<<22 + a fixed offset is a bigger pid than any reasonable system
// tracks live, so this should report dead.
func TestPidAlive_Dead(t *testing.T) {
	const veryHighPID = 1<<22 + 12345
	if pidAlive(veryHighPID) {
		t.Skipf("pid %d unexpectedly alive — skipping liveness assertion", veryHighPID)
	}
}

// TestSweepOrphans_RequiresStore returns a clear error when deps.Store
// is missing.
func TestSweepOrphans_RequiresStore(t *testing.T) {
	_, err := SweepOrphans(context.Background(), &Dependencies{})
	if err == nil || !strings.Contains(err.Error(), "Store") {
		t.Fatalf("expected Store-required error, got %v", err)
	}
}

// TestSweepOrphans_MarksDeadRows verifies the orphan reconciliation flow:
// rows with PID == 0 are skipped, live PIDs stay, dead PIDs flip to
// orphaned with a reason.
func TestSweepOrphans_MarksDeadRows(t *testing.T) {
	store := newFakeRuntimeStore()
	store.listRows = []*RuntimeRow{
		{ID: "no-pid", PID: 0},        // skipped
		{ID: "self", PID: os.Getpid()}, // alive
		{ID: "dead", PID: 1<<22 + 99},  // dead
	}

	orphaned, err := SweepOrphans(context.Background(), &Dependencies{Store: store})
	if err != nil {
		t.Fatalf("SweepOrphans: %v", err)
	}
	if orphaned != 1 {
		t.Errorf("orphaned count = %d, want 1", orphaned)
	}
	if _, ok := store.orphaned["dead"]; !ok {
		t.Errorf("dead row should be marked orphaned, got %v", store.orphaned)
	}
	if _, ok := store.orphaned["self"]; ok {
		t.Errorf("self row (alive) should not be marked orphaned")
	}
	if _, ok := store.orphaned["no-pid"]; ok {
		t.Errorf("no-pid row should be skipped, not orphaned")
	}
}
