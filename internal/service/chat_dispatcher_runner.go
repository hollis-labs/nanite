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
// chatServiceImpl.generateResponse. The adapter holds a pointer to the
// chat service so it can reach the unexported generateResponse method;
// dispatcher.Dispatcher holds an interface reference (Runner) and
// never sees the chatServiceImpl type directly — this preserves the
// "runner untouched, callers consolidated" sharp edge.
type chatRunnerAdapter struct {
	svc *chatServiceImpl
}

// newChatRunnerAdapter constructs the adapter. Required: svc must be
// non-nil at construction time (no production code path needs a nil
// runner). Returning the pointer rather than the interface lets the
// container wire the same value into Dispatcher.New while keeping
// per-package nil-safety obvious.
func newChatRunnerAdapter(svc *chatServiceImpl) *chatRunnerAdapter {
	return &chatRunnerAdapter{svc: svc}
}

// Invoke implements dispatcher.Runner by delegating to
// chatServiceImpl.generateResponse. The signature MUST match
// chatServiceImpl.generateResponse byte-for-byte (and it does — that
// is the load-bearing invariant the dispatcher refactor depends on).
func (a *chatRunnerAdapter) Invoke(ctx context.Context, sessionID, assistantMsgID, userContent string, ch chan chat.StreamEvent) {
	a.svc.generateResponse(ctx, sessionID, assistantMsgID, userContent, ch)
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
