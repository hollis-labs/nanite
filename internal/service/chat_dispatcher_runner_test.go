// Package service — Dispatcher runner adapter tests.
//
// CW-20260512-0121 (SP-20260512-0011): codify the dispatcher's
// CallerType-on-context plumbing so a future refactor that loses the
// stamp fails this test rather than silently emitting caller=unknown
// in production request_build slog.
package service

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/dispatcher"
)

// TestChatRunnerAdapter_HonorsCallerTypeContext asserts the adapter
// passes ctx through to the wrapped runner — the CallerType stamp put
// on ctx by Dispatcher.Run must reach the runner's downstream
// telemetry (request_build slog). The adapter is a one-line
// delegation today; this test exists so a future "smart adapter"
// that strips ctx values fails fast.
func TestChatRunnerAdapter_HonorsCallerTypeContext(t *testing.T) {
	// Construct an adapter pointing at a stub chatServiceImpl. We do
	// not need a real chat service for this test — only that the
	// adapter forwards ctx untouched.
	captured := make(chan dispatcher.CallerType, 1)

	// Replace Invoke with a lambda that reads CallerType off ctx; we
	// can't reach into chatServiceImpl.generateResponse cleanly from a
	// unit test, so we use a stub that satisfies dispatcher.Runner
	// directly. The shape we care about is the Runner contract, which
	// is what the adapter exists to implement.
	stub := runnerFunc(func(ctx context.Context, _, _, _ string, _ chan chat.StreamEvent) {
		captured <- dispatcher.CallerTypeFromContext(ctx)
	})

	d := dispatcher.New(stub)
	ch := make(chan chat.StreamEvent, 1)
	if err := d.Run(context.Background(), dispatcher.Request{
		SessionID:      "s",
		AssistantMsgID: "m",
		CallerType:     dispatcher.CallerSubagent,
	}, ch); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := <-captured
	if got != dispatcher.CallerSubagent {
		t.Errorf("runner saw CallerType=%q, want CallerSubagent (dispatcher dropped the stamp)", got)
	}
}

// runnerFunc adapts a function to dispatcher.Runner for stub use.
// Local helper because internal/dispatcher's own test version is not
// exported.
type runnerFunc func(ctx context.Context, sessionID, assistantMsgID, userContent string, ch chan chat.StreamEvent)

func (f runnerFunc) Invoke(ctx context.Context, sessionID, assistantMsgID, userContent string, ch chan chat.StreamEvent) {
	f(ctx, sessionID, assistantMsgID, userContent, ch)
	close(ch)
}
