package subagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/messaging"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"

	_ "modernc.org/sqlite"
)

// newTestDB opens a fresh file-backed SQLite DB with all nanite
// migrations applied. subagent uses only the subagent_runs table but
// the messaging reply path touches session_events + agent_messages, so
// we run the full migration set by going through the existing
// *store.Store constructor.
func newTestDB(t *testing.T) (*sql.DB, *store.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := storetest.New(t, context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()); _ = os.Remove(dbPath) })
	return s.DB, s
}

// stubPoster captures the last SendMessage input without actually
// going through messaging. Lets subagent tests assert reply shape
// without spinning up a full messaging.Service.
type stubPoster struct {
	mu   sync.Mutex
	last *messaging.SendInput
}

func (p *stubPoster) SendMessage(_ context.Context, in messaging.SendInput) (*messaging.Message, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	copy := in
	p.last = &copy
	return &messaging.Message{ID: "stub-" + in.FromAgentID, Body: in.Body}, nil
}

func (p *stubPoster) captured() *messaging.SendInput {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last
}

// stubEmitter records emit calls for assertions.
type stubEmitter struct {
	mu    sync.Mutex
	calls []emitCall
}
type emitCall struct {
	sessionID string
	typ       string
	payload   []byte
}

func (e *stubEmitter) Emit(_ context.Context, s, t string, p []byte) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, emitCall{s, t, append([]byte(nil), p...)})
	return "env-" + strconv.Itoa(len(e.calls)), nil
}
func (e *stubEmitter) Count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.calls)
}
func (e *stubEmitter) Last() emitCall {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls[len(e.calls)-1]
}

// stubSettings returns fixed UserSettings.
type stubSettings struct{ us store.UserSettings }

func (s stubSettings) GetUserSettings(ctx context.Context) (*store.UserSettings, error) {
	return &s.us, nil
}

// notCalledRunner fails the test if Run is invoked.
type notCalledRunner struct{ t *testing.T }

func (r *notCalledRunner) Run(_ context.Context, _ *Run) (*Result, error) {
	r.t.Fatal("runner should not be invoked during gated Spawn")
	return nil, nil
}

func TestSpawn_SyncEchoRunner_RoundTrip(t *testing.T) {
	db, _ := newTestDB(t)
	poster := &stubPoster{}
	svc := NewService(db, EchoRunner{}, poster, nil, stubSettings{})

	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "file-backend",
		Role:            "file-summarizer",
		Prompt:          "summarize the repo",
		Mode:            ModeSync,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if id == "" {
		t.Fatal("empty run id")
	}

	// Sync mode blocks until the runner returns, so status should
	// already be completed.
	run, err := svc.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if run.Status != StatusCompleted {
		t.Errorf("Status = %q, want %q", run.Status, StatusCompleted)
	}
	if run.Error != "" {
		t.Errorf("Error = %q, want empty", run.Error)
	}
	if run.CompletedAt == "" {
		t.Error("CompletedAt not set")
	}
	if run.ResultJSON == "" || run.ResultJSON == "{}" {
		t.Errorf("ResultJSON unexpectedly empty: %q", run.ResultJSON)
	}

	// Reply message delivered to parent on completion.
	in := poster.captured()
	if in == nil {
		t.Fatal("no message posted back to parent")
	}
	if in.ToSessionID != "sess-1" || in.ToAgentID != "file-backend" {
		t.Errorf("reply addressing: to=(%q,%q)", in.ToSessionID, in.ToAgentID)
	}
	// CW-20260512-0019: completion replies now carry Kind=subagent_result
	// (not the generic KindReply) so downstream turn-start injection and
	// the harness-reaction layer can filter for them specifically.
	if in.Kind != messaging.KindSubagentResult {
		t.Errorf("reply Kind = %q, want %q", in.Kind, messaging.KindSubagentResult)
	}
	if in.Channel != messaging.ChannelChat {
		t.Errorf("sync mode reply Channel = %q, want %q", in.Channel, messaging.ChannelChat)
	}
	if in.Body == "" {
		t.Error("reply Body empty")
	}
}

func TestSpawn_AsyncMode_RepliesViaInbox(t *testing.T) {
	db, _ := newTestDB(t)
	poster := &stubPoster{}
	svc := NewService(db, EchoRunner{}, poster, nil, stubSettings{})

	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "file-backend",
		Role:            "file-summarizer",
		Prompt:          "summarize the repo",
		Mode:            ModeAsync,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Async runs in a goroutine. Wait up to 1s for it to finish.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if poster.captured() != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	in := poster.captured()
	if in == nil {
		t.Fatal("async runner did not post reply within 1s")
	}
	if in.Channel != messaging.ChannelInbox {
		t.Errorf("async reply Channel = %q, want %q", in.Channel, messaging.ChannelInbox)
	}

	// Run should be completed.
	run, err := svc.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if run.Status != StatusCompleted {
		t.Errorf("Status = %q, want %q", run.Status, StatusCompleted)
	}
}

