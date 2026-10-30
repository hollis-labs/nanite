package agent

import "strings"

// agy_mcp_allow.go: the allow rules that let a Nanite-launched Antigravity
// (agy) agent call the MCP tools it is granted and nothing else
// (CW-20261024-0109).
//
// Verified live against agy (2026-10-24): headless, agy auto-denies an MCP tool
// unless settings.json carries `permissions.allow` entry
// `mcp(<ServerName>/<tool>)`; the denial is an exit 0 with an empty response.
// agy loads settings only from ~/.gemini/antigravity-cli/settings.json and
// /etc/antigravity/admin_settings.json (no workspace file), and a workspace
// hook's allow is advisory, so the rules are written into a PRIVATE ~/.gemini
// the launch's HOME points at (bootdir_agy_home.go).
//
// <ServerName> is "<plugin>_<server>" where the plugin is the directory name
// under .agents/plugins/ (always "tether", agyPluginName) and the server is the
// key in that plugin's mcp_config.json: Nanite's own server (mcpServerID) and
// each projected host server share the one plugin file.

// agyPluginName is the plugin Nanite plants (see agyMCPConfigPath).
const agyPluginName = "tether"

// agyServerName is the name agy reports for a server in the planted plugin.
func agyServerName(server string) string { return agyPluginName + "_" + server }

// agyMCPAllowRule is the settings.json allow entry for one tool of a server.
func agyMCPAllowRule(server, tool string) string {
	return "mcp(" + agyServerName(server) + "/" + tool + ")"
}

// agyRuleBreakers are characters that would end or widen a rule early; a tool
// or server containing one gets no rule (fail closed) rather than a malformed
// or wider one.
const agyRuleBreakers = " \t\n,*()/"

// agyOwnServerRule is the rule for the tools of the Nanite MCP server the
// launch plants. Its tool list is not known at plant time: the server decides
// it when agy connects (self tools plus dev tools, scoped per launch mode by
// SelfToolsScopeEnv, and forwarded to the live harness), so an explicit per-tool
// list would have to duplicate and track that surface. The rule therefore names
// the server with a wildcard tool, the equivalent of claudeMCPAllowRule, and
// widens nothing beyond the server Nanite itself plants. agy honors the
// `mcp(<server>/*)` wildcard (checked live 2026-10-24: a stub server's tool ran
// under it, while a sibling server's ungranted tool stayed denied).
func agyOwnServerRule(cfg MCPConfig) string {
	return "mcp(" + agyServerName(mcpServerID(cfg)) + "/*)"
}

// agyMCPAllowRules is every rule a launch's private settings carry: the planted
// Nanite server (when MCP planting is on) and, per granted tool, each projected
// host server. Deterministic order.
func agyMCPAllowRules(params SetupParams) []string {
	var rules []string
	if params.MCPConfig.DBPath != "" {
		rules = append(rules, agyOwnServerRule(params.MCPConfig))
	}
	if params.HostMCP == nil {
		return rules
	}
	for _, srv := range params.HostMCP.Servers {
		if strings.ContainsAny(srv.Name, agyRuleBreakers) {
			continue
		}
		for _, tool := range params.HostMCP.Tools {
			if tool == "" || strings.ContainsAny(tool, agyRuleBreakers) {
				continue
			}
			rules = append(rules, agyMCPAllowRule(srv.Name, tool))
		}
	}
	return rules
}
