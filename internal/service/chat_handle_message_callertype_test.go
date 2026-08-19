package service

// Regression coverage for TASKS/phase-0/03-fix-callertype-mistagging.md.
//
// HandleMessage is shared by two structurally different real callers:
//   - internal/api/harness_v1.go and internal/api/messages.go — real
//     end-user HTTP handlers that must always dispatch CallerChat, and
//     stamp nothing onto ctx.
//   - chatDurableAgentRuntimeController.SendMessage — a durable agent's
//     scheduled wake delivery, which stamps dispatcher.CallerBackground
//     onto ctx before calling in (see durable_agent_runtime_controller.go
//     and its own direct test).
//
// Before the fix, HandleMessage hardcoded dispatcher.CallerChat at its
// launchGeneration call site, so a durable-agent wake reached
// Dispatcher.Run mistagged as a real user chat turn. These tests exercise
// HandleMessage itself (through the real dispatcher, with a stub Runner
// standing in for the agent-sessions runner) and assert the CallerType
// that actually reaches Dispatcher.Run / the runner for both cases.

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/dispatcher"
	"github.com/hollis-labs/nanite/internal/lifecycle"
)

// callerRecordingRunner implements dispatcher.Runner and records the
// CallerType found on ctx (via dispatcher.CallerTypeFromContext), then
// signals done and closes ch — mirroring the real runner's close-on-
// completion contract (Dispatcher.Run's doc comment).
type callerRecordingRunner struct {
	done chan dispatcher.CallerType
}

func newCallerRecordingRunner() *callerRecordingRunner {
	return &callerRecordingRunner{done: make(chan dispatcher.CallerType, 1)}
}

func (r *callerRecordingRunner) Invoke(ctx context.Context, _, _, _ string, ch chan chat.StreamEvent) {
	r.done <- dispatcher.CallerTypeFromContext(ctx)
	close(ch)
}

func newHandleMessageCallerTypeTestService(t *testing.T, runner dispatcher.Runner) (*chatServiceImpl, *capturingStore) {
	t.Helper()
	cs := &capturingStore{}
	return &chatServiceImpl{
		store:      cs,
		streams:    NewStreamManager(),
		lifecycle:  lifecycle.NewManager("test.handle-message-callertype"),
		activeGen:  make(map[string]*inFlightGen),
		dispatcher: dispatcher.New(runner),
	}, cs
}

// TestHandleMessage_BareContext_DispatchesCallerChat exercises the real
// end-user HTTP-handler shape: harness_v1.go and messages.go both call
// HandleMessage with a plain request ctx that carries no CallerType.
// HandleMessage must still resolve and dispatch CallerChat — the
// unaffected, already-correct behavior the task requires be verified,
// not just assumed.
func TestHandleMessage_BareContext_DispatchesCallerChat(t *testing.T) {
	runner := newCallerRecordingRunner()
	svc, _ := newHandleMessageCallerTypeTestService(t, runner)

	if _, err := svc.HandleMessage(context.Background(), "sess-1", "hello"); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	select {
	case got := <-runner.done:
		if got != dispatcher.CallerChat {
			t.Errorf("dispatched CallerType = %q, want CallerChat", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runner.Invoke")
	}
}

// TestHandleMessage_CallerBackgroundContext_DispatchesCallerBackground
// exercises the durable-agent-wake shape:
// chatDurableAgentRuntimeController.SendMessage stamps
// dispatcher.CallerBackground onto ctx before calling HandleMessage. This
// is the exact bug from TASKS/phase-0/03-fix-callertype-mistagging.md —
// HandleMessage's old hardcoded dispatcher.CallerChat literal would have
// silently produced CallerChat here too.
func TestHandleMessage_CallerBackgroundContext_DispatchesCallerBackground(t *testing.T) {
	runner := newCallerRecordingRunner()
	svc, _ := newHandleMessageCallerTypeTestService(t, runner)

	ctx := dispatcher.WithCallerType(context.Background(), dispatcher.CallerBackground)
	if _, err := svc.HandleMessage(ctx, "sess-1", "wake prompt"); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	select {
	case got := <-runner.done:
		if got != dispatcher.CallerBackground {
			t.Errorf("dispatched CallerType = %q, want CallerBackground (durable-agent wake mistagged as %s otherwise)", got, got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runner.Invoke")
	}
}

// TestHandleMessage_DurableAgentRuntimeControllerEndToEnd_DispatchesCallerBackground
// wires the real chatDurableAgentRuntimeController.SendMessage in front of
// HandleMessage (rather than stamping ctx by hand, as the two tests above
// do) so the full durable-agent-wake-prompt-delivery path is exercised
// end to end, matching the "Done means" requirement in
// TASKS/phase-0/03-fix-callertype-mistagging.md.
func TestHandleMessage_DurableAgentRuntimeControllerEndToEnd_DispatchesCallerBackground(t *testing.T) {
	runner := newCallerRecordingRunner()
	svc, _ := newHandleMessageCallerTypeTestService(t, runner)

	runtime := NewChatDurableAgentRuntimeController(svc)
	if err := runtime.SendMessage(context.Background(), "sess-1", "wake prompt"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	select {
	case got := <-runner.done:
		if got != dispatcher.CallerBackground {
			t.Errorf("durable-agent wake reached Dispatcher.Run tagged CallerType=%q, want CallerBackground", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runner.Invoke")
	}
}
