package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
)

// TestShouldNotifyPause covers Q5 of CW-20260430-0009 — only the
// dev_* surface (and not its read-only discovery helpers) triggers the
// notify-pause middleware.
func TestShouldNotifyPause(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"dev_read", true},
		{"dev_write", true},
		{"dev_edit", true},
		{"dev_bash", true},
		{"dev_grep", false}, // exempted — read-only discovery
		{"dev_glob", false}, // exempted — read-only discovery
		{"card_show", false},
		{"fetch_tool_result", false},
		{"plugin_giphy_search", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldNotifyPause(tc.name); got != tc.want {
				t.Errorf("shouldNotifyPause(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestEmitNotifyPause_EmitsPlaceholderEnvelope confirms the middleware
// emits a `notify_pause` SSE event with the expected JSON payload.
func TestEmitNotifyPause_EmitsPlaceholderEnvelope(t *testing.T) {
	tu := llmtypes.ToolUseBlock{
		ID:   "call-1",
		Name: "dev_read",
		Input: map[string]any{
			"path": "/tmp/foo.txt",
		},
	}
	ch := make(chan chat.StreamEvent, 4)

	// Short delay so the test runs fast. 60ms is comfortably above the
	// scheduler's poll resolution and well under the floor.
	canceled := emitNotifyPause(context.Background(), tu, ch, nil, 60*time.Millisecond)
	if canceled {
		t.Fatal("expected proceed (not canceled) on a non-canceled ctx")
	}
	close(ch)

	var got *chat.StreamEvent
	for ev := range ch {
		if ev.Type == "notify_pause" {
			ev := ev
			got = &ev
			break
		}
	}
	if got == nil {
		t.Fatal("expected a notify_pause event")
	}
	if got.Tool != "dev_read" || got.ToolID != "call-1" {
		t.Errorf("event Tool/ToolID mismatch: got %+v", got)
	}
	if got.Data == "" {
		t.Fatal("expected JSON payload on Data")
	}
	var payload notifyPausePayload
	if err := json.Unmarshal([]byte(got.Data), &payload); err != nil {
		t.Fatalf("invalid JSON payload: %v", err)
	}
	if payload.Tool != "dev_read" {
		t.Errorf("payload.Tool = %q, want dev_read", payload.Tool)
	}
	if payload.Path != "/tmp/foo.txt" {
		t.Errorf("payload.Path = %q, want /tmp/foo.txt", payload.Path)
	}
	if payload.DelayMS <= 0 {
		t.Errorf("payload.DelayMS should be positive, got %d", payload.DelayMS)
	}
}

// TestEmitNotifyPause_SkipsNonDevTools ensures the middleware is a
// no-op for tools outside the dev_* surface — the agent's structured
// surface (nanite_*) and plugin tools do NOT pause.
func TestEmitNotifyPause_SkipsNonDevTools(t *testing.T) {
	tu := llmtypes.ToolUseBlock{
		ID:   "call-2",
		Name: "card_show",
	}
	ch := make(chan chat.StreamEvent, 4)
	canceled := emitNotifyPause(context.Background(), tu, ch, nil, 60*time.Millisecond)
	close(ch)

	if canceled {
		t.Fatal("non-dev tool should never report canceled")
	}
	for ev := range ch {
		if ev.Type == "notify_pause" {
			t.Errorf("expected no notify_pause for %s; got event %+v", tu.Name, ev)
		}
	}
}

// TestEmitNotifyPause_CancelsOnCtx confirms a ctx.Done() during the
// pause window returns canceled=true. This is the wiring path the
// CW-20260501-0003 toast cancel button will hit (it cancels the
// per-message ctx).
func TestEmitNotifyPause_CancelsOnCtx(t *testing.T) {
	tu := llmtypes.ToolUseBlock{
		ID:    "call-3",
		Name:  "dev_write",
		Input: map[string]any{"path": "/tmp/foo.txt"},
	}
	ch := make(chan chat.StreamEvent, 4)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel almost immediately — well before the 1.5s default would
	// expire, but after the pause loop entered the select.
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	canceled := emitNotifyPause(ctx, tu, ch, nil, 1500*time.Millisecond)
	close(ch)
	if !canceled {
		t.Fatal("expected canceled=true when ctx is canceled during the pause window")
	}
}

// TestEmitNotifyPause_HonorsMutex confirms that when a serializing
// mutex is supplied (concurrent execution path), the notify_pause SSE
// write does not race with the caller's tool_call write.
func TestEmitNotifyPause_HonorsMutex(t *testing.T) {
	tu := llmtypes.ToolUseBlock{
		ID:    "call-4",
		Name:  "dev_read",
		Input: map[string]any{"path": "/tmp/foo.txt"},
	}
	ch := make(chan chat.StreamEvent, 4)
	var mu sync.Mutex
	// Caller is "holding" the mutex while writing the tool_call event.
	// emitNotifyPause must wait its turn on the same mutex.
	mu.Lock()
	done := make(chan struct{})
	go func() {
		emitNotifyPause(context.Background(), tu, ch, &mu, 30*time.Millisecond)
		close(done)
	}()
	// Send a competing event under the lock to ensure ordering.
	ch <- chat.StreamEvent{Type: "tool_call", Tool: tu.Name, ToolID: tu.ID}
	mu.Unlock()
	<-done
	close(ch)

	// Drain — ordering is enforced by the mutex; we just want to
	// confirm both events landed (no deadlock, no panic).
	var seenToolCall, seenPause bool
	for ev := range ch {
		switch ev.Type {
		case "tool_call":
			seenToolCall = true
		case "notify_pause":
			seenPause = true
		}
	}
	if !seenToolCall || !seenPause {
		t.Errorf("missing events: tool_call=%v notify_pause=%v", seenToolCall, seenPause)
	}
}
