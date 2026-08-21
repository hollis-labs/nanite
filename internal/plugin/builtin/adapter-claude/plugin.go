// Package adapterclaude implements the Claude Code adapter plugin.
// Discovers agents from .claude/agents/*.md and syncs the project-root
// CLAUDE.md with a managed section listing available agents. Sandbox
// content (CLAUDE.md, .sandbox/agent-context.md, .sandbox/envelope-schema.md,
// .mcp.json) is now planted by the agent runtime via
// internal/runtime/agent/bootdir_claude.Setup at agent.Boot time
// (Phase 4c.6, CW-20260508-0002).
package adapterclaude

import (
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/nanite/internal/agent"
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

// loadManifest parses the embedded plugin.yaml exactly once. A parse failure
// means the builtin was shipped with invalid source metadata — panic so the
// bug surfaces at boot rather than producing a half-wired plugin at runtime.
func loadManifest() *hostplugin.PluginManifest {
	parsedManifestOnce.Do(func() {
		var m hostplugin.PluginManifest
		if err := yaml.Unmarshal(manifestYAML, &m); err != nil {
			panic("adapter-claude: invalid embedded plugin.yaml: " + err.Error())
		}
		parsedManifest = &m
	})
	return parsedManifest
}

func init() {
	hostplugin.RegisterPlugin("adapter-claude", func() plugin.Plugin { return New() })
}

// ---------------------------------------------------------------------------
// Plugin (implements plugin-sdk Plugin)
// ---------------------------------------------------------------------------

// Plugin is the Claude Code adapter plugin.
type Plugin struct {
	host    plugin.Host
	status  plugin.PluginStatus
	adapter *Adapter
}

// New creates a new Claude Code adapter plugin instance.
func New() *Plugin {
	p := &Plugin{}
	p.adapter = &Adapter{plugin: p}
	return p
}

// Adapter returns the CLIAgentAdapter for this plugin.
func (p *Plugin) Adapter() *Adapter { return p.adapter }

func (p *Plugin) ID() string             { return "adapter-claude" }
func (p *Plugin) Name() string           { return "Claude Code Adapter" }
func (p *Plugin) Version() string        { return "0.1.0" }
func (p *Plugin) Description() string    { return "Discovers .claude/agents/*.md and syncs project-root CLAUDE.md" }
func (p *Plugin) Dependencies() []string { return nil }

// Manifest exposes the embedded plugin.yaml so the host loader runs the same
// yaml-authoritative path used for subprocess plugins (H.3 / B.4). This
// adapter has no declarative host registrations — its CLIAgentAdapter is
// wired externally via internal/service/install/adapters.go.
func (p *Plugin) Manifest() *hostplugin.PluginManifest { return loadManifest() }

func (p *Plugin) Load(host plugin.Host) error {
	p.host = host
	p.status = plugin.PluginStatus{
		Loaded:   true,
		Enabled:  true,
		LoadedAt: time.Now(),
	}
	host.Logger().Info("adapter-claude: loaded")
	return nil
}

func (p *Plugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	if p.host != nil {
		p.host.Logger().Info("adapter-claude: unloaded")
	}
	return nil
}

func (p *Plugin) Status() plugin.PluginStatus {
	return p.status
}

// ---------------------------------------------------------------------------
// Adapter (implements agent.CLIAgentAdapter)
// ---------------------------------------------------------------------------

// Adapter implements agent.CLIAgentAdapter for Claude Code.
type Adapter struct {
	plugin *Plugin
}

// Compile-time interface check.
var _ agent.CLIAgentAdapter = (*Adapter)(nil)

// Name returns the adapter identifier.
func (a *Adapter) Name() string { return "claude" }

// Priority returns the discovery order. Lower = checked first.
func (a *Adapter) Priority() int { return 60 }

// Discover is a no-op. External-format agent import (scanning
// .claude/agents/*.md and parsing each as a Nanite agent.Definition) was cut
// in Phase 0 item 16 — see TASKS.md, docs/architecture-decision-log-2026-08-17.md
// §4/§6, and docs/engineering/architecture/01-agent-construction.md's "What's
// cut" section: Nanite agents are defined in Nanite's own schema, with no
// replacement for importing external CLI-agent config formats.
//
// The signature stays so the agent.CLIAgentAdapter interface contract holds
// (mirroring the precedent set by PopulateSandbox above). SyncProjectRoot
// below (the opposite, export direction) is unaffected and remains live.
func (a *Adapter) Discover(_ string) ([]agent.Definition, error) {
	return nil, nil
}

// PopulateSandbox is a no-op since Phase 4c.6 (CW-20260508-0002): claude
// sandbox content is planted by the agent runtime via
// internal/runtime/agent/bootdir_claude.Setup at agent.Boot time. The helper
// content builders moved to internal/runtime/agent/sandbox_content_*.go in
// Phase 3b.1 (commit 9dfcecf): the exported runtimeagent.BuildCLAUDEMD /
// BuildAgentContext, plus the unexported envelopeSchemaContent (planted via
// bootdir_plant.go's sandboxFiles — there never was a
// "BuildEnvelopeSchema" export; that name in an earlier revision of this
// comment didn't match any real symbol).
//
// The signature stays so the agent.CLIAgentAdapter interface contract holds
// for the OLD adapter system that other adapters still implement.
func (a *Adapter) PopulateSandbox(_ string, _ store.AgentProfile, _ agent.SandboxContext) error {
	return nil
}

// SyncProjectRoot writes a managed section into {projectDir}/CLAUDE.md
// listing available Nanite agents. If no agents are configured, writes
// a placeholder section pointing to NANITE.md for setup help.
func (a *Adapter) SyncProjectRoot(projectDir string, agents []store.AgentProfile) error {
	var content string
	if len(agents) == 0 {
		content = placeholderContent
	} else {
		var b strings.Builder
		b.WriteString("Available Nanite agents:\n\n")
		for _, ap := range agents {
			if ap.Description != "" {
				fmt.Fprintf(&b, "- **%s** — %s\n", ap.Name, ap.Description)
			} else {
				fmt.Fprintf(&b, "- **%s**\n", ap.Name)
			}
		}
		content = b.String()
	}

	claudePath := filepath.Join(projectDir, "CLAUDE.md")
	return agent.WriteManagedSection(claudePath, content)
}

const placeholderContent = `## Nanite Agents

No agents configured for this project yet. See NANITE.md for setup help, or add an agent definition to ` + "`.nanite/config.yaml`" + ` and re-run ` + "`nanite-agent init --project .`" + `.`
