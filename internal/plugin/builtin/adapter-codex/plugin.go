// Package adaptercodex implements the Codex/Copilot CLI adapter plugin.
// It discovers agents from AGENTS.md, populates sandboxes with AGENTS.md
// content, and syncs the project-root AGENTS.md with a managed section
// listing available Nanite agents.
package adaptercodex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/fsutil"
	hostplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/store"

	plugin "github.com/hollis-labs/go-plugin"
)

func init() {
	hostplugin.RegisterPlugin("adapter-codex", func() plugin.Plugin { return New() })
}

// ---------------------------------------------------------------------------
// Plugin (implements go-plugin.Plugin)
// ---------------------------------------------------------------------------

// Plugin is the Codex/Copilot CLI adapter plugin.
type Plugin struct {
	host    plugin.Host
	status  plugin.PluginStatus
	adapter *Adapter
}

// New creates a new Codex adapter plugin instance.
func New() *Plugin {
	p := &Plugin{}
	p.adapter = &Adapter{plugin: p}
	return p
}

// Adapter returns the CLIAgentAdapter for this plugin.
func (p *Plugin) Adapter() *Adapter { return p.adapter }

func (p *Plugin) ID() string             { return "adapter-codex" }
func (p *Plugin) Name() string           { return "Codex/Copilot Adapter" }
func (p *Plugin) Version() string        { return "0.1.0" }
func (p *Plugin) Description() string    { return "Discovers AGENTS.md and populates Codex/Copilot CLI sandboxes" }
func (p *Plugin) Dependencies() []string { return nil }

func (p *Plugin) Load(host plugin.Host) error {
	p.host = host
	p.status = plugin.PluginStatus{
		Loaded:   true,
		Enabled:  true,
		LoadedAt: time.Now(),
	}
	host.Logger().Info("adapter-codex: loaded")
	return nil
}

func (p *Plugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	if p.host != nil {
		p.host.Logger().Info("adapter-codex: unloaded")
	}
	return nil
}

func (p *Plugin) Status() plugin.PluginStatus {
	return p.status
}

// ---------------------------------------------------------------------------
// Adapter (implements agent.CLIAgentAdapter)
// ---------------------------------------------------------------------------

// Adapter implements agent.CLIAgentAdapter for Codex/Copilot CLI.
type Adapter struct {
	plugin *Plugin
}

// Compile-time interface check.
var _ agent.CLIAgentAdapter = (*Adapter)(nil)

// Name returns the adapter identifier.
func (a *Adapter) Name() string { return "codex" }

// Priority returns the discovery order. Lower = checked first.
func (a *Adapter) Priority() int { return 70 }

// Discover reads {projectDir}/AGENTS.md and returns a single Definition if present.
func (a *Adapter) Discover(projectDir string) ([]agent.Definition, error) {
	agentsPath := filepath.Join(projectDir, "AGENTS.md")

	data, err := os.ReadFile(agentsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("adapter-codex: read AGENTS.md: %w", err)
	}

	def := agent.Definition{
		Slug:         "codex-default",
		Source:       "codex",
		SystemPrompt: strings.TrimSpace(string(data)),
	}

	// Extract name from the first # heading.
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			def.Name = strings.TrimPrefix(line, "# ")
			break
		}
	}
	if def.Name == "" {
		def.Name = "Codex Agent"
	}

	return []agent.Definition{def}, nil
}

// PopulateSandbox writes AGENTS.md into the sandbox with agent identity and context.
func (a *Adapter) PopulateSandbox(sandboxDir string, ap store.AgentProfile, session agent.SandboxContext) error {
	content := buildAgentsMD(ap)
	path := filepath.Join(sandboxDir, "AGENTS.md")
	if err := fsutil.AtomicWriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("adapter-codex: write AGENTS.md: %w", err)
	}
	return nil
}

// SyncProjectRoot writes a managed section into {projectDir}/AGENTS.md
// listing available Nanite agents. If no agents are configured, writes
// a placeholder section pointing to NANITE.md for setup help.
func (a *Adapter) SyncProjectRoot(projectDir string, agents []store.AgentProfile) error {
	var content string
	if len(agents) == 0 {
		content = placeholderContent
	} else {
		content = buildNaniteAgentsSection(agents)
	}
	agentsPath := filepath.Join(projectDir, "AGENTS.md")
	return agent.WriteManagedSection(agentsPath, content)
}

const placeholderContent = `## Nanite Agents

No agents configured for this project yet. See NANITE.md for setup help, or add an agent definition to ` + "`.nanite/config.yaml`" + ` and re-run ` + "`nanite-agent init --project .`" + `.`

// ---------------------------------------------------------------------------
// Content generation
// ---------------------------------------------------------------------------

func buildAgentsMD(ap store.AgentProfile) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Agent: %s\n\n", ap.Name)
	if ap.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", ap.Description)
	}

	if ap.Tools != "" && ap.Tools != "[]" {
		b.WriteString("## Tools\n")
		b.WriteString(ap.Tools)
		b.WriteString("\n\n")
	}

	if ap.Constraints != "" && ap.Constraints != "{}" {
		b.WriteString("## Constraints\n")
		b.WriteString(ap.Constraints)
		b.WriteString("\n\n")
	}

	b.WriteString("## Context\n")
	b.WriteString("This agent is managed by Nanite. For full configuration, see NANITE.md.\n")

	return b.String()
}

func buildNaniteAgentsSection(agents []store.AgentProfile) string {
	var b strings.Builder

	b.WriteString("## Nanite Agents\n\n")
	b.WriteString("The following agents are available in this project:\n\n")
	for _, ap := range agents {
		if ap.Description != "" {
			fmt.Fprintf(&b, "- **%s** — %s\n", ap.Name, ap.Description)
		} else {
			fmt.Fprintf(&b, "- **%s**\n", ap.Name)
		}
	}
	b.WriteString("\nManaged by Nanite. See NANITE.md for configuration.\n")

	return b.String()
}
