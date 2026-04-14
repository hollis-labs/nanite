// Package nanitenative implements the nanite-native adapter plugin, replacing
// the agentrc-sync plugin. It reads agent definitions from .nanite/ (with
// .agentrc/ fallback) and implements both agent.CLIAgentAdapter and
// agent.AgentComposer for role/skill-based prompt composition.
package nanitenative

import (
	_ "embed"
	"encoding/json"
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
// Config types (backward-compatible with agentrc)
// ---------------------------------------------------------------------------

// naniteConfig is the structure of .nanite/config.yaml (or .agentrc/config.yaml).
type naniteConfig struct {
	Version string                 `yaml:"agentrc_version"` // keep for backward compat
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
// Plugin (implements go-plugin.Plugin)
// ---------------------------------------------------------------------------

// Plugin is the nanite-native adapter plugin. It implements go-plugin.Plugin.
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

func (p *Plugin) ID() string             { return "adapter-nanite-native" }
func (p *Plugin) Name() string           { return "Nanite Native Adapter" }
func (p *Plugin) Version() string        { return "0.2.0" }
func (p *Plugin) Description() string    { return "Syncs .nanite/ (or .agentrc/) agent definitions and provides CLIAgentAdapter + AgentComposer" }
func (p *Plugin) Dependencies() []string { return nil }

// Manifest exposes the embedded plugin.yaml so the host loader runs the
// yaml-authoritative path (H.3 / B.4). No declarative registrations — the
// CLIAgentAdapter is wired via internal/service/install/adapters.go.
func (p *Plugin) Manifest() *hostplugin.PluginManifest { return loadManifest() }

func (p *Plugin) Load(host plugin.Host) error {
	p.host = host
	logger := host.Logger()

	svc, err := host.GetService("store")
	if err != nil {
		return fmt.Errorf("adapter-nanite-native: store service unavailable: %w", err)
	}
	s, ok := svc.(*store.Store)
	if !ok {
		return fmt.Errorf("adapter-nanite-native: store service has unexpected type %T", svc)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("adapter-nanite-native: cannot determine home dir: %w", err)
	}

	// Read global config for role definitions.
	globalCfg, _ := readGlobalConfigWithFallback(home, logger)

	// Read project-level config.
	projectCfg, projectConfigDir, err := readProjectConfigWithFallback(".", logger)
	if err != nil {
		logger.Info("adapter-nanite-native: no project config found", "error", err.Error())
		p.status = plugin.PluginStatus{Loaded: true, Enabled: true, LoadedAt: time.Now()}
		return nil
	}

	if len(projectCfg.Agents) == 0 {
		logger.Info("adapter-nanite-native: no agents defined in project config")
		p.status = plugin.PluginStatus{Loaded: true, Enabled: true, LoadedAt: time.Now()}
		return nil
	}

	rolesDir := resolveGlobalDir(home, "roles", logger)
	configPath, _ := filepath.Abs(filepath.Join(projectConfigDir, "config.yaml"))

	// Track which slugs we synced so we can disable removed agents.
	syncedSlugs := make(map[string]bool, len(projectCfg.Agents))

	for slug, agentDef := range projectCfg.Agents {
		syncedSlugs[slug] = true

		// Compose system prompt from roles.
		systemPrompt := composeSystemPrompt(globalCfg, rolesDir, agentDef.Roles)

		// Append project context if specified.
		if agentDef.Context != "" {
			ctxPath := filepath.Join(projectConfigDir, agentDef.Context)
			if data, err := os.ReadFile(ctxPath); err == nil {
				systemPrompt += "\n\n[Project Context]\n" + string(data)
			} else {
				logger.Warn("adapter-nanite-native: could not read context file", "path", ctxPath, "error", err.Error())
			}
		}

		// Build tags from roles + skills.
		tags := []string{"nanite"}
		tags = append(tags, agentDef.Roles...)
		tagsJSON, _ := json.Marshal(tags)

		ap := &store.AgentProfile{
			Name:         agentDef.Name,
			Slug:         slug,
			Description:  agentDef.Description,
			SystemPrompt: systemPrompt,
			CanExecute:   true,
			Status:       "active",
			Source:       "nanite",
			SourceRef:    configPath,
			Tags:         string(tagsJSON),
		}

		if err := s.UpsertAgentBySlug(ap); err != nil {
			logger.Error("adapter-nanite-native: failed to upsert agent", "slug", slug, "error", err.Error())
			continue
		}
		logger.Info("adapter-nanite-native: synced agent", "slug", slug, "name", agentDef.Name)
	}

	// Disable agents that were previously synced but are no longer in the config.
	// Check both "nanite" and legacy "agentrc" sources.
	for _, src := range []string{"nanite", "agentrc"} {
		existing, err := s.ListAgentsBySource(src)
		if err != nil {
			logger.Warn("adapter-nanite-native: could not list existing agents", "source", src, "error", err.Error())
			continue
		}
		for _, a := range existing {
			if !syncedSlugs[a.Slug] && a.Status == "active" {
				a.Status = "disabled"
				if err := s.UpdateAgent(&a); err != nil {
					logger.Warn("adapter-nanite-native: failed to disable removed agent", "slug", a.Slug, "error", err.Error())
				} else {
					logger.Info("adapter-nanite-native: disabled removed agent", "slug", a.Slug)
				}
			}
		}
	}

	p.status = plugin.PluginStatus{
		Loaded:   true,
		Enabled:  true,
		LoadedAt: time.Now(),
	}
	logger.Info("adapter-nanite-native: loaded", "agents_synced", len(syncedSlugs))
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

// Discover scans projectDir for agent definitions and returns normalized Definitions.
func (a *Adapter) Discover(projectDir string) ([]agent.Definition, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("adapter-nanite-native: cannot determine home dir: %w", err)
	}

	globalCfg, _ := readGlobalConfigWithFallback(home, nil)

	projectCfg, projectConfigDir, err := readProjectConfigWithFallback(projectDir, nil)
	if err != nil {
		// No config file is not an error — just no agents to discover.
		return nil, nil
	}

	rolesDir := resolveGlobalDir(home, "roles", nil)

	var defs []agent.Definition
	for slug, agentDef := range projectCfg.Agents {
		systemPrompt := composeSystemPrompt(globalCfg, rolesDir, agentDef.Roles)

		if agentDef.Context != "" {
			ctxPath := filepath.Join(projectConfigDir, agentDef.Context)
			if data, err := os.ReadFile(ctxPath); err == nil {
				systemPrompt += "\n\n[Project Context]\n" + string(data)
			}
		}

		defs = append(defs, agent.Definition{
			Name:         agentDef.Name,
			Slug:         slug,
			Description:  agentDef.Description,
			SystemPrompt: systemPrompt,
			Source:       "nanite",
			SourceRef:    filepath.Join(projectConfigDir, "config.yaml"),
			Skills:       agentDef.Skills,
			Tags:         append([]string{"nanite"}, agentDef.Roles...),
		})
	}

	return defs, nil
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

	globalCfg, _ := readGlobalConfigWithFallback(home, nil)
	rolesDir := resolveGlobalDir(home, "roles", nil)

	systemPrompt := composeSystemPrompt(globalCfg, rolesDir, agentDef.Roles)

	if agentDef.Context != "" {
		_, projectConfigDir, err := readProjectConfigWithFallback(projectDir, nil)
		if err == nil {
			ctxPath := filepath.Join(projectConfigDir, agentDef.Context)
			if data, err := os.ReadFile(ctxPath); err == nil {
				systemPrompt += "\n\n[Project Context]\n" + string(data)
			}
		}
	}

	return systemPrompt, nil
}

// ListRoles reads ~/.nanite/roles/ (fallback ~/.agentrc/roles/) and returns
// available roles with metadata from the global config.
func (a *Adapter) ListRoles() ([]agent.RoleInfo, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("adapter-nanite-native: cannot determine home dir: %w", err)
	}

	globalCfg, _ := readGlobalConfigWithFallback(home, nil)
	rolesDir := resolveGlobalDir(home, "roles", nil)

	var roles []agent.RoleInfo

	err = filepath.WalkDir(rolesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
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

// ListSkills reads ~/.nanite/skills/ (fallback ~/.agentrc/skills/) and returns
// available skills.
func (a *Adapter) ListSkills() ([]agent.SkillInfo, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("adapter-nanite-native: cannot determine home dir: %w", err)
	}

	skillsDir := resolveGlobalDir(home, "skills", nil)

	var skills []agent.SkillInfo

	err = filepath.WalkDir(skillsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
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

// resolveGlobalDir returns the path for a global subdirectory (roles, skills)
// preferring .nanite/ over .agentrc/.
func resolveGlobalDir(home, subdir string, logger plugin.Logger) string {
	primary := filepath.Join(home, ".nanite", subdir)
	if dirExists(primary) {
		return primary
	}
	fallback := filepath.Join(home, ".agentrc", subdir)
	if dirExists(fallback) {
		if logger != nil {
			logger.Warn("adapter-nanite-native: using deprecated .agentrc/ path, migrate to .nanite/", "path", fallback)
		}
		return fallback
	}
	// Neither exists — return primary so callers get a clean "not found".
	return primary
}

// readGlobalConfigWithFallback reads the global config from ~/.nanite/config.yaml
// with fallback to ~/.agentrc/config.yaml.
func readGlobalConfigWithFallback(home string, logger plugin.Logger) (*globalConfig, error) {
	primary := filepath.Join(home, ".nanite", "config.yaml")
	cfg, err := readGlobalConfig(primary)
	if err == nil {
		return cfg, nil
	}

	fallback := filepath.Join(home, ".agentrc", "config.yaml")
	cfg, err = readGlobalConfig(fallback)
	if err == nil {
		if logger != nil {
			logger.Warn("adapter-nanite-native: using deprecated .agentrc/ path, migrate to .nanite/", "path", fallback)
		}
		return cfg, nil
	}

	// Neither found — return empty config.
	return &globalConfig{Roles: map[string]roleEntry{}}, err
}

// readProjectConfigWithFallback reads the project config from {dir}/.nanite/config.yaml
// with fallback to {dir}/.agentrc/config.yaml. Returns the config, the config
// directory path (e.g. "{dir}/.nanite"), and any error.
func readProjectConfigWithFallback(dir string, logger plugin.Logger) (*naniteConfig, string, error) {
	primaryDir := filepath.Join(dir, ".nanite")
	cfg, err := readProjectConfig(filepath.Join(primaryDir, "config.yaml"))
	if err == nil {
		return cfg, primaryDir, nil
	}

	fallbackDir := filepath.Join(dir, ".agentrc")
	cfg, err = readProjectConfig(filepath.Join(fallbackDir, "config.yaml"))
	if err == nil {
		if logger != nil {
			logger.Warn("adapter-nanite-native: using deprecated .agentrc/ path, migrate to .nanite/", "path", fallbackDir)
		}
		return cfg, fallbackDir, nil
	}

	return nil, "", fmt.Errorf("no config found in .nanite/ or .agentrc/: %w", err)
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
