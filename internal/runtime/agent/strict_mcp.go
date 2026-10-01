package agent

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
)

// strict_mcp.go makes the .mcp.json Nanite plants the only MCP configuration
// a Claude agent loads (CW-20261001-0221).
//
// Claude merges MCP servers from every scope it knows: the user's
// ~/.claude.json, the project's .mcp.json, plugins and account connectors.
// Launched without restriction, a Nanite agent therefore also ran whatever
// the operator's own Claude has, for example a `mux mcp --proxy` that spawns
// a second `nanite mcp` child with the real main.db open read-write inside
// the agent's sandbox, and a Cerberus (deploy, ssh) tool set Nanite never
// meant to give it.
//
// `--strict-mcp-config` (claude --help: "Only use MCP servers from
// --mcp-config, ignoring all other MCP configurations") with `--mcp-config
// <bootDir>/.mcp.json` leaves the planted server and nothing else. Checked
// against claude 2.1.286 with the operator's real HOME: strict mode with an
// empty config loaded no MCP server at all, and with a planted file loaded
// exactly its entries.
//
// INTERIM. Nanite adds the two flags itself, through the per-launch extra
// argv (see workRootArgs), because neither go-providers' ClaudeAdapter nor
// go-agent-wrapper has an option for strict MCP. This file is replaced by
// the library option, filed via orch-libs, when it lands.
//
// Claude only. A Codex launch sets CODEX_HOME to the boot dir, so codex
// reads no ~/.codex/config.toml, but it also reads no .mcp.json. An OpenCode
// launch sets OPENCODE_CONFIG_DIR, which adds a config directory and does not
// replace the user's ~/.config/opencode, so user-level servers still load
// (CW-20261001-0239). An ACP launch plants no boot dir.

// StrictMCPEnv is the interim kill switch. Set to 0 (or false), Claude
// launches go back to loading every MCP scope, as before CW-20261001-0221.
const StrictMCPEnv = "NANITE_CLAUDE_STRICT_MCP"

// strictMCPEnabled reports whether Claude launches run strict. Anything but
// an explicit off, including a typo, stays strict: the failure that matters
// is leaving user-level servers loaded.
func strictMCPEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(StrictMCPEnv))) {
	case "0", "false":
		return false
	}
	return true
}

// WarnIfStrictMCPOff logs, at startup, that the kill switch is on: Claude
// agents then load the operator's user-level MCP servers again.
func WarnIfStrictMCPOff() {
	if strictMCPEnabled() {
		return
	}
	slog.Warn("claude agents are launched WITHOUT --strict-mcp-config: they load every MCP server the operator's Claude has, including user-level ones that can open Nanite's database inside the agent's sandbox",
		"env", StrictMCPEnv)
}

// strictMCPArgs is the Claude argv that restricts MCP to the planted
// .mcp.json in bootDir, or nil when the launch is not a native Claude boot
// (another runtime, ACP with no boot dir) or the kill switch is off.
//
// With no .mcp.json planted (MCP planting is disabled when the composition
// root has no database path) the launch is still strict, so it gets no MCP
// server rather than whatever the operator has.
//
// Placement: extra args sit after go-providers' own argv and before the
// variadic `--add-dir` list, which is why --mcp-config is followed by a flag
// here, never by a value or a positional it would swallow. A streaming boot
// has no `--` and no positional; a per-turn one puts its extras before `--`.
func strictMCPArgs(runtime runtimes.ID, bootDir string) []string {
	if runtime != runtimes.Claude || bootDir == "" || !strictMCPEnabled() {
		return nil
	}
	var args []string
	planted := filepath.Join(bootDir, ".mcp.json")
	if info, err := os.Stat(planted); err == nil && info.Mode().IsRegular() {
		args = append(args, "--mcp-config", planted)
	}
	return append(args, "--strict-mcp-config")
}
