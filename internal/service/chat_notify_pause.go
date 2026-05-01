package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/chat"
)

// devToolPrefix is the namespace covered by the trust-agent notify-pause
// middleware. Per CW-20260430-0009 only the local dev_* surface (file IO,
// shell exec, grep) goes through the placeholder UI — every other tool
// either has its own permission/approval path (e.g. plugin tools through
// the existing permission engine) or is the agent's structured surface
// (nanite_*, self-tools).
const devToolPrefix = "dev_"

// notifyPauseDefaultDelay is the brief pause window between emitting the
// notify-pause envelope and proceeding with the tool call. Default 1.5s
// per Q5 of the locked design — long enough to meaningfully click cancel,
// short enough not to feel like a gate.
//
// Override via NewChatService -> notifyPauseDelay if a future codepath
// wants to disable it (set to 0) without removing the wiring.
const notifyPauseDefaultDelay = 1500 * time.Millisecond

// notifyPauseTimeoutFloor is the upper bound on how long we'll wait for
// a cancel before proceeding even if a stale ctx remains. Defensive — if
// a chat session goroutine has been parked for some reason we don't want
// the pause to wedge a tool call indefinitely.
const notifyPauseTimeoutFloor = 5 * time.Second

// notifyPausePayload is the JSON shape carried on the SSE Data field for
// `notify_pause` stream events. Documented here so frontend / toast
// upgrade work has a single canonical reference.
//
// When CW-20260501-0003's toast primitive lands, the FE swap is to read
// `payload.tool` + `payload.path` and render a toast with a cancel
// button instead of an inline chat-stream message — backend signal
// stays identical.
type notifyPausePayload struct {
	ToolID     string `json:"tool_id"`
	Tool       string `json:"tool"`
	Path       string `json:"path,omitempty"`
	Detail     string `json:"detail,omitempty"`
	DelayMS    int64  `json:"delay_ms"`
	CancelHint string `json:"cancel_hint"`
}

// emitNotifyPause emits a placeholder inline chat-stream message of the
// shape `notify_pause` so the user sees that a dev_* tool is about to
// run. Returns true if the user (or the surrounding ctx) cancels during
// the pause window; false if the call should proceed.
//
// The contract:
//
//   - The pause is brief (notifyPauseDefaultDelay, 1.5s default).
//   - A ctx.Err() check at the end of the window is the cancel signal —
//     when CW-20260501-0003 wires the toast cancel button, the FE
//     handler will cancel the per-message ctx (or set a session-scoped
//     "cancel pending tool" flag). Until then the pause is visible-only:
//     the user can read what's about to happen and the chat agent gets a
//     chance to surface the action before it lands.
//   - We do NOT block on ctx.Done() longer than notifyPauseTimeoutFloor —
//     a hung session should not wedge a tool call for hours.
//
// `mu` is the same shared mutex `executeSingleTool` uses to serialize
// SSE writes when the call is concurrent-safe; nil for serial execution.
func emitNotifyPause(
	ctx context.Context,
	tu provider.ToolUseBlock,
	ch chan chat.StreamEvent,
	mu *sync.Mutex,
	delay time.Duration,
) (cancelled bool) {
	if !shouldNotifyPause(tu.Name) {
		return false
	}
	if delay <= 0 {
		delay = notifyPauseDefaultDelay
	}
	if delay > notifyPauseTimeoutFloor {
		delay = notifyPauseTimeoutFloor
	}

	payload := notifyPausePayload{
		ToolID:     tu.ID,
		Tool:       tu.Name,
		Path:       firstNonEmptyPath(tu.Input),
		Detail:     toolCallDetail(tu.Name, tu.Input),
		DelayMS:    delay.Milliseconds(),
		CancelHint: "Cancel within ~1.5s to skip this call.",
	}
	data, err := json.Marshal(payload)
	if err != nil {
		// Should never happen with a static struct — log defensively
		// and proceed without the pause.
		slog.Warn("chat-service: notify-pause marshal failed", "err", err, "tool", tu.Name)
		return false
	}

	if mu != nil {
		mu.Lock()
	}
	ch <- chat.StreamEvent{
		Type:   "notify_pause",
		Tool:   tu.Name,
		ToolID: tu.ID,
		Data:   string(data),
	}
	if mu != nil {
		mu.Unlock()
	}

	// Pause briefly. Two ways out:
	//  - ctx.Done() fired → user cancelled (or session ended).
	//  - timer expired → proceed.
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		slog.Info("chat-service: notify-pause cancelled by ctx",
			"tool", tu.Name, "tool_id", tu.ID, "err", ctx.Err())
		return true
	case <-timer.C:
		return false
	}
}

// shouldNotifyPause returns true when tool name is part of the dev_*
// surface that the trust-agent middleware covers.
func shouldNotifyPause(name string) bool {
	if !strings.HasPrefix(name, devToolPrefix) {
		return false
	}
	// dev_grep is read-only and noisy (the agent runs it constantly
	// during code search). The notify-pause is meant to surface
	// state-changing or system-impact actions; making the agent visibly
	// pause every grep would be the same "rule-following defendant"
	// pattern docs/architecture/agent-context-architecture.md warns
	// against. The path-grant + AllowedPaths gate still runs.
	switch name {
	case "dev_grep", "dev_glob":
		return false
	}
	return true
}

// firstNonEmptyPath extracts the first path-like input field from a
// dev_* tool call (path / file_path / directory / working_dir) so the
// notify-pause UI can show "Reading <path>..." rather than just the tool
// name. Returns "" when no recognised field is present.
func firstNonEmptyPath(input map[string]any) string {
	for _, key := range []string{"path", "file_path", "directory", "working_dir"} {
		if v, ok := input[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}
