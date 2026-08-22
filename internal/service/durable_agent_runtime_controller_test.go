package service

// Regression coverage for TASKS/phase-0/03-fix-callertype-mistagging.md:
// a durable agent's scheduled wake delivers its prompt via
// chatDurableAgentRuntimeController.SendMessage -> ChatService.HandleMessage
// — the SAME method internal/api/harness_v1.go and internal/api/messages.go
// use for real end-user chat messages. Before this fix, HandleMessage
// hardcoded dispatcher.CallerChat at its launchGeneration call site
// regardless of which of those two very different callers invoked it, so a
// durable-agent wake was silently indistinguishable from a real user typing
// into chat at the dispatcher level.
//
// SendMessage must stamp dispatcher.CallerBackground onto ctx before
// calling into HandleMessage so HandleMessage's ctx-based CallerType
// resolution (see chat.go's HandleMessage) can tell the two callers apart.

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/dispatcher"
)

// callerCapturingChatService is a minimal ChatService stub that records the
// CallerType present on ctx (via dispatcher.CallerTypeFromContext) when
// HandleMessage is invoked. It does not need to do anything else — the
// mistagging bug lives entirely in what ctx SendMessage hands to
// HandleMessage, not in HandleMessage's other side effects.
type callerCapturingChatService struct {
	gotCtx context.Context
}

func (f *callerCapturingChatService) HandleMessage(ctx context.Context, _, _ string) (string, error) {
	f.gotCtx = ctx
	return "msg-1", nil
}

func (f *callerCapturingChatService) RetryLastMessage(context.Context, string) (string, error) {
	return "", nil
}

func (f *callerCapturingChatService) SendAgentMessage(context.Context, string, string, string) (string, error) {
	return "", nil
}

func (f *callerCapturingChatService) DelegateTask(context.Context, chat.DelegationRequest) (*chat.DelegationResult, error) {
	return nil, nil
}

func (f *callerCapturingChatService) DelegateAndAggregate(context.Context, string, string, string) (*chat.OrchestrationResult, error) {
	return nil, nil
}

func (f *callerCapturingChatService) GetStream(string) (<-chan chat.StreamEvent, bool) {
	return nil, false
}

func (f *callerCapturingChatService) CancelActiveGeneration(string) bool { return false }

func (f *callerCapturingChatService) RebootSessionAgent(context.Context, string) (RebootResult, error) {
	return RebootResult{}, nil
}

func (f *callerCapturingChatService) RecoverSession(context.Context, string) (RebootResult, error) {
	return RebootResult{}, nil
}

func (f *callerCapturingChatService) Shutdown() error { return nil }

// TestChatDurableAgentRuntimeController_SendMessage_StampsCallerBackground
// is the direct regression test for the mistagging bug: it invokes
// SendMessage (the durable-agent wake-prompt-delivery path) directly and
// asserts the ctx reaching ChatService.HandleMessage carries
// dispatcher.CallerBackground, not dispatcher.CallerChat and not empty.
func TestChatDurableAgentRuntimeController_SendMessage_StampsCallerBackground(t *testing.T) {
	fake := &callerCapturingChatService{}
	runtime := NewChatDurableAgentRuntimeController(fake)

	if err := runtime.SendMessage(context.Background(), "sess-1", "wake prompt"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	if fake.gotCtx == nil {
		t.Fatal("HandleMessage was not called")
	}
	got := dispatcher.CallerTypeFromContext(fake.gotCtx)
	if got != dispatcher.CallerBackground {
		t.Errorf("ctx reaching HandleMessage carried CallerType=%q, want CallerBackground (durable-agent wake mistagged as %s otherwise)", got, got)
	}
}

// TestChatDurableAgentRuntimeController_SendMessage_NilChat_NoOp preserves
// the existing nil-chat guard's behavior (documented on the other
// chatDurableAgentRuntimeController methods) under the new ctx-stamping
// logic — SendMessage must still short-circuit cleanly when no chat
// service is wired, without ctx handling changing that.
func TestChatDurableAgentRuntimeController_SendMessage_NilChat_NoOp(t *testing.T) {
	runtime := NewChatDurableAgentRuntimeController(nil)
	if err := runtime.SendMessage(context.Background(), "sess-1", "wake prompt"); err != nil {
		t.Fatalf("SendMessage with nil chat: %v", err)
	}
}