func TestSpawn_RejectsMissingFields(t *testing.T) {
	db, _ := newTestDB(t)
	svc := NewService(db, EchoRunner{}, nil, nil, stubSettings{})

	cases := []struct {
		name string
		req  SpawnRequest
	}{
		{"no parent session", SpawnRequest{Role: "r", Prompt: "p"}},
		{"no role", SpawnRequest{ParentSessionID: "s", Prompt: "p"}},
		{"no prompt", SpawnRequest{ParentSessionID: "s", Role: "r"}},
		{"bad mode", SpawnRequest{ParentSessionID: "s", Role: "r", Prompt: "p", Mode: "asdf"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.Spawn(context.Background(), tc.req); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestSpawn_NoRunnerConfigured(t *testing.T) {
	db, _ := newTestDB(t)
	svc := NewService(db, nil, nil, nil, stubSettings{})

	_, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "s", Role: "r", Prompt: "p",
	})
	if err == nil {
		t.Fatal("expected error when no runner configured")
	}
}

// failRunner returns an error — lets us verify that failed runs land
// in StatusFailed and that the failure surfaces in the reply body.
type failRunner struct{}

func (failRunner) Run(_ context.Context, _ *Run) (*Result, error) {
	return nil, errors.New("runner blew up")
}

func TestSpawn_FailedRunner_SetsStatusFailed(t *testing.T) {
	db, _ := newTestDB(t)
	poster := &stubPoster{}
	svc := NewService(db, failRunner{}, poster, nil, stubSettings{})

	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "file-backend",
		Role:            "file-summarizer",
		Prompt:          "will fail",
		Mode:            ModeSync,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	run, err := svc.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if run.Status != StatusFailed {
		t.Errorf("Status = %q, want %q", run.Status, StatusFailed)
	}
	if run.Error == "" {
		t.Error("Error field unexpectedly empty")
	}
}

// partialFailRunner mirrors the CW-20260519-0071 productive-but-cut
// case: the runner returns a non-nil *Result (the partial work it
// captured) ALONGSIDE a non-nil error. ChatRunner.Run does exactly
// this on the deadline/stream-error path.
type partialFailRunner struct{}

func (partialFailRunner) Run(_ context.Context, _ *Run) (*Result, error) {
	return &Result{
			Summary:    "wrote 2 of 5 files before deadline",
			ResultJSON: `{"partial":true,"summary":"wrote 2 of 5 files before deadline","tools":{"calls":2,"results_success":2,"results_error":0}}`,
		},
		errors.New("http chat stream error / cause:http_stream")
}

// stalledRunner mirrors the CW-20260517-0036 provider-stream stall: the
// runner returns a partial Result alongside an error that wraps
// subagent.ErrStalled — exactly what ChatRunner.Run does when
// drainCapture sees a `cause:"stalled"` error event.
type stalledRunner struct{}

func (stalledRunner) Run(_ context.Context, _ *Run) (*Result, error) {
	return &Result{
			Summary:    "produced nothing before the stream went silent",
			ResultJSON: `{"partial":true,"summary":"","tools":{"calls":0,"results_success":0,"results_error":0}}`,
		},
		errors.Join(errors.New("provider stream stalled — no response"), ErrStalled)
}

// TestSpawn_StalledRunner_SetsStatusStalled is the CW-20260519-0074
// acceptance test for the stall outcome. A run whose provider stream
// went silent (the runner error wraps ErrStalled) must be recorded
// `stalled`, NOT `failed` — so it is diagnosable from the run status
// alone and is not mistaken for a genuine crash.
func TestSpawn_StalledRunner_SetsStatusStalled(t *testing.T) {
	db, _ := newTestDB(t)
	svc := NewService(db, stalledRunner{}, &stubPoster{}, nil, stubSettings{})

	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "file-backend",
		Role:            "file-summarizer",
		Prompt:          "will stall",
		Mode:            ModeSync,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	run, err := svc.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if run.Status != StatusStalled {
		t.Errorf("Status = %q, want %q", run.Status, StatusStalled)
	}
	if run.Error == "" {
		t.Error("Error field unexpectedly empty — the error is still recorded on a stalled run")
	}
}

// TestSpawn_FailedRunner_PersistsPartialResultJSON is the acceptance
// test for CW-20260519-0071 (audit §P2). A subagent guillotined
// mid-productive-work returns a partial Result alongside its error;
// execute must stamp StatusFailed (the error is intact) AND persist
// result_json from that partial trace instead of leaving it at the
// insert-time default. Orphaned side-effects now have a record.
//
// CW-20260519-0074 note: partialFailRunner returns a generic stream
// error (no ErrStalled sentinel) and the run context is not expired in
// this test, so classifyRunOutcome correctly keeps the outcome `failed`
// — a generic stream crash IS a genuine failure.
func TestSpawn_FailedRunner_PersistsPartialResultJSON(t *testing.T) {
	db, _ := newTestDB(t)
	poster := &stubPoster{}
	svc := NewService(db, partialFailRunner{}, poster, nil, stubSettings{})

	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "file-backend",
		Role:            "file-summarizer",
		Prompt:          "implement the feature",
		Mode:            ModeSync,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	run, err := svc.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	// Error path is intact — status stays failed.
	if run.Status != StatusFailed {
		t.Errorf("Status = %q, want %q", run.Status, StatusFailed)
	}
	if run.Error == "" {
		t.Error("Error field unexpectedly empty")
	}
	// Partial-result capture: result_json must carry the partial trace,
	// not the insert-time default.
	if run.ResultJSON == "" || run.ResultJSON == "{}" {
		t.Errorf("ResultJSON = %q; want partial trace persisted on failure branch", run.ResultJSON)
	}
	if !strings.Contains(run.ResultJSON, `"partial":true`) {
		t.Errorf("ResultJSON = %q; want partial-capture marker", run.ResultJSON)
	}
}

func TestCancel_TerminalIsNoop(t *testing.T) {
	db, _ := newTestDB(t)
	svc := NewService(db, EchoRunner{}, &stubPoster{}, nil, stubSettings{})

	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "file-backend",
		Role:            "file-summarizer",
		Prompt:          "ok",
		Mode:            ModeSync,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// Sync run is already completed; Cancel must not corrupt it.
	if err := svc.Cancel(context.Background(), id); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	run, _ := svc.Status(context.Background(), id)
	if run.Status != StatusCompleted {
		t.Errorf("Status = %q, want %q (cancel must not demote terminal state)", run.Status, StatusCompleted)
	}
}

// slowRunner blocks on its ctx until cancelled, then returns ctx.Err().
// Lets us verify per-run Cancel actually cancels the in-flight runner:
// done closes after <-ctx.Done() returns, so the test can assert the
// runner goroutine actually exits (not just that the DB row flipped).
type slowRunner struct {
	started chan struct{} // closed when Run begins
	done    chan struct{} // closed when Run returns
}

func (r *slowRunner) Run(ctx context.Context, _ *Run) (*Result, error) {
	close(r.started)
	<-ctx.Done()
	close(r.done)
	return nil, ctx.Err()
}

func TestCancel_PerRunContextCancellation(t *testing.T) {
	db, _ := newTestDB(t)
	runner := &slowRunner{
		started: make(chan struct{}),
		done:    make(chan struct{}),
	}
	svc := NewService(db, runner, &stubPoster{}, nil, stubSettings{})

	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "file-backend",
		Role:            "file-summarizer",
		Prompt:          "slow task",
		Mode:            ModeAsync,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Wait for runner to actually be running before cancelling.
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start within 1s")
	}

	if err := svc.Cancel(context.Background(), id); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	// The core assertion: Cancel must propagate into the runner's ctx so
	// the goroutine actually exits. Without per-run cancel plumbing this
	// would block until execute's generous wall-clock backstop
	// (DefaultTimeoutSeconds, CW-20260519-0073) fired.
	select {
	case <-runner.done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("runner did not exit within 500ms of Cancel")
	}

	// Secondary: the DB row reflects the cancellation.
	run, _ := svc.Status(context.Background(), id)
	if run == nil || run.Status != StatusCancelled {
		t.Fatalf("Status = %+v, want StatusCancelled", run)
	}
}

// recordingSink captures every SubagentStatusChanged invocation in
// arrival order. Lets G-5 tests assert event count + payload shape.
type recordingSink struct {
	mu     sync.Mutex
	events []recordedSinkEvent
}

type recordedSinkEvent struct {
	ParentSessionID string
	Payload         map[string]any
}

func (s *recordingSink) SubagentStatusChanged(parentSessionID string, payload []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var decoded map[string]any
	_ = json.Unmarshal(payload, &decoded)
	s.events = append(s.events, recordedSinkEvent{ParentSessionID: parentSessionID, Payload: decoded})
}

func (s *recordingSink) snapshot() []recordedSinkEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]recordedSinkEvent, len(s.events))
	copy(out, s.events)
	return out
}

