package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/lifecycle"
	"github.com/hollis-labs/nanite/internal/structuredmessage"
)

// CW-20261001-0072: a turn sent while another is running queues behind it
// and reaches the agent at the next turn boundary. It does not cancel the
// running turn, whose reply is saved; then the queued turn runs and replies.

// fakeGatedClaudeScript is a long-lived streaming-stdio claude whose first
// turn holds until the test releases it, so a second turn can be posted while
// the first is in flight. It records every stdin frame, answers frame n with
// "reply n", and marks when the first frame arrived. %[1]s is a scratch dir.
const fakeGatedClaudeScript = `#!/bin/sh
echo '{"type":"system","subtype":"init","session_id":"claude-fake-0072"}'
n=0
while IFS= read -r line; do
  n=$((n+1))
  printf '%%s\n' "$line" >> "%[1]s/frames"
  if [ "$n" -eq 1 ]; then
    : > "%[1]s/turn1-started"
    while [ ! -f "%[1]s/release-turn1" ]; do sleep 0.05; done
  fi
  echo "{\"type\":\"assistant\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"reply $n\"}]}}"
  echo "{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"reply $n\",\"usage\":{\"input_tokens\":10,\"output_tokens\":5}}"
done
`

func TestSteering_TurnSentMidRunQueuesAndBothReply(t *testing.T) {
	dir := t.TempDir()
	tc := claudeStreamingStdioCase
	tc.script = fmt.Sprintf(fakeGatedClaudeScript, dir)
	f := newNativeCLIFixture(t, tc)
	owner := lifecycle.NewManager("test.steering-queue")
	t.Cleanup(func() { _ = owner.Shutdown(5 * time.Second) })
	f.svc.lifecycle = owner
	f.svc.activeGen = make(map[string]*inFlightGen)
	ctx := context.Background()

	first, err := f.svc.HandleMessage(ctx, f.session, "first: summarize the docs")
	if err != nil {
		t.Fatalf("HandleMessage(first): %v", err)
	}
	firstStream := subscribe(t, f, first)
	waitForFile(t, filepath.Join(dir, "turn1-started"))

	second, err := f.svc.HandleMessage(ctx, f.session, "second: steering update")
	if err != nil {
		t.Fatalf("HandleMessage(second): %v", err)
	}
	secondStream := subscribe(t, f, second)

	// The second turn is queued, not delivered and not interrupting: the
	// running turn still holds the agent.
	time.Sleep(200 * time.Millisecond)
	if frames := readLines(t, filepath.Join(dir, "frames")); len(frames) != 1 {
		t.Fatalf("agent received %d frames while turn 1 ran, want 1: %q", len(frames), frames)
	}
	if err := os.WriteFile(filepath.Join(dir, "release-turn1"), nil, 0o600); err != nil {
		t.Fatalf("release turn 1: %v", err)
	}

	for _, turn := range []struct {
		msgID  string
		stream <-chan chat.StreamEvent
		reply  string
	}{{first, firstStream, "reply 1"}, {second, secondStream, "reply 2"}} {
		events := drainTurnStream(t, turn.stream)
		if findEvent(events, "stream_end") == nil {
			t.Fatalf("turn %s: no stream_end; events %+v", turn.msgID, events)
		}
		if delta := findEvent(events, "delta"); delta == nil || delta.Content != turn.reply {
			t.Fatalf("turn %s: reply delta = %+v, want %q; events %v", turn.msgID, delta, turn.reply, eventTypes(events))
		}
		saved, err := f.st.GetMessage(ctx, turn.msgID)
		if err != nil || saved == nil {
			t.Fatalf("turn %s: assistant message not saved: %v", turn.msgID, err)
		}
		if text, _ := structuredmessage.UnwrapText(saved.Content); text != turn.reply {
			t.Fatalf("turn %s: saved %q, want %q", turn.msgID, saved.Content, turn.reply)
		}
		if strings.Contains(saved.Metadata, "interrupted") {
			t.Fatalf("turn %s: saved as interrupted: %s", turn.msgID, saved.Metadata)
		}
	}

	// The agent saw the steering message as its own next turn, after the
	// first turn finished.
	frames := readLines(t, filepath.Join(dir, "frames"))
	if len(frames) != 2 || !strings.Contains(frames[0], "summarize the docs") || !strings.Contains(frames[1], "steering update") {
		t.Fatalf("agent frames = %q, want the first turn then the steering turn", frames)
	}
}

func subscribe(t *testing.T, f *characterizationFixture, msgID string) <-chan chat.StreamEvent {
	t.Helper()
	ch, ok := f.svc.streams.GetStream(msgID)
	if !ok {
		t.Fatalf("no stream for %s", msgID)
	}
	return ch
}

func drainTurnStream(t *testing.T, ch <-chan chat.StreamEvent) []chat.StreamEvent {
	t.Helper()
	var events []chat.StreamEvent
	deadline := time.After(20 * time.Second)
	for {
		select {
		case ev, open := <-ch:
			if !open {
				return events
			}
			events = append(events, ev)
		case <-deadline:
			t.Fatalf("stream still open after 20s; events %v", eventTypes(events))
		}
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // a file the fake CLI wrote in this test's own t.TempDir()
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

// fakeStoppableClaudeScript starts the first turn with some output, then holds
// it until the process is stopped. Like Claude mid-turn, it does not exit on
// stdin EOF. It records a SIGTERM before exiting on it, which a SIGKILL never
// lets it do. %[1]s is a scratch dir.
const fakeStoppableClaudeScript = `#!/bin/sh
trap ': > "%[1]s/got-sigterm"; exit 0' TERM
echo '{"type":"system","subtype":"init","session_id":"claude-fake-0072-stop"}'
while IFS= read -r line; do
  printf '%%s\n' "$line" >> "%[1]s/frames"
  echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"partial work"}]}}'
  : > "%[1]s/turn1-started"
  while :; do sleep 0.05; done
done
`

// A user stop is the explicit interrupt: it cancels the running turn and the
// turn queued behind it. The running turn's output so far is saved, marked
// interrupted; the queued turn never reaches the agent. A native runtime has no
// turn cancel, so Nanite stops the process, and the child gets its SIGTERM
// grace rather than an immediate SIGKILL.
func TestSteering_UserStopSavesInterruptedTurnAndDropsQueuedTurn(t *testing.T) {
	dir := t.TempDir()
	tc := claudeStreamingStdioCase
	tc.script = fmt.Sprintf(fakeStoppableClaudeScript, dir)
	f := newNativeCLIFixture(t, tc)
	owner := lifecycle.NewManager("test.steering-stop")
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

	second, err := f.svc.HandleMessage(ctx, f.session, "second: queued")
	if err != nil {
		t.Fatalf("HandleMessage(second): %v", err)
	}
	secondStream := subscribe(t, f, second)

	if !f.svc.CancelActiveGeneration(f.session) {
		t.Fatal("CancelActiveGeneration found no active generation")
	}
	_ = drainTurnStream(t, firstStream)
	secondEvents := drainTurnStream(t, secondStream)
	if delta := findEvent(secondEvents, "delta"); delta != nil {
		t.Fatalf("queued turn produced output after the stop: %+v", delta)
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
	if frames := readLines(t, filepath.Join(dir, "frames")); len(frames) != 1 {
		t.Fatalf("agent received %d frames, want only the first turn: %q", len(frames), frames)
	}
	if msg, err := f.st.GetMessage(ctx, second); err == nil && msg != nil {
		t.Fatalf("queued turn saved a reply after the stop: %q", msg.Content)
	}
	waitForFile(t, filepath.Join(dir, "got-sigterm"))
}
