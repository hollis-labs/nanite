package agent

import (
	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
)

// claude_mcp_allow.go lets a Nanite-launched Claude agent call the tools of
// the MCP server Nanite plants for it, and nothing else (CW-20261001-0411).
//
// Claude's headless permission gate asks before running an MCP tool, and a
// `claude -p` launch has no TTY to answer, so every call was refused:
// "Claude requested permissions to use mcp__nanite__agent_list, but you
// haven't granted it yet". The planted settings.json carries
// permissions.defaultMode "acceptEdits", which auto-approves file edits and
// not MCP tools.
//
// The allow rule is carried on argv, not in the planted settings.json.
// Checked against claude 2.1.286 with a stub MCP server in a fresh directory:
//
//   - A permissions.allow in the project's .claude/settings.json is IGNORED
//     when the workspace has not been trusted ("Ignoring 1 permissions.allow
//     entry from .claude/settings.json: this workspace has not been trusted"),
//     and every Nanite boot dir is an untrusted throwaway directory. The same
//     file's defaultMode and additionalDirectories ARE honored; only allow
//     rules are gated. Both rule spellings, `mcp__nanite__*` and `mcp__nanite`,
//     were refused that way and honored once they came from a trusted scope.
//   - Seeding the trust in ~/.claude.json would trust the directory, but it
//     is a write to the operator's global Claude state for every launch, which
//     bootdir_provider_config.go's render deliberately avoids.
//   - `--allowedTools` is a trusted scope, so the rule holds in an untrusted
//     directory: `mcp__nanite__agent_list` returned its result while a call to
//     a second server's tool was still refused.
//
// So a settings.json allow entry would only print the ignored-entry warning,
// and the planted file stays free of one.
//
// The rule names the planted server only (`mcp__<server id>__*`, "nanite" by
// default), so it widens nothing else: the user-level servers that load with
// NANITE_CLAUDE_STRICT_MCP=0 are still refused. It is deliberately independent
// of that switch. The tools the server exposes are scoped per launch mode by
// the server itself (SelfToolsScopeEnv in the planted .mcp.json: a chat launch
// gets the harness's full self-tool set, subagent, background and one-shot
// launches the bare-store set), so the rule does not change which tools a
// mode can reach.
//
// The flag is spelled `--allowedTools=<rule>`, one argv entry. Claude's
// option is variadic: `--allowedTools <rule> <prompt>` swallows the prompt
// (verified: "Input must be provided either through stdin or as a prompt
// argument"), while the `=` form does not, whatever follows it.
//
// INTERIM, like the strict-MCP flags: neither go-providers' ClaudeAdapter nor
// go-agent-wrapper has an allowed-tools option (none up to go-providers
// v0.43.0 and go-agent-wrapper v0.25.6), so Nanite adds it through the
// per-launch extra argv. Replace it with the library option when one lands.

// claudeAllowedToolsFlag is the claude CLI flag that carries the allow rule.
const claudeAllowedToolsFlag = "--allowedTools"

// mcpServerID is the name the planted .mcp.json gives Nanite's MCP server.
func mcpServerID(cfg MCPConfig) string {
	if cfg.ServerID == "" {
		return "nanite"
	}
	return cfg.ServerID
}

// claudeMCPAllowRule is the permission rule that covers every tool of the
// planted server.
func claudeMCPAllowRule(cfg MCPConfig) string {
	return "mcp__" + mcpServerID(cfg) + "__*"
}

// claudeMCPAllowArgs is the Claude argv that allows the planted MCP server's
// tools, or nil when the launch plants no server: another runtime, an ACP
// launch with no boot dir, or a composition root with no database path, which
// disables MCP planting.
func claudeMCPAllowArgs(runtime runtimes.ID, bootDir string, cfg MCPConfig) []string {
	if runtime != runtimes.Claude || bootDir == "" || cfg.DBPath == "" {
		return nil
	}
	return []string{claudeAllowedToolsFlag + "=" + claudeMCPAllowRule(cfg)}
}