// gateRunner blocks Run until release is closed, then returns success.
// Used to inspect the sink mid-flight so we can isolate the Spawn-time
// emission from the terminal-time emission.
type gateRunner struct{ release chan struct{} }

func (r gateRunner) Run(_ context.Context, run *Run) (*Result, error) {
	<-r.release
	return &Result{Summary: "ok-" + run.ID, ResultJSON: `{"ok":true}`}, nil
}

func TestSpawn_EmitsRunningEventBeforeRunner(t *testing.T) {
	db, _ := newTestDB(t)
	sink := &recordingSink{}

	gate := make(chan struct{})
	runner := gateRunner{release: gate}

	svc := NewService(db, runner, &stubPoster{}, nil, stubSettings{})
	svc.SetStreamSink(sink)

	doneCh := make(chan string, 1)
	go func() {
		id, err := svc.Spawn(context.Background(), SpawnRequest{
			ParentSessionID: "sess-1",
			ParentAgentID:   "file-backend",
			Role:            "file-summarizer",
			Prompt:          "p",
			Mode:            ModeAsync,
		})
		if err != nil {
			t.Errorf("Spawn: %v", err)
		}
		doneCh <- id
	}()

	// Async Spawn returns immediately. Wait briefly for the running event.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(sink.snapshot()) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(gate) // let the runner finish

	events := sink.snapshot()
	if len(events) == 0 {
		t.Fatal("no running event emitted")
	}
	first := events[0]
	if first.ParentSessionID != "sess-1" {
		t.Errorf("ParentSessionID = %q, want sess-1", first.ParentSessionID)
	}
	if got := first.Payload["status"]; got != "running" {
		t.Errorf("status = %v, want running", got)
	}
	if got := first.Payload["role"]; got != "file-summarizer" {
		t.Errorf("role = %v, want file-summarizer", got)
	}
	if first.Payload["run_id"] == nil || first.Payload["run_id"] == "" {
		t.Error("run_id missing from payload")
	}
	<-doneCh
}

