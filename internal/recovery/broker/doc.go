// Package broker is the in-process subagent recovery broker for nanite's
// CLI/PTY agent sessions. It classifies whole-session failures (process
// crashes, sandbox dir write failures, MCP transport drops, etc.),
// attempts automated remediation, dispatches a replacement session
// preserving lineage, and emits user-facing status via the existing chat
// envelope kinds (info-card / error-report / chat-loop-terminated).
//
// The broker hooks two signals from the supervision primitives in
// go-agent-sessions v0.6.0:
//
//   - OnRestart — invoked from agent.Boot's existing SupervisorOptions.OnRestart
//     closure when the supervisor itself triggered a restart. The broker
//     observes (records breadcrumb, optionally overlays an info-card) but
//     does not re-dispatch; the lib already handled the restart.
//
//   - OnSessionExit — invoked from a Wait-observer goroutine the chat
//     composition root spawns per booted session. Fires when the lib's
//     terminal *agentsessions.ExitError lands (after RestartOnCrash
//     attempts have been exhausted). The broker classifies, remediates,
//     and decides whether to dispatch a replacement session (same
//     SessionID, fresh CLI process) or surface a permanent failure.
//
// The broker complements (does not replace) the existing per-turn
// recoverable-error handling in internal/service/chat_loop_state.go —
// that path stays in-loop for in-flight provider errors, rate-budget
// refusals, and context-overflow. The broker's lane is whole-session
// failures.
//
// In-process by design (not a separate subagent process). Locked decision
// per D-SUBAGENT-RECOVERY-BROKER in
// nanite/docs/architecture/chat-system/future-work.md.
package broker
