// Package adapteropencode implements the Opencode CLI adapter plugin.
// It discovers agents from OPENCODE.md, populates sandboxes with OPENCODE.md
// content, and syncs the project-root OPENCODE.md with a managed section
// listing available Nanite agents.
//
// NOTE: The exact format expected by the Opencode CLI for OPENCODE.md should
// be confirmed against official Opencode documentation before production use.
package adapteropencode

import (
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/fsutil"
	hostplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/store"

	plugin "github.com/hollis-labs/plugin-sdk"
)

//go:embed plugin.yaml
var manifestYAML []byte

var loadManifest = hostplugin.LoadEmbeddedManifest("adapter-opencode", manifestYAML)

func init() {
	// init remains per adapter because builtin registration is package-scoped.
	hostplugin.RegisterPlugin("adapter-opencode", func() plugin.Plugin { return New() })
}

// ---------------------------------------------------------------------------
// Plugin (implements plugin-sdk Plugin)
// ---------------------------------------------------------------------------

// Plugin is the Opencode CLI adapter plugin.
type Plugin struct {
	hostplugin.BasePlugin
	adapter *Adapter
}

// New creates a new Opencode adapter plugin instance.
func New() *Plugin {
	p := &Plugin{
		BasePlugin: hostplugin.NewBasePlugin(hostplugin.BasePluginConfig{
			ID:          "adapter-opencode",
			Name:        "Opencode CLI Adapter",
			Version:     "0.1.0",
			Description: "Discovers OPENCODE.md and populates Opencode CLI sandboxes",
			Manifest:    loadManifest,
		}),
	}
	p.adapter = &Adapter{plugin: p}
	return p
}

// Adapter returns the CLIAgentAdapter for this plugin.
func (p *Plugin) Adapter() *Adapter { return p.adapter }

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

// Discover is a no-op. External-format agent import (reading OPENCODE.md and
// synthesizing a Nanite agent.Definition from it) was cut in Phase 0 item 16
// — see TASKS.md, docs/architecture-decision-log-2026-08-17.md §4/§6, and
// docs/engineering/architecture/01-agent-construction.md's "What's cut"
// section: Nanite agents are defined in Nanite's own schema, with no
// replacement for importing external CLI-agent config formats.
//
// The signature stays so the agent.CLIAgentAdapter interface contract holds.
// PopulateSandbox and SyncProjectRoot below (the opposite, export direction)
// are unaffected and remain live.
func (a *Adapter) Discover(_ string) ([]agent.Definition, error) {
	return nil, nil
}

// PopulateSandbox writes OPENCODE.md into the sandbox with agent identity and context.
func (a *Adapter) PopulateSandbox(sandboxDir string, ap store.AgentProfile, session agent.SandboxContext) error {
	content := buildOpencodeMD(ap)
	path := filepath.Join(sandboxDir, "OPENCODE.md")
	if err := fsutil.AtomicWriteFile(path, []byte(content), 0o644); err != nil {
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