func TestSpawn_EmitsTerminalEventOnComplete(t *testing.T) {
	db, _ := newTestDB(t)
	sink := &recordingSink{}
	svc := NewService(db, EchoRunner{}, &stubPoster{}, nil, stubSettings{})
	svc.SetStreamSink(sink)

	_, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "file-backend",
		Role:            "file-summarizer",
		Prompt:          "ok",
		Mode:            ModeSync,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	events := sink.snapshot()
	if len(events) < 2 {
		t.Fatalf("expected >= 2 events (running + terminal), got %d", len(events))
	}
	terminal := events[len(events)-1]
	if got := terminal.Payload["status"]; got != "completed" {
		t.Errorf("terminal status = %v, want completed", got)
	}
	preview, _ := terminal.Payload["summary_preview"].(string)
	if preview == "" {
		t.Error("summary_preview empty for completed run")
	}
}

func TestSpawn_EmitsTerminalEventOnFailure(t *testing.T) {
	db, _ := newTestDB(t)
	sink := &recordingSink{}
	svc := NewService(db, failRunner{}, &stubPoster{}, nil, stubSettings{})
	svc.SetStreamSink(sink)

	_, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "file-backend",
		Role:            "file-summarizer",
		Prompt:          "will fail",
		Mode:            ModeSync,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	events := sink.snapshot()
	if len(events) < 2 {
		t.Fatalf("expected >= 2 events, got %d", len(events))
	}
	terminal := events[len(events)-1]
	if got := terminal.Payload["status"]; got != "failed" {
		t.Errorf("terminal status = %v, want failed", got)
	}
	if errStr, _ := terminal.Payload["error"].(string); errStr == "" {
		t.Error("error field empty for failed run")
	}
}

// TestSpawn_EmitsTerminalEventOnCancelled verifies Cancel owns exactly one
// terminal event with the DB-authoritative "cancelled" status. The runner's
// later finalizeRun reconciliation must not emit a duplicate terminal event.
func TestSpawn_EmitsTerminalEventOnCancelled(t *testing.T) {
	db, _ := newTestDB(t)
	sink := &recordingSink{}
	runner := &slowRunner{
		started: make(chan struct{}),
		done:    make(chan struct{}),
	}
	svc := NewService(db, runner, &stubPoster{}, nil, stubSettings{})
	svc.SetStreamSink(sink)

	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "file-backend",
		Role:            "file-summarizer",
		Prompt:          "cancel me",
		Mode:            ModeAsync,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start within 1s")
	}
	if err := svc.Cancel(context.Background(), id); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	select {
	case <-runner.done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("runner did not exit within 500ms of Cancel")
	}

	// Cancel emits synchronously in the uncontended path. Leave time for the
	// runner's finalize path to run too, so a duplicate terminal event cannot
	// hide behind the first successful assertion.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(sink.snapshot()) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(25 * time.Millisecond)

	events := sink.snapshot()
	if len(events) != 2 {
		t.Fatalf("events = %v; want exactly running + one terminal cancellation", events)
	}
	terminal := events[len(events)-1]
	if got := terminal.Payload["status"]; got != "cancelled" {
		t.Errorf("terminal status = %v, want cancelled", got)
	}
	if errStr, _ := terminal.Payload["error"].(string); errStr != "" {
		t.Errorf("error = %q, want empty on cancel path", errStr)
	}
}

