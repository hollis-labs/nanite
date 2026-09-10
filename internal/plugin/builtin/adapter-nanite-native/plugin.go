// Package nanitenative implements the nanite-native adapter plugin. As of
// TASKS/phase-2/06-cut-nanite-native-adapter-agent-sync.md it no longer
// reads .nanite/config.yaml's agents: block into agent_profiles (both the
// direct Plugin.Load sync and the Adapter.Discover composition into
// AutoIngestAgents were cut — see each method's doc comment). It still
// implements agent.CLIAgentAdapter (PopulateSandbox is real and live — a
// different, DB/config -> disposable-sandbox-file direction) and
// agent.AgentComposer for role/skill-based prompt composition, though the
// AgentComposer methods (ComposePrompt/ListRoles/ListSkills) currently have
// no callers anywhere in the codebase (a pre-existing condition, not
// created by this cut — flagged as a follow-up candidate in that task's
// Work Log rather than removed here).
package nanitenative

import (
	_ "embed"
	"fmt"
	"os"
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
			panic("adapter-nanite-native: invalid embedded plugin.yaml: " + err.Error())
		}
		parsedManifest = &m
	})
	return parsedManifest
}

func init() {
	hostplugin.RegisterPlugin("adapter-nanite-native", func() plugin.Plugin { return New() })
}

// ---------------------------------------------------------------------------
// Config types
// ---------------------------------------------------------------------------

// naniteConfig is the structure of .nanite/config.yaml.
type naniteConfig struct {
	Version string                 `yaml:"nanite_version"`
	Agents  map[string]naniteAgent `yaml:"agents"`
}

// NaniteAgent is one agent entry in the project config. Exported so callers
// can pass it to ComposePrompt.
type NaniteAgent struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Roles       []string `yaml:"roles"`
	Skills      []string `yaml:"skills"`
	Context     string   `yaml:"context"`
}

// naniteAgent is the internal alias used during YAML parsing.
type naniteAgent = NaniteAgent

// globalConfig is the structure of ~/.nanite/config.yaml (roles section only).
type globalConfig struct {
	Roles map[string]roleEntry `yaml:"roles"`
}

// roleEntry maps a role name to its file path and metadata.
type roleEntry struct {
	File        string `yaml:"file"`
	Type        string `yaml:"type"`
	Description string `yaml:"description"`
}

// ---------------------------------------------------------------------------
// Plugin (implements plugin-sdk Plugin)
// ---------------------------------------------------------------------------

// Plugin is the nanite-native adapter plugin. It implements the plugin-sdk Plugin interface.
// Call Adapter() to obtain the CLIAgentAdapter / AgentComposer implementation.
type Plugin struct {
	host    plugin.Host
	status  plugin.PluginStatus
	adapter *Adapter
}

// New creates a new nanite-native adapter plugin instance.
func New() *Plugin {
	p := &Plugin{}
	p.adapter = &Adapter{plugin: p}
	return p
}

// Adapter returns the CLIAgentAdapter / AgentComposer for this plugin.
func (p *Plugin) Adapter() *Adapter { return p.adapter }

func (p *Plugin) ID() string      { return "adapter-nanite-native" }
func (p *Plugin) Name() string    { return "Nanite Native Adapter" }
func (p *Plugin) Version() string { return "0.2.0" }
func (p *Plugin) Description() string {
	return "Provides CLIAgentAdapter (sandbox population) + AgentComposer for .nanite/ (agent_profiles sync removed, TASKS/phase-2/06)"
}
func (p *Plugin) Dependencies() []string { return nil }

// Manifest exposes the embedded plugin.yaml so the host loader runs the
// yaml-authoritative path (H.3 / B.4). No declarative registrations — the
// CLIAgentAdapter is wired via internal/service/install/adapters.go.
func (p *Plugin) Manifest() *hostplugin.PluginManifest { return loadManifest() }

// Load is a no-op with respect to agent storage. Prior to
// TASKS/phase-2/06-cut-nanite-native-adapter-agent-sync.md this method read
// .nanite/config.yaml's agents: block and directly upserted each entry into
// agent_profiles via store.UpsertAgentBySlug — an ungated, unconditional
// overwrite on every boot. That write path is cut in full per the standing
// decision in docs/engineering/architecture/01-agent-construction.md's
// "What's cut" section ("Files as agent storage, except builtin/seed
// content"), which applies to this adapter's file-based sync regardless of
// how deliberately it was originally built (see the task file's Context for
// the operator's own confirmation of that). Adapter.Discover's parallel
// composition into the AutoIngestAgents pipeline was cut alongside this one
// — see that method's doc comment.
//
// The plugin still loads successfully with no store dependency: it no
// longer needs the "store" service at all for this method.
// PopulateSandbox (per-launch sandbox population, a different DB/config ->
// disposable-file direction) is unaffected and does not depend on Load
// running any sync logic.
func (p *Plugin) Load(host plugin.Host) error {
	p.host = host
	p.status = plugin.PluginStatus{Loaded: true, Enabled: true, LoadedAt: time.Now()}
	host.Logger().Info("adapter-nanite-native: loaded (agent_profiles sync removed, TASKS/phase-2/06)")
	return nil
}

