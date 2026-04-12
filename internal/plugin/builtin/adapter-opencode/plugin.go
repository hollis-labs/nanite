// Package adapteropencode implements the Opencode CLI adapter plugin.
// It discovers agents from OPENCODE.md, populates sandboxes with OPENCODE.md
// content, and syncs the project-root OPENCODE.md with a managed section
// listing available Nanite agents.
//
// NOTE: The exact format expected by the Opencode CLI for OPENCODE.md should
// be confirmed against official Opencode documentation before production use.
package adapteropencode

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/agent"
	hostplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/store"

	plugin "github.com/hollis-labs/go-plugin"
)

func init() {
	hostplugin.RegisterPlugin("adapter-opencode", func() plugin.Plugin { return New() })
}

// ---------------------------------------------------------------------------
// Plugin (implements go-plugin.Plugin)
// ---------------------------------------------------------------------------

// Plugin is the Opencode CLI adapter plugin.
type Plugin struct {
	host    plugin.Host
	status  plugin.PluginStatus
	adapter *Adapter
}

// New creates a new Opencode adapter plugin instance.
func New() *Plugin {
	p := &Plugin{}
	p.adapter = &Adapter{plugin: p}
	return p
}

// Adapter returns the CLIAgentAdapter for this plugin.
func (p *Plugin) Adapter() *Adapter { return p.adapter }

func (p *Plugin) ID() string             { return "adapter-opencode" }
func (p *Plugin) Name() string           { return "Opencode CLI Adapter" }
func (p *Plugin) Version() string        { return "0.1.0" }
func (p *Plugin) Description() string    { return "Discovers OPENCODE.md and populates Opencode CLI sandboxes" }
func (p *Plugin) Dependencies() []string { return nil }

func (p *Plugin) Load(host plugin.Host) error {
	p.host = host
	p.status = plugin.PluginStatus{
		Loaded:   true,
		Enabled:  true,
		LoadedAt: time.Now(),
	}
	host.Logger().Info("adapter-opencode: loaded")
	return nil
}

func (p *Plugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	if p.host != nil {
		p.host.Logger().Info("adapter-opencode: unloaded")
	}
	return nil
}

func (p *Plugin) Status() plugin.PluginStatus {
	return p.status
}

// ---------------------------------------------------------------------------
// Adapter (implements agent.CLIAgentAdapter)
// ---------------------------------------------------------------------------

// Adapter implements agent.CLIAgentAdapter for Opencode CLI.
type Adapter struct {
	plugin *Plugin
}

// Compile-time interface check.
var _ agent.CLIAgentAdapter = (*Adapter)(nil)

// Name returns the adapter identifier.
func (a *Adapter) Name() string { return "opencode" }

// Priority returns the discovery order. Lower = checked first.
func (a *Adapter) Priority() int { return 70 }

// Discover reads {projectDir}/OPENCODE.md and returns a single Definition if present.
func (a *Adapter) Discover(projectDir string) ([]agent.Definition, error) {
	opencodePath := filepath.Join(projectDir, "OPENCODE.md")

	data, err := os.ReadFile(opencodePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("adapter-opencode: read OPENCODE.md: %w", err)
	}

	def := agent.Definition{
		Slug:         "opencode-default",
		Source:       "opencode",
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
		def.Name = "Opencode Agent"
	}

	return []agent.Definition{def}, nil
}

// PopulateSandbox writes OPENCODE.md into the sandbox with agent identity and context.
func (a *Adapter) PopulateSandbox(sandboxDir string, ap store.AgentProfile, session agent.SandboxContext) error {
	content := buildOpencodeMD(ap)
	path := filepath.Join(sandboxDir, "OPENCODE.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("adapter-opencode: write OPENCODE.md: %w", err)
	}
	return nil
}

// SyncProjectRoot writes a managed section into {projectDir}/OPENCODE.md
// listing available Nanite agents. If no agents are configured, writes
// a placeholder section pointing to NANITE.md for setup help.
func (a *Adapter) SyncProjectRoot(projectDir string, agents []store.AgentProfile) error {
	var content string
	if len(agents) == 0 {
		content = placeholderContent
	} else {
		content = buildNaniteAgentsSection(agents)
	}
	opencodePath := filepath.Join(projectDir, "OPENCODE.md")
	return agent.WriteManagedSection(opencodePath, content)
}

const placeholderContent = `## Nanite Agents

No agents configured for this project yet. See NANITE.md for setup help, or add an agent definition to ` + "`.nanite/config.yaml`" + ` and re-run ` + "`nanite-agent init --project .`" + `.`

// ---------------------------------------------------------------------------
// Content generation
// ---------------------------------------------------------------------------

func buildOpencodeMD(ap store.AgentProfile) string {
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
