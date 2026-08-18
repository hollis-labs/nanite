// Package adaptergemini implements the Gemini CLI adapter plugin.
// It discovers agents from GEMINI.md, populates sandboxes with GEMINI.md
// content, and syncs the project-root GEMINI.md with a managed section
// listing available Nanite agents.
package adaptergemini

import (
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/fsutil"
	hostplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/store"

	plugin "github.com/hollis-labs/plugin-sdk"
	"gopkg.in/yaml.v3"
)

//go:embed plugin.yaml
var manifestYAML []byte

var (
	parsedManifestOnce sync.Once
	parsedManifest     *hostplugin.PluginManifest
)

func loadManifest() *hostplugin.PluginManifest {
	parsedManifestOnce.Do(func() {
		var m hostplugin.PluginManifest
		if err := yaml.Unmarshal(manifestYAML, &m); err != nil {
			panic("adapter-gemini: invalid embedded plugin.yaml: " + err.Error())
		}
		parsedManifest = &m
	})
	return parsedManifest
}

func init() {
	hostplugin.RegisterPlugin("adapter-gemini", func() plugin.Plugin { return New() })
}

// ---------------------------------------------------------------------------
// Plugin (implements plugin-sdk Plugin)
// ---------------------------------------------------------------------------

// Plugin is the Gemini CLI adapter plugin.
type Plugin struct {
	host    plugin.Host
	status  plugin.PluginStatus
	adapter *Adapter
}

// New creates a new Gemini adapter plugin instance.
func New() *Plugin {
	p := &Plugin{}
	p.adapter = &Adapter{plugin: p}
	return p
}

// Adapter returns the CLIAgentAdapter for this plugin.
func (p *Plugin) Adapter() *Adapter { return p.adapter }

func (p *Plugin) ID() string             { return "adapter-gemini" }
func (p *Plugin) Name() string           { return "Gemini CLI Adapter" }
func (p *Plugin) Version() string        { return "0.1.0" }
func (p *Plugin) Description() string    { return "Discovers GEMINI.md and populates Gemini CLI sandboxes" }
func (p *Plugin) Dependencies() []string { return nil }

// Manifest exposes the embedded plugin.yaml so the host loader runs the
// yaml-authoritative path (H.3 / B.4). No declarative registrations — the
// CLIAgentAdapter is wired via internal/service/install/adapters.go.
func (p *Plugin) Manifest() *hostplugin.PluginManifest { return loadManifest() }

func (p *Plugin) Load(host plugin.Host) error {
	p.host = host
	p.status = plugin.PluginStatus{
		Loaded:   true,
		Enabled:  true,
		LoadedAt: time.Now(),
	}
	host.Logger().Info("adapter-gemini: loaded")
	return nil
}

func (p *Plugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	if p.host != nil {
		p.host.Logger().Info("adapter-gemini: unloaded")
	}
	return nil
}

func (p *Plugin) Status() plugin.PluginStatus {
	return p.status
}

// ---------------------------------------------------------------------------
// Adapter (implements agent.CLIAgentAdapter)
// ---------------------------------------------------------------------------

// Adapter implements agent.CLIAgentAdapter for Gemini CLI.
type Adapter struct {
	plugin *Plugin
}

// Compile-time interface check.
var _ agent.CLIAgentAdapter = (*Adapter)(nil)

// Name returns the adapter identifier.
func (a *Adapter) Name() string { return "gemini" }

// Priority returns the discovery order. Lower = checked first.
func (a *Adapter) Priority() int { return 70 }

// Discover is a no-op. External-format agent import (reading GEMINI.md and
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

// PopulateSandbox writes GEMINI.md into the sandbox with agent identity and context.
func (a *Adapter) PopulateSandbox(sandboxDir string, ap store.AgentProfile, session agent.SandboxContext) error {
	content := buildGeminiMD(ap)
	path := filepath.Join(sandboxDir, "GEMINI.md")
	if err := fsutil.AtomicWriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("adapter-gemini: write GEMINI.md: %w", err)
	}
	return nil
}

// SyncProjectRoot writes a managed section into {projectDir}/GEMINI.md
// listing available Nanite agents. If no agents are configured, writes
// a placeholder section pointing to NANITE.md for setup help.
func (a *Adapter) SyncProjectRoot(projectDir string, agents []store.AgentProfile) error {
	var content string
	if len(agents) == 0 {
		content = placeholderContent
	} else {
		content = buildNaniteAgentsSection(agents)
	}
	geminiPath := filepath.Join(projectDir, "GEMINI.md")
	return agent.WriteManagedSection(geminiPath, content)
}

const placeholderContent = `## Nanite Agents

No agents configured for this project yet. See NANITE.md for setup help, or add an agent definition to ` + "`.nanite/config.yaml`" + ` and re-run ` + "`nanite-agent init --project .`" + `.`

// ---------------------------------------------------------------------------
// Content generation
// ---------------------------------------------------------------------------

func buildGeminiMD(ap store.AgentProfile) string {
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
