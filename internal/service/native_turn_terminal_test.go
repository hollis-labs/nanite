package service

import (
	"context"
	"encoding/json"
	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	runtimeevents "github.com/hollis-labs/go-runtime-events/runtimeevents"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/dispatcher"
	"github.com/hollis-labs/nanite/internal/permission"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/structuredmessage"
)

// CW-20261001-0019: since go-agent-wrapper v0.13.1 every runtime turn, native
// or ACP, ends in exactly one tagged terminal event — turn.completed or
// turn.failed — carrying the turn's usage, followed by session.idle. Before,
// a native turn's usage arrived as a turn.completed of its own and the chat
// router waited for an empty second completion; once v0.13.1 stopped sending
// it, the turn never closed and no reply was saved.

// drainRouter reads the router's chan until it closes and returns the events
// in order. It fails if the chan is still open after the timeout.
func drainRouter(t *testing.T, router *sessionRouter) []llmtypes.StreamEvent {
	t.Helper()
	var got []llmtypes.StreamEvent
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-router.ch:
			if !ok {
				return got
			}
			got = append(got, ev)
		case <-deadline:
			t.Fatalf("router still open after 2s; events so far %v", streamTypes(got))
		}
	}
}

func streamTypes(evs []llmtypes.StreamEvent) []llmtypes.EventType {
	types := make([]llmtypes.EventType, len(evs))
	for i, ev := range evs {
		types[i] = ev.Type
	}
	return types
}

// TestRuntimeEventBridgeSink_TerminalEventCarriesUsage drives the v0.13.1
// terminal shapes through the canonical sink to the chat router: usage first,
// then the terminal, and the router closes.
func TestRuntimeEventBridgeSink_TerminalEventCarriesUsage(t *testing.T) {
	const usage = `"usage":{"InputTokens":10,"OutputTokens":5,"StopReason":"end_turn"}`
	for _, tc := range []struct {
		name      string
		acp       bool
		kind      runtimeevents.EventKind
		payload   string
		want      []llmtypes.EventType
		wantError string
	}{
		{"native completion with usage", false, runtimeevents.KindTurnCompleted, `{` + usage + `}`,
			[]llmtypes.EventType{llmtypes.EventDelta, llmtypes.EventUsage, llmtypes.EventDone}, ""},
		{"native completion without usage", false, runtimeevents.KindTurnCompleted, ``,
			[]llmtypes.EventType{llmtypes.EventDelta, llmtypes.EventDone}, ""},
		{"ACP completion with usage", true, runtimeevents.KindTurnCompleted, `{"stop_reason":"end_turn",` + usage + `}`,
			[]llmtypes.EventType{llmtypes.EventDelta, llmtypes.EventUsage, llmtypes.EventDone}, ""},
		{"native failure with usage", false, runtimeevents.KindTurnFailed, `{"error":"boom",` + usage + `}`,
			[]llmtypes.EventType{llmtypes.EventDelta, llmtypes.EventUsage, llmtypes.EventError}, "boom"},
		{"native process exited mid-turn", false, runtimeevents.KindTurnFailed,
			`{"error":"wrapper: process exited before the turn completed","reason":"process_exited","exit_code":1,` + usage + `}`,
			[]llmtypes.EventType{llmtypes.EventDelta, llmtypes.EventUsage, llmtypes.EventError}, "wrapper: process exited before the turn completed"},
		{"native failure without usage", false, runtimeevents.KindTurnFailed, `{"error":"boom"}`,
			[]llmtypes.EventType{llmtypes.EventDelta, llmtypes.EventError}, "boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := newRuntimeEventBridgeSink(&agentEventBridge{streams: NewStreamManager()}, "s-terminal", tc.acp)
			router := newSessionRouter(make(chan llmtypes.StreamEvent, 16))
			if !sink.AdmitRuntimeTurnOwner(router) {
				t.Fatal("router not admitted")
			}
			terminal := runtimeevents.Event{Kind: tc.kind, TurnID: "turn-1"}
			if tc.payload != "" {
				terminal.Payload = json.RawMessage(tc.payload)
			}
			for _, ev := range []runtimeevents.Event{
				{Kind: runtimeevents.KindTurnStarted, TurnID: "turn-1"},
				{Kind: runtimeevents.KindAgentDelta, TurnID: "turn-1", Payload: json.RawMessage(`{"content":"the reply"}`)},
				terminal,
				{Kind: runtimeevents.KindSessionIdle, TurnID: "turn-1"},
			} {
				if err := sink.Write(context.Background(), ev); err != nil {
					t.Fatalf("Write(%s): %v", ev.Kind, err)
				}
			}

			got := drainRouter(t, router)
			if !slices.Equal(streamTypes(got), tc.want) {
				t.Fatalf("router events = %v, want %v", streamTypes(got), tc.want)
			}
			for _, ev := range got {
				if ev.Type == llmtypes.EventUsage && (ev.Usage == nil || ev.Usage.InputTokens != 10 || ev.Usage.OutputTokens != 5) {
					t.Fatalf("usage = %+v, want input 10 / output 5", ev.Usage)
				}
				if ev.Type == llmtypes.EventError && ev.Error != tc.wantError {
					t.Fatalf("error = %q, want %q", ev.Error, tc.wantError)
				}
			}
			if _, live := sink.turns["turn-1"]; live {
				t.Fatal("turn-1 still mapped after its terminal")
			}
		})
	}
}