func TestSpawn_PersistsParentAgentID(t *testing.T) {
	db, _ := newTestDB(t)
	svc := NewService(db, EchoRunner{}, &stubPoster{}, nil, stubSettings{})

	runID, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "primary-agent",
		Role:            "file-backend",
		Prompt:          "x",
		Mode:            ModeSync,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	run, err := svc.Status(context.Background(), runID)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if got, want := run.ParentAgentID, "primary-agent"; got != want {
		t.Errorf("parent_agent_id = %q, want %q", got, want)
	}
}

// TestCancel_UnblocksRunnerEvenWhenDBUpdateFails verifies that Cancel
// still invokes the registered CancelFunc when the DB UPDATE errors —
// otherwise the runner goroutine would leak even though the caller
// asked us to stop it. Simulated by closing the underlying DB before
// calling Cancel; ExecContext returns an error but the deferred
// CancelFunc path must still fire.
func TestCancel_UnblocksRunnerEvenWhenDBUpdateFails(t *testing.T) {
	db, _ := newTestDB(t)
	runner := &slowRunner{
		started: make(chan struct{}),
		done:    make(chan struct{}),
	}
	svc := NewService(db, runner, &stubPoster{}, nil, stubSettings{})

	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "file-backend",
		Role:            "file-summarizer",
		Prompt:          "slow task",
		Mode:            ModeAsync,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start within 1s")
	}

	// Force the Cancel UPDATE to error.
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	cancelErr := svc.Cancel(context.Background(), id)
	if cancelErr == nil {
		t.Fatal("expected Cancel to return an error after db close")
	}

	// The CancelFunc must still have fired so the runner exits.
	select {
	case <-runner.done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("runner did not exit within 500ms — CancelFunc was skipped after DB error")
	}
}

func TestSpawn_Gated_InsertsRequestedAndEmitsEnvelope(t *testing.T) {
	db, _ := newTestDB(t)
	emitter := &stubEmitter{}
	settings := stubSettings{us: store.UserSettings{SubagentApprovalRequired: true}}
	runner := &notCalledRunner{t: t}
	svc := NewService(db, runner, nil, emitter, settings)

	runID, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "primary",
		Role:            "file-backend",
		Prompt:          "hello",
		Mode:            ModeSync,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	run, _ := svc.Status(context.Background(), runID)
	if run.Status != StatusRequested {
		t.Errorf("status = %q, want requested", run.Status)
	}
	if run.EnvelopeInstanceID == "" {
		t.Error("envelope_instance_id not persisted")
	}
	if emitter.Count() != 1 {
		t.Errorf("emit count = %d", emitter.Count())
	}
	if emitter.Last().typ != "subagent-spawn-approval" {
		t.Errorf("emit type = %q", emitter.Last().typ)
	}
}

func TestSpawn_InteractiveMode_GatesEvenWhenFlagOff(t *testing.T) {
	db, _ := newTestDB(t)
	emitter := &stubEmitter{}
	settings := stubSettings{us: store.UserSettings{SubagentApprovalRequired: false}}
	runner := &notCalledRunner{t: t}
	svc := NewService(db, runner, nil, emitter, settings)

	runID, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1", ParentAgentID: "primary",
		Role: "file-backend", Prompt: "hi", Mode: ModeInteractive,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	run, _ := svc.Status(context.Background(), runID)
	if run.Status != StatusRequested {
		t.Errorf("status = %q, want requested", run.Status)
	}
	if emitter.Count() != 1 {
		t.Errorf("emit count = %d", emitter.Count())
	}
}

func TestSpawn_Ungated_UnchangedBehavior(t *testing.T) {
	db, _ := newTestDB(t)
	emitter := &stubEmitter{}
	settings := stubSettings{us: store.UserSettings{SubagentApprovalRequired: false}}
	svc := NewService(db, EchoRunner{}, nil, emitter, settings)

	runID, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1", ParentAgentID: "primary",
		Role: "file-backend", Prompt: "hi", Mode: ModeSync,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	run, _ := svc.Status(context.Background(), runID)
	if run.Status != StatusCompleted {
		t.Errorf("status = %q, want completed", run.Status)
	}
	if emitter.Count() != 0 {
		t.Errorf("emit should not fire for ungated path: %d calls", emitter.Count())
	}
}

