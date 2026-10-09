package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/harness/adapters/agentsessions"
	"github.com/hollis-labs/substrate/harness/adapters/provider"
	"github.com/hollis-labs/substrate/harness/adapters/provider/events"
	runtimeevents "github.com/hollis-labs/substrate/harness/adapters/runtimeevents"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

// midTurnKinds are the events go-agent-wrapper v0.15.0 starts emitting once
// the go-providers adapter reaches agentkit unwrapped (tool results,
// subagent spawns, provider heartbeats), plus the raw output lines that will
// carry agentkit v0.14's byte-fanout markers. None of them is a reply or a
// terminal (CW-20260930-0113).
var midTurnKinds = []runtimeevents.Event{
	{Kind: runtimeevents.KindAgentToolResult, Payload: json.RawMessage(`{"tool_result":{"id":"tu_1","is_error":false,"content_preview":"ok"}}`)},
	{Kind: runtimeevents.KindAgentSubagentSpawn, Payload: json.RawMessage(`{"subagent_spawn":{"tool":"Task","args":{"description":"x"}}}`)},
	{Kind: runtimeevents.KindSessionHeartbeat, Payload: json.RawMessage(`{"last_activity_at":"2026-10-01T00:00:00Z"}`)},
	{Kind: runtimeevents.KindStdoutLine, Payload: json.RawMessage(`{"line":"[auth_failed]"}`)},
	{Kind: runtimeevents.KindStdoutLine, Payload: json.RawMessage(`{"line":"[permission_denied:Bash] rm -rf /"}`)},
	{Kind: runtimeevents.KindStdoutRaw, Payload: json.RawMessage(`{"bytes":"[session_lost] requested=a actual=b"}`)},
	{Kind: runtimeevents.KindStderrLine, Payload: json.RawMessage(`{"line":"warning"}`)},
	// go-agent-wrapper v0.17.0's session notices.
	{Kind: runtimeevents.KindSessionLost, Payload: json.RawMessage(`{"requested_id":"ses_a","actual_id":"ses_b","reason":"resume_replaced"}`)},
	{Kind: runtimeevents.KindSessionAuthFailed, Payload: json.RawMessage(`{"error":"Authentication required"}`)},
	{Kind: runtimeevents.KindAgentPermissionDenied, Payload: json.RawMessage(`{"action":"Bash","display_name":"rm -rf /"}`)},
}

// The legacy fanout projection (wrapper_sink.go) tolerates every new kind:
// Write succeeds, tool results and subagent spawns reach the typed callback,
// nothing else reaches the chat fanout (no stray delta carrying marker text,
// no stray terminal), and the turn still ends on its own terminal.
func TestRuntimeEventSink_NewEventKindsAreNotRepliesOrTerminals(t *testing.T) {
	h := newBlockSinkHarness(t, "codex", false)
	var typed []events.Event
	h.sink.typedCB = func(e events.Event) { typed = append(typed, e) }

	h.write(t, runtimeevents.KindTurnStarted, "")
	h.delta(t, "the reply")
	for _, ev := range midTurnKinds {
		if err := h.sink.Write(context.Background(), ev); err != nil {
			t.Fatalf("Write(%s): %v", ev.Kind, err)
		}
	}
	h.write(t, runtimeevents.KindTurnCompleted, "")

	var fanout []llmtypes.StreamEvent
	for len(h.fanout) > 0 {
		fanout = append(fanout, <-h.fanout)
	}
	if len(fanout) != 2 || fanout[0].Type != llmtypes.EventDelta || fanout[0].Content != "the reply" || fanout[1].Type != llmtypes.EventDone {
		t.Fatalf("fanout = %+v, want exactly [delta \"the reply\", done]", fanout)
	}
	var gotResult, gotSpawn, gotLost, gotAuth, gotDenied bool
	for _, e := range typed {
		switch ev := e.(type) {
		case events.ToolResult:
			gotResult = true
		case events.SubagentSpawn:
			gotSpawn = true
		case events.SessionLost:
			gotLost = ev.RequestedID == "ses_a" && ev.ActualID == "ses_b"
		case events.AuthFailed:
			gotAuth = ev.Message == "Authentication required"
		case events.PermissionDenied:
			gotDenied = ev.Action == "Bash"
		}
	}
	if !gotResult || !gotSpawn || !gotLost || !gotAuth || !gotDenied {
		t.Fatalf("typed callback = %#v, want ToolResult, SubagentSpawn, SessionLost, AuthFailed and PermissionDenied", typed)
	}
	if len(h.canonical) != 3+len(midTurnKinds) {
		t.Fatalf("canonical got %d events, want every one (%d)", len(h.canonical), 3+len(midTurnKinds))
	}
}

// A classified turn failure ends the turn with a user-facing error.
func TestRuntimeEventSink_ClassifiedFailureIsUserFacing(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&agentsessions.SessionLostError{RequestedID: "ses_dead", Err: errors.New("exit status 1")}, "your next message starts a fresh one"},
		{fmt.Errorf("agentsessions: %w: %w", provider.ErrProviderNotAuthenticated, errors.New("exit status 1")), "not logged in on this host"},
	} {
		h := newBlockSinkHarness(t, "opencode", false)
		payload, _ := json.Marshal(map[string]string{"error": tc.err.Error()})
		h.write(t, runtimeevents.KindTurnFailed, string(payload))
		ev := <-h.fanout
		if ev.Type != llmtypes.EventError || !strings.HasPrefix(ev.Error, tc.err.Error()) || !strings.Contains(ev.Error, tc.want) {
			t.Fatalf("fanout = %+v, want an error carrying %q and %q", ev, tc.err.Error(), tc.want)
		}
	}
}

func TestClassifyTurnFailure(t *testing.T) {
	lost := &agentsessions.SessionLostError{RequestedID: "ses_dead", Err: errors.New("exit status 1")}
	notAuth := fmt.Errorf("agentsessions: %w: %w", provider.ErrProviderNotAuthenticated, errors.New("exit status 1"))
	for msg, want := range map[string]string{
		lost.Error():    TurnFailureSessionLost,
		notAuth.Error(): TurnFailureNotAuthenticated,
		"driveBootSession: send input: " + lost.Error(): TurnFailureSessionLost,
		"boom": "",
		"":     "",
	} {
		if got := ClassifyTurnFailure(msg); got != want {
			t.Errorf("ClassifyTurnFailure(%q) = %q, want %q", msg, got, want)
		}
	}
	if got := UserFacingTurnError("boom"); got != "boom" {
		t.Errorf("UserFacingTurnError(boom) = %q, want it unchanged", got)
	}
}
