package dispatcher

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
)

// captureRunner records the calls Dispatcher.Run forwards into it so
// tests can assert on the CallerType propagation and the runner's
// observed inputs.
type captureRunner struct {
	calls []capturedInvoke
}

type capturedInvoke struct {
	sessionID      string
	assistantMsgID string
	userContent    string
	callerType     CallerType
}

func (r *captureRunner) Invoke(ctx context.Context, sessionID, assistantMsgID, userContent string, ch chan chat.StreamEvent) {
	r.calls = append(r.calls, capturedInvoke{
		sessionID:      sessionID,
		assistantMsgID: assistantMsgID,
		userContent:    userContent,
		callerType:     CallerTypeFromContext(ctx),
	})
	close(ch)
}

func TestRun_propagatesCallerTypeOnContext(t *testing.T) {
	tests := []struct {
		name   string
		caller CallerType
	}{
		{"chat", CallerChat},
		{"subagent", CallerSubagent},
		{"background", CallerBackground},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &captureRunner{}
			d := New(r)
			ch := make(chan chat.StreamEvent, 1)
			err := d.Run(context.Background(), Request{
				SessionID:      "sess-1",
				AssistantMsgID: "msg-1",
				UserContent:    "hello",
				CallerType:     tc.caller,
			}, ch)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if len(r.calls) != 1 {
				t.Fatalf("expected 1 runner invocation, got %d", len(r.calls))
			}
			got := r.calls[0]
			if got.callerType != tc.caller {
				t.Errorf("CallerType on ctx = %q, want %q", got.callerType, tc.caller)
			}
			if got.sessionID != "sess-1" {
				t.Errorf("sessionID = %q, want sess-1", got.sessionID)
			}
			if got.assistantMsgID != "msg-1" {
				t.Errorf("assistantMsgID = %q, want msg-1", got.assistantMsgID)
			}
			if got.userContent != "hello" {
				t.Errorf("userContent = %q, want hello", got.userContent)
			}
		})
	}
}

