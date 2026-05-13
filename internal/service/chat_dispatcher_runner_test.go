// Package service — Dispatcher runner adapter tests.
//
// CW-20260512-0121 (SP-20260512-0011): codify the chatRunnerAdapter's
// ctx-forwarding contract so a future refactor that strips ctx values
// (or wraps with a fresh context) fails this test rather than silently
// emitting caller=unknown in production request_build slog.
//
// The adapter is the named seam where ctx values cross the
// dispatcher → service boundary; it is the *only* hop between
// Dispatcher.Run (which stamps CallerType on ctx) and
// generateResponse (which reads CallerType off ctx for the
// request_build slog). If the adapter ever drops or replaces ctx,
// caller=unknown leaks into production telemetry. These tests pin
// the contract.
package service

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/dispatcher"
)

// TestChatRunnerAdapter_ForwardsCallerTypeContext invokes the adapter
// directly (not via Dispatcher.Run) with a pre-stamped ctx and asserts
// the wrapped runner sees the same CallerType. This is the tightest
// possible assertion that the adapter does not strip ctx values.
func TestChatRunnerAdapter_ForwardsCallerTypeContext(t *testing.T) {
	var gotCaller dispatcher.CallerType
	var gotSessionID, gotMsgID, gotContent string
	var gotCh chan chat.StreamEvent

	adapter := &chatRunnerAdapter{
		runner: func(ctx context.Context, sessionID, assistantMsgID, userContent string, ch chan chat.StreamEvent) {
			gotCaller = dispatcher.CallerTypeFromContext(ctx)
			gotSessionID = sessionID
			gotMsgID = assistantMsgID
			gotContent = userContent
			gotCh = ch
			close(ch)
		},
	}

	// Pre-stamp ctx the way Dispatcher.Run would, then call Invoke
	// directly. The adapter must forward this ctx untouched.
	ctx := dispatcher.WithCallerType(context.Background(), dispatcher.CallerSubagent)
	ch := make(chan chat.StreamEvent, 1)

	adapter.Invoke(ctx, "sess-1", "msg-1", "hello", ch)

	if gotCaller != dispatcher.CallerSubagent {
		t.Errorf("wrapped runner saw CallerType=%q, want CallerSubagent (adapter stripped or replaced ctx)", gotCaller)
	}
	if gotSessionID != "sess-1" {
		t.Errorf("sessionID = %q, want sess-1", gotSessionID)
	}
	if gotMsgID != "msg-1" {
		t.Errorf("assistantMsgID = %q, want msg-1", gotMsgID)
	}
	if gotContent != "hello" {
		t.Errorf("userContent = %q, want hello", gotContent)
	}
	if gotCh != ch {
		t.Error("ch was substituted; adapter must forward the caller's channel verbatim")
	}
}

// TestChatRunnerAdapter_ForwardsBareContext invokes the adapter with
// an un-stamped ctx (the test-stub seam case in subagent_runner_test.go
// et al., which call generateResponse without going through the
// dispatcher) and asserts CallerTypeFromContext returns empty. This
// is the smoke signal for caller=unknown in request_build slog —
// production must never see this.
func TestChatRunnerAdapter_ForwardsBareContext(t *testing.T) {
	var gotCaller dispatcher.CallerType

	adapter := &chatRunnerAdapter{
		runner: func(ctx context.Context, _, _, _ string, ch chan chat.StreamEvent) {
			gotCaller = dispatcher.CallerTypeFromContext(ctx)
			close(ch)
		},
	}

	ch := make(chan chat.StreamEvent, 1)
	adapter.Invoke(context.Background(), "s", "m", "u", ch)

	if gotCaller != "" {
		t.Errorf("wrapped runner saw CallerType=%q on bare ctx, want empty", gotCaller)
	}
}

// TestNewChatRunnerAdapter_WiresGenerateResponse asserts the
// production constructor (newChatRunnerAdapter) sets a non-nil runner
// field. A nil runner would NPE on Invoke; this test pins the
// invariant so a future refactor that forgets to wire the runner
// fails compile-or-test rather than at first Dispatcher.Run.
func TestNewChatRunnerAdapter_WiresGenerateResponse(t *testing.T) {
	svc := &chatServiceImpl{}
	a := newChatRunnerAdapter(svc)
	if a == nil {
		t.Fatal("newChatRunnerAdapter returned nil")
	}
	if a.runner == nil {
		t.Fatal("newChatRunnerAdapter left runner field nil — adapter would NPE on Invoke")
	}
}

// Compile-time assertion: chatRunnerAdapter must satisfy
// dispatcher.Runner. (Mirrors the production-side assertion in
// chat_dispatcher_runner.go; duplicated here so a test-only signature
// drift also surfaces at test compile.)
var _ dispatcher.Runner = (*chatRunnerAdapter)(nil)