// fakeStreamingStdioClaudeScript stands in for a long-lived
// `claude -p --input-format stream-json --output-format stream-json` process:
// it answers each stdin frame with one text block and a usage-bearing result,
// then waits for the next.
const fakeStreamingStdioClaudeScript = `#!/bin/sh
echo '{"type":"system","subtype":"init","session_id":"claude-fake-0019"}'
while IFS= read -r line; do
  echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"native reply"}]}}'
  echo '{"type":"result","subtype":"success","is_error":false,"result":"native reply","usage":{"input_tokens":10,"output_tokens":5}}'
done
`

// fakeClaudeExitsMidTurnScript starts a reply to the first frame, then exits
// before its result: the wrapper closes the open turn as turn.failed with
// reason "process_exited".
const fakeClaudeExitsMidTurnScript = `#!/bin/sh
echo '{"type":"system","subtype":"init","session_id":"claude-fake-0019-exit"}'
IFS= read -r line
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"partial reply"}]}}'
exit 1
`

// fakeCodexExecTurnScript stands in for one `codex exec --json` turn: an
// agent message, then a usage-bearing turn.completed, then exit.
const fakeCodexExecTurnScript = `#!/bin/sh
echo '{"type":"thread.started","thread_id":"codex-fake-0019"}'
echo '{"type":"turn.started"}'
echo '{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"native reply"}}'
echo '{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":0,"output_tokens":5}}'
`

type nativeCLICase struct {
	name, provider, adapter, pathEnv, script string
	newAdapter                               func() provider.CLIAdapter
}

var (
	claudeStreamingStdioCase = nativeCLICase{"claude streaming-stdio", "pty-claude", "claude", "CLAUDE_CLI_PATH", fakeStreamingStdioClaudeScript,
		func() provider.CLIAdapter { return provider.NewClaudeAdapterStreamingStdio() }}
	codexSubprocessCase = nativeCLICase{"codex subprocess-per-turn", "pty-codex", "codex", "CODEX_CLI_PATH", fakeCodexExecTurnScript,
		func() provider.CLIAdapter { return provider.NewCodexAdapter() }}
)

// TestNativeCLITurn_SavesReplyAndEndsStream drives one chat turn end to end
// over the production CLI path: dispatcher, driveBootSession, the real
// provider adapter and go-agent-wrapper against a fake CLI, the normalized
// bridge sink, and back. These are the two runtimes whose replies the
// 2026-10-01 smoke lost (sessions dde00791 and bce99e49).
func TestNativeCLITurn_SavesReplyAndEndsStream(t *testing.T) {
	for _, tc := range []nativeCLICase{claudeStreamingStdioCase, codexSubprocessCase} {
		t.Run(tc.name, func(t *testing.T) {
			f, events := runNativeCLITurn(t, tc)
			if findEvent(events, "stream_end") == nil {
				t.Fatalf("no stream_end; events %+v", events)
			}
			if delta := findEvent(events, "delta"); delta == nil || delta.Content != "native reply" {
				t.Fatalf("reply delta = %+v; events %v", delta, eventTypes(events))
			}
			saved, err := f.st.GetMessage(context.Background(), nativeCLIMessageID)
			if err != nil || saved == nil {
				t.Fatalf("assistant message %q not saved: %v", nativeCLIMessageID, err)
			}
			text, _ := structuredmessage.UnwrapText(saved.Content)
			if saved.Role != "assistant" || text != "native reply" {
				t.Fatalf("saved message = role %q content %q", saved.Role, saved.Content)
			}
		})
	}
}

