package agent

import (
	"log/slog"
	"os"
	"strings"
)

// opencode_config.go is the switch for keeping an OpenCode agent off the
// operator's own opencode config (CW-20261001-0239); the redirect itself is in
// opencodeLayout.AmendEnv.

// opencodeXDGConfigDir is the directory, inside the boot dir, an OpenCode
// launch's XDG_CONFIG_HOME points at. opencode creates opencode/ inside it.
const opencodeXDGConfigDir = "xdg-config"

// OpenCodeIsolateEnv is the kill switch. Set to 0 (or false), OpenCode
// launches load the operator's ~/.config/opencode again, as before
// CW-20261001-0239. Anything else, a typo included, stays isolated: the
// failure that matters is leaving user-level MCP servers loaded.
const OpenCodeIsolateEnv = "NANITE_OPENCODE_ISOLATE_CONFIG"

func opencodeConfigIsolated() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(OpenCodeIsolateEnv))) {
	case "0", "false":
		return false
	}
	return true
}

// WarnIfOpenCodeConfigNotIsolated logs, at startup, that the kill switch is
// on: OpenCode agents then load the operator's global opencode config,
// including any MCP server in it.
func WarnIfOpenCodeConfigNotIsolated() {
	if opencodeConfigIsolated() {
		return
	}
	slog.Warn("opencode agents are launched WITH the operator's ~/.config/opencode: they load every MCP server in it, including one that can open Nanite's database inside the agent's sandbox",
		"env", OpenCodeIsolateEnv)
}
