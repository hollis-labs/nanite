package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

func turnEvents(events ...llmtypes.StreamEvent) TurnStreamStarter {
	return func(context.Context) (<-chan llmtypes.StreamEvent, error) {
		ch := make(chan llmtypes.StreamEvent, len(events))
		for _, event := range events {
			ch <- event
		}
		close(ch)
		return ch, nil
	}
}

func TestExecuteTurnNormalCompletion(t *testing.T) {
	result, err := ExecuteTurn(context.Background(), TurnRequest{
		Stream: turnEvents(
			llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "hello "},
			llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "world"},
			llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: 4, OutputTokens: 2, StopReason: "end_turn"}},
			llmtypes.StreamEvent{Type: llmtypes.EventDone},
		),
		IdleTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("ExecuteTurn() error = %v", err)
	}
	if result.Text != "hello world" {
		t.Fatalf("Text = %q, want %q", result.Text, "hello world")
	}
	if result.StopReason != "end_turn" {
		t.Fatalf("StopReason = %q, want end_turn", result.StopReason)
	}
	if result.Usage == nil || result.Usage.InputTokens != 4 || result.Usage.OutputTokens != 2 {
		t.Fatalf("Usage = %#v, want input=4 output=2", result.Usage)
	}
}

func TestExecuteTurnToolUse(t *testing.T) {
	want := llmtypes.ToolUseBlock{ID: "tool-1", Name: "lookup", Input: map[string]any{"key": "value"}}
	result, err := ExecuteTurn(context.Background(), TurnRequest{
		Stream:      turnEvents(llmtypes.StreamEvent{Type: llmtypes.EventToolUse, ToolUse: &want}),
		IdleTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("ExecuteTurn() error = %v", err)
	}
	if !reflect.DeepEqual(result.ToolUseBlocks, []llmtypes.ToolUseBlock{want}) {
		t.Fatalf("ToolUseBlocks = %#v, want %#v", result.ToolUseBlocks, []llmtypes.ToolUseBlock{want})
	}
}

func TestExecuteTurnMidstreamErrorIsTyped(t *testing.T) {
	result, err := ExecuteTurn(context.Background(), TurnRequest{
		Stream: turnEvents(
			llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "partial"},
			llmtypes.StreamEvent{Type: llmtypes.EventError, Error: "upstream reset"},
		),
		IdleTimeout: time.Second,
	})
	if err == nil {
		t.Fatal("ExecuteTurn() error = nil, want mid-stream error")
	}
	var turnErr *TurnError
	if !errors.As(err, &turnErr) || turnErr.Kind != TurnErrorStream {
		t.Fatalf("error = %#v, want TurnErrorStream", err)
	}
	if result.Text != "partial" {
		t.Fatalf("Text = %q, want partial", result.Text)
	}
}

func TestExecuteTurnInactivityWatchdog(t *testing.T) {
	streamCanceled := make(chan struct{})
	result, err := ExecuteTurn(context.Background(), TurnRequest{
		Stream: func(ctx context.Context) (<-chan llmtypes.StreamEvent, error) {
			ch := make(chan llmtypes.StreamEvent)
			go func() {
				<-ctx.Done()
				close(streamCanceled)
			}()
			return ch, nil
		},
		IdleTimeout: 10 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("ExecuteTurn() error = nil, want stall error")
	}
	var turnErr *TurnError
	if !errors.As(err, &turnErr) || turnErr.Kind != TurnErrorStalled {
		t.Fatalf("error = %#v, want TurnErrorStalled", err)
	}
	if !errors.Is(err, ErrTurnStalled) {
		t.Fatalf("error = %v, want errors.Is(ErrTurnStalled)", err)
	}
	if !result.Stalled {
		t.Fatal("Stalled = false, want true")
	}
	select {
	case <-streamCanceled:
	case <-time.After(time.Second):
		t.Fatal("watchdog did not cancel stream context")
	}
}

func TestExecuteTurnSumsUsageEvents(t *testing.T) {
	result, err := ExecuteTurn(context.Background(), TurnRequest{
		Stream: turnEvents(
			llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: 10, OutputTokens: 2, CacheCreationTokens: 3, StopReason: "tool_use"}},
			llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: 4, OutputTokens: 5, CacheReadTokens: 7, StopReason: "end_turn"}},
		),
		IdleTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("ExecuteTurn() error = %v", err)
	}
	want := &llmtypes.Usage{InputTokens: 14, OutputTokens: 7, CacheCreationTokens: 3, CacheReadTokens: 7, StopReason: "end_turn"}
	if !reflect.DeepEqual(result.Usage, want) {
		t.Fatalf("Usage = %#v, want %#v", result.Usage, want)
	}
	if result.StopReason != "end_turn" {
		t.Fatalf("StopReason = %q, want end_turn", result.StopReason)
	}
}