// A child that exits mid-turn surfaces as the wrapper's process_exited
// turn.failed: an error on the stream, which then closes, rather than a turn
// left open for the idle timeout.
func TestNativeCLITurn_ProcessExitMidTurnEndsStreamWithError(t *testing.T) {
	tc := claudeStreamingStdioCase
	tc.script = fakeClaudeExitsMidTurnScript
	_, events := runNativeCLITurn(t, tc)
	errEvent := findEvent(events, "error")
	if errEvent == nil || errEvent.StructuredError == nil {
		t.Fatalf("no structured error event; events %+v", events)
	}
	if raw := errEvent.StructuredError.Details["raw"]; raw != "wrapper: process exited before the turn completed" {
		t.Fatalf("error raw = %v, want the wrapper's process_exited failure", raw)
	}
	if findEvent(events, "stream_end") != nil {
		t.Fatalf("stream_end after a failed turn; events %v", eventTypes(events))
	}
}

const nativeCLIMessageID = "assistant-native-cli"

// newNativeCLIFixture wires a characterization fixture for tc's CLI provider
// over the production runtime path: the real provider adapter and
// go-agent-wrapper against tc's fake CLI, and the normalized bridge sink.
func newNativeCLIFixture(t *testing.T, tc nativeCLICase) *characterizationFixture {
	t.Helper()
	scriptPath := filepath.Join(t.TempDir(), "fake-"+tc.adapter+".sh")
	if err := os.WriteFile(scriptPath, []byte(tc.script), 0o755); err != nil { //nolint:gosec // the stand-in CLI binary must be executable, in t.TempDir()
		t.Fatalf("write fake %s: %v", tc.adapter, err)
	}
	t.Setenv(tc.pathEnv, scriptPath)

	f := newCharacterizationFixture(t, nil)
	ctx := context.Background()
	session, err := f.st.GetSession(ctx, f.session)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	session.Provider = tc.provider
	session.Model = tc.adapter + "-cli"
	if updateErr := f.st.UpdateSession(ctx, session); updateErr != nil {
		t.Fatalf("UpdateSession: %v", updateErr)
	}

	bridge := &agentEventBridge{streams: f.svc.streams}
	deps := &runtimeagent.Dependencies{
		Agents: &fakeAgentProfilesResolver{profile: &store.AgentProfile{
			ID: "agent-characterization", Slug: "characterization-agent", DefaultProvider: tc.adapter,
		}},
		Manager:    runtimeagent.NewSessionManager(),
		Store:      &agentRuntimeStore{store: f.st},
		PathGrants: permission.NewPathGrants(),
		NativeCLIAdapter: func(id runtimes.ID) provider.CLIAdapter {
			if string(id) != tc.adapter {
				return nil
			}
			return tc.newAdapter()
		},
		// The production factory: every runtime, native or ACP, gets this sink.
		RuntimeEventSink: func(sessionID string, isACP bool) runtimeevents.Sink {
			return newRuntimeEventBridgeSink(bridge, sessionID, isACP)
		},
		TypedEventCallback: bridge.typedCallback,
		WorkspacesRoot:     t.TempDir(),
	}
	t.Cleanup(func() { _ = deps.Manager.Shutdown(context.Background()) })
	f.svc.agentDeps = deps
	f.svc.agentEventBridge = bridge
	f.svc.activeSessions = deps.Manager

	return f
}

// runNativeCLITurn runs one chat turn for tc and returns the fixture and the
// turn stream's events. It fails if the stream stays open.
func runNativeCLITurn(t *testing.T, tc nativeCLICase) (*characterizationFixture, []chat.StreamEvent) {
	t.Helper()
	f := newNativeCLIFixture(t, tc)
	ctx := context.Background()

	producer := f.svc.streams.CreateStream(nativeCLIMessageID, f.session)
	consumer, ok := f.svc.streams.GetStream(nativeCLIMessageID)
	if !ok {
		t.Fatal("GetStream: production stream was not registered")
	}
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	runErr := make(chan error, 1)
	go func() {
		runErr <- f.svc.dispatcher.Run(runCtx, dispatcher.Request{
			SessionID: f.session, AssistantMsgID: nativeCLIMessageID, UserContent: "say something", CallerType: dispatcher.CallerChat,
		}, producer)
	}()

	var events []chat.StreamEvent
	deadline := time.After(20 * time.Second)
	for streamOpen := true; streamOpen; {
		select {
		case ev, open := <-consumer:
			if !open {
				streamOpen = false
				break
			}
			events = append(events, ev)
		case <-deadline:
			t.Fatalf("turn stream still open after 20s; events %v", eventTypes(events))
		}
	}
	select {
	case <-runErr:
		// A failed turn may report its error here as well as on the stream;
		// callers assert on the stream.
	case <-time.After(5 * time.Second):
		t.Fatal("Dispatcher.Run did not return after the stream closed")
	}
	return f, events
}
