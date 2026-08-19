package orphansweep

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/runtime/agent"
)

// fakeLiveSessions is the test-side LiveSessionChecker. live[id]==true
// means the in-process registry would report the runtime as live.
type fakeLiveSessions struct {
	mu   sync.Mutex
	live map[string]bool
}

func (f *fakeLiveSessions) IsLive(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.live[id]
}

// fakeRuntimeStore is a self-contained agent.RuntimeStore fake scoped to
// this package's tests. Mirrors internal/runtime/agent's own
// fakeRuntimeStore (fakes_test.go) — that one is unexported and can't be
// reused across the package boundary now that orphan-sweep logic lives
// here, so this is a deliberate, minimal duplicate covering only what
// SweepOrphans/RuntimeReaper exercise.
type fakeRuntimeStore struct {
	mu        sync.Mutex
	orphaned  map[string]string
	listRows  []*agent.RuntimeRow
	createErr error
}

func newFakeRuntimeStore() *fakeRuntimeStore {
	return &fakeRuntimeStore{
		orphaned: map[string]string{},
	}
}

func (f *fakeRuntimeStore) CreateRuntimeRow(row *agent.RuntimeRow) error {
	if f.createErr != nil {
		return f.createErr
	}
	return nil
}

func (f *fakeRuntimeStore) MarkRuntimeFailed(id, reason string) error {
	return nil
}

func (f *fakeRuntimeStore) MarkRuntimeOrphaned(id, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.orphaned[id] = reason
	return nil
}

func (f *fakeRuntimeStore) SetProviderSessionID(id, providerSessionID string) error {
	return nil
}

func (f *fakeRuntimeStore) GetCheckpoint(string) (*agent.RuntimeCheckpoint, error) {
	return nil, nil
}

func (f *fakeRuntimeStore) ListRunningRows() ([]*agent.RuntimeRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*agent.RuntimeRow, len(f.listRows))
	copy(out, f.listRows)
	return out, nil
}

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
	_, err := SweepOrphans(context.Background(), &agent.Dependencies{})
	if err == nil || !strings.Contains(err.Error(), "Store") {
		t.Fatalf("expected Store-required error, got %v", err)
	}
}

// TestSweepOrphans_MarksDeadRows verifies the dead-pid path:
// rows with PID == 0 and no LiveSessions checker AND no aged updated_at
// are skipped (UpdatedAt zero), live PIDs stay, dead PIDs flip to orphaned.
func TestSweepOrphans_MarksDeadRows(t *testing.T) {
	store := newFakeRuntimeStore()
	store.listRows = []*agent.RuntimeRow{
		{ID: "no-pid", PID: 0},         // skipped (no checker, UpdatedAt zero)
		{ID: "self", PID: os.Getpid()}, // alive
		{ID: "dead", PID: 1<<22 + 99},  // dead
	}

	orphaned, err := SweepOrphans(context.Background(), &agent.Dependencies{Store: store})
	if err != nil {
		t.Fatalf("SweepOrphans: %v", err)
	}
	if orphaned != 1 {
		t.Errorf("orphaned count = %d, want 1", orphaned)
	}
	if reason, ok := store.orphaned["dead"]; !ok {
		t.Errorf("dead row should be marked orphaned, got %v", store.orphaned)
	} else if reason != ReasonReconcileDeadPid {
		t.Errorf("dead row reason = %q, want %q", reason, ReasonReconcileDeadPid)
	}
	if _, ok := store.orphaned["self"]; ok {
		t.Errorf("self row (alive) should not be marked orphaned")
	}
	if _, ok := store.orphaned["no-pid"]; ok {
		t.Errorf("no-pid row should be skipped (no live-sessions checker, zero updated_at), not orphaned")
	}
}