func TestRun_rejectsInvalidCallerType(t *testing.T) {
	d := New(&captureRunner{})
	ch := make(chan chat.StreamEvent, 1)
	err := d.Run(context.Background(), Request{
		SessionID:      "sess",
		AssistantMsgID: "msg",
		CallerType:     CallerType("bogus"),
	}, ch)
	if err == nil {
		t.Fatal("expected invalid CallerType error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid caller_type") {
		t.Errorf("error did not mention caller_type: %v", err)
	}
}

func TestRun_rejectsEmptySessionID(t *testing.T) {
	d := New(&captureRunner{})
	ch := make(chan chat.StreamEvent, 1)
	err := d.Run(context.Background(), Request{
		AssistantMsgID: "msg",
		CallerType:     CallerChat,
	}, ch)
	if err == nil {
		t.Fatal("expected missing session_id error, got nil")
	}
	if !strings.Contains(err.Error(), "session_id") {
		t.Errorf("error did not mention session_id: %v", err)
	}
}

func TestRun_rejectsEmptyAssistantMsgID(t *testing.T) {
	d := New(&captureRunner{})
	ch := make(chan chat.StreamEvent, 1)
	err := d.Run(context.Background(), Request{
		SessionID:  "sess",
		CallerType: CallerChat,
	}, ch)
	if err == nil {
		t.Fatal("expected missing assistant_msg_id error, got nil")
	}
	if !strings.Contains(err.Error(), "assistant_msg_id") {
		t.Errorf("error did not mention assistant_msg_id: %v", err)
	}
}

func TestRun_nilRunnerReturnsError(t *testing.T) {
	d := New(nil)
	ch := make(chan chat.StreamEvent, 1)
	err := d.Run(context.Background(), Request{
		SessionID:      "sess",
		AssistantMsgID: "msg",
		CallerType:     CallerChat,
	}, ch)
	if err == nil {
		t.Fatal("expected nil-runner error, got nil")
	}
}

func TestRun_nilDispatcherReturnsError(t *testing.T) {
	var d *Dispatcher
	ch := make(chan chat.StreamEvent, 1)
	err := d.Run(context.Background(), Request{
		SessionID:      "sess",
		AssistantMsgID: "msg",
		CallerType:     CallerChat,
	}, ch)
	if err == nil {
		t.Fatal("expected nil-dispatcher error, got nil")
	}
}

func TestCallerTypeFromContext_missingReturnsEmpty(t *testing.T) {
	if got := CallerTypeFromContext(context.Background()); got != "" {
		t.Errorf("CallerTypeFromContext on bare ctx = %q, want empty", got)
	}
	if got := CallerTypeFromContext(nil); got != "" { //nolint:staticcheck // intentional nil ctx
		t.Errorf("CallerTypeFromContext on nil = %q, want empty", got)
	}
}

func TestCallerType_Valid(t *testing.T) {
	for _, c := range []CallerType{CallerChat, CallerSubagent, CallerBackground} {
		if !c.Valid() {
			t.Errorf("%q.Valid() = false, want true", c)
		}
	}
	if CallerType("").Valid() {
		t.Error(`CallerType("").Valid() = true, want false`)
	}
	if CallerType("bogus").Valid() {
		t.Error(`CallerType("bogus").Valid() = true, want false`)
	}
}

// TestRun_identicalShapeAcrossCallerTypes is the load-bearing
// acceptance smoke for CW-20260512-0121: every CallerType produces an
// identical runner invocation shape (same parameter set, same channel
// type). The CallerType only differs in the slog label propagated via
// context — the runner does NOT branch on it. This is the
// "structural shape invariant" the orchestrator boot prompt calls
// out. If a future change adds caller-conditional behavior inside
// Dispatcher.Run, this test fails — that is the regression signal.
func TestRun_identicalShapeAcrossCallerTypes(t *testing.T) {
	const sessionID = "sess"
	const msgID = "msg"
	const content = "user-content"

	want := capturedInvoke{
		sessionID:      sessionID,
		assistantMsgID: msgID,
		userContent:    content,
	}

	for _, c := range []CallerType{CallerChat, CallerSubagent, CallerBackground} {
		r := &captureRunner{}
		d := New(r)
		ch := make(chan chat.StreamEvent, 1)
		if err := d.Run(context.Background(), Request{
			SessionID:      sessionID,
			AssistantMsgID: msgID,
			UserContent:    content,
			CallerType:     c,
		}, ch); err != nil {
			t.Fatalf("Run %q: %v", c, err)
		}
		if len(r.calls) != 1 {
			t.Fatalf("Run %q: invocations = %d, want 1", c, len(r.calls))
		}
		got := r.calls[0]
		// CallerType differs by design; zero it for the structural diff.
		got.callerType = ""
		if got != want {
			t.Errorf("Run %q: invocation = %+v, want %+v (structural shape differs across CallerTypes)", c, got, want)
		}
	}
}

// runnerFuncErr is a Runner that mimics a runner returning early (in
// chat_generate.go, the runner closes its own channel via defer; this
// stand-in proves Dispatcher.Run does NOT double-close).
type runnerFuncErr func(ch chan chat.StreamEvent)

func (f runnerFuncErr) Invoke(_ context.Context, _, _, _ string, ch chan chat.StreamEvent) {
	f(ch)
}

func TestRun_doesNotDoubleCloseChannel(t *testing.T) {
	closed := false
	runner := runnerFuncErr(func(ch chan chat.StreamEvent) {
		close(ch)
		closed = true
	})
	d := New(runner)
	ch := make(chan chat.StreamEvent, 1)
	if err := d.Run(context.Background(), Request{
		SessionID:      "sess",
		AssistantMsgID: "msg",
		CallerType:     CallerChat,
	}, ch); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !closed {
		t.Error("runner did not run")
	}
	// If Dispatcher.Run had double-closed, the runner's close would
	// have panicked above and we'd be in t.Fatal land. Reaching here
	// is the proof.
}
