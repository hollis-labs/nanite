package service

// CW-20260418-0043 diagnostics — precursor to BLG-20260418-012.
//
// Split into two tiers:
//
//   CHEAP (always on):   loop start, iter start, provider stream closed,
//                        loop exit, defer reached. One slog.Info per
//                        boundary; ~5 log lines per turn; negligible cost.
//                        These match the "Layer 1 structured slog events"
//                        strategy in BLG-20260418-012 and are the kind of
//                        narrative observability the chat stream always
//                        needs, so they stay wired even after the original
//                        stall investigation is closed.
//
//   EXPENSIVE (opt-in):  diagWatchChSend spawns a watchdog goroutine per
//                        ch send. In a streaming response that's ~200
//                        goroutines + timers per turn. Useful during a
//                        stall hunt, risky in steady-state production —
//                        PR #65 review. Gated behind the env flag
//                        NANITE_CHAT_LOOP_DIAG=1 so production is
//                        zero-cost by default.
//
// The whole file is replaced by a first-class LoopEvent emitter when
// BLG-20260418-012 lands.

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
)

// diagExpensiveEnabled gates the per-ch-send watchdog. Resolved once at
// init from NANITE_CHAT_LOOP_DIAG=1; zero cost afterward (single bool
// check on the hot path).
var (
	diagExpensiveEnabled     bool
	diagExpensiveOnce        sync.Once
	diagExpensiveEnvVar      = "NANITE_CHAT_LOOP_DIAG"
	diagExpensiveEnabledTrue = "1"
)

func diagExpensive() bool {
	diagExpensiveOnce.Do(func() {
		diagExpensiveEnabled = os.Getenv(diagExpensiveEnvVar) == diagExpensiveEnabledTrue
		if diagExpensiveEnabled {
			slog.Info("chat-loop-diag: expensive diagnostics enabled",
				"env", diagExpensiveEnvVar+"="+diagExpensiveEnabledTrue)
		}
	})
	return diagExpensiveEnabled
}

// diagWatchChSend starts a watchdog goroutine that logs if a ch send takes
// unusually long. Call like:
//
//	stop := diagWatchChSend(ctx, "streamLoop.delta", ch, sessionID, msgID, iter, "delta")
//	ch <- chat.StreamEvent{Type: "delta", Content: ...}
//	stop()
//
// When the send is fast the goroutine exits on the first `stop()` before
// firing any timer. When the send blocks: warn at 100ms, error at 1s, 5s,
// and 30s. After 30s no further logs fire.
//
// Disabled path: returns a no-op func and spawns no goroutine. Enable via
// NANITE_CHAT_LOOP_DIAG=1 during UAT. PR #65 review: keeps steady-state
// production free of per-delta goroutine + timer overhead.
func diagWatchChSend(ctx context.Context, site string, ch chan chat.StreamEvent, sessionID, msgID string, iter int, evtType string) func() {
	if !diagExpensive() {
		return func() {}
	}
	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done) }) }
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
				slog.Warn("chat-loop-diag: ctx canceled while send pending",
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
	return stop
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