func (p *Plugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	if p.host != nil {
		p.host.Logger().Info("adapter-nanite-native: unloaded")
	}
	return nil
}

func (p *Plugin) Status() plugin.PluginStatus {
	return p.status
}

// ---------------------------------------------------------------------------
// Adapter (implements agent.CLIAgentAdapter + agent.AgentComposer)
// ---------------------------------------------------------------------------

// Adapter implements agent.CLIAgentAdapter and agent.AgentComposer.
// It is obtained via Plugin.Adapter().
type Adapter struct {
	plugin *Plugin
}

// Compile-time interface checks.
var (
	_ agent.CLIAgentAdapter = (*Adapter)(nil)
	_ agent.AgentComposer   = (*Adapter)(nil)
)

// Name returns the adapter identifier.
func (a *Adapter) Name() string { return "nanite-native" }

// Priority returns the discovery order. Lower = checked first.
func (a *Adapter) Priority() int { return 50 }

// Import is not implemented for this adapter, and its absence is the point.
//
// The method it replaces (Discover) used to re-parse .nanite/config.yaml's
// agents: block independently of Load (above) and feed the results into the
// AutoIngestAgents/upsertAgentDef pipeline via internal/agent/discovery.go's
// adapter tier — a second, parallel .nanite/config.yaml -> agent_profiles
// write path alongside Load's direct sync. Both were cut together by
// TASKS/phase-2/06-cut-nanite-native-adapter-agent-sync.md, closing the
// carve-out TASKS/phase-0/16-cut-external-agent-import.md had left open.
//
// CW-20260910-0012 re-armed the seam as an explicit, operator-initiated
// Import(path) and did NOT restore this adapter's behavior with it. There is
// no project agent catalog in Nanite — no .nanite/agents/, no
// config/agents/, no file that defines a runtime agent — so there is nothing
// here for an import to read. A Nanite-format definition file names its own
// path and is imported by internal/agentimport.NativeParser directly.
//
// (nil, nil) is the interface's "this path is not my format" answer, so an
// AdapterRegistry moves on. PopulateSandbox below (the opposite, DB/config ->
// disposable-sandbox-file direction) is unaffected and remains live.
func (a *Adapter) Import(_ string) ([]agent.Definition, error) {
	return nil, nil
}

// PopulateSandbox writes a minimal .nanite/ structure into sandboxDir for the agent.
func (a *Adapter) PopulateSandbox(sandboxDir string, ap store.AgentProfile, session agent.SandboxContext) error {
	naniteDir := filepath.Join(sandboxDir, ".nanite")
	if err := os.MkdirAll(naniteDir, 0o755); err != nil {
		return fmt.Errorf("adapter-nanite-native: create sandbox .nanite/: %w", err)
	}

	cfg := naniteConfig{
		Version: "2.2.0",
		Agents: map[string]naniteAgent{
			ap.Slug: {
				Name:        ap.Name,
				Description: ap.Description,
			},
		},
	}

	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("adapter-nanite-native: marshal sandbox config: %w", err)
	}

	return fsutil.AtomicWriteFile(filepath.Join(naniteDir, "config.yaml"), data, 0o644)
}

// SyncProjectRoot is a no-op for nanite-native — it manages its own files.
func (a *Adapter) SyncProjectRoot(_ string, _ []store.AgentProfile) error {
	return nil
}

