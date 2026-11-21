package events

import (
	"context"
	"encoding/json"
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

func TestRuntimeIntegrityEvent(t *testing.T) {
	var sink recordSink
	NewRuntime(&sink, slog.Default()).Observe(t.Context(), "conversation", "", IntegrityChecked{
		Conversation: "conversation", Stage: "resume", Disposition: "blocked", Severity: "unsafe",
		Codes: map[string]int{"orphan_tool_result": 2, "interrupted_turn": 1},
	})
	if len(sink) != 1 || sink[0].Kind != runtimeevents.KindAgentDelta {
		t.Fatalf("events %+v", sink)
	}
	var p struct {
		Phase, Stage, Disposition, Severity string
		Codes                               map[string]int
		Text                                *string
	}
	if err := json.Unmarshal(sink[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Phase != "integrity" || p.Stage != "resume" || p.Disposition != "blocked" || p.Severity != "unsafe" ||
		p.Codes["orphan_tool_result"] != 2 || p.Codes["interrupted_turn"] != 1 || p.Text != nil {
		t.Fatalf("payload %s", sink[0].Payload)
	}
}
