// Package recovery is the namespace root for Nanite's four recovery
// mechanisms — genuinely distinct answers to "what went wrong", grouped
// under one prefix rather than consolidated:
//
//   - internal/recovery/broker — the Recovery Broker. Classifies an
//     in-process crash while the daemon is alive and may dispatch a
//     replacement session.
//   - internal/recovery/orphansweep — the Orphan/Runtime Reaper.
//     Reconciles stale agent_runtime rows against actually-dead PIDs,
//     most commonly after a daemon restart.
//   - internal/recovery/pack — the Recovery Pack. Replays trailing
//     context into a freshly cold-booted session so it isn't blind on
//     its next turn.
//   - This file — interrupted-turn detection. A cheap, read-only
//     heuristic surfaced to the frontend; small enough it doesn't
//     warrant its own subpackage.
package recovery

// DetectInterruptedTurn is the pure decision core of interrupted-turn
// detection (CW-20260518-0084): a service restart (deploy/reload) can
// kill a turn mid-generation, leaving the GUI spinning forever with no
// indication anything went wrong. The signal is derived from two facts
// the caller has already resolved:
//
//   - lastMessageRole is the session's chronologically-last persisted
//     message's role. A completed turn always ends with an "assistant"
//     (or "tool") row; a turn that started but never produced a reply
//     leaves a "user" message dangling.
//   - hasLiveStream reports whether this process holds a live in-memory
//     stream for the session. During normal generation the StreamManager
//     always has one for the message being generated, so this is false
//     for genuinely in-flight turns. After a restart the StreamManager is
//     a fresh empty instance, so a turn that was generating at restart
//     time reads as having no live stream.
//
// Both conditions together mean "a turn was dispatched, no reply landed,
// and nothing in this process is producing one" — i.e. the agent process
// is gone. Reconciling the dead agent_runtime rows is a separate concern
// (internal/recovery/orphansweep); this only surfaces the state so the
// FE can stop the endless spinner.
//
// Returns nil when the turn is not interrupted — callers (the FE, via
// the API layer) treat a null/absent field as "no interruption".
func DetectInterruptedTurn(lastMessageRole, lastMessageID, lastActivityAt string, hasLiveStream bool) map[string]any {
	if lastMessageRole != "user" {
		return nil
	}
	if hasLiveStream {
		return nil
	}
	return map[string]any{
		"interrupted":      true,
		"reason":           "service_restart",
		"last_message_id":  lastMessageID,
		"last_activity_at": lastActivityAt,
	}
}