// ComposePrompt assembles a system prompt from the given agent config.
// agentConfig should be a NaniteAgent (or *NaniteAgent).
func (a *Adapter) ComposePrompt(agentConfig any, projectDir string) (string, error) {
	var agentDef NaniteAgent
	switch v := agentConfig.(type) {
	case NaniteAgent:
		agentDef = v
	case *NaniteAgent:
		agentDef = *v
	default:
		return "", fmt.Errorf("adapter-nanite-native: ComposePrompt expects NaniteAgent, got %T", agentConfig)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("adapter-nanite-native: cannot determine home dir: %w", err)
	}

	globalCfg := readGlobalConfigFromHome(home)
	rolesDir := globalDir(home, "roles")

	systemPrompt := composeSystemPrompt(globalCfg, rolesDir, agentDef.Roles)

	if agentDef.Context != "" {
		_, projectConfigDir, err := readProjectConfigFromRoot(projectDir)
		if err == nil {
			ctxPath := filepath.Join(projectConfigDir, agentDef.Context)
			if data, err := os.ReadFile(ctxPath); err == nil {
				systemPrompt += "\n\n[Project Context]\n" + string(data)
			}
		}
	}

	return systemPrompt, nil
}

// ListRoles reads ~/.nanite/roles/ and returns available roles with metadata
// from the global config.
func (a *Adapter) ListRoles() ([]agent.RoleInfo, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("adapter-nanite-native: cannot determine home dir: %w", err)
	}

	globalCfg := readGlobalConfigFromHome(home)
	rolesDir := globalDir(home, "roles")

	var roles []agent.RoleInfo

	err = filepath.WalkDir(rolesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // Role discovery is best-effort; unreadable entries are omitted.
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}

		relPath, _ := filepath.Rel(rolesDir, path)
		name := strings.TrimSuffix(d.Name(), ".md")

		ri := agent.RoleInfo{
			Name:     name,
			FilePath: path,
		}

		// Try to enrich from global config.
		for roleName, entry := range globalCfg.Roles {
			if entry.File == relPath {
				ri.Name = roleName
				ri.Type = entry.Type
				ri.Description = entry.Description
				break
			}
		}

		roles = append(roles, ri)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("adapter-nanite-native: walk roles dir: %w", err)
	}

	return roles, nil
}

// ListSkills reads ~/.nanite/skills/ and returns available skills.
func (a *Adapter) ListSkills() ([]agent.SkillInfo, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("adapter-nanite-native: cannot determine home dir: %w", err)
	}

	skillsDir := globalDir(home, "skills")

	var skills []agent.SkillInfo

	err = filepath.WalkDir(skillsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // Skill discovery is best-effort; unreadable entries are omitted.
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}

		name := strings.TrimSuffix(d.Name(), ".md")
		skills = append(skills, agent.SkillInfo{
			Name:     name,
			FilePath: path,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("adapter-nanite-native: walk skills dir: %w", err)
	}

	return skills, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func globalDir(home, subdir string) string {
	return filepath.Join(home, ".nanite", subdir)
}

func readGlobalConfigFromHome(home string) *globalConfig {
	cfg, err := readGlobalConfig(filepath.Join(home, ".nanite", "config.yaml"))
	if err != nil {
		return &globalConfig{Roles: map[string]roleEntry{}}
	}
	return cfg
}

// readProjectConfigFromRoot reads the project config from {dir}/.nanite/config.yaml.
func readProjectConfigFromRoot(dir string) (*naniteConfig, string, error) {
	configDir := filepath.Join(dir, ".nanite")
	cfg, err := readProjectConfig(filepath.Join(configDir, "config.yaml"))
	if err == nil {
		return cfg, configDir, nil
	}
	return nil, "", fmt.Errorf("no config found in .nanite/: %w", err)
}

// readGlobalConfig reads a global config file for role definitions.
func readGlobalConfig(path string) (*globalConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg globalConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse global config: %w", err)
	}
	if cfg.Roles == nil {
		cfg.Roles = map[string]roleEntry{}
	}
	return &cfg, nil
}

// readProjectConfig reads a project config file for agent definitions.
func readProjectConfig(path string) (*naniteConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg naniteConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse project config: %w", err)
	}
	return &cfg, nil
}

// composeSystemPrompt reads and concatenates role files into a system prompt.
func composeSystemPrompt(globalCfg *globalConfig, rolesDir string, roles []string) string {
	var parts []string
	for _, roleName := range roles {
		entry, ok := globalCfg.Roles[roleName]
		if !ok {
			parts = append(parts, fmt.Sprintf("[Role: %s]\n(role definition not found)\n", roleName))
			continue
		}

		rolePath := filepath.Join(rolesDir, entry.File)
		data, err := os.ReadFile(rolePath)
		if err != nil {
			parts = append(parts, fmt.Sprintf("[Role: %s]\n(could not read %s: %v)\n", roleName, entry.File, err))
			continue
		}

		parts = append(parts, strings.TrimSpace(string(data)))
	}

	return strings.Join(parts, "\n\n")
}

// dirExists returns true if path is an existing directory.
func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
