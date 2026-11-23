package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/libs/agent/agentdir"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/headless"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/logging"
	"github.com/hollis-labs/substrate/modules/secrets"
	"github.com/spf13/cobra"
)

// claudeFlags are the claude -p flags Nanite accepts, so a program that drives Claude Code can
// drive Nanite with the same argv (docs/headless.md).
type claudeFlags struct {
	model, permissionMode, settings, promptTool string
	appendPrompt, appendPromptFile              string
	mcpConfig, addDirs, pluginDirs              []string
	settingSources                              string
	skipPermissions, strictMCP                  bool
}

func (f *claudeFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&f.model, "model", "", "Model for this run, over host.yaml and providers.yaml's chat selection")
	fl.StringVar(&f.permissionMode, "permission-mode", "", "default, acceptEdits (accept-edits), plan, or bypassPermissions (yolo)")
	fl.BoolVar(&f.skipPermissions, "dangerously-skip-permissions", false, "Run every tool without asking (--permission-mode bypassPermissions)")
	fl.StringArrayVar(&f.mcpConfig, "mcp-config", nil, "MCP servers to add, a .mcp.json-shaped file or inline JSON; repeatable")
	fl.BoolVar(&f.strictMCP, "strict-mcp-config", false, "Use only the --mcp-config servers, not the agent directory's .mcp.json")
	fl.StringVar(&f.appendPrompt, "append-system-prompt", "", "Instructions added after the agent's own")
	fl.StringVar(&f.appendPromptFile, "append-system-prompt-file", "", "Like --append-system-prompt, from a file")
	fl.StringVar(&f.settings, "settings", "", "A claude settings file or inline JSON: permissions (allow, ask, deny, defaultMode, additionalDirectories), env and model are used")
	fl.StringVar(&f.promptTool, "permission-prompt-tool", "", "Ask this MCP tool (mcp__<server>__<tool>) to approve tool calls, with claude's request and reply")
	fl.StringArrayVar(&f.addDirs, "add-dir", nil, "Another directory the file tools may use; repeatable")
	fl.StringArrayVar(&f.pluginDirs, "plugin-dir", nil, "A claude plugin directory: its skills/ are offered; repeatable")
	fl.StringVar(&f.settingSources, "setting-sources", "", "Accepted for compatibility; Nanite reads only --settings")
}

// projectConfig is what workspace mode found for the workspace (internal/workspace), least
// specific first: settings files, MCP files and skill directories that the flags then override.
type projectConfig struct {
	settings, mcp, skillDirs []string
}

// apply sets the flags on the agent's options, over base. It returns the gate to use when
// --permission-prompt-tool is set, and a func that releases it.
func (f *claudeFlags) apply(app *module.App, dir string, stderr io.Writer, o *agent.Options, base projectConfig) (agent.Gate, func() error, error) {
	noop := func() error { return nil }
	// Settings: each file in order, then --settings; a later one's model, mode and env win, and
	// rules and directories add up.
	var settings headless.Settings
	sources := append([]string(nil), base.settings...)
	if f.settings != "" {
		sources = append(sources, f.settings)
	}
	for _, src := range sources {
		one, err := headless.ReadSettings(src)
		if err != nil {
			return nil, noop, err
		}
		if len(one.Ignored) > 0 {
			_, _ = fmt.Fprintf(stderr, "nanite: settings %s: not used: %s\n", settingsName(src), strings.Join(one.Ignored, ", "))
		}
		for k, v := range one.Env {
			if err := os.Setenv(k, v); err != nil {
				return nil, noop, fmt.Errorf("settings env %s: %w", k, err)
			}
		}
		rules, err := one.Rules()
		if err != nil {
			return nil, noop, fmt.Errorf("settings %s: %w", settingsName(src), err)
		}
		o.PermissionRules = append(o.PermissionRules, rules...)
		settings.Model = firstNonEmpty(one.Model, settings.Model)
		settings.Permissions.DefaultMode = firstNonEmpty(one.Permissions.DefaultMode, settings.Permissions.DefaultMode)
		settings.Permissions.AdditionalDirectories = append(settings.Permissions.AdditionalDirectories, one.Permissions.AdditionalDirectories...)
	}
	o.Model = firstNonEmpty(f.model, settings.Model)
	mode := firstNonEmpty(f.permissionMode, settings.Permissions.DefaultMode)
	if f.skipPermissions {
		mode = "bypassPermissions"
	}
	var err error
	if o.PermissionMode, err = headless.PermissionMode(mode); err != nil {
		return nil, noop, err
	}
	mcpFiles := f.mcpConfig
	if !f.strictMCP {
		mcpFiles = append(append([]string(nil), base.mcp...), f.mcpConfig...)
	}
	mcp, err := headless.ReadMCPConfig(mcpFiles)
	if err != nil {
		return nil, noop, err
	}
	o.MCPServers, o.StrictMCP = mcp.Servers, f.strictMCP
	if f.appendPrompt != "" {
		o.Instructions = append(o.Instructions, f.appendPrompt)
	}
	if f.appendPromptFile != "" {
		b, err := os.ReadFile(f.appendPromptFile) // #nosec G304 -- named on the command line.
		if err != nil {
			return nil, noop, fmt.Errorf("--append-system-prompt-file: %w", err)
		}
		o.Instructions = append(o.Instructions, string(b))
	}
	for _, d := range append(append([]string(nil), f.addDirs...), settings.Permissions.AdditionalDirectories...) {
		abs, err := filepath.Abs(d)
		if err != nil {
			return nil, noop, err
		}
		o.AddDirs = append(o.AddDirs, abs)
	}
	o.AddSkillDirs = append(o.AddSkillDirs, base.skillDirs...)
	for _, d := range f.pluginDirs {
		skills := filepath.Join(d, "skills")
		if info, err := os.Stat(skills); err == nil && info.IsDir() {
			o.AddSkillDirs = append(o.AddSkillDirs, skills)
		}
	}
	if f.promptTool == "" {
		return nil, noop, nil
	}
	// The tool's server comes from --mcp-config, else the agent directory's .mcp.json.
	servers := mcp
	if _, ok := servers.Servers[promptServer(f.promptTool)]; !ok && !f.strictMCP {
		if snap, err := agentdir.Load(dir); err == nil {
			servers = snap.MCP
		}
	}
	value, _ := app.Value(logging.Key)
	logger, _ := value.(*slog.Logger)
	gate, err := headless.NewPromptGate(f.promptTool, servers, dir, secrets.Resolver(app), logger)
	if err != nil {
		return nil, noop, err
	}
	return gate, gate.Close, nil
}

// settingsName is a settings source as messages name it: inline JSON or a file path.
func settingsName(src string) string {
	if strings.HasPrefix(strings.TrimSpace(src), "{") {
		return "(inline)"
	}
	return src
}

func promptServer(tool string) string {
	rest, _ := strings.CutPrefix(tool, "mcp__")
	server, _, _ := strings.Cut(rest, "__")
	return server
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

var errPromptToolInteractive = errors.New("--permission-prompt-tool works with -p; the terminal asks you itself")
