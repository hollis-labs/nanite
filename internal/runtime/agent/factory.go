package agent

import (
	agentsessions "github.com/hollis-labs/go-agent-sessions/agentsessions"
	"github.com/hollis-labs/go-providers/provider"
)

// shouldUsePTY decides between the long-lived PTY runtime and the
// subprocess-per-turn fallback. Per
// decisions.nanite.architecture.cli_pty_long_lived_default rev 01KR2Y16TZJC8X88E6P497JBH3:
//
//   - claude + ModeLongLived → PTY (the chat case; closes G-PTY-RESUME-DROP and
//     enables per-tool SSE via TypedEventCallback).
//   - everything else → subprocess-per-turn (until per-adapter PTY
//     work lands).
func shouldUsePTY(providerName string, mode Mode) bool {
	if mode != ModeLongLived {
		return false
	}
	switch providerName {
	case "claude", "claude-code", "claudecode":
		return true
	default:
		return false
	}
}

// shouldAutoFireFirstTurn picks the StartOptions.AutoFireFirstTurn value
// per Mode.
//
//   - ModeLongLived  → false; chat harness drives the first turn
//     explicitly (the user's first message).
//   - ModeOneShot    → true;  caller's intent is one immediate kickoff.
//   - ModeResume     → false; resume restores prior state, caller drives next turn.
//   - ModeSubagent   → true;  caller's intent is one immediate kickoff.
//   - ModeBackground → true;  fire-and-forget.
func shouldAutoFireFirstTurn(mode Mode) bool {
	switch mode {
	case ModeOneShot, ModeSubagent, ModeBackground:
		return true
	default:
		return false
	}
}

// runtimeConfigForAdapter is the factory the chat / subagent / background
// callers use to construct an agentsessions.AdapterRuntimeConfig with the
// correct Caps for the spawn. Phase 3 fills in capability propagation
// (CheckpointResume, ProviderSessionID, BinaryRequired, Resize).
func runtimeConfigForAdapter(adapter provider.CLIAdapter, providerName string, mode Mode) agentsessions.AdapterRuntimeConfig {
	cfg := agentsessions.AdapterRuntimeConfig{
		Adapter: adapter,
	}
	cfg.Caps.PTY = shouldUsePTY(providerName, mode)
	cfg.Caps.Resize = true
	return cfg
}