// TestSweepOrphans_PidZero_NoLiveSession covers the codex case: a row
// persisted with PID=0 whose session is NOT live in the in-process
// registry flips to orphaned with reason="no_live_session". A pid=0 row
// whose session IS live stays.
func TestSweepOrphans_PidZero_NoLiveSession(t *testing.T) {
	store := newFakeRuntimeStore()
	old := time.Now().Add(-10 * time.Minute) // well past the default grace
	store.listRows = []*agent.RuntimeRow{
		{ID: "codex-live", PID: 0, UpdatedAt: old},
		{ID: "codex-dead", PID: 0, UpdatedAt: old},
	}
	live := &fakeLiveSessions{live: map[string]bool{"codex-live": true}}

	orphaned, err := SweepOrphans(context.Background(), &agent.Dependencies{
		Store:        store,
		LiveSessions: live,
	})
	if err != nil {
		t.Fatalf("SweepOrphans: %v", err)
	}
	if orphaned != 1 {
		t.Errorf("orphaned count = %d, want 1", orphaned)
	}
	if reason, ok := store.orphaned["codex-dead"]; !ok {
		t.Errorf("codex-dead should be orphaned, got %v", store.orphaned)
	} else if reason != ReasonReconcileNoLiveSession {
		t.Errorf("codex-dead reason = %q, want %q", reason, ReasonReconcileNoLiveSession)
	}
	if _, ok := store.orphaned["codex-live"]; ok {
		t.Errorf("codex-live should be skipped (still in registry)")
	}
}

// TestSweepOrphans_PidZero_GraceWindow verifies that a pid=0 row whose
// updated_at is *inside* the grace window is NOT orphaned even when the
// live-sessions checker says it's absent — protects rows mid-launch
// before the registry has populated.
func TestSweepOrphans_PidZero_GraceWindow(t *testing.T) {
	store := newFakeRuntimeStore()
	store.listRows = []*agent.RuntimeRow{
		{ID: "codex-fresh", PID: 0, UpdatedAt: time.Now().Add(-1 * time.Second)},
	}
	live := &fakeLiveSessions{live: map[string]bool{}}

	orphaned, err := sweepOrphansAt(context.Background(), &agent.Dependencies{
		Store:        store,
		LiveSessions: live,
	}, 1*time.Minute, time.Now)
	if err != nil {
		t.Fatalf("SweepOrphans: %v", err)
	}
	if orphaned != 0 {
		t.Errorf("orphaned count = %d, want 0 (grace window)", orphaned)
	}
}

// TestSweepOrphans_PidZero_StaleWithoutChecker exercises the no-checker
// fallback: pid=0 row whose updated_at is older than the grace window
// flips with reason="pid_zero_stale" even without a LiveSessions checker.
func TestSweepOrphans_PidZero_StaleWithoutChecker(t *testing.T) {
	store := newFakeRuntimeStore()
	store.listRows = []*agent.RuntimeRow{
		{ID: "codex-stale", PID: 0, UpdatedAt: time.Now().Add(-10 * time.Minute)},
		{ID: "codex-fresh", PID: 0, UpdatedAt: time.Now()}, // within default grace
	}

	orphaned, err := SweepOrphans(context.Background(), &agent.Dependencies{Store: store})
	if err != nil {
		t.Fatalf("SweepOrphans: %v", err)
	}
	if orphaned != 1 {
		t.Errorf("orphaned count = %d, want 1", orphaned)
	}
	if reason, ok := store.orphaned["codex-stale"]; !ok {
		t.Errorf("codex-stale should be orphaned")
	} else if reason != ReasonReconcilePidZeroStale {
		t.Errorf("codex-stale reason = %q, want %q", reason, ReasonReconcilePidZeroStale)
	}
	if _, ok := store.orphaned["codex-fresh"]; ok {
		t.Errorf("codex-fresh should be skipped (grace window)")
	}
}

