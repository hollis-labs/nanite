package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/go-agent-wrapper/acp"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider/events"
	runtimeevents "github.com/hollis-labs/go-runtime-events/runtimeevents"
)

func TestRuntimeEventSink_ACPDeltaShapes(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantType llmtypes.EventType
		wantText string
	}{
		{"message", `{"content":"hello","phase":"message"}`, llmtypes.EventDelta, "hello"},
		{"opencode thought", `{"content":"pondering","phase":"thought"}`, llmtypes.EventThinking, "pondering"},
		{"copilot thinking", `{"content":"reasoning","thinking":true}`, llmtypes.EventThinking, "reasoning"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fanout := make(chan llmtypes.StreamEvent, 1)
			sink := &runtimeEventSink{fanout: fanout, acp: true}
			if err := sink.Write(context.Background(), runtimeevents.Event{
				Kind: runtimeevents.KindAgentDelta, Payload: json.RawMessage(tc.raw),
			}); err != nil {
				t.Fatalf("Write: %v", err)
			}
			got := <-fanout
			if got.Type != tc.wantType {
				t.Fatalf("type = %v, want %v", got.Type, tc.wantType)
			}
			if got.Type == llmtypes.EventThinking {
				if got.ThinkingBlock == nil || got.ThinkingBlock.Thinking != tc.wantText {
					t.Fatalf("thinking = %+v, want %q", got.ThinkingBlock, tc.wantText)
				}
			} else if got.Content != tc.wantText {
				t.Fatalf("content = %q, want %q", got.Content, tc.wantText)
			}
		})
	}
}

func TestRuntimeEventSink_ACPTurnCompletedUsageAlsoTerminates(t *testing.T) {
	fanout := make(chan llmtypes.StreamEvent, 2)
	sink := &runtimeEventSink{fanout: fanout, acp: true}
	if err := sink.Write(context.Background(), runtimeevents.Event{
		Kind:    runtimeevents.KindTurnCompleted,
		Payload: json.RawMessage(`{"usage":{"InputTokens":12,"OutputTokens":34}}`),
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	usage, done := <-fanout, <-fanout
	if usage.Type != llmtypes.EventUsage || usage.Usage == nil || usage.Usage.InputTokens != 12 || usage.Usage.OutputTokens != 34 {
		t.Fatalf("usage event = %+v", usage)
	}
	if done.Type != llmtypes.EventDone {
		t.Fatalf("terminal event = %+v, want EventDone", done)
	}
}

func TestRuntimeEventSink_ACPToolShapes(t *testing.T) {
	t.Run("flat ACP tool use and result", func(t *testing.T) {
		var gotUse events.ToolUse
		var gotResult events.ToolResult
		sink := &runtimeEventSink{acp: true, typedCB: func(event events.Event) {
			switch value := event.(type) {
			case events.ToolUse:
				gotUse = value
			case events.ToolResult:
				gotResult = value
			}
		}}
		_ = sink.Write(context.Background(), runtimeevents.Event{Kind: runtimeevents.KindAgentToolUse,
			Payload: json.RawMessage(`{"tool_call_id":"call-1","title":"Run command","raw_input":{"command":"pwd"}}`)})
		_ = sink.Write(context.Background(), runtimeevents.Event{Kind: runtimeevents.KindAgentToolResult,
			Payload: json.RawMessage(`{"tool_call_id":"call-1","is_error":true,"result":{"output":"denied"}}`)})
		if gotUse.ID != "call-1" || gotUse.Name != "Run command" || gotUse.Args["command"] != "pwd" {
			t.Fatalf("tool use = %+v", gotUse)
		}
		if gotResult.ID != "call-1" || !gotResult.IsError || gotResult.ContentPreview != `{"output":"denied"}` {
			t.Fatalf("tool result = %+v", gotResult)
		}
	})

	t.Run("Copilot nested tool use", func(t *testing.T) {
		var got events.ToolUse
		sink := &runtimeEventSink{acp: true, typedCB: func(event events.Event) {
			got, _ = event.(events.ToolUse)
		}}
		_ = sink.Write(context.Background(), runtimeevents.Event{Kind: runtimeevents.KindAgentToolUse,
			Payload: json.RawMessage(`{"tool_use":{"id":"call-2","name":"shell","input":{"command":"pwd"}}}`)})
		if got.ID != "call-2" || got.Name != "shell" || got.Args["command"] != "pwd" {
			t.Fatalf("tool use = %+v", got)
		}
	})
}

func TestRecoveryCompatibleWrapperError_PreservesACPAndBrokerShapes(t *testing.T) {
	lifecycle := &acp.LifecycleError{
		Kind: acp.OutcomeChildExit, Operation: "wait", Diagnostic: "child exited",
	}
	got := recoveryCompatibleWrapperError(fmt.Errorf("wrapper: ACP session ended: %w", lifecycle), true)
	var gotLifecycle *acp.LifecycleError
	if !errors.As(got, &gotLifecycle) || gotLifecycle != lifecycle {
		t.Fatalf("ACP lifecycle error was not preserved: %v", got)
	}
	var exit *agentsessions.ExitError
	if !errors.As(got, &exit) || exit.Code != -1 {
		t.Fatalf("broker-compatible exit = %+v, error %v", exit, got)
	}
	if native := recoveryCompatibleWrapperError(lifecycle, false); native != lifecycle {
		t.Fatal("native error was unexpectedly translated")
	}
}