// recordingPoster captures all SendMessage calls in order. Extends the
// stubPoster pattern with Count() + Last() helpers for multi-call assertions.
type recordingPoster struct {
	mu    sync.Mutex
	calls []messaging.SendInput
}

func (p *recordingPoster) SendMessage(_ context.Context, in messaging.SendInput) (*messaging.Message, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, in)
	return &messaging.Message{ID: "m-" + strconv.Itoa(len(p.calls))}, nil
}

func (p *recordingPoster) Count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

func (p *recordingPoster) Last() messaging.SendInput {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[len(p.calls)-1]
}

func TestApprove_TransitionsAndRunsRunner(t *testing.T) {
	db, _ := newTestDB(t)
	emitter := &stubEmitter{}
	settings := stubSettings{us: store.UserSettings{SubagentApprovalRequired: true, SubagentApprovalTimeoutSeconds: 3600}}
	poster := &recordingPoster{}
	svc := NewService(db, EchoRunner{}, poster, emitter, settings)

	runID, _ := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "s", ParentAgentID: "p",
		Role: "r", Prompt: "hi", Mode: ModeSync,
	})
	if err := svc.Approve(context.Background(), runID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// Poll until EchoRunner completes the run in the background.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		run, _ := svc.Status(context.Background(), runID)
		if run.Status == StatusCompleted {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	run, _ := svc.Status(context.Background(), runID)
	t.Fatalf("run did not reach completed within 2s; status=%q", run.Status)
}

func TestApprove_NotPending_WhenAlreadyTerminal(t *testing.T) {
	db, _ := newTestDB(t)
	emitter := &stubEmitter{}
	settings := stubSettings{us: store.UserSettings{SubagentApprovalRequired: true}}
	svc := NewService(db, EchoRunner{}, nil, emitter, settings)

	runID, _ := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "s", ParentAgentID: "p",
		Role: "r", Prompt: "hi", Mode: ModeSync,
	})
	_, _ = db.Exec(`UPDATE subagent_runs SET status='completed' WHERE id=?`, runID)

	if err := svc.Approve(context.Background(), runID); !errors.Is(err, ErrNotPending) {
		t.Errorf("err = %v, want ErrNotPending", err)
	}
}

func TestApprove_StaleReturnsExpiredError(t *testing.T) {
	db, _ := newTestDB(t)
	emitter := &stubEmitter{}
	settings := stubSettings{us: store.UserSettings{SubagentApprovalRequired: true, SubagentApprovalTimeoutSeconds: 1}}
	poster := &recordingPoster{}
	svc := NewService(db, EchoRunner{}, poster, emitter, settings)

	runID, _ := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "s", ParentAgentID: "p",
		Role: "r", Prompt: "hi", Mode: ModeSync,
	})
	backdated := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339Nano)
	_, _ = db.Exec(`UPDATE subagent_runs SET created_at=? WHERE id=?`, backdated, runID)

	if err := svc.Approve(context.Background(), runID); !errors.Is(err, ErrApprovalExpired) {
		t.Errorf("err = %v, want ErrApprovalExpired", err)
	}
	run, _ := svc.Status(context.Background(), runID)
	if run.Status != StatusRejected {
		t.Errorf("status = %q, want rejected", run.Status)
	}
	if run.RejectionReason != "approval timed out" {
		t.Errorf("reason = %q", run.RejectionReason)
	}
}

func TestReject_TransitionsAndPostsReply(t *testing.T) {
	db, _ := newTestDB(t)
	emitter := &stubEmitter{}
	settings := stubSettings{us: store.UserSettings{SubagentApprovalRequired: true}}
	poster := &recordingPoster{}
	svc := NewService(db, EchoRunner{}, poster, emitter, settings)

	runID, _ := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1", ParentAgentID: "primary",
		Role: "file-backend", Prompt: "hi", Mode: ModeSync,
	})

	if err := svc.Reject(context.Background(), runID, "too risky"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	run, _ := svc.Status(context.Background(), runID)
	if run.Status != StatusRejected {
		t.Errorf("status = %q", run.Status)
	}
	if run.RejectionReason != "too risky" {
		t.Errorf("reason = %q", run.RejectionReason)
	}

	if poster.Count() != 1 {
		t.Fatalf("poster calls = %d", poster.Count())
	}
	got := poster.Last()
	if got.ToAgentID != "primary" {
		t.Errorf("to = %q", got.ToAgentID)
	}
	if got.Body != "Subagent spawn rejected: too risky" {
		t.Errorf("body = %q", got.Body)
	}
}

