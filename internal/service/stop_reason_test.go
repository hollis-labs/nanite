package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	runtimeevents "github.com/hollis-labs/substrate/harness/adapters/runtimeevents"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

func TestNormalizeStopReason(t *testing.T) {
	for in, want := range map[string]string{
		"length":            "max_tokens",
		"max_output_tokens": "max_tokens",
		"max_tokens":        "max_tokens",
		"end_turn":          "end_turn",
		"tool_use":          "tool_use",
		"":                  "",
	} {
		if got := normalizeStopReason(in); got != want {
			t.Errorf("normalizeStopReason(%q) = %q, want %q", in, got, want)
		}
	}
}

// ACP adapters report stop_reason at the top of their turn-completed
// payload. The service bridge must hand it to the chat router inside usage,
// where the max_tokens check reads it (CW-20260930-0113).
func TestRuntimeEventBridgeSink_ACPStopReasonReachesRouterAsUsage(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		acp           bool
		wantUsage     *llmtypes.Usage
	}{
		{"acp stop_reason only", `{"stop_reason":"max_tokens"}`, true, &llmtypes.Usage{StopReason: "max_tokens"}},
		{"acp with usage", `{"stop_reason":"max_tokens","usage":{"OutputTokens":9}}`, true, &llmtypes.Usage{OutputTokens: 9, StopReason: "max_tokens"}},
		{"native has no top-level stop_reason", `{"stop_reason":"max_tokens"}`, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bridge := &agentEventBridge{streams: NewStreamManager()}
			sink := newRuntimeEventBridgeSink(bridge, "s-stop", tc.acp)
			router := newSessionRouter(make(chan llmtypes.StreamEvent, 8))
			if !sink.AdmitRuntimeTurnOwner(router) {
				t.Fatal("router not admitted")
			}
			ctx := context.Background()
			_ = sink.Write(ctx, runtimeevents.Event{Kind: runtimeevents.KindTurnStarted, SessionID: "s-stop", TurnID: "t1"})
			_ = sink.Write(ctx, runtimeevents.Event{Kind: runtimeevents.KindTurnCompleted, SessionID: "s-stop", TurnID: "t1", Payload: json.RawMessage(tc.payload)})

			var usage *llmtypes.Usage
			var sawDone bool
			timeout := time.After(2 * time.Second)
			for !sawDone {
				select {
				case ev := <-router.ch:
					if ev.Type == llmtypes.EventUsage {
						usage = ev.Usage
					}
					sawDone = ev.Type == llmtypes.EventDone
				case <-timeout:
					t.Fatal("router never received done")
				}
			}
			if (usage == nil) != (tc.wantUsage == nil) || (usage != nil && *usage != *tc.wantUsage) {
				t.Fatalf("router usage = %+v, want %+v", usage, tc.wantUsage)
			}
		})
	}
}
