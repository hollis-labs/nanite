package events

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
	"github.com/hollis-labs/nanite/internal/permission"
)

type recordSink []runtimeevents.Event

func (s *recordSink) Write(_ context.Context, event runtimeevents.Event) error {
	*s = append(*s, event)
	return nil
}

func TestRuntimeTurnLifecycle(t *testing.T) {
	ctx := t.Context()
	NewRuntime(nil, nil).Observe(ctx, "ignored", "ignored", RunStarted{})
	var sink recordSink
	runtime := NewRuntime(&sink, slog.Default())
	for _, item := range []Event{
		RunStarted{}, TextDelta{Text: "hello"}, Thinking{Text: "think"}, ToolCallStarted{Name: "lookup", CallID: "one"},
		PermissionAsked{Request: permission.PermissionRequest{ID: "request"}}, PermissionResolved{ID: "request"},
		ToolCallFinished{CallID: "one", IsError: true}, SubagentStarted{ID: "child"}, SubagentFinished{ID: "child"}, RunFinished{},
	} {
		runtime.Observe(ctx, "conversation", "first", item)
	}
	runtime.Observe(ctx, "conversation", "second", RunStarted{})
	runtime.Observe(ctx, "conversation", "second", RunFailed{Err: errors.New("stopped")})
	var got []runtimeevents.EventKind
	for _, event := range sink {
		got = append(got, event.Kind)
	}
	want := []runtimeevents.EventKind{
		runtimeevents.KindSessionReady, runtimeevents.KindSessionProcessing, runtimeevents.KindTurnStarted,
		runtimeevents.KindAgentDelta, runtimeevents.KindAgentDelta, runtimeevents.KindAgentToolUse,
		runtimeevents.KindAgentPermissionRequested, runtimeevents.KindAgentPermissionResolved, runtimeevents.KindAgentPermissionDenied,
		runtimeevents.KindAgentToolResult, runtimeevents.KindAgentSubagentSpawn, runtimeevents.KindAgentSubagentStop,
		runtimeevents.KindTurnCompleted, runtimeevents.KindSessionIdle,
		runtimeevents.KindSessionProcessing, runtimeevents.KindTurnStarted, runtimeevents.KindTurnFailed, runtimeevents.KindSessionIdle,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
}

func TestTracingSpansClose(t *testing.T) {
	ctx, endTurn := TurnSpan(t.Context())
	defer endTurn()
	_, endModel := ModelSpan(ctx, "fixture")
	endModel()
	_, endTool := ToolSpan(ctx, "fixture")
	endTool()
}