func TestReject_NotPending(t *testing.T) {
	db, _ := newTestDB(t)
	emitter := &stubEmitter{}
	settings := stubSettings{us: store.UserSettings{SubagentApprovalRequired: true}}
	svc := NewService(db, EchoRunner{}, nil, emitter, settings)

	runID, _ := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "s", ParentAgentID: "p",
		Role: "r", Prompt: "hi", Mode: ModeSync,
	})
	_, _ = db.Exec(`UPDATE subagent_runs SET status='cancelled' WHERE id=?`, runID)

	if err := svc.Reject(context.Background(), runID, ""); !errors.Is(err, ErrNotPending) {
		t.Errorf("err = %v, want ErrNotPending", err)
	}
}

// storeAgentResolver adapts *store.Store to messaging.AgentResolver so
// tests can wire a REAL messaging.Service instead of stubPoster —
// necessary for TestSpawn_ReplyDelivery_ExistingRoleSlug_NoAutoRegisterCollision,
// which needs to exercise messaging's actual auto-register/slug-collision
// path, not just assert the shape of what subagent handed it.
type storeAgentResolver struct{ st *store.Store }

func (r storeAgentResolver) Get(_ context.Context, id string) (*store.AgentProfile, error) {
	return r.st.GetAgent(context.

		// TestSpawn_ReplyDelivery_ExistingRoleSlug_NoAutoRegisterCollision is the
		// regression pin for CW-20260815-0023: reply delivery used to pass the bare
		// role string (e.g. "worker") as FromAgentID with RegisterAs="external".
		// Since every dispatchable role already has an agent_profiles row (Spawn's
		// own GetAgentBySlug gate requires it) whose real ID is never equal to its
		// slug, messaging's auto-register path would try to INSERT a brand-new row
		// with Slug=role and collide with the UNIQUE constraint on the existing
		// row — on every single reply, not just repeated ones. Spawns two children
		// with the SAME role in sequence (mirrors the ticket's ask) against a REAL
		// messaging.Service (not a stub) so the actual DB constraint is exercised.
		Background(), id)
}

func TestSpawn_ReplyDelivery_ExistingRoleSlug_NoAutoRegisterCollision(t *testing.T) {
	db, st := newTestDB(t)

	// newTestDB runs the full migration set, which already seeds the
	// real internal "worker" profile at id="blt-worker-001" (migration
	// 060) — the exact real-world shape this bug depends on: a role
	// whose agent_profiles.id is never equal to its slug. No manual seed
	// needed/possible here (it would collide with the migration's row).
	if err := st.CreateAgent(context.Background(), &store.AgentProfile{
		ID:     "parent-1",
		Slug:   "parent-1",
		Name:   "Parent",
		Kind:   "internal",
		Status: "active",
	}); err != nil {
		t.Fatalf("seed parent profile: %v", err)
	}

	msgStore := messaging.NewSQLiteStore(db)
	messagingSvc := messaging.NewService(msgStore, db, storeAgentResolver{st: st}, st)

	svc := NewService(db, EchoRunner{}, messagingSvc, nil, stubSettings{})
	svc.SetProfileResolver(st)

	for i := 0; i < 2; i++ {
		id, err := svc.Spawn(context.Background(), SpawnRequest{
			ParentSessionID: "sess-1",
			ParentAgentID:   "parent-1",
			Role:            "worker",
			Prompt:          "do work",
			Mode:            ModeSync,
		})
		if err != nil {
			t.Fatalf("Spawn #%d: %v", i, err)
		}
		run, err := svc.Status(context.Background(), id)
		if err != nil {
			t.Fatalf("Status #%d: %v", i, err)
		}
		if run.Status != StatusCompleted {
			t.Fatalf("run #%d Status = %q, want %q (Error=%q)", i, run.Status, StatusCompleted, run.Error)
		}

		// The run completing does NOT prove the parent learned about it —
		// reply-delivery failures are only slog.Warn'd, never surfaced back
		// onto run.Status (that's the actual failure mode this ticket is
		// about: "parent may never learn a dispatched child completed").
		// So assert directly on delivery: the parent session must have
		// exactly i+1 reply messages by now.
		msgs, err := messagingSvc.RecentForSession(context.Background(), "sess-1", 10)
		if err != nil {
			t.Fatalf("RecentForSession #%d: %v", i, err)
		}
		// CW-20260512-0019: completion replies now carry
		// Kind=subagent_result, not the generic KindReply.
		replies := 0
		for _, m := range msgs {
			if m.Kind == messaging.KindSubagentResult {
				replies++
			}
		}
		if replies != i+1 {
			t.Fatalf("reply #%d: parent has %d subagent_result message(s) in sess-1, want %d — reply delivery failed (likely the agent_profiles.slug constraint)", i, replies, i+1)
		}
	}

	// No phantom row with id="worker" should ever have been created by a
	// (would-be) failed auto-register attempt.
	if _, err := st.GetAgent(context.Background(), "worker"); err == nil {
		t.Error(`a spurious agent_profiles row with id="worker" was created — auto-register should never have been attempted for an already-known role`)
	}
	real, err := st.GetAgent(context.Background(), "blt-worker-001")
	if err != nil {
		t.Fatalf("real worker profile missing: %v", err)
	}
	if real.Slug != "worker" {
		t.Errorf("real worker profile slug = %q, want %q", real.Slug, "worker")
	}
}

