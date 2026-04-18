package service

// CW-20260418-0043 DIAGNOSTIC — REMOVE AFTER ROOT CAUSE CONFIRMED.
//
// This file adds disposable instrumentation to chase an intermittent chat
// stall after ~7 tool calls. Leading hypothesis: bare `ch <- evt` sends
// block when the SSE reader falls behind, filling the 128-event buffer and
// deadlocking the generateResponse goroutine.
//
// Checks:
//   1. Effective loop config at start (ToolPerTurnCap, caps, buffer size).
//   2. len(ch) at every outer-loop iteration top.
//   3. Watchdog around critical ch sends — logs at 100ms/1s/5s/30s if the
//      send hasn't completed. Cheapest way to positively identify a blocked
//      send without changing behavior.
//   4. Outer-loop exit site + reason.
//   5. Provider stream open/close duration + event count.
//   6. Defer cleanup confirmation.
//
// The follow-up BLG-20260418-012 replaces all of this with a first-class
// LoopEvent emitter (slog + debug-SSE fanout). These helpers are scaffolding
// until then. Delete the whole file once the stall root cause is pinned down.

import (
	"context"
	"log/slog"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
)

// diagWatchChSend starts a watchdog goroutine that logs if a ch send takes
// unusually long. Call like:
//
//	done := diagWatchChSend(ctx, "streamLoop.delta", ch, sessionID, msgID, iter, "delta")
//	ch <- chat.StreamEvent{Type: "delta", Content: ...}
//	close(done)
//
// Cheap when the send is fast (goroutine exits on `done` before firing any
// timer). When the send blocks, progressively noisier: warn at 100ms,
// error at 1s, error at 5s, error at 30s. After 30s we stop logging — the
// log has already said everything it's going to say.
func diagWatchChSend(ctx context.Context, site string, ch chan chat.StreamEvent, sessionID, msgID string, iter int, evtType string) chan struct{} {
	done := make(chan struct{})
	go func() {
		thresholds := []struct {
			after time.Duration
			level slog.Level
		}{
			{100 * time.Millisecond, slog.LevelWarn},
			{1 * time.Second, slog.LevelError},
			{5 * time.Second, slog.LevelError},
			{30 * time.Second, slog.LevelError},
		}
		start := time.Now()
		for _, t := range thresholds {
			select {
			case <-done:
				return
			case <-ctx.Done():
				slog.Warn("chat-loop-diag: ctx cancelled while send pending",
					"site", site, "waited_ms", time.Since(start).Milliseconds(),
					"ch_len", len(ch), "ch_cap", cap(ch),
					"session_id", sessionID, "msg_id", msgID, "iter", iter, "event_type", evtType)
				return
			case <-time.After(t.after - time.Since(start)):
				slog.LogAttrs(ctx, t.level, "chat-loop-diag: ch send slow / possibly blocked",
					slog.String("site", site),
					slog.Int64("waited_ms", time.Since(start).Milliseconds()),
					slog.Int("ch_len", len(ch)),
					slog.Int("ch_cap", cap(ch)),
					slog.String("session_id", sessionID),
					slog.String("msg_id", msgID),
					slog.Int("iter", iter),
					slog.String("event_type", evtType),
				)
			}
		}
	}()
	return done
}

// diagLogLoopStart logs the resolved loop config once per generateResponse.
// Surfaces the effective ToolPerTurnCap (theory: if set to 7 in user
// settings, that alone could explain the stall) and the loop limits so we
// know whether any are hit during a repro.
func diagLogLoopStart(sessionID, msgID, agentID string, ls *loopState, chCap int) {
	slog.Info("chat-loop-diag: loop start",
		"session_id", sessionID,
		"msg_id", msgID,
		"agent_id", agentID,
		"max_turns", ls.resolvedMaxTurns(),
		"hard_ceiling", ls.limits.hardCeiling,
		"runaway_fail_cap", ls.limits.runawayFailCap,
		"consecutive_fail_cap", ls.limits.consecutiveFailCap,
		"idle_timeout_s", int(ls.limits.idleTimeout.Seconds()),
		"tool_per_turn_cap", ls.limits.defaultPerToolCap,
		"ch_cap", chCap,
	)
}

// diagLogIterStart logs iteration top + current channel occupancy. Cheap
// and prints a heartbeat per iteration; if the loop is progressing we see
// these tick. If ch_len trends toward cap(ch), that's the smoking gun.
func diagLogIterStart(ctx context.Context, sessionID, msgID string, iter int, ch chan chat.StreamEvent) {
	deadline, ok := ctx.Deadline()
	remaining := int64(-1)
	if ok {
		remaining = int64(time.Until(deadline).Seconds())
	}
	slog.Info("chat-loop-diag: iter start",
		"session_id", sessionID,
		"msg_id", msgID,
		"iter", iter,
		"ch_len", len(ch),
		"ch_cap", cap(ch),
		"ctx_deadline_remaining_s", remaining,
	)
}

// diagLogLoopExit logs every exit path with a reason. Pair with the existing
// shouldStop logs (which cover only the iteration-limit exits) to catch the
// other exit sites: `stopReason != tool_use`, `max_tokens`, `circuit_open`,
// `plugin_cancel`, `ctx_timeout`, `break streamLoop`, etc.
func diagLogLoopExit(sessionID, msgID string, iter int, reason string, toolCallCount int, ch chan chat.StreamEvent) {
	slog.Info("chat-loop-diag: loop exit",
		"session_id", sessionID,
		"msg_id", msgID,
		"iter", iter,
		"reason", reason,
		"tool_call_count", toolCallCount,
		"ch_len", len(ch),
		"ch_cap", cap(ch),
	)
}

// diagLogProviderStream logs the duration and event count of a provider
// stream (the inner `for evt := range provCh` loop). If provCh never closes,
// we'd never log this — absence of this log line during a stall is itself
// a signal that the provider hung.
func diagLogProviderStream(sessionID, msgID string, iter int, duration time.Duration, eventCount int, stopReason string, toolUseBlocks int) {
	slog.Info("chat-loop-diag: provider stream closed",
		"session_id", sessionID,
		"msg_id", msgID,
		"iter", iter,
		"duration_ms", duration.Milliseconds(),
		"event_count", eventCount,
		"stop_reason", stopReason,
		"tool_use_blocks", toolUseBlocks,
	)
}

// diagLogDeferReached is called at the top of generateResponse's defer to
// confirm the cleanup path ran. If the defer never runs, stream_end is
// never broadcast and the UI shows a permanent spinner. Absence of this log
// line after a reported stall means the goroutine was blocked before defer.
func diagLogDeferReached(sessionID, msgID string, finalIter int, blockedSendSite string) {
	slog.Info("chat-loop-diag: defer cleanup reached",
		"session_id", sessionID,
		"msg_id", msgID,
		"final_iter", finalIter,
		"last_blocked_send_site", blockedSendSite, // "" if none recorded
	)
}
