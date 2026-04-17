package subagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

func TestSpawn_SyncEchoRunner_RoundTrip(t *testing.T) {
	db, _ := newTestDB(t)
	poster := &stubPoster{}
	svc := NewService(db, EchoRunner{}, poster)

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
	svc := NewService(db, EchoRunner{}, poster)

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
	svc := NewService(db, EchoRunner{}, nil)

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
	svc := NewService(db, nil, nil)

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
	svc := NewService(db, failRunner{}, poster)

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
	svc := NewService(db, EchoRunner{}, &stubPoster{})

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
	svc := NewService(db, runner, &stubPoster{})

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

	svc := NewService(db, runner, &stubPoster{})
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
