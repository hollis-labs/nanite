package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/lifecycle"
	"github.com/hollis-labs/nanite/internal/structuredmessage"
	runtimeevents "github.com/hollis-labs/substrate/harness/adapters/runtimeevents"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

// CW-20261001-0168: since go-agent-wrapper v0.22.0 a native streaming Claude
// turn can be interrupted (CancelTurn: Claude's stream-json control_request
// interrupt) and the process stays up. A user stop now uses it instead of
// stopping the process.

// fakeInterruptibleClaudeScript is a long-lived streaming-stdio claude that
// honors the interrupt the way the recorded claude 2.1.286 transcript does
// (go-providers providertest/fixtures/claude/stream_interrupt.transcript.jsonl):
// it answers a control_request with a success control_response and ends the
// open turn with an error_during_execution result. The first user turn
// streams some text and stays open; later turns reply at once. The turn count
// lives in a file, so a process cold-booted after a stop carries on from it
// instead of starting over. Every user frame appends "<pid> <n>" to frames,
// and a SIGTERM is recorded before exiting, so the test can tell the same
// process ran both turns and was never stopped. %[1]s is a scratch dir; %[2]s is how many seconds the aborted
// result takes after the acknowledgement, as when a running tool is slow to
// stop.
const fakeInterruptibleClaudeScript = `#!/bin/sh
trap ': > "%[1]s/got-sigterm"; exit 0' TERM
echo '{"type":"system","subtype":"init","session_id":"claude-fake-0168"}'
busy=
while IFS= read -r line; do
  case "$line" in
    *'"control_request"'*)
      id=$(printf '%%s' "$line" | sed -n 's/.*"request_id":"\([^"]*\)".*/\1/p')
      echo "{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":\"$id\",\"response\":{\"still_queued\":[]}}}"
      if [ -n "$busy" ]; then
        busy=
        sleep %[2]s
        echo '{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_streaming","stop_reason":"end_turn","errors":["aborted by interrupt"],"usage":{"input_tokens":3,"output_tokens":1}}'
      fi
      ;;
    *)
      n=$(cat "%[1]s/turns" 2>/dev/null || echo 0)
      n=$((n+1))
      echo "$n" > "%[1]s/turns"
      printf '%%s %%s\n' "$$" "$n" >> "%[1]s/frames"
      if [ "$n" -eq 1 ]; then
        echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"partial work"}]}}'
        busy=1
        : > "%[1]s/turn1-started"
      else
        echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"reply after the stop"}]}}'
        echo '{"type":"result","subtype":"success","is_error":false,"result":"reply after the stop","usage":{"input_tokens":10,"output_tokens":5}}'
      fi
      ;;
  esac
done
`

// A user stop on a native streaming Claude turn interrupts the turn and keeps
// the process: the partial output is saved as interrupted, no error reaches
// the stream, and the next turn runs on the same process.
func TestUserStopInterruptsNativeClaudeTurnAndKeepsTheProcess(t *testing.T) {
	runUserStopKeepsProcess(t, "0")
}

// The interrupted turn's result can lag the acknowledgement, as when a
// running tool is slow to stop. That is still an interrupt: the stop must not
// give up on it and fall back to stopping the process. 3s is past the 2s ACP
// cancel bound and inside the native budget (runtimeStopMaxWait).
func TestUserStopWaitsOutASlowInterruptWithoutKillingTheProcess(t *testing.T) {
	runUserStopKeepsProcess(t, "3")
}

func runUserStopKeepsProcess(t *testing.T, abortDelaySeconds string) {
	t.Helper()
	dir := t.TempDir()
	tc := claudeStreamingStdioCase
	tc.script = fmt.Sprintf(fakeInterruptibleClaudeScript, dir, abortDelaySeconds)
	f := newNativeCLIFixture(t, tc)
	owner := lifecycle.NewManager("test.cancel-turn")
	t.Cleanup(func() { _ = owner.Shutdown(5 * time.Second) })
	f.svc.lifecycle = owner
	f.svc.activeGen = make(map[string]*inFlightGen)
	ctx := context.Background()

	first, err := f.svc.HandleMessage(ctx, f.session, "first: long task")
	if err != nil {
		t.Fatalf("HandleMessage(first): %v", err)
	}
	firstStream := subscribe(t, f, first)
	waitForFile(t, filepath.Join(dir, "turn1-started"))
	// Let the partial output cross the runtime bridge into the chat loop.
	time.Sleep(300 * time.Millisecond)

	if !f.svc.CancelActiveGeneration(f.session) {
		t.Fatal("CancelActiveGeneration found no active generation")
	}
	firstEvents := drainTurnStream(t, firstStream)
	if errEvent := findEvent(firstEvents, "error"); errEvent != nil {
		t.Fatalf("the interrupt surfaced as an error: %+v", errEvent)
	}
	saved, err := f.st.GetMessage(ctx, first)
	if err != nil || saved == nil {
		t.Fatalf("interrupted turn not saved: %v", err)
	}
	if text, _ := structuredmessage.UnwrapText(saved.Content); !strings.Contains(text, "partial work") {
		t.Fatalf("interrupted turn saved %q, want its partial output", saved.Content)
	}
	if !strings.Contains(saved.Metadata, `"interrupted":true`) {
		t.Fatalf("interrupted turn metadata = %s, want interrupted", saved.Metadata)
	}

	second, err := f.svc.HandleMessage(ctx, f.session, "second: after the stop")
	if err != nil {
		t.Fatalf("HandleMessage(second): %v", err)
	}
	secondEvents := drainTurnStream(t, subscribe(t, f, second))
	if findEvent(secondEvents, "stream_end") == nil {
		t.Fatalf("second turn: no stream_end; events %+v", secondEvents)
	}
	if delta := findEvent(secondEvents, "delta"); delta == nil || delta.Content != "reply after the stop" {
		t.Fatalf("second turn reply delta = %+v; events %v", delta, eventTypes(secondEvents))
	}

	// One process ran both turns, and nothing stopped it.
	frames := readLines(t, filepath.Join(dir, "frames"))
	if len(frames) != 2 {
		t.Fatalf("agent frames = %q, want the two user turns", frames)
	}
	pid1, pid2 := strings.Fields(frames[0])[0], strings.Fields(frames[1])[0]
	if pid1 != pid2 {
		t.Fatalf("turn 1 ran in pid %s, turn 2 in pid %s; want one process kept across the stop", pid1, pid2)
	}
	if _, err := os.Stat(filepath.Join(dir, "got-sigterm")); err == nil {
		t.Fatal("the agent process got SIGTERM; a stop should interrupt the turn, not the process")
	}
}

