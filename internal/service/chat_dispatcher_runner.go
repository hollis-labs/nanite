// Package service — Dispatcher runner adapter.
//
// chatRunnerAdapter binds chatServiceImpl.generateResponse to the
// internal/dispatcher.Runner interface. The dispatcher package is the
// single agent-dispatch door (CW-20260512-0121); this adapter is the
// thin glue that lets it delegate to the existing single-runner
// (chat_generate.go's generateResponse, the agent-sessions surface)
// without exposing the unexported method through the public interface.
//
// One adapter per chatServiceImpl. The lifetime matches the chat
// service; constructed once at container-wire time and shared across
// all callers.
package service

import (
	"context"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/dispatcher"
)

// chatRunnerAdapter is the dispatcher.Runner implementation backed by
// chatServiceImpl.generateResponse. The adapter holds a function
// reference to the runner method (captured at construction time via a
// bound-method closure) so dispatcher.Dispatcher sees only the Runner
// interface and never the chatServiceImpl type directly — this
// preserves the "runner untouched, callers consolidated" sharp edge.
//
// The runner field is a function value rather than a method dispatch
// off a pointer so tests can construct an adapter with a stub runner
// (see chat_dispatcher_runner_test.go) and exercise Invoke directly
// without spinning up a full chatServiceImpl.
type chatRunnerAdapter struct {
	runner func(ctx context.Context, sessionID, assistantMsgID, userContent string, ch chan chat.StreamEvent)
}

// newChatRunnerAdapter constructs the adapter from a chat service.
// Required: svc must be non-nil at construction time (no production
// code path needs a nil runner). The adapter captures
// svc.generateResponse as a bound-method closure so Invoke can call
// the unexported method without exposing it through the interface.
func newChatRunnerAdapter(svc *chatServiceImpl) *chatRunnerAdapter {
	return &chatRunnerAdapter{runner: svc.generateResponse}
}

// Invoke implements dispatcher.Runner by delegating to the wrapped
// runner function. The signature MUST match
// chatServiceImpl.generateResponse byte-for-byte (and it does — that
// is the load-bearing invariant the dispatcher refactor depends on).
// ctx is forwarded verbatim so the CallerType stamp put on it by
// Dispatcher.Run reaches generateResponse's request_build telemetry.
func (a *chatRunnerAdapter) Invoke(ctx context.Context, sessionID, assistantMsgID, userContent string, ch chan chat.StreamEvent) {
	a.runner(ctx, sessionID, assistantMsgID, userContent, ch)
}

// chatServiceImpl satisfies dispatcher.Runner structurally because its
// generateResponse method has the matching shape. We use an explicit
// adapter rather than relying on that structural match for two reasons:
//
//  1. generateResponse is unexported, so other packages can't see it
//     through the interface anyway — the adapter is the necessary
//     export seam.
//
//  2. The adapter is a load-bearing comment surface: when a future
//     refactor adds a different runner (mux-backed, in-process
//     reusable), it shows up next to this file rather than buried in
//     wiring.
var _ dispatcher.Runner = (*chatRunnerAdapter)(nil)
