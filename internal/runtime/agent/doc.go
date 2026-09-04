// Package agent is nanite's single agent-spawn entry point. It owns the
// lifecycle of CLI-agent processes (claude / codex / opencode / etc.) for
// chat sessions, subagent invocations, scheduler-dispatched executors, and
// fire-and-forget background tasks.
//
// Design references:
//   - agent-workspaces/planning/agent-boot-unification/2026-05-07-cross-app-design.md
//   - agent-workspaces/planning/agent-boot-unification/2026-05-08-lib-tier-status.md
//   - Tesseract decisions:
//     decisions.nanite.architecture.adopt_agent_boot_pattern
//     decisions.nanite.architecture.cli_pty_long_lived_default
//     decisions.portfolio.architecture.agent_boot_pattern
//
// The package replaces three previous spawn paths:
//   - internal/service/subagent_runner.go  (-> ModeSubagent)
//   - internal/background/pty.go           (-> ModeBackground)
//   - internal/service/chat_generate.go    (provider.StreamChat per turn -> ModeLongLived PTY + SendInput)
//
// Boot() is the only public entry point. Callers describe intent through
// Options.Mode; the package owns boot-dir / workspace-dir materialization,
// env composition, runtime selection (PTY vs subprocess-per-turn), supervisor
// wiring, and DB persistence.
package agent
