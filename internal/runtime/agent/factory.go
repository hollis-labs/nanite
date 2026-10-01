package agent

import (
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/go-agent-wrapper/adapters"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/store"
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
// Adapter selection is delegated to go-agent-wrapper/adapters.Select; this
// helper remains only because Claude-native payload framing follows the same
// selected launch mode.
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

// selectNativeAdapter delegates descriptor, lifecycle shape, and CLIAdapter
// validation to the wrapper's canonical native factory. Nanite keeps the
// product decision (Claude streaming stdio; Codex/OpenCode per-turn) and its
// already-configured adapter, including Claude developer-mode behavior.
// The launch mode is always explicit: the libraries' Codex default is now
// jsonrpc-stdio (app-server; agentkit v0.12.0, go-providers v0.31.0), and
// Nanite runs Codex per turn (CW-20260930-0113).
//
// workRoot is the session's project directory (Options.Workdir); see
// workRootArgs for what it adds to argv.
func selectNativeAdapter(providerName string, mode Mode, cli provider.CLIAdapter, workRoot string) (adapters.RuntimeAdapter, error) {
	streaming := shouldUseStreamingStdio(providerName, mode)
	launchMode := adapters.LaunchSubprocessPerTurn
	if streaming {
		launchMode = adapters.LaunchStreamingStdio
	}
	extra := workRootArgs(providerName, workRoot)
	if err := checkExtraArgsPlacement(streaming, extra); err != nil {
		return nil, err
	}
	return adapters.Select(adapters.Selection{
		Provider:    adapters.Provider(normalizeProviderName(providerName)),
		RuntimeKind: adapters.RuntimeKindCLI,
		LaunchMode:  launchMode,
		CLIAdapter:  cli,
		ExtraArgs:   extra,
	})
}

// checkExtraArgsPlacement refuses Nanite argv additions on a per-turn
// launch (CW-20261001-0069). go-agent-wrapper appends Selection.ExtraArgs
// after the adapter's whole argv, and since go-providers v0.34.1 a per-turn
// convention ends with `-- <prompt>` (codex exec, opencode run, claude -p),
// so anything appended there would reach the CLI as prompt text, not as
// flags. Claude streaming stdio carries no prompt in argv (turns go over
// stdin), so its --add-dir is safe at the end. A future per-turn addition
// belongs in the adapter's own ExtraArgs field, which go-providers places
// before the `--`.
func checkExtraArgsPlacement(streaming bool, extra []string) error {
	if len(extra) > 0 && !streaming {
		return fmt.Errorf("agent: a per-turn native launch puts the prompt after `--`, so argv additions %q appended by the wrapper would be read as prompt text", extra)
	}
	return nil
}

// workRootArgs returns the argv that grants a native CLI agent its work
// root while its cwd stays the boot dir (CW-20261001-0020). Claude gets
// --add-dir <root>. Codex reaches the root through its planted
// config.toml writable_roots and opencode spawns in it (SpawnWorkdir), so
// neither needs an argument. Empty workRoot adds nothing.
//
// Nothing else on Nanite's launch path emits Claude's --add-dir, so this is
// its only source and it appears once (TestSelectNativeAdapter_
// ClaudeAddDirExactlyOnce). Since go-providers v0.31.0 the projected launch
// convention carries --add-dir <project> in every mode, but Nanite does not
// launch through the projection: it hands the wrapper its own CLIAdapter.
// That adapter's BuildArgs emits --add-dir only when its ProjectDir field is
// set (in every mode since v0.33.0), and Nanite's Claude adapter is one
// instance shared by every session (cmd/nanite/main.go), so ProjectDir
// stays empty rather than racing per session.
//
// Interim: the shared PreparedExecution (CW-20260930-0113) owns cwd and
// --add-dir once Nanite launches through it; drop this then.
func workRootArgs(providerName, workRoot string) []string {
	if workRoot == "" {
		return nil
	}
	switch normalizeProviderName(providerName) {
	case "claude", "claude-code", "claudecode":
		return []string{"--add-dir", workRoot}
	default:
		return nil
	}
}

// useACPProtocol reports whether profile is explicitly configured to
// launch through the ACP client abstraction (TASKS/agent-host-acp/08-11,
// libs/go-agent-wrapper's acp package plus its opencodeacp/copilotacp
// native adapters) instead of its provider's existing native protocol.
//
// This is the per-agent Protocol/Transport dispatch decision
// TASKS/agent-host-acp/11-nanite-per-agent-protocol-transport-config.md
// adds, consulted from agent.Boot at the same adapter-selection boundary as
// the native wrapper factory.
//
// Empty/unset profile.Protocol (the default for every pre-existing
// agent_profiles row, and for any agent an operator hasn't explicitly
// opted in) means "use the native protocol" — 17-acp.md's explicit
// "additive, not a cutover" framing. No new agents.runtime_kind value is
// introduced or consulted here: an ACP-configured agent still carries
// runtime_kind='cli' unchanged (verified by
// TestUseACPProtocolDoesNotTouchRuntimeKind) — this function only ever
// runs once runtime_kind has already routed the launch into this CLI
// runtime package (see effectiveProvider's own doc comment for the
// identical, established precedent of a narrower in-package dispatch
// question that "runtime_kind alone can't answer").
func useACPProtocol(profile *store.AgentProfile) bool {
	return profile != nil && profile.Protocol == "acp"
}

// effectiveACPTransport resolves the adapters.Transport an ACP-configured
// agent should connect over, defaulting to stdio when
// agent_profiles.transport is unset — every native ACP adapter this batch
// ships (task 09 OpenCode, task 10 Copilot CLI's stdio mode) supports
// stdio; only Copilot CLI's daemon mode additionally supports tcp (task
// 10). Only meaningful when useACPProtocol(profile) is true —
// validateAgentMultiAgentFields (internal/store/agents.go) rejects a
// transport value paired with any protocol other than "acp" before a row
// can ever reach this function with a non-empty Transport.
func effectiveACPTransport(profile *store.AgentProfile) adapters.Transport {
	if profile != nil && profile.Transport == "tcp" {
		return adapters.TransportTCP
	}
	return adapters.TransportStdio
}
