package subagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/messaging"
	"github.com/hollis-labs/nanite/internal/store"

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
	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(); _ = os.Remove(dbPath) })
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

func (s stubSettings) GetUserSettings() (*store.UserSettings, error) { return &s.us, nil }

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
	if in.Kind != messaging.KindReply {
		t.Errorf("reply Kind = %q, want %q", in.Kind, messaging.KindReply)
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
	// would time out (runner stays blocked until execute's 300s WithTimeout).
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

// TestSpawn_EmitsTerminalEventOnCancelled verifies the terminal event
// payload reports the DB-authoritative "cancelled" status, not the
// in-memory "failed" that execute sets after the runner returns
// ctx.Err(). finalizeRun's 0-rows-affected branch re-reads the row and
// patches run.Status before the emit.
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

	// Wait for the terminal emit to land (it fires from the runner
	// goroutine after finalizeRun, which re-reads the cancelled row).
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(sink.snapshot()) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	events := sink.snapshot()
	if len(events) < 2 {
		t.Fatalf("expected >= 2 events (running + terminal), got %d", len(events))
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