func TestExecuteTurnSinkOrder(t *testing.T) {
	tool := llmtypes.ToolUseBlock{ID: "tool-1", Name: "lookup"}
	thinking := llmtypes.ThinkingBlock{Thinking: "consider", Signature: "sig"}
	var calls []string
	result, err := ExecuteTurn(context.Background(), TurnRequest{
		Stream: turnEvents(
			llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "first"},
			llmtypes.StreamEvent{Type: llmtypes.EventToolUse, ToolUse: &tool},
			llmtypes.StreamEvent{Type: llmtypes.EventThinking, ThinkingBlock: &thinking},
			llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "last"},
		),
		IdleTimeout: time.Second,
		Sink: TurnSink{
			OnDelta:    func(content string) { calls = append(calls, "delta:"+content) },
			OnToolUse:  func(toolUse llmtypes.ToolUseBlock) { calls = append(calls, "tool:"+toolUse.ID) },
			OnThinking: func(block llmtypes.ThinkingBlock) { calls = append(calls, "thinking:"+block.Thinking) },
		},
	})
	if err != nil {
		t.Fatalf("ExecuteTurn() error = %v", err)
	}
	wantCalls := []string{"delta:first", "tool:tool-1", "thinking:consider", "delta:last"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("sink calls = %#v, want %#v", calls, wantCalls)
	}
	if result.Text != "firstlast" || len(result.ToolUseBlocks) != 1 || len(result.ThinkingBlocks) != 1 {
		t.Fatalf("result returned after hooks = %#v", result)
	}
}

func TestExecuteTurnZeroValueSink(t *testing.T) {
	tool := llmtypes.ToolUseBlock{ID: "tool-1", Name: "lookup"}
	thinking := llmtypes.ThinkingBlock{Thinking: "consider"}
	result, err := ExecuteTurn(context.Background(), TurnRequest{
		Stream: turnEvents(
			llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "answer"},
			llmtypes.StreamEvent{Type: llmtypes.EventToolUse, ToolUse: &tool},
			llmtypes.StreamEvent{Type: llmtypes.EventThinking, ThinkingBlock: &thinking},
		),
		IdleTimeout: time.Second,
		Sink:        TurnSink{},
	})
	if err != nil {
		t.Fatalf("ExecuteTurn() error = %v", err)
	}
	if result.Text != "answer" || len(result.ToolUseBlocks) != 1 || len(result.ThinkingBlocks) != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestExecuteTurnStartErrorPreservesCause(t *testing.T) {
	want := errors.New("dial failed")
	_, err := ExecuteTurn(context.Background(), TurnRequest{
		Stream:      func(context.Context) (<-chan llmtypes.StreamEvent, error) { return nil, want },
		IdleTimeout: time.Second,
	})
	var turnErr *TurnError
	if !errors.As(err, &turnErr) || turnErr.Kind != TurnErrorStart {
		t.Fatalf("error = %#v, want TurnErrorStart", err)
	}
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want preserved starter cause", err)
	}
}

func TestExecuteTurnPreCanceledContextCannotReturnSuccess(t *testing.T) {
	want := errors.New("operator canceled")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(want)

	starterCalled := false
	_, err := ExecuteTurn(ctx, TurnRequest{
		Stream: func(context.Context) (<-chan llmtypes.StreamEvent, error) {
			starterCalled = true
			ch := make(chan llmtypes.StreamEvent)
			close(ch)
			return ch, nil
		},
		IdleTimeout: time.Second,
	})
	if starterCalled {
		t.Fatal("stream starter called for a pre-canceled Turn")
	}
	var turnErr *TurnError
	if !errors.As(err, &turnErr) || turnErr.Kind != TurnErrorStream {
		t.Fatalf("error = %#v, want TurnErrorStream", err)
	}
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want preserved cancel cause %v", err, want)
	}
}

func TestExecuteTurnCancellationPreservesCause(t *testing.T) {
	want := errors.New("workflow superseded")
	ctx, cancel := context.WithCancelCause(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := ExecuteTurn(ctx, TurnRequest{
			Stream: func(context.Context) (<-chan llmtypes.StreamEvent, error) {
				close(started)
				return make(chan llmtypes.StreamEvent), nil
			},
			IdleTimeout: time.Second,
		})
		done <- err
	}()

	<-started
	cancel(want)
	err := <-done
	var turnErr *TurnError
	if !errors.As(err, &turnErr) || turnErr.Kind != TurnErrorStream {
		t.Fatalf("error = %#v, want TurnErrorStream", err)
	}
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want preserved cancel cause %v", err, want)
	}
}