// A turn.failed that CancelTurn caused carries reason "interrupted". It is
// an interrupt, not an error: the chat router gets usage marked interrupted,
// then Done.
func TestRuntimeEventBridgeSink_InterruptedTurnIsNotAnError(t *testing.T) {
	sink := newRuntimeEventBridgeSink(&agentEventBridge{streams: NewStreamManager()}, "s-interrupted", false)
	router := newSessionRouter(make(chan llmtypes.StreamEvent, 16))
	if !sink.AdmitRuntimeTurnOwner(router) {
		t.Fatal("router not admitted")
	}
	for _, ev := range []runtimeevents.Event{
		{Kind: runtimeevents.KindTurnStarted, TurnID: "turn-1"},
		{Kind: runtimeevents.KindAgentDelta, TurnID: "turn-1", Payload: json.RawMessage(`{"content":"partial"}`)},
		{Kind: runtimeevents.KindTurnFailed, TurnID: "turn-1", Payload: json.RawMessage(`{"error":"aborted by interrupt","reason":"interrupted","usage":{"InputTokens":3,"OutputTokens":1}}`)},
	} {
		if err := sink.Write(context.Background(), ev); err != nil {
			t.Fatalf("Write(%s): %v", ev.Kind, err)
		}
	}
	got := drainRouter(t, router)
	types := streamTypes(got)
	if len(types) != 3 || types[0] != llmtypes.EventDelta || types[1] != llmtypes.EventUsage || types[2] != llmtypes.EventDone {
		t.Fatalf("router events = %v, want delta, usage, done", types)
	}
	if got[1].Usage == nil || got[1].Usage.StopReason != "interrupted" || got[1].Usage.OutputTokens != 1 {
		t.Fatalf("usage = %+v, want the turn's usage marked interrupted", got[1].Usage)
	}
}

// The runtime feed shows an interrupted turn as interrupted, not failed.
func TestProjectHostRuntimePayloadInterruptedTurnIsNotFailed(t *testing.T) {
	raw, _, _ := projectHostRuntimePayload(runtimeevents.KindTurnFailed, false,
		json.RawMessage(`{"error":"aborted by interrupt","reason":"interrupted"}`))
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode projection: %v", err)
	}
	if got["interrupted"] != true || got["terminal"] != true || got["failed"] != nil {
		t.Fatalf("interrupted turn projection = %s, want terminal and interrupted, not failed", raw)
	}
}

// An interrupted turn the chat loop did not cancel itself (its usage carries
// the interrupted stop reason, then Done) is saved as interrupted too.
func TestFinalizeRun_InterruptedStopReasonMarksTheTurnInterrupted(t *testing.T) {
	f := newCharacterizationFixture(t, []characterizationProviderStep{{events: []llmtypes.StreamEvent{
		{Type: llmtypes.EventDelta, Content: "partial"},
		{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{StopReason: "interrupted", OutputTokens: 1}},
		{Type: llmtypes.EventDone},
	}}})
	events := f.run(t, "assistant-interrupted")
	if errEvent := findEvent(events, "error"); errEvent != nil {
		t.Fatalf("interrupted turn surfaced an error: %+v", errEvent)
	}
	saved, err := f.st.GetMessage(context.Background(), "assistant-interrupted")
	if err != nil || saved == nil {
		t.Fatalf("interrupted turn not saved: %v", err)
	}
	if !strings.Contains(saved.Metadata, `"interrupted":true`) {
		t.Fatalf("metadata = %s, want interrupted", saved.Metadata)
	}
}
