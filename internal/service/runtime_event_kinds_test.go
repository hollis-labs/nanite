package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/agentkit/agentsessions"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	runtimeevents "github.com/hollis-labs/go-runtime-events/runtimeevents"
)

// CW-20260930-0113: with go-agent-wrapper v0.15.0 the go-providers adapter
// reaches agentkit unwrapped, so native sessions now emit tool results,
// subagent spawns and provider heartbeats, and agentkit's session-lost and
// auth-failure classification switches on. Raw output lines will also carry
// agentkit v0.14's byte-fanout markers. These pin that the chat router sink
// and the host runtime feed tolerate all of it.

var newRuntimeKinds = []runtimeevents.Event{
	{Kind: runtimeevents.KindAgentToolResult, Payload: json.RawMessage(`{"tool_result":{"id":"tu_1","is_error":false,"content_preview":"ok"}}`)},
	{Kind: runtimeevents.KindAgentSubagentSpawn, Payload: json.RawMessage(`{"subagent_spawn":{"tool":"Task","args":{"description":"x"}}}`)},
	{Kind: runtimeevents.KindSessionHeartbeat, Payload: json.RawMessage(`{"last_activity_at":"2026-10-01T00:00:00Z"}`)},
	{Kind: runtimeevents.KindStdoutLine, Payload: json.RawMessage(`{"line":"[auth_failed]"}`)},
	{Kind: runtimeevents.KindStdoutLine, Payload: json.RawMessage(`{"line":"[permission_denied:Bash] rm -rf /"}`)},
	{Kind: runtimeevents.KindStdoutRaw, Payload: json.RawMessage(`{"bytes":"[session_lost] requested=a actual=b"}`)},
	{Kind: runtimeevents.KindStderrLine, Payload: json.RawMessage(`{"line":"warning"}`)},
}

var (
	errSessionLost      = &agentsessions.SessionLostError{RequestedID: "ses_dead", Err: errors.New("exit status 1")}
	errNotAuthenticated = fmt.Errorf("agentsessions: %w: %w", provider.ErrProviderNotAuthenticated, errors.New("exit status 1"))
)

// The chat router: the new kinds mid-turn neither reach chat as text nor
// end the turn; the turn's own terminal does, and the router closes (no
// hang). A classified failure reaches chat as a user-facing error.
func TestRuntimeEventBridgeSink_NewEventKinds(t *testing.T) {
	failed := func(err error) runtimeevents.Event {
		payload, _ := json.Marshal(map[string]string{"error": err.Error()})
		return runtimeevents.Event{Kind: runtimeevents.KindTurnFailed, TurnID: "turn-1", Payload: payload}
	}
	for _, tc := range []struct {
		name      string
		terminal  runtimeevents.Event
		want      []llmtypes.EventType
		wantError string
	}{
		{"completed", runtimeevents.Event{Kind: runtimeevents.KindTurnCompleted, TurnID: "turn-1"}, []llmtypes.EventType{llmtypes.EventDelta, llmtypes.EventDone}, ""},
		{"session lost", failed(errSessionLost), []llmtypes.EventType{llmtypes.EventDelta, llmtypes.EventError}, "your next message starts a fresh one"},
		{"not authenticated", failed(errNotAuthenticated), []llmtypes.EventType{llmtypes.EventDelta, llmtypes.EventError}, "not logged in on this host"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := newRuntimeEventBridgeSink(&agentEventBridge{streams: NewStreamManager()}, "s-kinds", false)
			router := newSessionRouter(make(chan llmtypes.StreamEvent, 16))
			if !sink.AdmitRuntimeTurnOwner(router) {
				t.Fatal("router not admitted")
			}
			evs := []runtimeevents.Event{
				{Kind: runtimeevents.KindTurnStarted, TurnID: "turn-1"},
				{Kind: runtimeevents.KindAgentDelta, TurnID: "turn-1", Payload: json.RawMessage(`{"content":"the reply"}`)},
			}
			for _, ev := range newRuntimeKinds {
				ev.TurnID = "turn-1"
				evs = append(evs, ev)
			}
			evs = append(evs, tc.terminal, runtimeevents.Event{Kind: runtimeevents.KindSessionIdle, TurnID: "turn-1"})
			for _, ev := range evs {
				if err := sink.Write(context.Background(), ev); err != nil {
					t.Fatalf("Write(%s): %v", ev.Kind, err)
				}
			}
			got := drainRouter(t, router)
			if !slices.Equal(streamTypes(got), tc.want) {
				t.Fatalf("router events = %v, want %v", streamTypes(got), tc.want)
			}
			if got[0].Content != "the reply" {
				t.Fatalf("delta = %q, want only the reply text", got[0].Content)
			}
			if tc.wantError != "" && !strings.Contains(got[len(got)-1].Error, tc.wantError) {
				t.Fatalf("error = %q, want it to say %q", got[len(got)-1].Error, tc.wantError)
			}
		})
	}
}

// The host runtime feed: every new kind projects as non-terminal public
// metadata, raw output (and any marker in it) is omitted, and a classified
// failure is terminal and carries a closed failure enum, not its text.
func TestProjectHostRuntimePayload_NewEventKinds(t *testing.T) {
	for _, ev := range newRuntimeKinds {
		raw, _, truncated := projectHostRuntimePayload(ev.Kind, false, ev.Payload)
		if truncated {
			t.Fatalf("%s projection truncated: %s", ev.Kind, raw)
		}
		var projected map[string]any
		if err := json.Unmarshal(raw, &projected); err != nil {
			t.Fatalf("%s: projection is not JSON: %v", ev.Kind, err)
		}
		if projected["terminal"] == true {
			t.Fatalf("%s projected as terminal: %s", ev.Kind, raw)
		}
		for _, marker := range []string{"auth_failed", "permission_denied", "session_lost", "rm -rf"} {
			if strings.Contains(string(raw), marker) {
				t.Fatalf("%s projection leaks %q: %s", ev.Kind, marker, raw)
			}
		}
	}
	for err, want := range map[error]string{errSessionLost: "session_lost", errNotAuthenticated: "not_authenticated"} {
		payload, _ := json.Marshal(map[string]string{"error": err.Error()})
		raw, _, truncated := projectHostRuntimePayload(runtimeevents.KindTurnFailed, false, payload)
		if truncated {
			t.Fatalf("turn.failed projection truncated: %s", raw)
		}
		var projected map[string]any
		_ = json.Unmarshal(raw, &projected)
		if projected["terminal"] != true || projected["failed"] != true || projected["failure"] != want {
			t.Fatalf("turn.failed projection = %s, want terminal, failed, failure=%q", raw, want)
		}
		if strings.Contains(string(raw), "exit status") {
			t.Fatalf("turn.failed projection leaks the error text: %s", raw)
		}
	}
}
