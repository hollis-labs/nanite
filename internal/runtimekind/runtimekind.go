// Package runtimekind is Nanite's runtime-kind vocabulary: the token a
// durable agent instance, a recipe or an agent_runtime row persists to say
// how its agent runs.
//
// agentkit v0.12.0 removed the shared vocabulary this used to come from
// (agentkit/agentruntime/runtimekind) in favor of agent-contracts-leaf's
// runtimes.Mode, with no aliases for the old spellings, and left each host
// to normalize its own tokens at its boundary (CW-20260930-0113). This is
// that boundary.
//
// Every runtime mode is spelled as its runtimes.Mode. Two Nanite tokens are
// not modes: "api" (the agent runs through a provider API, not a CLI) and
// "unknown" (what an agent_runtime row carries when nothing set its kind).
//
// Persisted rows are not rewritten. Parse accepts the pre-v0.12.0 spellings
// and every alias the old Parse took, so a row written before the change
// reads as its current kind wherever Nanite interprets one; new code writes
// only the constants below.
package runtimekind

import (
	"strings"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
)

// Kind is a normalized runtime-kind token.
type Kind string

const (
	// API runs the agent through a provider API rather than a CLI.
	API Kind = "api"
	// StreamingStdio is one long-lived CLI process fed turns on stdin.
	StreamingStdio = Kind(runtimes.ModeStreamingStdio)
	// SubprocessPerTurn spawns the CLI once per turn. Was "subprocess".
	SubprocessPerTurn = Kind(runtimes.ModeSubprocessPerTurn)
	// JSONRPCStdio is a JSON-RPC daemon on stdio (Codex app-server). Was
	// also spelled "app-server".
	JSONRPCStdio = Kind(runtimes.ModeJSONRPCStdio)
	// HTTPSSE is an HTTP server streaming over SSE (OpenCode serve). Was
	// "serve-http".
	HTTPSSE = Kind(runtimes.ModeHTTPSSE)
	// PTY is the raw terminal path. The old "pty-debug" is PTY under the
	// debug posture; Nanite has no consumer that tells the two apart, and
	// neither is managed automation.
	PTY = Kind(runtimes.ModePTY)
	// ACPStdio is an Agent Client Protocol agent on stdio.
	ACPStdio = Kind(runtimes.ModeACPStdio)
	// ACPTCP is an Agent Client Protocol agent over TCP (Copilot's daemon).
	ACPTCP = Kind(runtimes.ModeACPTCP)
	// Unknown is an empty or unrecognized token.
	Unknown Kind = "unknown"
)

// Parse normalizes a persisted, catalog or request token: case and
// surrounding space are ignored, "_" reads as "-", the current spellings
// pass through, and the pre-agentkit-v0.12.0 spellings and aliases map to
// their current kind. It returns Unknown for an empty or unrecognized
// value.
func Parse(raw string) Kind {
	s := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(raw)), "_", "-")
	switch s {
	case "api", "provider-api", "http-api":
		return API
	case "streaming-stdio", "stream-json", "streaming", "claude-code", "managed-streaming":
		return StreamingStdio
	case "subprocess-per-turn", "subprocess", "exec", "cli", "single-turn", "oneshot", "one-shot":
		return SubprocessPerTurn
	case "jsonrpc-stdio", "json-rpc-stdio", "jsonrpc", "app-server", "codex-app-server":
		return JSONRPCStdio
	case "http-sse", "serve-http", "http", "sse", "opencode-serve":
		return HTTPSSE
	case "pty", "tui", "terminal", "pty-debug", "debug-pty", "raw-pty":
		return PTY
	case "acp-stdio", "acp":
		return ACPStdio
	case "acp-tcp":
		return ACPTCP
	default:
		return Unknown
	}
}

// IsManagedAutomation reports whether k can run unattended under Nanite's
// management: the API and every non-terminal CLI mode. PTY is a terminal
// for a human, and Unknown says nothing about how to run.
func IsManagedAutomation(k Kind) bool {
	switch k {
	case API, StreamingStdio, SubprocessPerTurn, JSONRPCStdio, HTTPSSE:
		return true
	default:
		return false
	}
}