// TestSpawn_ProviderOverride_PropagatesIntoRun verifies that
// SpawnRequest.Provider is copied to the persisted Run row and visible
// via Status. Empty string (no override) also round-trips cleanly.
func TestSpawn_ProviderOverride_PropagatesIntoRun(t *testing.T) {
	db, _ := newTestDB(t)
	poster := &stubPoster{}
	svc := NewService(db, EchoRunner{}, poster, nil, stubSettings{})

	// Non-empty override.
	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-prov",
		ParentAgentID:   "agent-primary",
		Role:            "role-worker",
		Prompt:          "do the thing",
		Mode:            ModeSync,
		Provider:        "pty-claude",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	run, err := svc.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if run.Provider != "pty-claude" {
		t.Errorf("Run.Provider = %q, want %q", run.Provider, "pty-claude")
	}

	// Empty override (no provider in SpawnRequest).
	id2, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-prov",
		ParentAgentID:   "agent-primary",
		Role:            "role-worker",
		Prompt:          "lightweight task",
		Mode:            ModeSync,
		// Provider intentionally omitted
	})
	if err != nil {
		t.Fatalf("Spawn (no provider): %v", err)
	}

	run2, err := svc.Status(context.Background(), id2)
	if err != nil {
		t.Fatalf("Status (no provider): %v", err)
	}
	if run2.Provider != "" {
		t.Errorf("Run.Provider = %q, want empty (no override)", run2.Provider)
	}
}

// emptyEnvelopeRunner mimics a runner whose child emitted no structured
// envelope: drainCapture / drainBootSession seed the envelope buffer with
// "{}" and BootRunner's PTY surface never overwrites it. The runner still
// produces a meaningful Summary. Regression fixture for CW-20260516-0060.
type emptyEnvelopeRunner struct{ summary string }

func (r emptyEnvelopeRunner) Run(_ context.Context, _ *Run) (*Result, error) {
	return &Result{Summary: r.summary, ResultJSON: "{}"}, nil
}

// TestSpawn_EmptyEnvelope_PersistsSummaryNotEmptyObject is the CW-20260516-0060
// regression: when the runner hands back ResultJSON == "{}" (no structured
// envelope), the persisted run row must capture the summary in a structured
// object rather than storing a bare {}.
func TestSpawn_EmptyEnvelope_PersistsSummaryNotEmptyObject(t *testing.T) {
	db, _ := newTestDB(t)
	svc := NewService(db, emptyEnvelopeRunner{summary: "did the work"}, &stubPoster{}, nil, stubSettings{})

	runID, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "primary-agent",
		Role:            "file-backend",
		Prompt:          "do the work",
		Mode:            ModeSync,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	run, err := svc.Status(context.Background(), runID)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if run.ResultJSON == "" || run.ResultJSON == "{}" {
		t.Fatalf("ResultJSON = %q, want a non-empty structured object", run.ResultJSON)
	}
	var decoded struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(run.ResultJSON), &decoded); err != nil {
		t.Fatalf("ResultJSON is not valid JSON (%q): %v", run.ResultJSON, err)
	}
	if decoded.Summary != "did the work" {
		t.Errorf("ResultJSON summary = %q, want %q", decoded.Summary, "did the work")
	}
}

// TestStructuredResultJSON_PreservesRealEnvelope verifies the fallback does
// not clobber a genuine structured payload the runner produced.
func TestStructuredResultJSON_PreservesRealEnvelope(t *testing.T) {
	got := structuredResultJSON(&Result{Summary: "ignored", ResultJSON: `{"ok":true}`})
	if got != `{"ok":true}` {
		t.Errorf("structuredResultJSON = %q, want real envelope preserved", got)
	}
}