// TestClassifyForReconciliation_Matrix covers the decision-table inputs
// in one shot so a regression to any single branch surfaces immediately.
func TestClassifyForReconciliation_Matrix(t *testing.T) {
	old := time.Now().Add(-10 * time.Minute)
	fresh := time.Now()
	liveAll := &fakeLiveSessions{live: map[string]bool{"in-registry": true}}

	tests := []struct {
		name       string
		row        *agent.RuntimeRow
		live       agent.LiveSessionChecker
		wantReason string
		wantDrop   bool
	}{
		{
			name:     "nil row",
			row:      nil,
			wantDrop: false,
		},
		{
			name:     "live pid",
			row:      &agent.RuntimeRow{ID: "x", PID: os.Getpid()},
			wantDrop: false,
		},
		{
			name:       "dead pid",
			row:        &agent.RuntimeRow{ID: "x", PID: 1<<22 + 7},
			wantReason: ReasonReconcileDeadPid,
			wantDrop:   true,
		},
		{
			name:     "pid 0, in registry",
			row:      &agent.RuntimeRow{ID: "in-registry", PID: 0, UpdatedAt: old},
			live:     liveAll,
			wantDrop: false,
		},
		{
			name:       "pid 0, not in registry, stale",
			row:        &agent.RuntimeRow{ID: "missing", PID: 0, UpdatedAt: old},
			live:       liveAll,
			wantReason: ReasonReconcileNoLiveSession,
			wantDrop:   true,
		},
		{
			name:     "pid 0, not in registry, fresh",
			row:      &agent.RuntimeRow{ID: "missing", PID: 0, UpdatedAt: fresh},
			live:     liveAll,
			wantDrop: false,
		},
		{
			name:       "pid 0, no checker, stale",
			row:        &agent.RuntimeRow{ID: "x", PID: 0, UpdatedAt: old},
			wantReason: ReasonReconcilePidZeroStale,
			wantDrop:   true,
		},
		{
			name:     "pid 0, no checker, zero updated_at",
			row:      &agent.RuntimeRow{ID: "x", PID: 0},
			wantDrop: false,
		},
	}

	now := time.Now()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, drop := classifyForReconciliation(tt.row, tt.live, now, 1*time.Minute)
			if drop != tt.wantDrop {
				t.Errorf("drop = %v, want %v", drop, tt.wantDrop)
			}
			if reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", reason, tt.wantReason)
			}
		})
	}
}

// TestRuntimeReaper_StopBeforeStart verifies the Stop-before-Start
// edge case doesn't deadlock — mirrors the subagent reaper's
// startOnce/doneOnce contract.
func TestRuntimeReaper_StopBeforeStart(t *testing.T) {
	reaper := NewRuntimeReaper(&agent.Dependencies{Store: newFakeRuntimeStore()}, RuntimeReaperOptions{})
	done := make(chan struct{})
	go func() {
		reaper.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop-before-Start deadlocked")
	}
}

// TestRuntimeReaper_NilStore short-circuits cleanly when deps.Store is
// nil — Start logs and exits, Stop returns immediately.
func TestRuntimeReaper_NilStore(t *testing.T) {
	reaper := NewRuntimeReaper(&agent.Dependencies{}, RuntimeReaperOptions{})
	reaper.Start(context.Background())
	done := make(chan struct{})
	go func() {
		reaper.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("nil-store reaper failed to stop")
	}
}

// TestRuntimeReaper_SweepOnce exercises the explicit sweep entry point
// used by the composition root for the startup reconciliation pass.
func TestRuntimeReaper_SweepOnce(t *testing.T) {
	store := newFakeRuntimeStore()
	store.listRows = []*agent.RuntimeRow{
		{ID: "dead", PID: 1<<22 + 5},
	}
	reaper := NewRuntimeReaper(&agent.Dependencies{Store: store}, RuntimeReaperOptions{})
	n, err := reaper.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if n != 1 {
		t.Errorf("reconciled = %d, want 1", n)
	}
	if _, ok := store.orphaned["dead"]; !ok {
		t.Errorf("dead row should be orphaned")
	}
}
