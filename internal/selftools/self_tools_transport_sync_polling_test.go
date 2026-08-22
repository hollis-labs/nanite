package selftools

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// delayedCompletionRunner is a test Runner that sleeps for a configured
// duration, then completes successfully — creating a real child session
// and a real assistant message (mirroring what internal/service's real
// ChatRunner/BootRunner do via persistChildSessionID, so
// recoverSyncSummary has genuine content to find rather than hitting the
// legitimately-empty ErrorKindEmptyReply path).
type delayedCompletionRunner struct {
	db    *sql.DB
	delay time.Duration
}

func (r *delayedCompletionRunner) Run(ctx context.Context, run *subagent.Run) (*subagent.Result, error) {
	select {
	case <-time.After(r.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	childID := "child-" + run.ID
	if _, err := r.db.Exec(
		`INSERT INTO sessions (id, short_code, status, message_count, metadata, last_activity, created_at, updated_at)
		 VALUES (?, ?, 'active', 0, '{}', datetime('now'), datetime('now'), datetime('now'))`,
		childID, childID,
	); err != nil {
		return nil, err
	}
	if _, err := r.db.Exec(
		`INSERT INTO messages (id, session_id, role, content, metadata, created_at)
		 VALUES (?, ?, 'assistant', ?, '{}', datetime('now'))`,
		"msg-"+run.ID, childID, "work completed after delay",
	); err != nil {
		return nil, err
	}
	if _, err := r.db.Exec(
		`UPDATE subagent_runs SET child_session_id = ? WHERE id = ?`, childID, run.ID,
	); err != nil {
		return nil, err
	}

	return &subagent.Result{Summary: "work completed after delay"}, nil
}

// waitForTerminal polls svc.Status(id) until it reaches a terminal state
// or timeout elapses. Used to drain background (async) runs before a
// test returns, so their goroutine doesn't outlive the test.
func waitForTerminal(t *testing.T, svc *subagent.Service, id string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		run, err := svc.Status(context.Background(), id)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if subagent.IsTerminalStatus(run.Status) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s did not reach terminal state in time, last status=%s", id, run.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newSyncPollingTestService(t *testing.T, delay time.Duration) (*subagent.Service, *SelfToolsTransport) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })

	runner := &delayedCompletionRunner{db: s.DB, delay: delay}
	svc := subagent.NewService(s.DB, runner, nil, nil, nil)
	return svc, &SelfToolsTransport{Store: s, Subagent: svc}
}

// TestSyncSubagentEnvelope_ModeSyncAlreadyTerminal pins the realistic
// production shape: Spawn's ModeSync branch (internal/subagent/service.go)
// blocks internally on executeWithSlot and does not return the run_id
// until the run is terminal — so by the time syncSubagentEnvelope is
// entered, the run is already done. This verifies the ordinary happy
// path still returns the real result with a fresh (non-expired) ctx.
func TestSyncSubagentEnvelope_ModeSyncAlreadyTerminal(t *testing.T) {
	svc, st := newSyncPollingTestService(t, 50*time.Millisecond)

	id, err := svc.Spawn(context.Background(), subagent.SpawnRequest{
		ParentSessionID: "sess-happy-path",
		ParentAgentID:   "primary",
		Role:            "worker",
		Prompt:          "quick work",
		Mode:            subagent.ModeSync,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	env := st.syncSubagentEnvelope(context.Background(), id)
	if !env.Success {
		t.Fatalf("expected Success=true, got envelope: %+v (error: %+v)", env, env.Error)
	}
	if env.Result == nil || env.Result.Summary != "work completed after delay" {
		t.Errorf("expected recovered summary, got: %+v", env.Result)
	}
}

// TestSyncSubagentEnvelope_ExpiredCtx_RecoversRealResult is the direct
// regression pin for CW-20260816-0002. It reproduces the live bug: the
// caller's ctx expires (in production, because it was already used for
// Spawn's own long internal block) but the run has genuinely completed
// by the time syncSubagentEnvelope's Status(ctx, ...) call fires. Before
// the fix, Status(ctx, ...) failed immediately on the dead ctx and the
// completed result was discarded as ErrorKindInternal. After the fix, a
// fresh-context retry recovers the real, already-available result
// instead of reporting failure.
func TestSyncSubagentEnvelope_ExpiredCtx_RecoversRealResult(t *testing.T) {
	svc, st := newSyncPollingTestService(t, 50*time.Millisecond)

	// Spawn async so we control exactly when syncSubagentEnvelope is
	// called relative to completion, independent of Spawn's own blocking
	// behavior under ModeSync.
	id, err := svc.Spawn(context.Background(), subagent.SpawnRequest{
		ParentSessionID: "sess-expired-ctx-recovers",
		ParentAgentID:   "primary",
		Role:            "worker",
		Prompt:          "quick work",
		Mode:            subagent.ModeAsync,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Wait past the runner's delay so the run has genuinely completed.
	waitForTerminal(t, svc, id, 2*time.Second)

	// Now call syncSubagentEnvelope with an ALREADY-expired ctx — the
	// exact shape of the live bug (a dead ctx on a completed run).
	expiredCtx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(5 * time.Millisecond) // guarantee the deadline has passed

	env := st.syncSubagentEnvelope(expiredCtx, id)

	if !env.Success {
		t.Fatalf("expected the fresh-context retry to recover the real completed result, got failure envelope: %+v", env.Error)
	}
	if env.Result == nil || env.Result.Summary != "work completed after delay" {
		t.Errorf("expected recovered summary despite expired ctx, got: %+v", env.Result)
	}
}

// TestSyncSubagentEnvelope_ExpiredCtx_GenuinelyStillRunning_ReturnsTimeout
// verifies the other side of the fresh-context retry: when the run is
// genuinely NOT terminal yet (not just blocked behind a dead ctx), an
// expired ctx correctly produces ErrorKindTimeout with a hint to retry
// via subagent_status — it must not fabricate a success.
func TestSyncSubagentEnvelope_ExpiredCtx_GenuinelyStillRunning_ReturnsTimeout(t *testing.T) {
	const runnerDelay = 300 * time.Millisecond
	svc, st := newSyncPollingTestService(t, runnerDelay)

	id, err := svc.Spawn(context.Background(), subagent.SpawnRequest{
		ParentSessionID: "sess-genuinely-running",
		ParentAgentID:   "primary",
		Role:            "worker",
		Prompt:          "slow work",
		Mode:            subagent.ModeAsync,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// Drain the background run before returning so its goroutine doesn't
	// outlive the test (goleak flags any still-running spawn goroutine
	// at suite teardown).
	defer waitForTerminal(t, svc, id, runnerDelay+2*time.Second)

	// The async runner's delay has not elapsed — the run is genuinely
	// still running. Call syncSubagentEnvelope with an already-expired ctx.
	expiredCtx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(5 * time.Millisecond)

	env := st.syncSubagentEnvelope(expiredCtx, id)

	if env.Success {
		t.Fatalf("expected Success=false for a genuinely still-running run, got: %+v", env)
	}
	if env.Error == nil {
		t.Fatal("expected Error block")
	}
	if env.Error.Kind != subagent.ErrorKindTimeout {
		t.Errorf("Error.Kind = %q, want %q", env.Error.Kind, subagent.ErrorKindTimeout)
	}
	if env.Error.Context == nil || env.Error.Context["run_id"] != id {
		t.Errorf("expected Error.Context[run_id] = %q on timeout envelope, got %+v", id, env.Error.Context)
	}
	hint, _ := env.Error.Context["hint"].(string)
	if !strings.Contains(hint, "subagent_status") {
		t.Errorf("Error.Context[hint] = %q, want hint mentioning subagent_status", hint)
	}
}

// TestSyncSubagentEnvelope_StatusError_ReturnsInternal verifies that a
// genuine DB error (not a context timeout) is still reported as
// ErrorKindInternal.
func TestSyncSubagentEnvelope_StatusError_ReturnsInternal(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	runner := &delayedCompletionRunner{db: s.DB, delay: 100 * time.Millisecond}
	svc := subagent.NewService(s.DB, runner, nil, nil, nil)
	st := &SelfToolsTransport{Store: s, Subagent: svc}

	id, err := svc.Spawn(context.Background(), subagent.SpawnRequest{
		ParentSessionID: "sess-db-error-test",
		ParentAgentID:   "primary",
		Role:            "worker",
		Prompt:          "work",
		Mode:            subagent.ModeSync,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	env := st.syncSubagentEnvelope(context.Background(), id)

	if env.Success {
		t.Fatalf("expected Success=false after DB error, got envelope: %+v", env)
	}
	if env.Error == nil {
		t.Fatal("expected Error block on DB-error envelope")
	}
	if env.Error.Kind != subagent.ErrorKindInternal {
		t.Errorf("Error.Kind = %q, want %q (internal for DB error)", env.Error.Kind, subagent.ErrorKindInternal)
	}
}

// TestSyncSubagentEnvelope_RunNotFound_ReturnsInternal verifies that a
// non-existent run ID produces a clear "not found" message rather than
// the raw sql.ErrNoRows text.
func TestSyncSubagentEnvelope_RunNotFound_ReturnsInternal(t *testing.T) {
	svc, st := newSyncPollingTestService(t, 100*time.Millisecond)
	_ = svc

	env := st.syncSubagentEnvelope(context.Background(), "nonexistent-run-id")

	if env.Success {
		t.Fatalf("expected Success=false for non-existent run, got envelope: %+v", env)
	}
	if env.Error == nil {
		t.Fatal("expected Error block on non-existent run envelope")
	}
	if env.Error.Kind != subagent.ErrorKindInternal {
		t.Errorf("Error.Kind = %q, want %q (internal for not found)", env.Error.Kind, subagent.ErrorKindInternal)
	}
	if !strings.Contains(env.Error.Message, "not found") {
		t.Errorf("Error.Message = %q, want message mentioning 'not found'", env.Error.Message)
	}
}
