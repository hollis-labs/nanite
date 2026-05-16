package agent

import (
	"encoding/json"

	agentsessions "github.com/hollis-labs/go-agent-sessions/agentsessions"
	"github.com/hollis-labs/go-providers/provider"
)

// streamingStdioUserFrame wraps raw text as a single NDJSON object in the
// shape Anthropic's claude "Streaming Input Mode" expects on stdin:
//
//	{"type":"user","message":{"role":"user","content":"<text>"}}
//
// CW-20260516-0007: the streaming-stdio runtime writes SendInput bytes
// (and the boot-prompt-on-stdin payload) to the child verbatim, only
// appending '\n'. claude with `-p --input-format stream-json` parses
// every stdin line as JSON — un-framed markdown/text crashes its input
// parser within ~500ms (c207/c208: `SyntaxError: JSON Parse error`).
//
// Returns JSON-encoded bytes WITHOUT a trailing newline; the runtime's
// SendInput appends one. json.Marshal handles content escaping, so any
// text (multi-line boot prompts, quotes, control chars) is safe.
func streamingStdioUserFrame(text string) ([]byte, error) {
	type userMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type frame struct {
		Type    string  `json:"type"`
		Message userMsg `json:"message"`
	}
	return json.Marshal(frame{Type: "user", Message: userMsg{Role: "user", Content: text}})
}

// shouldUsePTY decides between PTY allocation and regular stdio pipes
// for the long-lived runtime. The function name now slightly outlives
// its original intent: post-CW-20260515-0004 it returns false for every
// supported provider because the long-lived shape we ship today is
// claude's Streaming Input Mode (NDJSON over regular stdin/stdout
// pipes, parsed by ParseLineEvents), which does NOT want a PTY. The
// helper is retained as the single insertion point for any future
// adapter that genuinely needs a PTY.
//
// History (decisions.nanite.architecture.cli_pty_long_lived_default
// rev 01KR2Y16TZJC8X88E6P497JBH3): the original design routed claude
// long-lived through a PTY runtime expecting "per-tool SSE via
// TypedEventCallback". That path emitted bare-claude (TUI) argv whose
// output is ANSI/screen redraws — ParseLine/ParseLineEvents have no
// TUI scraper, so sessions ran forever with zero assistant deltas
// surfaced (c202). StreamingStdio replaces both prior shapes:
// long-lived AND parseable AND no PTY required.
//
// CW-20260514-0045: dropdown / legacy prefixed aliases ("pty",
// "pty-claude") normalize to "claude" before the switch so any
// future adapter that DOES want a PTY can be added below without
// drift between the chat and runtime layers. See
// bootdir.normalizeProviderName for the rule set.
func shouldUsePTY(providerName string, mode Mode) bool {
	if mode != ModeLongLived {
		return false
	}
	_ = normalizeProviderName(providerName) // normalize kept warm for future cases
	return false
}

// shouldUseStreamingStdio picks the long-lived NDJSON-over-stdio
// runtime kind for adapters whose BuildArgs produce a long-lived
// child-process shape (Anthropic's Streaming Input Mode for claude
// today: `-p --input-format stream-json --output-format stream-json
// --verbose`).
//
// CW-20260515-0006: this used to be implicit (CW-20260515-0004 turned
// off Caps.PTY without turning on any other lifecycle flag, which
// agentsessions interprets as "subprocess-per-turn adapter runtime" —
// the wrong runtime for the StreamingStdio adapter argv we register).
// The runtime would spawn claude per turn, send the NDJSON payload,
// close stdin, claude would exit, no parseable output surfaced (c204,
// c205).
//
// Mode-agnostic by design: cmd/nanite registers exactly one Claude
// adapter (StreamingStdio shape) for every Boot site — chat
// long-lived, BootRunner ModeSubagent kickoff, recovery's ModeResume,
// background tasks, one-shot dispatches. All of those drive the same
// argv, so all of them need the same runtime kind. Round-1 Copilot
// review caught that a ModeLongLived gate here left ModeResume /
// ModeSubagent / ModeBackground / ModeOneShot Claude boots on the
// subprocess-per-turn fallback — same hang, different code path. The
// StreamingStdio runtime honors AutoFireFirstTurn (see
// streaming_stdio_session.go:96-105), so kickoff modes work; resume
// modes spawn with --resume and skip the kickoff — also fine.
//
// Other adapters (codex, opencode) still flow through the implicit
// subprocess-per-turn adapter runtime — their BuildArgs emit per-turn
// shape and per-adapter long-lived work is outstanding.
//
// Lifecycle flags are mutually exclusive in agentsessions
// (Capabilities.validateLifecycle); callers must ensure at most one of
// {PTY, StreamingStdio, JsonRpcStdio} is true. runtimeConfigForAdapter
// is the single insertion point and enforces this by construction.
func shouldUseStreamingStdio(providerName string, mode Mode) bool {
	_ = mode // intentionally mode-agnostic; see doc comment
	switch normalizeProviderName(providerName) {
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
//
// Lifecycle-flag invariant (CW-20260515-0006): {PTY, StreamingStdio,
// JsonRpcStdio} are mutually exclusive in agentsessions; this factory
// is the single insertion point and picks at most one. PTY currently
// returns false everywhere (see shouldUsePTY); StreamingStdio handles
// claude long-lived. Codex / opencode get the implicit
// subprocess-per-turn adapter runtime (no flag set), which is correct
// for their per-turn argv shape.
func runtimeConfigForAdapter(adapter provider.CLIAdapter, providerName string, mode Mode) agentsessions.AdapterRuntimeConfig {
	cfg := agentsessions.AdapterRuntimeConfig{
		Adapter: adapter,
	}
	cfg.Caps.PTY = shouldUsePTY(providerName, mode)
	cfg.Caps.StreamingStdio = shouldUseStreamingStdio(providerName, mode)
	cfg.Caps.Resize = true
	return cfg
}
